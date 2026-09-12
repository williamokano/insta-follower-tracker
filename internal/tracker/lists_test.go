package tracker_test

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/instagram"
	"github.com/williamokano/insta-follower-tracker/internal/store"
)

// multiListZip builds an export carrying several relationship lists, the way a
// real download does.
func multiListZip(t *testing.T, taken time.Time, lists map[string][]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, members := range lists {
		path := "connections/followers_and_following/" + name + ".json"
		w, err := zw.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Deflate, Modified: taken})
		if err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		if _, err := w.Write(exportJSON(t, members...)); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func kindsOf(t *testing.T, st *store.Store, uploadID int64) map[string]store.ListTotals {
	t.Helper()
	totals, err := st.ListTotalsForUpload(context.Background(), uploadID)
	if err != nil {
		t.Fatalf("list totals: %v", err)
	}
	out := map[string]store.ListTotals{}
	for _, tot := range totals {
		out[tot.Kind] = tot
	}
	return out
}

// TestEveryListInTheExportIsRecorded is the headline of this change: an export
// carries far more than followers and all of it was being discarded.
func TestEveryListInTheExportIsRecorded(t *testing.T) {
	svc, st, _ := newService(t)

	up := upload(t, svc, "acme", "export.zip", multiListZip(t, day(2024, time.July, 12), map[string][]string{
		"followers_1":             {"alice", "bob", "carol"},
		"following":               {"alice", "dave"},
		"close_friends":           {"alice"},
		"pending_follow_requests": {"erin"},
		"blocked_profiles":        {"spammer"},
		"restricted_profiles":     {"noisy"},
		// Not a list of accounts, so deliberately not tracked.
		"following_hashtags": {"golang"},
	}))

	if up.Status != store.StatusCompleted {
		t.Fatalf("status = %q (%s)", up.Status, up.ErrorMessage)
	}

	got := kindsOf(t, st, up.ID)
	want := map[string]int{
		"followers": 3, "following": 2, "close_friends": 1,
		"pending_follow_requests": 1, "blocked": 1, "restricted": 1,
	}
	for kind, count := range want {
		tot, ok := got[kind]
		if !ok {
			t.Fatalf("list %q was not recorded", kind)
		}
		if tot.MemberCount != count {
			t.Fatalf("%s member count = %d, want %d", kind, tot.MemberCount, count)
		}
	}
	if _, ok := got["following_hashtags"]; ok {
		t.Fatal("hashtags are topics, not accounts, and should not be tracked as a list")
	}

	// The follower columns on the upload row still describe followers.
	if up.FollowerCount != 3 {
		t.Fatalf("follower_count = %d, want 3", up.FollowerCount)
	}
}

// TestEachListIsDiffedIndependently: a change in one list must not appear in
// another.
func TestEachListIsDiffedIndependently(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	upload(t, svc, "acme", "e1.zip", multiListZip(t, day(2024, time.January, 1), map[string][]string{
		"followers_1": {"alice", "bob"},
		"following":   {"carol", "dave"},
	}))
	second := upload(t, svc, "acme", "e2.zip", multiListZip(t, day(2024, time.February, 1), map[string][]string{
		"followers_1": {"alice", "erin"},
		"following":   {"carol", "dave", "frank"},
	}))

	totals := kindsOf(t, st, second.ID)
	if totals["followers"].AddedCount != 1 || totals["followers"].RemovedCount != 1 {
		t.Fatalf("followers = +%d/-%d, want +1/-1",
			totals["followers"].AddedCount, totals["followers"].RemovedCount)
	}
	if totals["following"].AddedCount != 1 || totals["following"].RemovedCount != 0 {
		t.Fatalf("following = +%d/-%d, want +1/-0",
			totals["following"].AddedCount, totals["following"].RemovedCount)
	}

	followerChanges, err := st.ChangesForUpload(ctx, second.ID, "followers", store.ChangeFollowed)
	if err != nil {
		t.Fatalf("follower changes: %v", err)
	}
	if len(followerChanges) != 1 || followerChanges[0].Username != "erin" {
		t.Fatalf("followers followed = %+v, want [erin]", followerChanges)
	}

	followingChanges, err := st.ChangesForUpload(ctx, second.ID, "following", store.ChangeFollowed)
	if err != nil {
		t.Fatalf("following changes: %v", err)
	}
	if len(followingChanges) != 1 || followingChanges[0].Username != "frank" {
		t.Fatalf("following added = %+v, want [frank]", followingChanges)
	}
}

// TestAbsentListIsNotTreatedAsEmpty is the subtle one. An export that did not
// carry a list says nothing about it; reading that silence as "everybody left"
// would invent departures, exactly as a date-limited export does.
func TestAbsentListIsNotTreatedAsEmpty(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	upload(t, svc, "acme", "full.zip", multiListZip(t, day(2024, time.January, 1), map[string][]string{
		"followers_1": {"alice", "bob"},
		"following":   {"carol", "dave"},
	}))
	// A later export of followers only, as happens when the download is
	// narrowed or an older format is used.
	second := upload(t, svc, "acme", "followers-only.zip",
		multiListZip(t, day(2024, time.February, 1), map[string][]string{
			"followers_1": {"alice", "bob"},
		}))

	totals := kindsOf(t, st, second.ID)
	if _, ok := totals["following"]; ok {
		t.Fatal("an export that did not carry the following list must not record one")
	}

	removed, err := st.ChangesForUpload(ctx, second.ID, "following", store.ChangeUnfollowed)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("silence about a list invented %d departures from it", len(removed))
	}
}

