package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/store"
)

// day is a fixed calendar the insight tests place executions and follows on.
func day(n int) time.Time {
	return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n)
}

// withListsAt uploads one execution generated at a chosen moment, since the
// insights reason about the real gaps between executions.
func withListsAt(t *testing.T, s *store.Store, accountID int64, name string, at time.Time, lists []store.ListSnapshot) store.Upload {
	t.Helper()
	ctx := context.Background()

	id, err := s.CreateUpload(ctx, store.NewUpload{
		AccountID: accountID, Filename: name, StoredPath: "/tmp/" + name,
		SHA256: "sha-" + name, SizeBytes: 10,
	})
	if err != nil {
		t.Fatalf("create upload %s: %v", name, err)
	}
	if _, err := s.ApplySnapshot(ctx, id, lists, at, "test"); err != nil {
		t.Fatalf("apply snapshot %s: %v", name, err)
	}
	up, err := s.Upload(ctx, id)
	if err != nil {
		t.Fatalf("load upload %s: %v", name, err)
	}
	return up
}

func insights(t *testing.T, s *store.Store, accountID int64, days int) store.Insights {
	t.Helper()
	got, err := s.Insights(context.Background(), accountID, days)
	if err != nil {
		t.Fatalf("insights: %v", err)
	}
	return got
}

func newAccount(t *testing.T, s *store.Store) store.Account {
	t.Helper()
	account, err := s.EnsureAccount(context.Background(), "owner")
	if err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	return account
}

func TestInsightsOnAnEmptyAccount(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	got := insights(t, s, account.ID, 0)
	if got.Latest != nil || got.Executions != 0 {
		t.Fatalf("an account with no executions has no latest one: %+v", got)
	}
	if got.QuickUnfollowDays != store.DefaultQuickUnfollowDays {
		t.Fatalf("window = %d, want the default", got.QuickUnfollowDays)
	}
}

// A quick unfollow needs a dated follow, a follow back, and a gap short enough
// to bound the stay. Each person here fails exactly one of those, except the
// one who is the pattern.
func TestQuickUnfollowsNeedEvidenceForEveryPart(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	withListsAt(t, s, account.ID, "first", day(10), []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{
			"flipper":   day(5).Unix(),
			"slow":      day(-200).Unix(),
			"unfollowd": day(6).Unix(),
		}),
		list(store.ListKindFollowing, "flipper", "slow"),
	})
	withListsAt(t, s, account.ID, "second", day(12), []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{
			"flipper": day(5).Unix(), "slow": day(-200).Unix(), "unfollowd": day(6).Unix(),
		}),
		list(store.ListKindFollowing, "flipper", "slow"),
	})
	withListsAt(t, s, account.ID, "third", day(20), []store.ListSnapshot{
		list(store.ListKindFollowers),
		list(store.ListKindFollowing, "flipper", "slow"),
	})

	got := insights(t, s, account.ID, 30)
	if len(got.QuickUnfollows) != 1 {
		t.Fatalf("quick unfollows = %+v, want only flipper", got.QuickUnfollows)
	}
	q := got.QuickUnfollows[0]
	if q.Username != "flipper" {
		t.Fatalf("got %s, want flipper", q.Username)
	}
	// Followed on day 5, gone by day 20: at most fifteen days.
	if q.StayedAtMostDays != 15 {
		t.Errorf("stayed at most %d days, want 15", q.StayedAtMostDays)
	}
	if !q.LastSeen.Equal(day(12)) || !q.GoneBy.Equal(day(20)) {
		t.Errorf("bracket = %s..%s, want day 12..day 20", q.LastSeen, q.GoneBy)
	}
	if !q.YouStillFollow || q.FollowsAgain {
		t.Errorf("you still follow = %v, follows again = %v", q.YouStillFollow, q.FollowsAgain)
	}

	// A narrower window than the bound claims nothing.
	if n := len(insights(t, s, account.ID, 14).QuickUnfollows); n != 0 {
		t.Errorf("a 14 day window found %d, want 0", n)
	}
}

// A long gap between exports makes the bound too loose to call anything quick,
// even when the person in fact left the next day.
func TestQuickUnfollowsAreHiddenByLongGaps(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	withListsAt(t, s, account.ID, "first", day(1), []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{"flipper": day(0).Unix()}),
		list(store.ListKindFollowing, "flipper"),
	})
	withListsAt(t, s, account.ID, "second", day(120), []store.ListSnapshot{
		list(store.ListKindFollowers),
		list(store.ListKindFollowing, "flipper"),
	})

	if n := len(insights(t, s, account.ID, 30).QuickUnfollows); n != 0 {
		t.Fatalf("found %d quick unfollows across a 119 day gap, want 0", n)
	}
}

