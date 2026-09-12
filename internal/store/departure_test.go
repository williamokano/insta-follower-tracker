package store_test

import (
	"context"
	"testing"

	"github.com/williamokano/insta-follower-tracker/internal/store"
)

// withLists uploads one execution carrying several named lists.
func withLists(t *testing.T, s *store.Store, accountID int64, name string, lists []store.ListSnapshot) store.Upload {
	t.Helper()
	ctx := context.Background()

	id, err := s.CreateUpload(ctx, store.NewUpload{
		AccountID: accountID, Filename: name, StoredPath: "/tmp/" + name,
		SHA256: "sha-" + name, SizeBytes: 10,
	})
	if err != nil {
		t.Fatalf("create upload %s: %v", name, err)
	}
	if _, err := s.ApplySnapshot(ctx, id, lists, snapshotTime(id), "test"); err != nil {
		t.Fatalf("apply snapshot %s: %v", name, err)
	}
	up, err := s.Upload(ctx, id)
	if err != nil {
		t.Fatalf("load upload %s: %v", name, err)
	}
	return up
}

func list(kind string, names ...string) store.ListSnapshot {
	return store.ListSnapshot{Kind: kind, Members: members(names...)}
}

// timed builds a list whose members carry follow timestamps, which is what
// rename detection pairs on.
func timed(kind string, entries map[string]int64) store.ListSnapshot {
	out := store.ListSnapshot{Kind: kind}
	for name, at := range entries {
		ts := at
		out.Members = append(out.Members, store.Member{
			Username:   name,
			Href:       "https://www.instagram.com/" + name,
			FollowedAt: &ts,
		})
	}
	return out
}

// reasons maps each departure in an execution to its classification.
func reasons(t *testing.T, s *store.Store, uploadID int64) map[string]store.DepartureReason {
	t.Helper()
	changes, err := s.ChangesForUpload(context.Background(), uploadID, store.ListKindFollowers, store.ChangeUnfollowed)
	if err != nil {
		t.Fatalf("load changes: %v", err)
	}
	out := make(map[string]store.DepartureReason, len(changes))
	for _, c := range changes {
		out[c.Username] = c.Reason
	}
	return out
}

func TestDepartureIsClassifiedAgainstTheOtherLists(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	account, err := s.EnsureAccount(ctx, "owner")
	if err != nil {
		t.Fatalf("ensure account: %v", err)
	}

	// Four people leave the follower list for four different reasons.
	withLists(t, s, account.ID, "first", []store.ListSnapshot{
		list(store.ListKindFollowers, "blocked_one", "still_known", "vanisher", "stranger"),
		list(store.ListKindFollowing, "still_known", "vanisher"),
		list(store.ListKindBlocked),
	})
	second := withLists(t, s, account.ID, "second", []store.ListSnapshot{
		list(store.ListKindFollowers),
		// still_known stays in following, so that account plainly still exists.
		// vanisher is gone from following too, which an unfollow cannot cause.
		list(store.ListKindFollowing, "still_known"),
		list(store.ListKindBlocked, "blocked_one"),
	})

	got := reasons(t, s, second.ID)
	want := map[string]store.DepartureReason{
		"blocked_one": store.DepartureYouBlocked,
		"still_known": store.DepartureUnfollowed,
		"vanisher":    store.DepartureVanished,
		"stranger":    store.DepartureUnknown,
	}
	for name, reason := range want {
		if got[name] != reason {
			t.Errorf("%s = %q, want %q", name, got[name], reason)
		}
	}
}

// An export that never carried the following list cannot support any conclusion
// drawn from it. Reading the silence as an empty list would report everybody as
// having vanished.
func TestDeparturesAreUnknownWithoutTheFollowingList(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	account, err := s.EnsureAccount(ctx, "owner")
	if err != nil {
		t.Fatalf("ensure account: %v", err)
	}

	snapshot(t, s, account.ID, "first", "alice", "bob")
	second := snapshot(t, s, account.ID, "second", "alice")

	if got := reasons(t, s, second.ID)["bob"]; got != store.DepartureUnknown {
		t.Fatalf("bob = %q, want %q", got, store.DepartureUnknown)
	}
}

// A rename has no stable id to follow, but it keeps the moment the person
// started following, so a departure and an arrival sharing one are the same
// account under a new name.
func TestRenameIsRecognisedByTheFollowTimestamp(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	account, err := s.EnsureAccount(ctx, "owner")
	if err != nil {
		t.Fatalf("ensure account: %v", err)
	}

	withLists(t, s, account.ID, "first", []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{"old_name": 1600000000, "unrelated": 1700000000}),
	})
	second := withLists(t, s, account.ID, "second", []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{"new_name": 1600000000, "unrelated": 1700000000}),
	})

	changes, err := s.ChangesForUpload(ctx, second.ID, store.ListKindFollowers, "")
	if err != nil {
		t.Fatalf("load changes: %v", err)
	}

	var sawDeparture, sawArrival bool
	for _, c := range changes {
		switch c.Username {
		case "old_name":
			sawDeparture = true
			if c.Reason != store.DepartureRenamed {
				t.Errorf("old_name reason = %q, want %q", c.Reason, store.DepartureRenamed)
			}
			if c.RenamedTo != "new_name" {
				t.Errorf("old_name renamed_to = %q, want new_name", c.RenamedTo)
			}
		case "new_name":
			sawArrival = true
			if c.RenamedFrom != "old_name" {
				t.Errorf("new_name renamed_from = %q, want old_name", c.RenamedFrom)
			}
		}
	}
	if !sawDeparture || !sawArrival {
		t.Fatalf("expected both sides of the rename in %+v", changes)
	}
}

// Two people who happen to share a follow timestamp cannot be paired, and
// guessing between them would invent a rename. Ambiguity stays unclassified.
func TestAmbiguousTimestampsAreNotCalledRenames(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	account, err := s.EnsureAccount(ctx, "owner")
	if err != nil {
		t.Fatalf("ensure account: %v", err)
	}

	withLists(t, s, account.ID, "first", []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{"gone_a": 1600000000, "gone_b": 1600000000}),
	})
	second := withLists(t, s, account.ID, "second", []store.ListSnapshot{
		timed(store.ListKindFollowers, map[string]int64{"new_a": 1600000000, "new_b": 1600000000}),
	})

	for name, reason := range reasons(t, s, second.ID) {
		if reason == store.DepartureRenamed {
			t.Fatalf("%s was called a rename on an ambiguous timestamp", name)
		}
	}
}

// Timestamps are absent from HTML exports, and a missing timestamp must not
// pair with another missing one.
func TestMissingTimestampsNeverPair(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	account, err := s.EnsureAccount(ctx, "owner")
	if err != nil {
		t.Fatalf("ensure account: %v", err)
	}

	snapshot(t, s, account.ID, "first", "old_name")
	second := snapshot(t, s, account.ID, "second", "new_name")

	if got := reasons(t, s, second.ID)["old_name"]; got == store.DepartureRenamed {
		t.Fatal("a rename was inferred from two absent timestamps")
	}
}