// TestNotFollowingBackIsComputedWithinOneExecution: this question compares two
// lists at the same moment, so it needs no history at all.
func TestNotFollowingBackIsComputedWithinOneExecution(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	up := upload(t, svc, "acme", "export.zip", multiListZip(t, day(2024, time.July, 12), map[string][]string{
		"followers_1": {"alice", "bob", "carol"},
		"following":   {"alice", "dave", "erin"},
	}))

	notBack, err := st.NotFollowingBack(ctx, up.ID)
	if err != nil {
		t.Fatalf("not following back: %v", err)
	}
	assertNames(t, "not following back", notBack, "dave", "erin")

	fans, err := st.Fans(ctx, up.ID)
	if err != nil {
		t.Fatalf("fans: %v", err)
	}
	assertNames(t, "fans", fans, "bob", "carol")
}

// TestBackfillRecomputesEveryList: inserting an execution in the middle must
// rewrite the diffs of all lists, not only followers.
func TestBackfillRecomputesEveryList(t *testing.T) {
	svc, st, _ := newService(t)

	upload(t, svc, "acme", "jan.zip", multiListZip(t, day(2024, time.January, 1), map[string][]string{
		"followers_1": {"alice"},
		"following":   {"carol"},
	}))
	upload(t, svc, "acme", "mar.zip", multiListZip(t, day(2024, time.March, 1), map[string][]string{
		"followers_1": {"alice", "bob"},
		"following":   {"carol", "dave", "erin"},
	}))

	// February arrives late and sits between them.
	upload(t, svc, "acme", "feb.zip", multiListZip(t, day(2024, time.February, 1), map[string][]string{
		"followers_1": {"alice", "bob"},
		"following":   {"carol", "dave"},
	}))

	got := executions(t, st, "acme")
	if got[2].OriginalFilename != "mar.zip" {
		t.Fatalf("last execution is %s, want mar.zip", got[2].OriginalFilename)
	}

	// March is now measured against February for both lists.
	totals := kindsOf(t, st, got[2].ID)
	if totals["followers"].AddedCount != 0 {
		t.Fatalf("followers added = %d, want 0 after the backfill", totals["followers"].AddedCount)
	}
	if totals["following"].AddedCount != 1 {
		t.Fatalf("following added = %d, want 1 after the backfill", totals["following"].AddedCount)
	}
}

// TestFollowersRemainRequired: an archive with other lists but no followers is
// still not something this service can use.
func TestFollowersRemainRequired(t *testing.T) {
	svc, _, _ := newService(t)

	up := upload(t, svc, "acme", "no-followers.zip",
		multiListZip(t, day(2024, time.July, 12), map[string][]string{
			"following":     {"alice"},
			"close_friends": {"bob"},
		}))

	if up.Status != store.StatusFailed {
		t.Fatalf("status = %q, want failed", up.Status)
	}
}

func TestListDefinitionsAreDistinct(t *testing.T) {
	seen := map[instagram.ListKind]bool{}
	for _, info := range instagram.Lists() {
		if seen[info.Kind] {
			t.Fatalf("duplicate list kind %q", info.Kind)
		}
		seen[info.Kind] = true
		if info.Label == "" || info.Description == "" {
			t.Fatalf("list %q needs a label and a description", info.Kind)
		}
	}
	if _, ok := instagram.LookupList("followers"); !ok {
		t.Fatal("followers must be a known list")
	}
	if _, ok := instagram.LookupList("not_a_list"); ok {
		t.Fatal("unknown kinds must not resolve")
	}
}

// TestEmptyListIsRecordedAsEmpty separates two things an export can mean. A
// list that is present but empty says "there are none of these"; a list that is
// absent says nothing at all. Conflating them either invents departures or
// hides real ones.
func TestEmptyListIsRecordedAsEmpty(t *testing.T) {
	svc, st, _ := newService(t)
	ctx := context.Background()

	upload(t, svc, "acme", "e1.zip", multiListZip(t, day(2024, time.January, 1), map[string][]string{
		"followers_1":             {"alice"},
		"pending_follow_requests": {"grace", "heidi"},
	}))
	// The requests were answered, so the list is now present and empty.
	second := upload(t, svc, "acme", "e2.zip", multiListZip(t, day(2024, time.February, 1), map[string][]string{
		"followers_1":             {"alice"},
		"pending_follow_requests": {},
	}))

	totals := kindsOf(t, st, second.ID)
	pending, ok := totals["pending_follow_requests"]
	if !ok {
		t.Fatal("an empty list is still a list and must be recorded")
	}
	if pending.MemberCount != 0 {
		t.Fatalf("member count = %d, want 0", pending.MemberCount)
	}
	if pending.RemovedCount != 2 {
		t.Fatalf("removed = %d, want 2: both requests left the list", pending.RemovedCount)
	}

	gone, err := st.ChangesForUpload(ctx, second.ID, "pending_follow_requests", store.ChangeUnfollowed)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(gone) != 2 {
		t.Fatalf("recorded %d departures, want 2", len(gone))
	}
}