func TestEndedMutualsOrderTheTwoSidesWhereTheHistoryCan(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	everyone := []string{"they_first", "you_first", "same_gap", "they_only", "you_only", "still_mutual"}
	withListsAt(t, s, account.ID, "first", day(0), []store.ListSnapshot{
		list(store.ListKindFollowers, everyone...),
		list(store.ListKindFollowing, everyone...),
	})
	withListsAt(t, s, account.ID, "second", day(10), []store.ListSnapshot{
		// they_first has left your followers; you still follow them here.
		// you_first has been unfollowed by you; they still follow you here.
		// same_gap is still mutual, and ends on both sides next time.
		list(store.ListKindFollowers, "you_first", "same_gap", "you_only", "still_mutual"),
		list(store.ListKindFollowing, "they_first", "same_gap", "they_only", "still_mutual"),
	})
	withListsAt(t, s, account.ID, "third", day(20), []store.ListSnapshot{
		list(store.ListKindFollowers, "you_only", "still_mutual"),
		list(store.ListKindFollowing, "they_only", "still_mutual"),
	})

	got := map[string]store.EndedMutual{}
	for _, m := range insights(t, s, account.ID, 0).EndedMutuals {
		got[m.Username] = m
	}

	want := map[string]store.MutualEnding{
		"they_first": store.EndedTheyLeftFirst,
		"you_first":  store.EndedYouLeftFirst,
		"same_gap":   store.EndedSameGap,
		"they_only":  store.EndedTheyLeftFirst,
		"you_only":   store.EndedYouLeftFirst,
	}
	if len(got) != len(want) {
		t.Fatalf("ended mutuals = %v, want %d of them", got, len(want))
	}
	for name, ending := range want {
		if got[name].Ending != ending {
			t.Errorf("%s = %q, want %q", name, got[name].Ending, ending)
		}
	}
	if !got["they_only"].YouStillFollow || got["they_only"].StillFollowsYou {
		t.Errorf("they_only should still be followed by you and not follow you: %+v", got["they_only"])
	}
	if !got["same_gap"].LastMutual.Equal(day(10)) {
		t.Errorf("same_gap last mutual = %s, want day 10", got["same_gap"].LastMutual)
	}
}

// An execution that did not carry the following list says nothing about it, so
// it cannot be where your side of a follow ended.
func TestEndedMutualsSkipExecutionsWithoutTheList(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	withListsAt(t, s, account.ID, "first", day(0), []store.ListSnapshot{
		list(store.ListKindFollowers, "friend"),
		list(store.ListKindFollowing, "friend"),
	})
	// Followers only: friend has left, and your side is unknown here.
	withListsAt(t, s, account.ID, "second", day(10), []store.ListSnapshot{
		list(store.ListKindFollowers),
	})
	withListsAt(t, s, account.ID, "third", day(20), []store.ListSnapshot{
		list(store.ListKindFollowers),
		list(store.ListKindFollowing),
	})

	ended := insights(t, s, account.ID, 0).EndedMutuals
	if len(ended) != 1 {
		t.Fatalf("ended mutuals = %+v, want one", ended)
	}
	// Your unfollow falls anywhere between day 0 and day 20, which overlaps
	// their departure between day 0 and day 10.
	if ended[0].Ending != store.EndedSameGap {
		t.Fatalf("ending = %q, want %q", ended[0].Ending, store.EndedSameGap)
	}
}

func TestEndedMutualsNeedBothListsInTheLatestExecution(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	withListsAt(t, s, account.ID, "first", day(0), []store.ListSnapshot{
		list(store.ListKindFollowers, "friend"),
		list(store.ListKindFollowing, "friend"),
	})
	withListsAt(t, s, account.ID, "second", day(10), []store.ListSnapshot{
		list(store.ListKindFollowers),
	})

	if n := len(insights(t, s, account.ID, 0).EndedMutuals); n != 0 {
		t.Fatalf("found %d ended mutuals without a current following list, want 0", n)
	}
}

func TestRepeatLeaversCountEveryDeparture(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	for i, names := range [][]string{
		{"yoyo", "twice", "once"},
		{"twice"},
		{"yoyo"},
		{"twice"},
		{"yoyo"},
	} {
		withListsAt(t, s, account.ID, string(rune('a'+i)), day(i*10), []store.ListSnapshot{
			list(store.ListKindFollowers, names...),
		})
	}

	got := insights(t, s, account.ID, 0).RepeatLeavers
	if len(got) != 2 {
		t.Fatalf("repeat leavers = %+v, want yoyo and twice", got)
	}
	if got[0].Username != "twice" && got[0].Username != "yoyo" {
		t.Fatalf("unexpected leaver %s", got[0].Username)
	}
	byName := map[string]store.RepeatLeaver{got[0].Username: got[0], got[1].Username: got[1]}
	if byName["yoyo"].Departures != 2 || !byName["yoyo"].FollowsNow {
		t.Errorf("yoyo = %+v, want two departures and following now", byName["yoyo"])
	}
	if byName["twice"].Departures != 2 || byName["twice"].FollowsNow {
		t.Errorf("twice = %+v, want two departures and gone now", byName["twice"])
	}
}

