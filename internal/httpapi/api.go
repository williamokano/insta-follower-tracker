package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/instagram"

	"github.com/williamokano/insta-follower-tracker/internal/store"
	"github.com/williamokano/insta-follower-tracker/internal/tracker"
)

// unfollowerNote is attached to the raw event log so a caller cannot mistake it
// for the list of followers actually lost.
const unfollowerNote = "Every unfollow event ever recorded, including people who later followed again. " +
	"For who is genuinely gone, use the diff endpoint and read its 'lost' list."

// handleCreateUpload stores an export and queues it. It answers 202 as soon as
// the file is safely on disk; parsing happens on the background worker.
func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	// Leave headroom over the upload cap for the multipart envelope itself.
	r.Body = http.MaxBytesReader(w, r.Body, s.opts.MaxUploadBytes+(1<<20))

	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeError(w, r, http.StatusRequestEntityTooLarge, tracker.ErrUploadTooLarge)
			return
		}
		s.writeError(w, r, http.StatusBadRequest, fmt.Errorf("could not read the upload: %w", err))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	// The account may be left out: it is read from the export when the export
	// says who it belongs to.
	handle := strings.TrimSpace(r.FormValue("account"))

	file, header, err := r.FormFile("file")
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, errors.New("a file is required in the 'file' field"))
		return
	}
	defer file.Close()

	filename := "upload"
	if header != nil && header.Filename != "" {
		filename = header.Filename
	}

	snapshotDate, err := formDate(r, "snapshot_date")
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	upload, err := s.svc.Accept(r.Context(), handle, filename, file, tracker.AcceptOptions{
		AllowPartial: formFlag(r, "allow_partial"),
		SnapshotDate: snapshotDate,
	})
	if errors.Is(err, tracker.ErrUploadTooLarge) {
		s.writeError(w, r, http.StatusRequestEntityTooLarge, err)
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/uploads/%d", upload.ID))
	s.writeJSON(w, r, http.StatusAccepted, map[string]any{
		"upload":  upload,
		"message": "Upload stored and queued. Processing continues in the background.",
	})
}

func (s *Server) handleGetUpload(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	upload, err := s.svc.Store().Upload(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, errors.New("no such execution"))
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, upload)
}

// handleUploadChanges returns one execution's delta against its predecessor.
func (s *Server) handleUploadChanges(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	kind, err := changeTypeParam(r.URL.Query().Get("type"))
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	upload, err := s.svc.Store().Upload(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, errors.New("no such execution"))
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	listKind, err := listKindParam(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	changes, err := s.svc.Store().ChangesForUpload(r.Context(), id, listKind, kind)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	followed, unfollowed := splitChanges(changes)
	body := map[string]any{
		"upload":      upload,
		"list_kind":   listKind,
		"followed":    followed,
		"unfollowed":  unfollowed,
		"is_baseline": upload.IsBaseline,
	}
	if upload.IsBaseline {
		body["message"] = "This is the first execution for the account, so there is nothing to compare it against."
	}
	s.writeJSON(w, r, http.StatusOK, body)
}

// handleUploadFollowers returns the complete list an execution recorded, which
// is the only thing there is to look at for a first upload.
func (s *Server) handleUploadFollowers(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	upload, err := s.svc.Store().Upload(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, errors.New("no such execution"))
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	listKind, err := listKindParam(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	followers, err := s.svc.Store().MembersForUpload(r.Context(), id, listKind)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	totals, err := s.svc.Store().ListTotalsForUpload(r.Context(), id)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"upload":    upload,
		"list_kind": listKind,
		"lists":     inPresentationOrder(totals),
		"count":     len(followers),
		"followers": followers,
	})
}

// handleReprocess queues an account's executions to be read again from the
// files kept on disk.
func (s *Server) handleReprocess(w http.ResponseWriter, r *http.Request) {
	account, ok := s.resolveAccount(w, r)
	if !ok {
		return
	}

	queued, err := s.svc.Reprocess(r.Context(), account.Handle)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	s.writeJSON(w, r, http.StatusAccepted, map[string]any{
		"account": account,
		"queued":  queued,
		"message": "Queued for rereading. Each execution keeps the data it has until its " +
			"file has been read again, so nothing is lost if a file has gone missing.",
	})
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.svc.Store().ListAccounts(r.Context())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	if accounts == nil {
		accounts = []store.AccountSummary{}
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"accounts": accounts})
}

func (s *Server) handleAccountUploads(w http.ResponseWriter, r *http.Request) {
	account, ok := s.resolveAccount(w, r)
	if !ok {
		return
	}

	uploads, err := s.svc.Store().ListUploads(r.Context(), account.ID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"account": account,
		"uploads": uploads,
	})
}

func (s *Server) handleAccountFollowers(w http.ResponseWriter, r *http.Request) {
	account, ok := s.resolveAccount(w, r)
	if !ok {
		return
	}

	followers, err := s.svc.Store().CurrentFollowers(r.Context(), account.ID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"account":   account,
		"count":     len(followers),
		"followers": followers,
	})
}

