package httpapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/httpapi"
	"github.com/williamokano/insta-follower-tracker/internal/store"
	"github.com/williamokano/insta-follower-tracker/internal/tracker"
)

type harness struct {
	t      *testing.T
	server *httptest.Server
	svc    *tracker.Service
	store  *store.Store
}

func newHarness(t *testing.T, maxUpload int64) *harness {
	t.Helper()
	dir := t.TempDir()

	st, err := store.Open(filepath.Join(dir, "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if maxUpload <= 0 {
		maxUpload = 1 << 20
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc, err := tracker.New(st, tracker.Options{
		UploadDir:      filepath.Join(dir, "uploads"),
		MaxUploadBytes: maxUpload,
		RetainUploads:  true,
		Logger:         quiet,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	srv, err := httpapi.New(svc, httpapi.Options{
		MaxUploadBytes: maxUpload,
		Version:        "test",
		Logger:         quiet,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &harness{t: t, server: ts, svc: svc, store: st}
}

func (h *harness) drain() {
	h.t.Helper()
	for {
		processed, err := h.svc.ProcessNext(context.Background())
		if err != nil {
			h.t.Fatalf("process: %v", err)
		}
		if !processed {
			return
		}
	}
}

func (h *harness) get(path string) (*http.Response, map[string]any) {
	h.t.Helper()
	resp, err := h.server.Client().Get(h.server.URL + path)
	if err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		h.t.Fatalf("decode %s: %v", path, err)
	}
	return resp, body
}

// multipartBody builds the same form the browser upload form submits.
func multipartBody(t *testing.T, account, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("account", account); err != nil {
		t.Fatalf("write account field: %v", err)
	}
	if filename != "" {
		part, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("create file part: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write file part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

// upload posts a multipart form to the JSON API.
func (h *harness) upload(account, filename string, content []byte) (*http.Response, map[string]any) {
	h.t.Helper()

	buf, contentType := multipartBody(h.t, account, filename, content)

	resp, err := h.server.Client().Post(h.server.URL+"/api/uploads", contentType, buf)
	if err != nil {
		h.t.Fatalf("POST /api/uploads: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		h.t.Fatalf("decode upload response: %v", err)
	}
	return resp, body
}

func exportZip(t *testing.T, usernames ...string) []byte {
	t.Helper()

	type item struct {
		Href      string `json:"href"`
		Value     string `json:"value"`
		Timestamp int64  `json:"timestamp"`
	}
	type entry struct {
		StringListData []item `json:"string_list_data"`
	}

	entries := make([]entry, 0, len(usernames))
	for i, u := range usernames {
		entries = append(entries, entry{StringListData: []item{{
			Href: "https://www.instagram.com/" + u, Value: u, Timestamp: int64(1700000000 + i),
		}}})
	}
	payload, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("connections/followers_and_following/followers_1.json")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func namesFrom(t *testing.T, body map[string]any, key string) []string {
	t.Helper()
	raw, ok := body[key].([]any)
	if !ok {
		t.Fatalf("field %q is %T, want a list", key, body[key])
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("entry in %q is %T", key, item)
		}
		out = append(out, entry["username"].(string))
	}
	return out
}

func assertNames(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func TestHealthReportsVersion(t *testing.T) {
	h := newHarness(t, 0)

	resp, body := h.get("/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body["version"] != "test" {
		t.Fatalf("version = %v, want test", body["version"])
	}
}

// TestUploadIsAcceptedBeforeProcessing pins the asynchronous contract: the
// response arrives while the file is still queued.
func TestUploadIsAcceptedBeforeProcessing(t *testing.T) {
	h := newHarness(t, 0)

	resp, body := h.upload("acme", "export.zip", exportZip(t, "alice", "bob"))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); !strings.HasPrefix(got, "/api/uploads/") {
		t.Fatalf("Location = %q", got)
	}

	upload := body["upload"].(map[string]any)
	if upload["status"] != string(store.StatusPending) {
		t.Fatalf("status = %v, want pending", upload["status"])
	}
	if upload["follower_count"].(float64) != 0 {
		t.Fatal("a queued upload must not report follower counts yet")
	}

	h.drain()

	_, after := h.get("/api/uploads/1")
	if after["status"] != string(store.StatusCompleted) {
		t.Fatalf("after processing status = %v (%v)", after["status"], after["error_message"])
	}
	if after["follower_count"].(float64) != 2 {
		t.Fatalf("follower_count = %v, want 2", after["follower_count"])
	}
}

func TestUploadValidation(t *testing.T) {
	h := newHarness(t, 0)

	t.Run("missing account", func(t *testing.T) {
		resp, body := h.upload("", "export.zip", exportZip(t, "alice"))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		if !strings.Contains(body["error"].(string), "account handle") {
			t.Fatalf("error = %v", body["error"])
		}
	})

	t.Run("missing file", func(t *testing.T) {
		resp, body := h.upload("acme", "", nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		if !strings.Contains(body["error"].(string), "file is required") {
			t.Fatalf("error = %v", body["error"])
		}
	})

	t.Run("oversized file", func(t *testing.T) {
		small := newHarness(t, 512)
		resp, _ := small.upload("acme", "big.zip", bytes.Repeat([]byte("x"), 4096))
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", resp.StatusCode)
		}
	})
}

func TestUnparseableUploadFailsAsynchronously(t *testing.T) {
	h := newHarness(t, 0)

	resp, _ := h.upload("acme", "notes.json", []byte(`{"nothing":"here"}`))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: intake should not parse", resp.StatusCode)
	}

	h.drain()

	_, body := h.get("/api/uploads/1")
	if body["status"] != string(store.StatusFailed) {
		t.Fatalf("status = %v, want failed", body["status"])
	}
	if !strings.Contains(body["error_message"].(string), "no follower list") {
		t.Fatalf("error_message = %v", body["error_message"])
	}
}

func TestExecutionChangesAndBaseline(t *testing.T) {
	h := newHarness(t, 0)

	h.upload("acme", "e1.zip", exportZip(t, "alice", "bob"))
	h.drain()
	h.upload("acme", "e2.zip", exportZip(t, "alice", "carol"))
	h.drain()

	_, baseline := h.get("/api/uploads/1/changes")
	if baseline["is_baseline"] != true {
		t.Fatal("the first execution should be reported as the baseline")
	}
	assertNames(t, "baseline followed", namesFrom(t, baseline, "followed"), nil)

	_, second := h.get("/api/uploads/2/changes")
	assertNames(t, "followed", namesFrom(t, second, "followed"), []string{"carol"})
	assertNames(t, "unfollowed", namesFrom(t, second, "unfollowed"), []string{"bob"})

	_, filtered := h.get("/api/uploads/2/changes?type=unfollowed")
	assertNames(t, "filtered followed", namesFrom(t, filtered, "followed"), nil)
	assertNames(t, "filtered unfollowed", namesFrom(t, filtered, "unfollowed"), []string{"bob"})

	resp, _ := h.get("/api/uploads/2/changes?type=sideways")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown change type", resp.StatusCode)
	}
}

// TestDiffEndpointSeparatesReturningFollowers is the API-level assertion of the
// caveat that motivates the whole diff design.
func TestDiffEndpointSeparatesReturningFollowers(t *testing.T) {
	h := newHarness(t, 0)

	for _, snapshot := range [][]string{
		{"alice", "bob", "dave"},
		{"alice", "carol", "dave"},
		{"alice"},
		{"alice", "bob", "erin"},
	} {
		h.upload("acme", "export.zip", exportZip(t, snapshot...))
		h.drain()
	}

	resp, body := h.get("/api/accounts/acme/diff")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	assertNames(t, "lost", namesFrom(t, body, "lost"), []string{"dave"})
	assertNames(t, "gained", namesFrom(t, body, "gained"), []string{"erin"})
	assertNames(t, "returned", namesFrom(t, body, "returned"), []string{"bob"})
	assertNames(t, "transient", namesFrom(t, body, "transient"), []string{"carol"})

	// The raw event log over-reports on purpose, and says so.
	_, raw := h.get("/api/accounts/acme/unfollowers")
	if raw["count"].(float64) != 3 {
		t.Fatalf("raw unfollow events = %v, want 3", raw["count"])
	}
	if !strings.Contains(raw["note"].(string), "followed again") {
		t.Fatalf("the raw log should warn about re-followers, got %v", raw["note"])
	}
}

func TestDiffAcceptsExplicitBoundsAndNormalisesOrder(t *testing.T) {
	h := newHarness(t, 0)

	for _, snapshot := range [][]string{{"alice"}, {"alice", "bob"}, {"bob"}} {
		h.upload("acme", "export.zip", exportZip(t, snapshot...))
		h.drain()
	}

	// Bounds given backwards should be interpreted in execution order rather
	// than inverting the meaning of lost and gained.
	_, body := h.get("/api/accounts/acme/diff?from=3&to=1")
	assertNames(t, "lost", namesFrom(t, body, "lost"), []string{"alice"})
	assertNames(t, "gained", namesFrom(t, body, "gained"), []string{"bob"})

	resp, _ := h.get("/api/accounts/acme/diff?from=nonsense")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	resp, _ = h.get("/api/accounts/acme/diff?from=999")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown execution", resp.StatusCode)
	}
}

func TestAccountListingAndFollowers(t *testing.T) {
	h := newHarness(t, 0)

	h.upload("Acme", "e1.zip", exportZip(t, "alice", "bob"))
	h.drain()
	h.upload("acme", "e2.zip", exportZip(t, "bob", "carol"))
	h.drain()

	_, accounts := h.get("/api/accounts")
	list := accounts["accounts"].([]any)
	if len(list) != 1 {
		t.Fatalf("accounts = %d, want 1: handles differing only by case are the same account", len(list))
	}
	summary := list[0].(map[string]any)
	if summary["handle"] != "acme" {
		t.Fatalf("handle = %v, want acme", summary["handle"])
	}
	if summary["follower_count"].(float64) != 2 {
		t.Fatalf("follower_count = %v, want 2", summary["follower_count"])
	}

	_, followers := h.get("/api/accounts/acme/followers")
	assertNames(t, "current followers", namesFrom(t, followers, "followers"), []string{"bob", "carol"})

	_, uploads := h.get("/api/accounts/acme/uploads")
	if len(uploads["uploads"].([]any)) != 2 {
		t.Fatalf("uploads = %v, want 2", uploads["uploads"])
	}
}

func TestUnknownResourcesReturn404(t *testing.T) {
	h := newHarness(t, 0)

	for _, path := range []string{
		"/api/accounts/nobody/uploads",
		"/api/accounts/nobody/diff",
		"/api/accounts/nobody/followers",
		"/api/accounts/nobody/unfollowers",
		"/api/uploads/4242",
	} {
		resp, _ := h.get(path)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestDiffBeforeAnyExecutionIsProcessed(t *testing.T) {
	h := newHarness(t, 0)

	h.upload("acme", "e1.zip", exportZip(t, "alice"))
	// Deliberately not drained: the account exists but has nothing processed.

	resp, body := h.get("/api/accounts/acme/diff")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if !strings.Contains(body["error"].(string), "no processed executions") {
		t.Fatalf("error = %v", body["error"])
	}
}

func TestConcurrentUploadsAreAllQueued(t *testing.T) {
	h := newHarness(t, 0)

	const n = 8
	done := make(chan struct{}, n)
	for i := range n {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			h.upload("acme", "export.zip", exportZip(t, "alice", "user"+string(rune('a'+i))))
		}(i)
	}
	for range n {
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("upload did not complete in time")
		}
	}

	pending, err := h.store.PendingCount(context.Background())
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if pending != n {
		t.Fatalf("pending = %d, want %d", pending, n)
	}

	h.drain()

	acc, err := h.store.AccountByHandle(context.Background(), "acme")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	uploads, err := h.store.ListUploads(context.Background(), acc.ID)
	if err != nil {
		t.Fatalf("uploads: %v", err)
	}

	seq := map[int64]bool{}
	for _, up := range uploads {
		if up.Status != store.StatusCompleted {
			t.Fatalf("upload %d is %q (%s)", up.ID, up.Status, up.ErrorMessage)
		}
		if up.SequenceNo == nil {
			t.Fatalf("upload %d has no sequence number", up.ID)
		}
		if seq[*up.SequenceNo] {
			t.Fatalf("sequence number %d was assigned twice", *up.SequenceNo)
		}
		seq[*up.SequenceNo] = true
	}
	if len(seq) != n {
		t.Fatalf("distinct sequence numbers = %d, want %d", len(seq), n)
	}
}

// TestFirstUploadExposesItsFollowerList is the gap this closes: before, a
// single upload gave you a count and nothing else.
func TestFirstUploadExposesItsFollowerList(t *testing.T) {
	h := newHarness(t, 0)

	h.upload("acme", "e1.zip", exportZip(t, "alice", "bob", "carol"))
	h.drain()

	resp, body := h.get("/api/uploads/1/followers")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body["count"].(float64) != 3 {
		t.Fatalf("count = %v, want 3", body["count"])
	}
	assertNames(t, "followers", namesFrom(t, body, "followers"), []string{"alice", "bob", "carol"})

	// The execution is still reported as the baseline; having a list to show
	// does not make it comparable to anything.
	_, changes := h.get("/api/uploads/1/changes")
	if changes["is_baseline"] != true {
		t.Fatal("the first execution is still the baseline")
	}
}

// TestEveryExecutionExposesItsOwnList checks the list is per-execution rather
// than always the newest one.
func TestEveryExecutionExposesItsOwnList(t *testing.T) {
	h := newHarness(t, 0)

	h.upload("acme", "e1.zip", exportZip(t, "alice", "bob"))
	h.drain()
	h.upload("acme", "e2.zip", exportZip(t, "alice", "carol", "dave"))
	h.drain()

	_, first := h.get("/api/uploads/1/followers")
	assertNames(t, "first execution", namesFrom(t, first, "followers"), []string{"alice", "bob"})

	_, second := h.get("/api/uploads/2/followers")
	assertNames(t, "second execution", namesFrom(t, second, "followers"), []string{"alice", "carol", "dave"})

	// The account-level endpoint still answers with the latest.
	_, current := h.get("/api/accounts/acme/followers")
	assertNames(t, "current", namesFrom(t, current, "followers"), []string{"alice", "carol", "dave"})
}

func TestFollowerListForUnknownExecutionIs404(t *testing.T) {
	h := newHarness(t, 0)

	resp, _ := h.get("/api/uploads/999/followers")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestFollowerListOfARefusedExecutionIsEmpty: a refused upload records no
// membership, and asking for it must not fail.
func TestFollowerListOfARefusedExecutionIsEmpty(t *testing.T) {
	h := newHarness(t, 0)

	h.upload("acme", "bad.json", []byte(`{"nothing":"here"}`))
	h.drain()

	resp, body := h.get("/api/uploads/1/followers")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body["count"].(float64) != 0 {
		t.Fatalf("count = %v, want 0", body["count"])
	}
}
