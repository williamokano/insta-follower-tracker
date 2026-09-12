package tracker_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/store"
	"github.com/williamokano/insta-follower-tracker/internal/tracker"
)

// drainService runs the worker until both queues are empty.
func drainService(t *testing.T, svc interface {
	ProcessNext(context.Context) (bool, error)
	ReprocessPending(context.Context) (int, error)
	Run(context.Context) error
}) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		left, err := svc.ReprocessPending(context.Background())
		if err != nil {
			t.Fatalf("reprocess pending: %v", err)
		}
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d executions never finished rereading", left)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	<-done
}

// TestReprocessPicksUpListsAddedSinceTheUpload is the reason this exists. An
// export uploaded before other relationship lists were understood holds only
// its follower list, and the rest are sitting in the file on disk.
func TestReprocessPicksUpListsAddedSinceTheUpload(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	up := upload(t, svc, "acme", "export.zip", multiListZip(t, day(2024, time.July, 12), map[string][]string{
		"followers_1": {"alice", "bob"},
		"following":   {"carol"},
	}))

	// Simulate what an older version left behind: only the follower list.
	if _, err := st.DB().ExecContext(ctx,
		`DELETE FROM snapshot_members WHERE upload_id = ? AND list_kind <> 'followers'`, up.ID); err != nil {
		t.Fatalf("strip lists: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`DELETE FROM upload_lists WHERE upload_id = ? AND list_kind <> 'followers'`, up.ID); err != nil {
		t.Fatalf("strip list totals: %v", err)
	}
	if got := kindsOf(t, st, up.ID); len(got) != 1 {
		t.Fatalf("setup left %d lists, want 1", len(got))
	}

	queued, err := svc.Reprocess(ctx, "acme")
	if err != nil {
		t.Fatalf("reprocess: %v", err)
	}
	if queued != 1 {
		t.Fatalf("queued = %d, want 1", queued)
	}

	drainService(t, svc)

	got := kindsOf(t, st, up.ID)
	if _, ok := got["following"]; !ok {
		t.Fatal("rereading the stored file should have recovered the following list")
	}
	if got["following"].MemberCount != 1 {
		t.Fatalf("following members = %d, want 1", got["following"].MemberCount)
	}
}

// TestReprocessKeepsDataWhenTheFileIsGone is the safety property. Rereading
// must never be able to destroy what is already recorded.
func TestReprocessKeepsDataWhenTheFileIsGone(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	up := upload(t, svc, "acme", "export.zip", multiListZip(t, day(2024, time.July, 12), map[string][]string{
		"followers_1": {"alice", "bob", "carol"},
	}))

	stored, err := st.Upload(ctx, up.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := os.Remove(stored.StoredPath); err != nil {
		t.Fatalf("remove stored file: %v", err)
	}

	if _, err := svc.Reprocess(ctx, "acme"); err != nil {
		t.Fatalf("reprocess: %v", err)
	}
	drainService(t, svc)

	after, err := st.Upload(ctx, up.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Status != store.StatusCompleted {
		t.Fatalf("status = %q; a missing file must not unseat a recorded execution", after.Status)
	}
	if after.FollowerCount != 3 {
		t.Fatalf("follower count = %d, want 3 kept", after.FollowerCount)
	}

	members, err := st.MembersForUpload(ctx, up.ID, "followers")
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	assertNames(t, "kept members", members, "alice", "bob", "carol")
}

// TestReprocessKeepsDataWhenTheFileNoLongerParses covers a file that is present
// but unreadable, for instance truncated on the way in.
func TestReprocessKeepsDataWhenTheFileNoLongerParses(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	up := upload(t, svc, "acme", "export.zip", multiListZip(t, day(2024, time.July, 12), map[string][]string{
		"followers_1": {"alice", "bob"},
	}))

	stored, _ := st.Upload(ctx, up.ID)
	if err := os.WriteFile(stored.StoredPath, []byte("not an export any more"), 0o644); err != nil {
		t.Fatalf("corrupt file: %v", err)
	}

	if _, err := svc.Reprocess(ctx, "acme"); err != nil {
		t.Fatalf("reprocess: %v", err)
	}
	drainService(t, svc)

	after, _ := st.Upload(ctx, up.ID)
	if after.Status != store.StatusCompleted || after.FollowerCount != 2 {
		t.Fatalf("status %q with %d followers; the recorded execution must survive an unreadable file",
			after.Status, after.FollowerCount)
	}
}

// TestReprocessIsIdempotent: reading the same files again must not shift any
// number, or the operation would be unsafe to repeat.
func TestReprocessIsIdempotent(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	upload(t, svc, "acme", "jan.zip", multiListZip(t, day(2024, time.January, 1), map[string][]string{
		"followers_1": {"alice", "bob"},
		"following":   {"carol"},
	}))
	upload(t, svc, "acme", "mar.zip", multiListZip(t, day(2024, time.March, 1), map[string][]string{
		"followers_1": {"alice", "dave"},
		"following":   {"carol", "erin"},
	}))

	before := executions(t, st, "acme")

	if _, err := svc.Reprocess(ctx, "acme"); err != nil {
		t.Fatalf("reprocess: %v", err)
	}
	drainService(t, svc)

	after := executions(t, st, "acme")
	if len(after) != len(before) {
		t.Fatalf("executions = %d, want %d", len(after), len(before))
	}
	for i := range before {
		if after[i].ID != before[i].ID {
			t.Fatalf("execution %d changed identity", i+1)
		}
		if after[i].AddedCount != before[i].AddedCount || after[i].RemovedCount != before[i].RemovedCount {
			t.Fatalf("execution %d counts moved: +%d/-%d -> +%d/-%d",
				i+1, before[i].AddedCount, before[i].RemovedCount,
				after[i].AddedCount, after[i].RemovedCount)
		}
		if after[i].FollowerCount != before[i].FollowerCount {
			t.Fatalf("execution %d follower count moved: %d -> %d",
				i+1, before[i].FollowerCount, after[i].FollowerCount)
		}
	}
}

// TestReprocessKeepsAManualDate: a date somebody set by hand is the one source
// a reread must not overwrite.
func TestReprocessKeepsAManualDate(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	want := day(2019, time.May, 4)
	uploadWith(t, svc, "acme", "mystery.zip",
		multiListZip(t, day(2026, time.September, 11), map[string][]string{"followers_1": {"alice"}}),
		tracker.AcceptOptions{SnapshotDate: want})

	if _, err := svc.Reprocess(ctx, "acme"); err != nil {
		t.Fatalf("reprocess: %v", err)
	}
	drainService(t, svc)

	got := executions(t, st, "acme")[0]
	if got.SnapshotTakenAt == nil || !got.SnapshotTakenAt.Equal(want) {
		t.Fatalf("snapshot date = %v, want the manual %s", got.SnapshotTakenAt, want)
	}
}

// TestReprocessOfAnUnknownAccountIsAnError keeps a typo from silently doing
// nothing.
func TestReprocessOfAnUnknownAccountIsAnError(t *testing.T) {
	svc, _, _ := newService(t)

	if _, err := svc.Reprocess(context.Background(), "nobody"); err == nil {
		t.Fatal("expected an unknown account to be reported")
	}
}