// handleAccountDiff compares two executions, defaulting to the first and the
// last. The response is split so that "lost" means genuinely gone, while
// somebody who unfollowed and came back appears under "returned".
func (s *Server) handleAccountDiff(w http.ResponseWriter, r *http.Request) {
	account, ok := s.resolveAccount(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	query := r.URL.Query()

	from, err := s.svc.ResolveUpload(ctx, account.ID, query.Get("from"), false)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound,
			errors.New("this account has no processed executions yet"))
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	to, err := s.svc.ResolveUpload(ctx, account.ID, query.Get("to"), true)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound,
			errors.New("this account has no processed executions yet"))
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	if from.SequenceNo != nil && to.SequenceNo != nil && *from.SequenceNo > *to.SequenceNo {
		from, to = to, from
	}

	listKind, err := listKindParam(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	diff, err := s.svc.Store().Diff(ctx, account.ID, from, to, listKind)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"account":   account,
		"list_kind": diff.ListKind,
		"from":      diff.From,
		"to":        diff.To,
		"lost":      diff.Lost,
		"gained":    diff.Gained,
		"returned":  diff.Returned,
		"transient": diff.Transient,
		"counts": map[string]int{
			"lost":      len(diff.Lost),
			"gained":    len(diff.Gained),
			"returned":  len(diff.Returned),
			"transient": len(diff.Transient),
		},
		"legend": map[string]string{
			"lost":      "Followed you at the start and does not now.",
			"gained":    "Does not appear at the start and follows you now.",
			"returned":  "Unfollowed at some point in between but follows you again now.",
			"transient": "Appeared only in the middle: not present at either end.",
		},
	})
}

// handleAccountUnfollowers returns the raw unfollow event log. It is
// deliberately not the same as the diff's "lost" list.
func (s *Server) handleAccountUnfollowers(w http.ResponseWriter, r *http.Request) {
	account, ok := s.resolveAccount(w, r)
	if !ok {
		return
	}

	listKind, err := listKindParam(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	events, err := s.svc.Store().AllUnfollowers(r.Context(), account.ID, listKind)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"account":   account,
		"list_kind": listKind,
		"count":     len(events),
		"events":    events,
		"note":      unfollowerNote,
	})
}

func changeTypeParam(raw string) (store.ChangeType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case "followed", "follow", "added":
		return store.ChangeFollowed, nil
	case "unfollowed", "unfollow", "removed":
		return store.ChangeUnfollowed, nil
	default:
		return "", fmt.Errorf("unknown change type %q; use followed or unfollowed", raw)
	}
}

func splitChanges(changes []store.Change) (followed, unfollowed []store.Change) {
	followed, unfollowed = []store.Change{}, []store.Change{}
	for _, c := range changes {
		if c.ChangeType == store.ChangeFollowed {
			followed = append(followed, c)
		} else {
			unfollowed = append(unfollowed, c)
		}
	}
	return followed, unfollowed
}

// formFlag reads a checkbox-style field, accepting the several spellings a
// browser form or an API client may send.
func formFlag(r *http.Request, name string) bool {
	switch strings.ToLower(strings.TrimSpace(r.FormValue(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// formDate reads an optional YYYY-MM-DD field, the shape an HTML date input
// submits.
func formDate(r *http.Request, name string) (time.Time, error) {
	raw := strings.TrimSpace(r.FormValue(name))
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be a date as YYYY-MM-DD, got %q", name, raw)
	}
	return t.UTC(), nil
}

// listKindParam reads which relationship list a request is about, defaulting to
// followers so every existing caller keeps working unchanged.
func listKindParam(r *http.Request) (string, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("list"))
	if raw == "" {
		return store.DefaultListKind, nil
	}
	if _, ok := instagram.LookupList(raw); !ok {
		return "", fmt.Errorf("unknown list %q", raw)
	}
	return raw, nil
}

// handleLists reports which relationship lists the service understands.
func (s *Server) handleLists(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, map[string]any{"lists": instagram.Lists()})
}

// handleUploadRelationships answers the questions that compare two lists at the
// same moment rather than one list across time.
func (s *Server) handleUploadRelationships(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	upload, err := s.svc.Store().Upload(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, errors.New("no such execution"))
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	notBack, err := s.svc.Store().NotFollowingBack(r.Context(), id)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	fans, err := s.svc.Store().Fans(r.Context(), id)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	totals, err := s.svc.Store().ListTotalsForUpload(r.Context(), id)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	hasFollowing := false
	for _, t := range totals {
		if t.Kind == string(instagram.ListFollowing) {
			hasFollowing = true
		}
	}

	body := map[string]any{
		"upload":             upload,
		"not_following_back": notBack,
		"fans":               fans,
		"counts": map[string]int{
			"not_following_back": len(notBack),
			"fans":               len(fans),
		},
	}
	if !hasFollowing {
		body["message"] = "This export did not include the list of accounts you follow, " +
			"so there is nothing to compare the followers against."
	}
	s.writeJSON(w, r, http.StatusOK, body)
}

// inPresentationOrder sorts an execution's lists the way they are described
// rather than the way they sort alphabetically, so followers leads.
func inPresentationOrder(totals []store.ListTotals) []store.ListTotals {
	rank := map[string]int{}
	for i, info := range instagram.Lists() {
		rank[string(info.Kind)] = i
	}

	out := make([]store.ListTotals, len(totals))
	copy(out, totals)
	sort.SliceStable(out, func(i, j int) bool {
		ri, oki := rank[out[i].Kind]
		rj, okj := rank[out[j].Kind]
		if oki != okj {
			return oki
		}
		if ri != rj {
			return ri < rj
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}
