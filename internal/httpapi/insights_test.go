package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// uploadHistory uploads and processes one export per list of followers, in
// order.
func uploadHistory(h *harness, account string, history ...[]string) {
	h.t.Helper()
	for i, names := range history {
		h.upload(account, "e"+string(rune('1'+i))+".zip", exportZip(h.t, names...))
		h.drain()
	}
}

func TestInsightsEndpoint(t *testing.T) {
	h := newHarness(t, 0)
	uploadHistory(h, "acme",
		[]string{"alice", "bob"},
		[]string{"alice"},
		[]string{"alice", "bob"},
		[]string{"alice"},
	)

	resp, body := h.get("/api/accounts/acme/insights")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", resp.StatusCode, body)
	}
	insights, ok := body["insights"].(map[string]any)
	if !ok {
		t.Fatalf("insights is %T", body["insights"])
	}
	assertNames(t, "repeat leavers", namesFrom(t, insights, "repeat_leavers"), []string{"bob"})
	if insights["executions"].(float64) != 4 {
		t.Errorf("executions = %v, want 4", insights["executions"])
	}
	// Only the follower list was uploaded, so nothing else can be claimed.
	if insights["following_recorded"] != false || insights["pending_recorded"] != false {
		t.Errorf("lists that were never uploaded are reported as recorded: %v", insights)
	}
	if insights["quick_unfollow_days"].(float64) != 30 {
		t.Errorf("default window = %v, want 30", insights["quick_unfollow_days"])
	}
}

func TestInsightsEndpointValidatesTheWindow(t *testing.T) {
	h := newHarness(t, 0)
	uploadHistory(h, "acme", []string{"alice"})

	for _, days := range []string{"0", "366", "soon"} {
		resp, _ := h.get("/api/accounts/acme/insights?days=" + days)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("days=%s: status = %d, want 400", days, resp.StatusCode)
		}
	}
	if resp, body := h.get("/api/accounts/acme/insights?days=7"); resp.StatusCode != http.StatusOK {
		t.Errorf("days=7: status = %d: %v", resp.StatusCode, body)
	}
}

func TestInsightsEndpointForAnUnknownAccount(t *testing.T) {
	h := newHarness(t, 0)
	if resp, _ := h.get("/api/accounts/nobody/insights"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestInsightsPage(t *testing.T) {
	h := newHarness(t, 0)
	uploadHistory(h, "acme",
		[]string{"alice", "bob"},
		[]string{"alice"},
		[]string{"alice", "bob"},
		[]string{"alice"},
	)

	resp, body := h.html("/accounts/acme/insights")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	text := squash(body)
	for _, want := range []string{
		"Insights for acme",
		`href="/accounts/acme/insights" class="active"`,
		"Left more than once",
		"https://www.instagram.com/bob",
		// The follower list alone cannot support the mutual-based sections.
		"None of these exports included the list of accounts you follow",
		"The latest export did not include this list.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

func TestInsightsPageSurvivesABadWindow(t *testing.T) {
	h := newHarness(t, 0)
	uploadHistory(h, "acme", []string{"alice"})

	resp, body := h.html("/accounts/acme/insights?days=nope")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "days must be a whole number") {
		t.Fatal("a bad window should be explained, not fail the page")
	}
}

func TestInsightsPageForAnUnknownAccount(t *testing.T) {
	h := newHarness(t, 0)
	if resp, _ := h.html("/accounts/nobody/insights"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
