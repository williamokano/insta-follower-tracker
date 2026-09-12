package tracker_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/store"
	"github.com/williamokano/insta-follower-tracker/internal/tracker"
)

func newService(t *testing.T, opts ...func(*tracker.Options)) (*tracker.Service, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()

	st, err := store.Open(filepath.Join(dir, "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	o := tracker.Options{
		UploadDir:      filepath.Join(dir, "uploads"),
		MaxUploadBytes: 1 << 20,
		RetainUploads:  true,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollInterval:   10 * time.Millisecond,
	}
	for _, fn := range opts {
		fn(&o)
	}

	svc, err := tracker.New(st, o)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, st, o.UploadDir
}

// exportJSON renders a follower list in the shape Instagram emits.
func exportJSON(t *testing.T, usernames ...string) []byte {
	t.Helper()
	type item struct {
		Href      string `json:"href"`
		Value     string `json:"value"`
		Timestamp int64  `json:"timestamp"`
	}
	type entry struct {
		Title          string `json:"title"`
		StringListData []item `json:"string_list_data"`
	}

	entries := make([]entry, 0, len(usernames))
	for i, u := range usernames {
		entries = append(entries, entry{StringListData: []item{{
			Href:      "https://www.instagram.com/" + u,
			Value:     u,
			Timestamp: int64(1700000000 + i),
		}}})
	}

	body, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	return body
}

// exportZip wraps a follower list in an archive shaped like a real export.
func exportZip(t *testing.T, usernames ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	w, err := zw.Create("connections/followers_and_following/followers_1.json")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write(exportJSON(t, usernames...)); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// upload accepts a file and runs the worker until the queue is empty, so the
// test observes the same end state the background worker would produce.
func upload(t *testing.T, svc *tracker.Service, handle, filename string, body []byte) store.Upload {
	t.Helper()
	return uploadWith(t, svc, handle, filename, body, tracker.AcceptOptions{})
}

// uploadWith is upload with explicit intake options.
func uploadWith(t *testing.T, svc *tracker.Service, handle, filename string, body []byte, opts tracker.AcceptOptions) store.Upload {
	t.Helper()
	ctx := context.Background()

	up, err := svc.Accept(ctx, handle, filename, bytes.NewReader(body), opts)
	if err != nil {
		t.Fatalf("accept %s: %v", filename, err)
	}
	if up.Status != store.StatusPending {
		t.Fatalf("accept returned status %q, want pending", up.Status)
	}

	for {
		processed, err := svc.ProcessNext(ctx)
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if !processed {
			break
		}
	}

	final, err := svc.Store().Upload(ctx, up.ID)
	if err != nil {
		t.Fatalf("reload upload: %v", err)
	}
	return final
}

func assertNames(t *testing.T, what string, got []store.Follower, want ...string) {
	t.Helper()
	names := make([]string, 0, len(got))
	for _, f := range got {
		names = append(names, f.Username)
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("%s = %v, want %v", what, names, want)
	}
}

func TestAcceptQueuesWithoutParsing(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	// Deliberately not a valid export: Accept must still succeed, because
	// parsing is the worker's job and happens after the response.
	up, err := svc.Accept(ctx, "acme", "garbage.json", strings.NewReader("not an export"), tracker.AcceptOptions{})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if up.Status != store.StatusPending {
		t.Fatalf("status = %q, want pending", up.Status)
	}

	pending, err := st.PendingCount(ctx)
	if err != nil {
		t.Fatalf("pending count: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending = %d, want 1", pending)
	}

	// Only once the worker runs does the file get rejected.
	if _, err := svc.ProcessNext(ctx); err != nil {
		t.Fatalf("process: %v", err)
	}
	final, err := st.Upload(ctx, up.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if final.Status != store.StatusFailed {
		t.Fatalf("status = %q, want failed", final.Status)
	}
	if final.ErrorMessage == "" {
		t.Fatal("a failed upload should explain why")
	}
}

func TestFirstUploadIsBaseline(t *testing.T) {
	svc, _, _ := newService(t)

	up := upload(t, svc, "acme", "export.zip", exportZip(t, "alice", "bob"))

	if up.Status != store.StatusCompleted {
		t.Fatalf("status = %q (%s), want completed", up.Status, up.ErrorMessage)
	}
	if !up.IsBaseline {
		t.Fatal("the first execution should be flagged as the baseline")
	}
	if up.FollowerCount != 2 || up.AddedCount != 0 || up.RemovedCount != 0 {
		t.Fatalf("counts = %d/+%d/-%d, want 2/+0/-0", up.FollowerCount, up.AddedCount, up.RemovedCount)
	}
}

// TestTenExecutionsProduceNineDiffs checks the headline requirement directly.
func TestTenExecutionsProduceNineDiffs(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	for i := range 10 {
		// Each execution swaps one follower, so every diff is non-empty.
		upload(t, svc, "acme", "export.zip", exportZip(t, "alice", "follower"+string(rune('a'+i))))
	}

	acc, err := st.AccountByHandle(ctx, "acme")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	uploads, err := st.ListUploads(ctx, acc.ID)
	if err != nil {
		t.Fatalf("list uploads: %v", err)
	}
	if len(uploads) != 10 {
		t.Fatalf("uploads = %d, want 10", len(uploads))
	}

	withDiffs := 0
	for _, up := range uploads {
		if up.Status != store.StatusCompleted {
			t.Fatalf("upload %d is %q", up.ID, up.Status)
		}
		if !up.IsBaseline {
			withDiffs++
		}
	}
	if withDiffs != 9 {
		t.Fatalf("executions carrying a diff = %d, want 9", withDiffs)
	}
}

// TestOverallDiffDistinguishesReturningFollowers is the end-to-end version of
// the caveat: somebody who unfollows and later follows again must not be
// reported as lost, even though the raw event log records their unfollow.
func TestOverallDiffDistinguishesReturningFollowers(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	upload(t, svc, "acme", "e1.zip", exportZip(t, "alice", "bob", "dave"))
	upload(t, svc, "acme", "e2.zip", exportZip(t, "alice", "carol", "dave"))
	upload(t, svc, "acme", "e3.zip", exportZip(t, "alice"))
	upload(t, svc, "acme", "e4.zip", exportZip(t, "alice", "bob", "erin"))

	acc, err := st.AccountByHandle(ctx, "acme")
	if err != nil {
		t.Fatalf("account: %v", err)
	}

	first, err := svc.ResolveUpload(ctx, acc.ID, "first", false)
	if err != nil {
		t.Fatalf("resolve first: %v", err)
	}
	last, err := svc.ResolveUpload(ctx, acc.ID, "", true)
	if err != nil {
		t.Fatalf("resolve last: %v", err)
	}

	diff, err := st.Diff(ctx, acc.ID, first, last, "")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}

	assertNames(t, "lost", diff.Lost, "dave")
	assertNames(t, "gained", diff.Gained, "erin")
	assertNames(t, "returned", diff.Returned, "bob")
	assertNames(t, "transient", diff.Transient, "carol")

	raw, err := st.AllUnfollowers(ctx, acc.ID, "")
	if err != nil {
		t.Fatalf("raw unfollowers: %v", err)
	}
	if len(raw) != 3 {
		t.Fatalf("raw unfollow events = %d, want 3 (bob, carol, dave)", len(raw))
	}
	if len(diff.Lost) >= len(raw) {
		t.Fatal("the raw event log must over-report compared to the net diff")
	}
}

func TestBareJSONUploadIsAccepted(t *testing.T) {
	svc, _, _ := newService(t)

	up := upload(t, svc, "acme", "followers_1.json", exportJSON(t, "alice", "bob"))
	if up.Status != store.StatusCompleted {
		t.Fatalf("status = %q (%s), want completed", up.Status, up.ErrorMessage)
	}
	if up.FollowerCount != 2 {
		t.Fatalf("followers = %d, want 2", up.FollowerCount)
	}
}

func TestUploadSizeCapIsEnforced(t *testing.T) {
	svc, st, uploadDir := newService(t, func(o *tracker.Options) { o.MaxUploadBytes = 32 })
	ctx := context.Background()

	_, err := svc.Accept(ctx, "acme", "big.json", bytes.NewReader(bytes.Repeat([]byte("x"), 1024)), tracker.AcceptOptions{})
	if err == nil {
		t.Fatal("expected an oversized upload to be rejected")
	}

	// The partial file must not be left behind, and no queue row should exist.
	entries, err := os.ReadDir(filepath.Join(uploadDir, "acme"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read upload dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected upload left %d file(s) on disk", len(entries))
	}

	pending, err := st.PendingCount(ctx)
	if err != nil {
		t.Fatalf("pending count: %v", err)
	}
	if pending != 0 {
		t.Fatalf("rejected upload queued %d row(s)", pending)
	}
}

func TestEmptyUploadIsRejected(t *testing.T) {
	svc, _, _ := newService(t)

	if _, err := svc.Accept(context.Background(), "acme", "empty.json", bytes.NewReader(nil), tracker.AcceptOptions{}); err == nil {
		t.Fatal("expected an empty upload to be rejected")
	}
}

func TestAccountHandleIsRequired(t *testing.T) {
	svc, _, _ := newService(t)

	if _, err := svc.Accept(context.Background(), "  ", "e.json", bytes.NewReader(exportJSON(t, "alice")), tracker.AcceptOptions{}); err == nil {
		t.Fatal("expected a blank account handle to be rejected")
	}
}

func TestUploadedFilenameCannotEscapeTheUploadDirectory(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	up, err := svc.Accept(ctx, "acme", "../../../../etc/passwd", bytes.NewReader(exportJSON(t, "alice")), tracker.AcceptOptions{})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	stored, err := st.Upload(ctx, up.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if strings.Contains(stored.OriginalFilename, "/") || strings.Contains(stored.OriginalFilename, "..") {
		t.Fatalf("recorded filename %q still contains path components", stored.OriginalFilename)
	}
}

func TestRunProcessesQueuedUploadsInBackground(t *testing.T) {
	svc, st, _ := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	up, err := svc.Accept(ctx, "acme", "e1.zip", bytes.NewReader(exportZip(t, "alice", "bob")), tracker.AcceptOptions{})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		current, err := st.Upload(ctx, up.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if current.Status == store.StatusCompleted {
			break
		}
		if current.Status == store.StatusFailed {
			t.Fatalf("background processing failed: %s", current.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("upload stayed %q; the worker never picked it up", current.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestRunRequeuesUploadsInterruptedByRestart(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()
	acc, err := st.EnsureAccount(ctx, "acme")
	if err != nil {
		t.Fatalf("ensure account: %v", err)
	}

	// Simulate a crash: an upload left in the processing state with no worker.
	path := filepath.Join(t.TempDir(), "orphan.json")
	if err := os.WriteFile(path, exportJSON(t, "alice"), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	id, err := st.CreateUpload(ctx, store.NewUpload{
		AccountID: acc.ID, Filename: "orphan.json", StoredPath: path, SHA256: "sha", SizeBytes: 10,
	})
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	if _, err := st.ClaimNextPending(ctx); err != nil {
		t.Fatalf("claim: %v", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.Run(runCtx) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		current, err := st.Upload(ctx, id)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if current.Status == store.StatusCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphaned upload stayed %q instead of being requeued", current.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	<-done
}

func TestResolveUploadReferences(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	first := upload(t, svc, "acme", "e1.zip", exportZip(t, "alice"))
	upload(t, svc, "acme", "e2.zip", exportZip(t, "alice", "bob"))
	last := upload(t, svc, "acme", "e3.zip", exportZip(t, "bob"))

	acc, _ := st.AccountByHandle(ctx, "acme")

	cases := map[string]int64{
		"first":  first.ID,
		"oldest": first.ID,
		"last":   last.ID,
		"latest": last.ID,
	}
	for ref, want := range cases {
		got, err := svc.ResolveUpload(ctx, acc.ID, ref, false)
		if err != nil {
			t.Fatalf("resolve %q: %v", ref, err)
		}
		if got.ID != want {
			t.Fatalf("resolve %q = %d, want %d", ref, got.ID, want)
		}
	}

	byID, err := svc.ResolveUpload(ctx, acc.ID, "2", false)
	if err != nil {
		t.Fatalf("resolve by id: %v", err)
	}
	if byID.ID != 2 {
		t.Fatalf("resolve by id = %d, want 2", byID.ID)
	}

	if _, err := svc.ResolveUpload(ctx, acc.ID, "nonsense", false); err == nil {
		t.Fatal("expected an invalid reference to be rejected")
	}
}
