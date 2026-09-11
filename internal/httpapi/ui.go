package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/williamokano/insta-follower-tracker/internal/store"
	"github.com/williamokano/insta-follower-tracker/internal/tracker"
	"github.com/williamokano/insta-follower-tracker/internal/web"
)

// pageData is the view model shared by both pages.
type pageData struct {
	Page      string
	Title     string
	Version   string
	Flash     string
	FlashKind string

	Accounts []store.AccountSummary
	Account  *store.Account

	// Dashboard.
	Uploads        []store.Upload
	CompletedCount int
	DiffCount      int

	// Diff page.
	Executions []store.Upload
	Diff       *store.NetDiff
}

// uiRoutes registers the web interface. It is defined separately so the JSON
// API can be served without it.
func (s *Server) uiRoutes() error {
	templates, err := web.Templates()
	if err != nil {
		return err
	}
	s.templates = templates

	static, err := web.StaticHandler()
	if err != nil {
		return err
	}

	s.mux.Handle("GET /static/", http.StripPrefix("/static/", static))
	s.mux.HandleFunc("GET /{$}", s.handleDashboard)
	s.mux.HandleFunc("POST /upload", s.handleUploadForm)
	s.mux.HandleFunc("GET /accounts/{handle}/diff", s.handleDiffPage)

	return nil
}

// render writes a page, buffering first so a template failure cannot leave a
// half-written response behind.
func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, data pageData) {
	tmpl, ok := s.templates[page]
	if !ok {
		s.writeError(w, r, http.StatusInternalServerError, fmt.Errorf("unknown page %q", page))
		return
	}
	data.Version = s.opts.Version

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.log.Error("rendering page failed", "page", page, "error", err)
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = buf.WriteTo(w)
}

// handleDashboard renders the upload form and the execution history of the
// selected account.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()

	data := pageData{
		Page:      "index",
		Title:     "Executions",
		Flash:     query.Get("flash"),
		FlashKind: query.Get("kind"),
	}

	accounts, err := s.svc.Store().ListAccounts(ctx)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.Accounts = accounts

	// Fall back to the only account when the user has not chosen one, which is
	// the common single-account case.
	handle := store.NormalizeHandle(query.Get("account"))
	if handle == "" && len(accounts) == 1 {
		handle = accounts[0].Handle
	}
	if handle == "" {
		s.render(w, r, "index.html", data)
		return
	}

	account, err := s.svc.Store().AccountByHandle(ctx, handle)
	if errors.Is(err, store.ErrNotFound) {
		data.Flash = "There is no account named " + handle + " yet."
		data.FlashKind = "error"
		s.render(w, r, "index.html", data)
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.Account = &account
	data.Title = account.Handle

	uploads, err := s.svc.Store().ListUploads(ctx, account.ID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.Uploads = uploads

	for _, up := range uploads {
		if up.Status == store.StatusCompleted {
			data.CompletedCount++
		}
	}
	// The first execution is a baseline with nothing before it, so n executions
	// yield n-1 diffs.
	if data.CompletedCount > 0 {
		data.DiffCount = data.CompletedCount - 1
	}

	s.render(w, r, "index.html", data)
}

// handleUploadForm backs the browser form. It redirects afterwards so a reload
// does not resubmit the file, and so the queued upload is visible immediately.
func (s *Server) handleUploadForm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.opts.MaxUploadBytes+(1<<20))

	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.redirectWithFlash(w, r, "", tracker.ErrUploadTooLarge.Error(), "error")
			return
		}
		s.redirectWithFlash(w, r, "", "The upload could not be read: "+err.Error(), "error")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	// Left blank, the account is read from the export.
	handle := store.NormalizeHandle(r.FormValue("account"))

	file, header, err := r.FormFile("file")
	if err != nil {
		s.redirectWithFlash(w, r, handle, "Choose an export file to upload.", "error")
		return
	}
	defer file.Close()

	filename := "upload"
	if header != nil && header.Filename != "" {
		filename = header.Filename
	}

	snapshotDate, err := formDate(r, "snapshot_date")
	if err != nil {
		s.redirectWithFlash(w, r, handle, err.Error(), "error")
		return
	}

	upload, err := s.svc.Accept(r.Context(), handle, filename, file, tracker.AcceptOptions{
		AllowPartial: formFlag(r, "allow_partial"),
		SnapshotDate: snapshotDate,
	})
	if err != nil {
		s.redirectWithFlash(w, r, handle, "Upload failed: "+err.Error(), "error")
		return
	}

	s.redirectWithFlash(w, r, handle,
		fmt.Sprintf("%s was uploaded and queued. Processing runs in the background.", upload.OriginalFilename),
		"")
}

func (s *Server) redirectWithFlash(w http.ResponseWriter, r *http.Request, handle, flash, kind string) {
	target := url.URL{Path: "/"}
	query := url.Values{}
	if handle != "" {
		query.Set("account", handle)
	}
	if flash != "" {
		query.Set("flash", flash)
	}
	if kind != "" {
		query.Set("kind", kind)
	}
	target.RawQuery = query.Encode()

	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

// handleDiffPage renders the first-to-last comparison, which is the view that
// answers "who is not following me anymore" correctly.
func (s *Server) handleDiffPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	handle := store.NormalizeHandle(r.PathValue("handle"))
	account, err := s.svc.Store().AccountByHandle(ctx, handle)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	data := pageData{
		Page:    "diff",
		Title:   "Overall diff",
		Account: &account,
	}

	accounts, err := s.svc.Store().ListAccounts(ctx)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.Accounts = accounts

	uploads, err := s.svc.Store().ListUploads(ctx, account.ID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	// Oldest first, so the selects read in execution order.
	for i := len(uploads) - 1; i >= 0; i-- {
		if uploads[i].Status == store.StatusCompleted {
			data.Executions = append(data.Executions, uploads[i])
		}
	}

	if len(data.Executions) < 2 {
		s.render(w, r, "diff.html", data)
		return
	}

	query := r.URL.Query()
	from, err := s.svc.ResolveUpload(ctx, account.ID, query.Get("from"), false)
	if err != nil {
		data.Flash = "Could not read the starting execution: " + err.Error()
		data.FlashKind = "error"
		from, err = s.svc.ResolveUpload(ctx, account.ID, "first", false)
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, err)
			return
		}
	}

	to, err := s.svc.ResolveUpload(ctx, account.ID, query.Get("to"), true)
	if err != nil {
		data.Flash = "Could not read the ending execution: " + err.Error()
		data.FlashKind = "error"
		to, err = s.svc.ResolveUpload(ctx, account.ID, "last", true)
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, err)
			return
		}
	}

	if from.SequenceNo != nil && to.SequenceNo != nil && *from.SequenceNo > *to.SequenceNo {
		from, to = to, from
	}

	diff, err := s.svc.Store().Diff(ctx, account.ID, from, to)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.Diff = &diff

	s.render(w, r, "diff.html", data)
}
