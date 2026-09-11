package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/williamokano/insta-follower-tracker/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func members(names ...string) []store.Member {
	out := make([]store.Member, 0, len(names))
	for _, n := range names {
		out = append(out, store.Member{Username: n, Href: "https://www.instagram.com/" + n})
	}
	return out
}

// snapshot uploads and immediately processes one execution, returning it.
func snapshot(t *testing.T, s *store.Store, accountID int64, name string, names ...string) store.Upload {
	t.Helper()
	ctx := context.Background()

	id, err := s.CreateUpload(ctx, accountID, name, "/tmp/"+name, "sha-"+name, 10, false)
	if err != nil {
		t.Fatalf("create upload %s: %v", name, err)
	}
	if _, err := s.ApplySnapshot(ctx, id, members(names...)); err != nil {
		t.Fatalf("apply snapshot %s: %v", name, err)
	}
	up, err := s.Upload(ctx, id)
	if err != nil {
		t.Fatalf("load upload %s: %v", name, err)
	}
	return up
}

func usernames(fs []store.Follower) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Username)
	}
	return out
}

func assertNames(t *testing.T, what string, got []store.Follower, want ...string) {
	t.Helper()
	names := usernames(got)
	if len(names) != len(want) {
		t.Fatalf("%s: got %v, want %v", what, names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", what, names, want)
		}
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tracker.db")

	first, err := store.Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := first.EnsureAccount(context.Background(), "someone"); err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	if _, err := second.AccountByHandle(context.Background(), "someone"); err != nil {
		t.Fatalf("account should survive reopen: %v", err)
	}
}

func TestEnsureAccountNormalizesAndDeduplicates(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	a, err := s.EnsureAccount(ctx, "  @WilliamOkano/ ")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if a.Handle != "williamokano" {
		t.Fatalf("handle = %q, want williamokano", a.Handle)
	}

	b, err := s.EnsureAccount(ctx, "williamokano")
	if err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if a.ID != b.ID {
		t.Fatalf("expected the same account, got %d and %d", a.ID, b.ID)
	}

	if _, err := s.EnsureAccount(ctx, "   "); err == nil {
		t.Fatal("expected an error for a blank handle")
	}
}

func TestFirstSnapshotIsBaselineWithNoChanges(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	acc, _ := s.EnsureAccount(ctx, "acme")

	up := snapshot(t, s, acc.ID, "export-1.json", "alice", "bob")

	if up.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want completed", up.Status)
	}
	if up.SequenceNo == nil || *up.SequenceNo != 1 {
		t.Fatalf("sequence_no = %v, want 1", up.SequenceNo)
	}
	if !up.IsBaseline {
		t.Fatal("first execution should be the baseline")
	}
	if up.FollowerCount != 2 || up.AddedCount != 0 || up.RemovedCount != 0 {
		t.Fatalf("counts = %d/%d/%d, want 2/0/0", up.FollowerCount, up.AddedCount, up.RemovedCount)
	}

	changes, err := s.ChangesForUpload(ctx, up.ID, "")
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("baseline should record no changes, got %d", len(changes))
	}
}

func TestConsecutiveExecutionsRecordDeltas(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	acc, _ := s.EnsureAccount(ctx, "acme")

	snapshot(t, s, acc.ID, "e1", "alice", "bob")
	second := snapshot(t, s, acc.ID, "e2", "alice", "carol")

	if second.AddedCount != 1 || second.RemovedCount != 1 {
		t.Fatalf("counts = +%d/-%d, want +1/-1", second.AddedCount, second.RemovedCount)
	}

	followed, err := s.ChangesForUpload(ctx, second.ID, store.ChangeFollowed)
	if err != nil {
		t.Fatalf("followed: %v", err)
	}
	if len(followed) != 1 || followed[0].Username != "carol" {
		t.Fatalf("followed = %+v, want [carol]", followed)
	}

	unfollowed, err := s.ChangesForUpload(ctx, second.ID, store.ChangeUnfollowed)
	if err != nil {
		t.Fatalf("unfollowed: %v", err)
	}
	if len(unfollowed) != 1 || unfollowed[0].Username != "bob" {
		t.Fatalf("unfollowed = %+v, want [bob]", unfollowed)
	}
}

// TestDiffSeparatesReturningFollowersFromLostOnes covers the caveat this whole
// design exists for: aggregating every recorded unfollow event over-reports who
// is actually gone, because a follower can leave and come back.
func TestDiffSeparatesReturningFollowersFromLostOnes(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	acc, _ := s.EnsureAccount(ctx, "acme")

	// bob leaves at execution 2 and returns at execution 4.
	// dave leaves at execution 3 and never comes back.
	// carol only ever appears at execution 2.
	// erin joins at execution 4.
	first := snapshot(t, s, acc.ID, "e1", "alice", "bob", "dave")
	snapshot(t, s, acc.ID, "e2", "alice", "carol", "dave")
	snapshot(t, s, acc.ID, "e3", "alice")
	last := snapshot(t, s, acc.ID, "e4", "alice", "bob", "erin")

	diff, err := s.Diff(ctx, acc.ID, first, last)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}

	assertNames(t, "lost", diff.Lost, "dave")
	assertNames(t, "gained", diff.Gained, "erin")
	assertNames(t, "returned", diff.Returned, "bob")
	assertNames(t, "transient", diff.Transient, "carol")

	// The raw event log does contain bob, which is exactly why it must not be
	// used on its own to answer "who is not following anymore".
	raw, err := s.AllUnfollowers(ctx, acc.ID)
	if err != nil {
		t.Fatalf("all unfollowers: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range raw {
		seen[c.Username] = true
	}
	if !seen["bob"] {
		t.Fatal("raw unfollow log should contain bob")
	}
	if !seen["dave"] {
		t.Fatal("raw unfollow log should contain dave")
	}
	if len(raw) <= len(diff.Lost) {
		t.Fatalf("raw log (%d) should over-report versus net lost (%d)", len(raw), len(diff.Lost))
	}
}

