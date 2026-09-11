package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func (h *harness) html(path string) (*http.Response, string) {
	h.t.Helper()
	resp, err := h.server.Client().Get(h.server.URL + path)
	if err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read %s: %v", path, err)
	}
	return resp, string(body)
}

// squash collapses runs of whitespace so assertions on rendered prose are not
// sensitive to how the template happens to wrap.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestDashboardRendersWhenEmpty(t *testing.T) {
	h := newHarness(t, 0)

	resp, body := h.html("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Upload a follower export") {
		t.Fatal("the dashboard should always offer the upload form")
	}
}

func TestDashboardListsExecutions(t *testing.T) {
	h := newHarness(t, 0)

	h.upload("acme", "e1.zip", exportZip(t, "alice", "bob"))
	h.drain()
	h.upload("acme", "e2.zip", exportZip(t, "alice", "carol"))
	h.drain()

	_, body := h.html("/?account=acme")

	if strings.Count(body, `class="execution"`) != 2 {
		t.Fatalf("expected two execution rows, got %d", strings.Count(body, `class="execution"`))
	}
	if !strings.Contains(squash(body), "2 processed executions give 1 diff") {
		t.Fatal("the dashboard should explain that n executions give n-1 diffs")
	}
	// A zero removal count must not render as "-0".
	if strings.Contains(body, ">-0<") {
		t.Fatal("a zero unfollow count should not render as -0")
	}
}

// TestDashboardSelectsTheOnlyAccount keeps the single-account case one click
// shorter, which is how most instances will be used.
func TestDashboardSelectsTheOnlyAccount(t *testing.T) {
	h := newHarness(t, 0)

	h.upload("acme", "e1.zip", exportZip(t, "alice"))
	h.drain()

	_, body := h.html("/")
	if !strings.Contains(body, "Executions for acme") {
		t.Fatal("with a single account the dashboard should select it automatically")
	}
}

func TestUploadFormRedirectsBackToTheAccount(t *testing.T) {
	h := newHarness(t, 0)

	client := h.server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { client.CheckRedirect = nil }()

	body, contentType := multipartBody(t, "acme", "e1.zip", exportZip(t, "alice"))
	resp, err := client.Post(h.server.URL+"/upload", contentType, body)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	// A redirect, so reloading the page cannot resubmit the file.
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if !strings.Contains(location, "account=acme") {
		t.Fatalf("Location = %q, want it to carry the account", location)
	}
	if !strings.Contains(location, "flash=") {
		t.Fatalf("Location = %q, want it to carry a confirmation message", location)
	}
}

func TestUploadFormRejectsMissingAccount(t *testing.T) {
	h := newHarness(t, 0)

	client := h.server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { client.CheckRedirect = nil }()

	body, contentType := multipartBody(t, "", "e1.zip", exportZip(t, "alice"))
	resp, err := client.Post(h.server.URL+"/upload", contentType, body)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if !strings.Contains(resp.Header.Get("Location"), "kind=error") {
		t.Fatalf("Location = %q, want an error flash", resp.Header.Get("Location"))
	}
}

func TestDiffPageShowsFourBuckets(t *testing.T) {
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

	resp, body := h.html("/accounts/acme/diff")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	for _, bucket := range []string{"Lost", "Gained", "Returned", "Transient"} {
		if !strings.Contains(body, ">"+bucket+" <span") {
			t.Fatalf("the diff page is missing the %s bucket", bucket)
		}
	}
	// The page must explain why lost is not the sum of the unfollow events.
	if !strings.Contains(squash(body), "Why four lists?") {
		t.Fatal("the diff page should explain the four buckets")
	}
	for _, who := range []string{"dave", "erin", "bob", "carol"} {
		if !strings.Contains(body, ">"+who+"</a>") {
			t.Fatalf("%s should appear on the diff page", who)
		}
	}
}

func TestDiffPageNeedsTwoExecutions(t *testing.T) {
	h := newHarness(t, 0)

	h.upload("acme", "e1.zip", exportZip(t, "alice"))
	h.drain()

	_, body := h.html("/accounts/acme/diff")
	if !strings.Contains(squash(body), "at least two processed executions") {
		t.Fatal("a single execution should explain that a diff needs two")
	}
}

func TestDiffPageForUnknownAccountIs404(t *testing.T) {
	h := newHarness(t, 0)

	resp, _ := h.html("/accounts/nobody/diff")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	h := newHarness(t, 0)

	for path, needle := range map[string]string{
		"/static/app.css": "--accent",
		"/static/app.js":  "loadDetail",
	} {
		resp, body := h.html(path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		if !strings.Contains(body, needle) {
			t.Fatalf("%s does not look like the expected asset", path)
		}
	}
}
