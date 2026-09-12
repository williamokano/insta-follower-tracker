package tracker_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/store"
	"github.com/williamokano/insta-follower-tracker/internal/tracker"
)

// datedZip builds an export archive whose entries carry a modification time,
// which is how a real download states when it was generated.
func datedZip(t *testing.T, taken time.Time, usernames ...string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	write := func(name string, body []byte) {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: taken}
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	write("connections/followers_and_following/followers_1.json", exportJSON(t, usernames...))
	write("files/Instagram-Logo.png", []byte{0x89, 'P', 'N', 'G'})

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// executions lists an account's completed executions oldest first.
func executions(t *testing.T, st *store.Store, handle string) []store.Upload {
	t.Helper()
	ctx := context.Background()

	acc, err := st.AccountByHandle(ctx, handle)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	all, err := st.ListUploads(ctx, acc.ID)
	if err != nil {
		t.Fatalf("list uploads: %v", err)
	}

	out := make([]store.Upload, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Status == store.StatusCompleted {
			out = append(out, all[i])
		}
	}
	return out
}

func day(year int, month time.Month, d int) time.Time {
	return time.Date(year, month, d, 12, 0, 0, 0, time.UTC)
}

// TestBackfilledExportSortsIntoPlace is the point of this change: an old export
// uploaded last still belongs between its neighbours in time.
func TestBackfilledExportSortsIntoPlace(t *testing.T) {
	svc, st, _ := newService(t)

	// Uploaded newest first, deliberately out of order.
	upload(t, svc, "acme", "2026.zip", datedZip(t, day(2026, time.September, 11), "alice", "bob", "erin"))
	upload(t, svc, "acme", "2024-07.zip", datedZip(t, day(2024, time.July, 12), "alice", "bob", "dave"))
	upload(t, svc, "acme", "2024-10.zip", datedZip(t, day(2024, time.October, 23), "alice", "carol", "dave"))

	got := executions(t, st, "acme")
	if len(got) != 3 {
		t.Fatalf("executions = %d, want 3", len(got))
	}

	wantOrder := []string{"2024-07.zip", "2024-10.zip", "2026.zip"}
	for i, up := range got {
		if up.OriginalFilename != wantOrder[i] {
			t.Fatalf("execution %d is %s, want %s (order must follow export date, not upload order)",
				i+1, up.OriginalFilename, wantOrder[i])
		}
		if up.SequenceNo == nil || *up.SequenceNo != int64(i+1) {
			t.Fatalf("%s has sequence %v, want %d", up.OriginalFilename, up.SequenceNo, i+1)
		}
	}

	// The oldest export is the baseline even though it was not uploaded first.
	if !got[0].IsBaseline {
		t.Fatal("the earliest export should be the baseline")
	}
	if got[0].AddedCount != 0 || got[0].RemovedCount != 0 {
		t.Fatalf("baseline has +%d/-%d, want 0/0", got[0].AddedCount, got[0].RemovedCount)
	}
}

// TestBackfillRewritesTheDiffsAroundIt: inserting an execution in the middle
// changes what its neighbours should be compared against, and the stored diffs
// have to follow.
func TestBackfillRewritesTheDiffsAroundIt(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	upload(t, svc, "acme", "jan.zip", datedZip(t, day(2024, time.January, 1), "alice"))
	upload(t, svc, "acme", "mar.zip", datedZip(t, day(2024, time.March, 1), "alice", "bob", "carol"))

	// Before the backfill, March is compared against January.
	mid := executions(t, st, "acme")[1]
	if mid.AddedCount != 2 {
		t.Fatalf("march added = %d, want 2 against january", mid.AddedCount)
	}

	// February arrives late and now sits between them.
	upload(t, svc, "acme", "feb.zip", datedZip(t, day(2024, time.February, 1), "alice", "bob"))

	got := executions(t, st, "acme")
	if len(got) != 3 {
		t.Fatalf("executions = %d, want 3", len(got))
	}
	if got[1].OriginalFilename != "feb.zip" {
		t.Fatalf("middle execution is %s, want feb.zip", got[1].OriginalFilename)
	}

	// February against January: bob arrived.
	if got[1].AddedCount != 1 || got[1].RemovedCount != 0 {
		t.Fatalf("feb = +%d/-%d, want +1/-0", got[1].AddedCount, got[1].RemovedCount)
	}
	// March must now be against February, not January: only carol is new.
	if got[2].AddedCount != 1 || got[2].RemovedCount != 0 {
		t.Fatalf("march = +%d/-%d, want +1/-0 after the backfill rewrote it",
			got[2].AddedCount, got[2].RemovedCount)
	}

	changes, err := st.ChangesForUpload(ctx, got[2].ID, "", store.ChangeFollowed)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(changes) != 1 || changes[0].Username != "carol" {
		t.Fatalf("march followed = %+v, want [carol]", changes)
	}
}

// TestBackfillCorrectsTheOverallDiff checks the answer the service exists to
// give, after history has been rewritten underneath it.
func TestBackfillCorrectsTheOverallDiff(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	upload(t, svc, "acme", "late.zip", datedZip(t, day(2024, time.December, 1), "alice", "bob"))
	upload(t, svc, "acme", "early.zip", datedZip(t, day(2024, time.January, 1), "alice", "dave"))

	acc, _ := st.AccountByHandle(ctx, "acme")
	first, err := svc.ResolveUpload(ctx, acc.ID, "first", false)
	if err != nil {
		t.Fatalf("resolve first: %v", err)
	}
	last, err := svc.ResolveUpload(ctx, acc.ID, "last", true)
	if err != nil {
		t.Fatalf("resolve last: %v", err)
	}

	if first.OriginalFilename != "early.zip" || last.OriginalFilename != "late.zip" {
		t.Fatalf("first/last = %s/%s, want early.zip/late.zip",
			first.OriginalFilename, last.OriginalFilename)
	}

	diff, err := st.Diff(ctx, acc.ID, first, last, "")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	assertNames(t, "lost", diff.Lost, "dave")
	assertNames(t, "gained", diff.Gained, "bob")
}

// TestBackfillOfSmallerOlderExportIsNotRefused guards the interaction with the
// partial-export check. An older snapshot legitimately holds fewer followers
// than a later one, and must not be mistaken for a truncated download.
func TestBackfillOfSmallerOlderExportIsNotRefused(t *testing.T) {
	svc, st, _ := newService(t)

	// The account grew from 40 followers to 400 over two years.
	upload(t, svc, "acme", "2026.zip", datedZip(t, day(2026, time.September, 11), manyFollowers(400)...))
	older := upload(t, svc, "acme", "2024.zip", datedZip(t, day(2024, time.July, 12), manyFollowers(40)...))

	if older.Status != store.StatusCompleted {
		t.Fatalf("status = %q (%s): an older, smaller snapshot is history, not a truncated export",
			older.Status, older.ErrorMessage)
	}

	got := executions(t, st, "acme")
	if got[0].OriginalFilename != "2024.zip" {
		t.Fatalf("oldest execution is %s, want 2024.zip", got[0].OriginalFilename)
	}
	// Growth of 360, not a loss.
	if got[1].AddedCount != 360 || got[1].RemovedCount != 0 {
		t.Fatalf("2026 = +%d/-%d, want +360/-0", got[1].AddedCount, got[1].RemovedCount)
	}
}

// TestCollapseIsStillCaughtInChronologicalOrder: moving the comparison to the
// chronological predecessor must not blunt the partial-export check.
func TestCollapseIsStillCaughtInChronologicalOrder(t *testing.T) {
	svc, _, _ := newService(t)

	upload(t, svc, "acme", "jan.zip", datedZip(t, day(2024, time.January, 1), manyFollowers(400)...))
	windowed := upload(t, svc, "acme", "jun.zip", datedZip(t, day(2024, time.June, 1), manyFollowers(20)...))

	if windowed.Status != store.StatusFailed {
		t.Fatalf("status = %q, want failed: a later snapshot collapsing is still suspect",
			windowed.Status)
	}
}

func TestSnapshotDateComesFromTheArchive(t *testing.T) {
	svc, st, _ := newService(t)

	taken := day(2024, time.October, 23)
	upload(t, svc, "acme", "export.zip", datedZip(t, taken, "alice"))

	got := executions(t, st, "acme")[0]
	if got.SnapshotTakenAt == nil {
		t.Fatal("no snapshot date recorded")
	}
	if got.SnapshotTakenAt.Format("2006-01-02") != taken.Format("2006-01-02") {
		t.Fatalf("snapshot date = %s, want %s", got.SnapshotTakenAt, taken)
	}
	if got.SnapshotSource == "" {
		t.Fatal("the source of the date should be recorded")
	}
}

// TestManualDateOverridesTheArchive covers a file whose timestamps were lost,
// for instance by being re-zipped or passed through a tool that rewrites them.
func TestManualDateOverridesTheArchive(t *testing.T) {
	svc, st, _ := newService(t)

	want := day(2019, time.May, 4)
	uploadWith(t, svc, "acme", "mystery.zip",
		datedZip(t, day(2026, time.September, 11), "alice"),
		tracker.AcceptOptions{SnapshotDate: want})

	got := executions(t, st, "acme")[0]
	if got.SnapshotTakenAt == nil || !got.SnapshotTakenAt.Equal(want) {
		t.Fatalf("snapshot date = %v, want %s", got.SnapshotTakenAt, want)
	}
	if !strings.Contains(got.SnapshotSource, "manual") {
		t.Fatalf("source = %q, want it to record that a person set it", got.SnapshotSource)
	}
}

// TestFilenameDateIsUsedWhenTheArchiveIsSilent covers a bare JSON upload, which
// carries no archive timestamps at all.
func TestFilenameDateIsUsedWhenTheArchiveIsSilent(t *testing.T) {
	svc, st, _ := newService(t)

	upload(t, svc, "acme", "instagram-example_account-2024-10-23-tQF9URpZ.zip",
		exportJSON(t, "alice", "bob"))

	got := executions(t, st, "acme")[0]
	if got.SnapshotTakenAt == nil {
		t.Fatal("no snapshot date recorded")
	}
	if got.SnapshotTakenAt.Format("2006-01-02") != "2024-10-23" {
		t.Fatalf("snapshot date = %s, want 2024-10-23 from the file name",
			got.SnapshotTakenAt.Format("2006-01-02"))
	}
}

// TestReprocessingKeepsHistoryStable: recomputing must be idempotent, or a
// retried upload would quietly change the numbers.
func TestReprocessingKeepsHistoryStable(t *testing.T) {
	svc, st, _ := newService(t)

	upload(t, svc, "acme", "jan.zip", datedZip(t, day(2024, time.January, 1), "alice"))
	upload(t, svc, "acme", "feb.zip", datedZip(t, day(2024, time.February, 1), "alice", "bob"))
	upload(t, svc, "acme", "mar.zip", datedZip(t, day(2024, time.March, 1), "bob"))

	before := executions(t, st, "acme")

	// A fourth upload of an already-known date must not disturb the numbering
	// of what came before it.
	upload(t, svc, "acme", "apr.zip", datedZip(t, day(2024, time.April, 1), "bob", "carol"))
	after := executions(t, st, "acme")

	for i := range before {
		if after[i].OriginalFilename != before[i].OriginalFilename {
			t.Fatalf("execution %d changed identity: %s -> %s",
				i+1, before[i].OriginalFilename, after[i].OriginalFilename)
		}
		if after[i].AddedCount != before[i].AddedCount || after[i].RemovedCount != before[i].RemovedCount {
			t.Fatalf("execution %d counts changed: +%d/-%d -> +%d/-%d",
				i+1, before[i].AddedCount, before[i].RemovedCount,
				after[i].AddedCount, after[i].RemovedCount)
		}
	}
}

// TestLegacyExecutionsRecoverTheirExportDates covers upgrading a database
// recorded before export dates existed.
//
// Migrating those rows to their processing time preserves the order they had,
// but mixes badly with a real backfill: an export generated in 2024 would sort
// after a legacy row stamped with the day of the upgrade. Since the uploads are
// retained, the real dates are read on startup instead.
func TestLegacyExecutionsRecoverTheirExportDates(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	upload(t, svc, "acme", "old.zip", datedZip(t, day(2024, time.July, 12), "alice", "bob"))
	upload(t, svc, "acme", "new.zip", datedZip(t, day(2026, time.September, 11), "alice", "erin"))

	// Simulate what the migration leaves behind: order preserved, real dates lost.
	for i, up := range executions(t, st, "acme") {
		stamped := day(2026, time.September, 11).Add(time.Duration(i) * time.Minute)
		if err := st.SetSnapshotDate(ctx, up.ID, stamped, store.SourceProcessingOrder); err != nil {
			t.Fatalf("stamp legacy date: %v", err)
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.Run(runCtx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		got := executions(t, st, "acme")
		if got[0].SnapshotSource != store.SourceProcessingOrder {
			// Dates recovered; check they are the real ones.
			if got[0].SnapshotTakenAt.Format("2006-01-02") != "2024-07-12" {
				t.Fatalf("oldest export date = %s, want 2024-07-12",
					got[0].SnapshotTakenAt.Format("2006-01-02"))
			}
			if got[1].SnapshotTakenAt.Format("2006-01-02") != "2026-09-11" {
				t.Fatalf("newest export date = %s, want 2026-09-11",
					got[1].SnapshotTakenAt.Format("2006-01-02"))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup never re-read the export dates of legacy executions")
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	<-done

	// A backfill must now land between them rather than after both.
	upload(t, svc, "acme", "mid.zip", datedZip(t, day(2025, time.March, 4), "alice"))

	got := executions(t, st, "acme")
	want := []string{"old.zip", "mid.zip", "new.zip"}
	for i, up := range got {
		if up.OriginalFilename != want[i] {
			t.Fatalf("execution %d is %s, want %s", i+1, up.OriginalFilename, want[i])
		}
	}
}

// TestMissingFileLeavesLegacyDateAlone: retention can be off, in which case
// there is nothing to re-read and the recorded date stands.
func TestMissingFileLeavesLegacyDateAlone(t *testing.T) {
	svc, st, _ := newService(t, func(o *tracker.Options) { o.RetainUploads = false })
	ctx := context.Background()

	upload(t, svc, "acme", "gone.zip", datedZip(t, day(2024, time.July, 12), "alice"))

	only := executions(t, st, "acme")[0]
	stamped := day(2026, time.September, 11)
	if err := st.SetSnapshotDate(ctx, only.ID, stamped, store.SourceProcessingOrder); err != nil {
		t.Fatalf("stamp legacy date: %v", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.Run(runCtx) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done

	after := executions(t, st, "acme")[0]
	if after.SnapshotSource != store.SourceProcessingOrder {
		t.Fatalf("source = %q; with no file on disk there is nothing to re-read",
			after.SnapshotSource)
	}
	if after.Status != store.StatusCompleted {
		t.Fatalf("status = %q: a missing file must not break startup", after.Status)
	}
}

// countFiles counts regular files anywhere beneath dir.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return n
}

// countFilesIn counts regular files in one subdirectory of dir.
func countFilesIn(t *testing.T, dir, sub string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, sub))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read %s: %v", sub, err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n
}