func TestDiffOfAdjacentExecutionsHasNoMiddle(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	acc, _ := s.EnsureAccount(ctx, "acme")

	first := snapshot(t, s, acc.ID, "e1", "alice", "bob")
	second := snapshot(t, s, acc.ID, "e2", "alice", "carol")

	diff, err := s.Diff(ctx, acc.ID, first, second)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	assertNames(t, "lost", diff.Lost, "bob")
	assertNames(t, "gained", diff.Gained, "carol")
	assertNames(t, "returned", diff.Returned)
	assertNames(t, "transient", diff.Transient)
}

func TestBoundaryUploadsAndCurrentFollowers(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	acc, _ := s.EnsureAccount(ctx, "acme")

	first := snapshot(t, s, acc.ID, "e1", "alice")
	snapshot(t, s, acc.ID, "e2", "alice", "bob")
	last := snapshot(t, s, acc.ID, "e3", "bob", "carol")

	gotFirst, err := s.BoundaryUpload(ctx, acc.ID, false)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if gotFirst.ID != first.ID {
		t.Fatalf("first = %d, want %d", gotFirst.ID, first.ID)
	}

	gotLast, err := s.BoundaryUpload(ctx, acc.ID, true)
	if err != nil {
		t.Fatalf("last: %v", err)
	}
	if gotLast.ID != last.ID {
		t.Fatalf("last = %d, want %d", gotLast.ID, last.ID)
	}

	current, err := s.CurrentFollowers(ctx, acc.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	assertNames(t, "current followers", current, "bob", "carol")
}

func TestQueueClaimAndRequeue(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	acc, _ := s.EnsureAccount(ctx, "acme")

	if _, err := s.ClaimNextPending(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("empty queue should report ErrNotFound, got %v", err)
	}

	firstID, err := s.CreateUpload(ctx, acc.ID, "a.json", "/tmp/a", "sha-a", 1, false)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	secondID, err := s.CreateUpload(ctx, acc.ID, "b.json", "/tmp/b", "sha-b", 1, false)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}

	claimed, err := s.ClaimNextPending(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.ID != firstID {
		t.Fatalf("claimed %d, want the oldest pending upload %d", claimed.ID, firstID)
	}
	if claimed.Status != store.StatusProcessing {
		t.Fatalf("claimed status = %q, want processing", claimed.Status)
	}

	// A crash mid-processing must not strand the row.
	requeued, err := s.RequeueProcessing(ctx)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued = %d, want 1", requeued)
	}

	again, err := s.ClaimNextPending(ctx)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if again.ID != firstID {
		t.Fatalf("reclaimed %d, want %d", again.ID, firstID)
	}

	if _, err := s.Upload(ctx, secondID); err != nil {
		t.Fatalf("second upload should still exist: %v", err)
	}
}

func TestFailUploadRecordsReason(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	acc, _ := s.EnsureAccount(ctx, "acme")

	id, _ := s.CreateUpload(ctx, acc.ID, "bad.json", "/tmp/bad", "sha", 1, false)
	if err := s.FailUpload(ctx, id, "not an export"); err != nil {
		t.Fatalf("fail: %v", err)
	}

	up, err := s.Upload(ctx, id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if up.Status != store.StatusFailed || up.ErrorMessage != "not an export" {
		t.Fatalf("got %q/%q, want failed/not an export", up.Status, up.ErrorMessage)
	}
	if up.SequenceNo != nil {
		t.Fatal("a failed upload must not consume a sequence number")
	}
}

func TestReprocessingClearsPreviousState(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	acc, _ := s.EnsureAccount(ctx, "acme")

	snapshot(t, s, acc.ID, "e1", "alice")

	id, _ := s.CreateUpload(ctx, acc.ID, "e2", "/tmp/e2", "sha", 1, false)
	if _, err := s.ApplySnapshot(ctx, id, members("alice", "bob")); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	res, err := s.ApplySnapshot(ctx, id, members("alice", "bob"))
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if res.AddedCount != 1 || res.FollowerCount != 2 {
		t.Fatalf("reapply produced +%d/%d followers, want +1/2", res.AddedCount, res.FollowerCount)
	}

	changes, err := s.ChangesForUpload(ctx, id, "")
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("reprocessing duplicated changes: got %d, want 1", len(changes))
	}
}