func TestCohortsCountSurvivorsByFollowMonth(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	march := time.Date(2025, 3, 5, 0, 0, 0, 0, time.UTC).Unix()
	may := time.Date(2025, 5, 20, 0, 0, 0, 0, time.UTC).Unix()
	laterMay := time.Date(2025, 5, 25, 0, 0, 0, 0, time.UTC).Unix()

	withListsAt(t, s, account.ID, "first", time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC), []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{"old_friend": march}),
	})
	withListsAt(t, s, account.ID, "second", time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{"old_friend": march, "stayer": may, "leaver": laterMay}),
		list(store.ListKindFollowing),
	})
	withListsAt(t, s, account.ID, "third", time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC), []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{"old_friend": march, "stayer": may}),
	})

	got := insights(t, s, account.ID, 0)
	if len(got.Cohorts) != 2 {
		t.Fatalf("cohorts = %+v, want March and May", got.Cohorts)
	}
	mayCohort, marchCohort := got.Cohorts[0], got.Cohorts[1]
	if mayCohort.Month != "2025-05" || mayCohort.Followed != 2 || mayCohort.Stayed != 1 || mayCohort.Percent != 50 {
		t.Errorf("May = %+v, want 1 of 2 stayed", mayCohort)
	}
	if mayCohort.BeforeTracking {
		t.Errorf("May began after the first execution and is fully observed")
	}
	if marchCohort.Month != "2025-03" || marchCohort.Stayed != 1 || !marchCohort.BeforeTracking {
		t.Errorf("March = %+v, want one survivor, flagged as before tracking", marchCohort)
	}
}

func TestCohortsLeaveOutUndatedFollowers(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	withListsAt(t, s, account.ID, "first", day(0), []store.ListSnapshot{
		list(store.ListKindFollowers, "undated_one", "undated_two"),
	})

	got := insights(t, s, account.ID, 0)
	if len(got.Cohorts) != 0 || got.UndatedFollowers != 2 {
		t.Fatalf("cohorts = %+v, undated = %d; want none and 2", got.Cohorts, got.UndatedFollowers)
	}
}

// A rename is the same person under a new name, with the same follow date. It
// must not count as somebody in that month who left.
func TestCohortsIgnoreTheOldNameOfARename(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	at := day(-5).Unix()
	withListsAt(t, s, account.ID, "first", day(0), []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{"old_name": at}),
	})
	withListsAt(t, s, account.ID, "second", day(10), []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{"new_name": at}),
	})

	got := insights(t, s, account.ID, 0).Cohorts
	if len(got) != 1 || got[0].Followed != 1 || got[0].Stayed != 1 {
		t.Fatalf("cohorts = %+v, want one person who stayed", got)
	}
}

func TestPendingRequestsOldestFirst(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	withListsAt(t, s, account.ID, "first", day(100), []store.ListSnapshot{
		list(store.ListKindFollowers, "someone"),
		timed(store.ListKindPendingRequests, map[string]int64{
			"recent":  day(95).Unix(),
			"ancient": day(10).Unix(),
		}),
	})

	got := insights(t, s, account.ID, 0).PendingRequests
	if len(got) != 2 || got[0].Username != "ancient" || got[1].Username != "recent" {
		t.Fatalf("pending = %+v, want ancient then recent", got)
	}
	if got[0].AgeDays == nil || *got[0].AgeDays != 90 {
		t.Fatalf("ancient age = %v, want 90 days", got[0].AgeDays)
	}
}

// A list the latest export did not carry is unknown, not empty.
func TestCurrentStateListsAreNilWhenNotRecorded(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	withListsAt(t, s, account.ID, "first", day(0), []store.ListSnapshot{
		list(store.ListKindFollowers, "someone"),
	})

	got := insights(t, s, account.ID, 0)
	if got.PendingRecorded || got.CloseFriendsRecorded || got.PendingRequests != nil || got.CloseFriendsNotFollowing != nil {
		t.Fatalf("pending = %v, close friends = %v; want both unknown", got.PendingRequests, got.CloseFriendsNotFollowing)
	}
	if got.FollowingRecorded {
		t.Fatalf("following was never recorded")
	}
}

func TestCloseFriendsWhoDoNotFollowYou(t *testing.T) {
	s := newStore(t)
	account := newAccount(t, s)

	withListsAt(t, s, account.ID, "first", day(0), []store.ListSnapshot{
		list(store.ListKindFollowers, "mutual_friend"),
		list(store.ListKindCloseFriends, "mutual_friend", "one_sided"),
	})

	got := insights(t, s, account.ID, 0).CloseFriendsNotFollowing
	if len(got) != 1 || got[0].Username != "one_sided" {
		t.Fatalf("close friends not following = %+v, want one_sided", got)
	}
}
