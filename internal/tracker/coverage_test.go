package tracker_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/williamokano/insta-follower-tracker/internal/store"
	"github.com/williamokano/insta-follower-tracker/internal/tracker"
)

// manyFollowers names n distinct accounts, so the collapse check has an
// account large enough to be judged.
func manyFollowers(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("member%03d", i))
	}
	return out
}

// TestCollapseInFollowerCountIsRefused is the backstop that works on every
// export format, including the older ones that declare no date range.
//
// It reproduces the real incident: a full list of hundreds followed by a
// date-limited download holding a few dozen. Recorded as-is that would report
// an unfollow for everybody missing from the window.
func TestCollapseInFollowerCountIsRefused(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	upload(t, svc, "acme", "full.zip", exportZip(t, manyFollowers(400)...))
	windowed := upload(t, svc, "acme", "windowed.zip", exportZip(t, manyFollowers(25)...))

	if windowed.Status != store.StatusFailed {
		t.Fatalf("status = %q, want failed: a 94%% fall is not a real loss", windowed.Status)
	}
	for _, want := range []string{"25", "400", "date range"} {
		if !strings.Contains(windowed.ErrorMessage, want) {
			t.Fatalf("message should mention %q, got: %s", want, windowed.ErrorMessage)
		}
	}

	// The good snapshot must remain the account's current state.
	acc, err := st.AccountByHandle(ctx, "acme")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	current, err := st.CurrentFollowers(ctx, acc.ID)
	if err != nil {
		t.Fatalf("current followers: %v", err)
	}
	if len(current) != 400 {
		t.Fatalf("current followers = %d, want the 400 from the good export", len(current))
	}
}

// TestRefusedExportRecordsNoChanges is the point of the whole guard: a refused
// upload must leave no phantom unfollows behind.
func TestRefusedExportRecordsNoChanges(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	upload(t, svc, "acme", "full.zip", exportZip(t, manyFollowers(400)...))
	windowed := upload(t, svc, "acme", "windowed.zip", exportZip(t, manyFollowers(25)...))

	changes, err := st.ChangesForUpload(ctx, windowed.ID, "", "")
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("a refused export recorded %d changes; it must record none", len(changes))
	}

	acc, _ := st.AccountByHandle(ctx, "acme")
	unfollowers, err := st.AllUnfollowers(ctx, acc.ID, "")
	if err != nil {
		t.Fatalf("unfollowers: %v", err)
	}
	if len(unfollowers) != 0 {
		t.Fatalf("guard let %d phantom unfollows through", len(unfollowers))
	}
}

// TestPartialOverrideIsHonoured keeps the decision with the person uploading:
// the guard refuses by default but never becomes a wall.
func TestPartialOverrideIsHonoured(t *testing.T) {
	svc, _, _ := newService(t)

	upload(t, svc, "acme", "full.zip", exportZip(t, manyFollowers(400)...))
	forced := uploadWith(t, svc, "acme", "windowed.zip", exportZip(t, manyFollowers(25)...),
		tracker.AcceptOptions{AllowPartial: true})

	if forced.Status != store.StatusCompleted {
		t.Fatalf("status = %q (%s), want completed with the override set",
			forced.Status, forced.ErrorMessage)
	}
	if forced.RemovedCount != 375 {
		t.Fatalf("removed = %d, want 375: the override means the drop is taken at face value",
			forced.RemovedCount)
	}
}

// TestOrdinaryLossIsNotRefused guards against the check being so eager that
// normal churn trips it.
func TestOrdinaryLossIsNotRefused(t *testing.T) {
	svc, _, _ := newService(t)

	upload(t, svc, "acme", "e1.zip", exportZip(t, manyFollowers(400)...))
	shrunk := upload(t, svc, "acme", "e2.zip", exportZip(t, manyFollowers(340)...))

	if shrunk.Status != store.StatusCompleted {
		t.Fatalf("status = %q (%s): losing 15%% of followers is ordinary",
			shrunk.Status, shrunk.ErrorMessage)
	}
	if shrunk.RemovedCount != 60 {
		t.Fatalf("removed = %d, want 60", shrunk.RemovedCount)
	}
}

// TestSmallAccountsAreNotJudgedOnCollapse: proportional swings are meaningless
// when the numbers are tiny.
func TestSmallAccountsAreNotJudgedOnCollapse(t *testing.T) {
	svc, _, _ := newService(t)

	upload(t, svc, "acme", "e1.zip", exportZip(t, "alice", "bob", "carol", "dave"))
	shrunk := upload(t, svc, "acme", "e2.zip", exportZip(t, "alice"))

	if shrunk.Status != store.StatusCompleted {
		t.Fatalf("status = %q (%s): four followers down to one is not evidence of anything",
			shrunk.Status, shrunk.ErrorMessage)
	}
}

// TestFirstExportIsNotJudgedOnCollapse: there is nothing to compare a baseline
// against, so the declared range is the only signal that can apply.
func TestFirstExportIsNotJudgedOnCollapse(t *testing.T) {
	svc, _, _ := newService(t)

	first := upload(t, svc, "acme", "e1.zip", exportZip(t, manyFollowers(30)...))
	if first.Status != store.StatusCompleted {
		t.Fatalf("status = %q (%s), want completed", first.Status, first.ErrorMessage)
	}
}

// TestRefusedExportCanBeRetried: the guard rejects the file, not the account.
func TestRefusedExportCanBeRetried(t *testing.T) {
	svc, _, _ := newService(t)

	upload(t, svc, "acme", "full.zip", exportZip(t, manyFollowers(400)...))
	upload(t, svc, "acme", "windowed.zip", exportZip(t, manyFollowers(25)...))

	// A proper full export afterwards must still be accepted and diffed
	// against the last good snapshot, not the refused one.
	recovered := upload(t, svc, "acme", "full-again.zip", exportZip(t, manyFollowers(395)...))
	if recovered.Status != store.StatusCompleted {
		t.Fatalf("status = %q (%s), want completed", recovered.Status, recovered.ErrorMessage)
	}
	if recovered.RemovedCount != 5 {
		t.Fatalf("removed = %d, want 5 against the last good snapshot", recovered.RemovedCount)
	}
}
