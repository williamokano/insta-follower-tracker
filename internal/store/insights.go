package store

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// The lists the insights read. Pending requests and close friends are named
// here for the same reason the kinds in models.go are: storage reasons about
// them without importing the parser.
const (
	ListKindCloseFriends    = "close_friends"
	ListKindPendingRequests = "pending_follow_requests"
)

// DefaultQuickUnfollowDays is how short a stay has to be, at most, for a
// departure to count as a quick unfollow when the caller does not say.
const DefaultQuickUnfollowDays = 30

// Insights are the questions the stored history can answer beyond who came and
// went: patterns that only show up across several executions, and clean-up
// lists read from the latest one.
//
// Everything here is derived from data already recorded. Nothing is guessed:
// where the export cannot settle a question, the answer says so rather than
// picking one.
type Insights struct {
	// Latest is the execution the current-state sections are read from. Nil
	// when the account has no processed execution yet.
	Latest *Upload `json:"latest"`
	// Executions is how many processed executions the history holds.
	Executions int `json:"executions"`

	QuickUnfollowDays int             `json:"quick_unfollow_days"`
	QuickUnfollows    []QuickUnfollow `json:"quick_unfollows"`
	EndedMutuals      []EndedMutual   `json:"ended_mutuals"`
	RepeatLeavers     []RepeatLeaver  `json:"repeat_leavers"`
	Cohorts           []Cohort        `json:"cohorts"`
	// UndatedFollowers counts followers the cohorts had to leave out because
	// no export ever gave their follow date, which HTML exports never do.
	UndatedFollowers int `json:"undated_followers"`

	// PendingRequests is nil when the latest execution did not carry the list,
	// which is different from carrying it empty. PendingRecorded says which.
	PendingRequests []PendingRequest `json:"pending_requests"`
	PendingRecorded bool             `json:"pending_recorded"`
	// CloseFriendsNotFollowing is nil when the latest execution did not carry
	// the close friends list. CloseFriendsRecorded says which.
	CloseFriendsNotFollowing []Follower `json:"close_friends_not_following"`
	CloseFriendsRecorded     bool       `json:"close_friends_recorded"`
	// FollowingRecorded reports whether the history ever carried the
	// following list, without which the mutual-based sections are empty for
	// want of evidence rather than because nothing happened.
	FollowingRecorded bool `json:"following_recorded"`
}

// QuickUnfollow is somebody who followed you, was followed back, and left again
// within a short time: the follow-for-follow pattern.
//
// The departure itself is never dated, only bracketed by two executions, so the
// stay is reported as an upper bound: they were certainly gone by GoneBy.
type QuickUnfollow struct {
	Username   string    `json:"username"`
	Href       string    `json:"href,omitempty"`
	FollowedAt time.Time `json:"followed_at"`
	LastSeen   time.Time `json:"last_seen"`
	GoneBy     time.Time `json:"gone_by"`
	// StayedAtMostDays is GoneBy minus FollowedAt, rounded up.
	StayedAtMostDays int `json:"stayed_at_most_days"`
	// YouStillFollow reports whether the latest execution still has them in
	// your following list, which is the part worth acting on.
	YouStillFollow bool `json:"you_still_follow"`
	// FollowsAgain reports whether they are back in the latest follower list.
	FollowsAgain bool `json:"follows_again"`
}

// MutualEnding says which side of a mutual follow ended it first.
type MutualEnding string

const (
	// EndedTheyLeftFirst: they were gone from your followers while you were
	// still following them.
	EndedTheyLeftFirst MutualEnding = "they_left_first"
	// EndedYouLeftFirst: you had stopped following them while they were still
	// following you.
	EndedYouLeftFirst MutualEnding = "you_left_first"
	// EndedSameGap: both happened between the same two executions, so the
	// export cannot order them.
	EndedSameGap MutualEnding = "same_gap"
)

// Label renders an ending for a person to read.
func (m MutualEnding) Label() string {
	switch m {
	case EndedTheyLeftFirst:
		return "they left first"
	case EndedYouLeftFirst:
		return "you left first"
	default:
		return "cannot tell"
	}
}

// EndedMutual is an account that at some point both followed you and was
// followed by you, and no longer is both.
type EndedMutual struct {
	Username string       `json:"username"`
	Href     string       `json:"href,omitempty"`
	Ending   MutualEnding `json:"ending"`
	Label    string       `json:"label"`
	// LastMutual is the last execution in which the follow went both ways.
	LastMutual time.Time `json:"last_mutual"`
	// StillFollowsYou and YouStillFollow describe the latest execution.
	StillFollowsYou bool `json:"still_follows_you"`
	YouStillFollow  bool `json:"you_still_follow"`
}

// RepeatLeaver is somebody who has left the follower list more than once.
type RepeatLeaver struct {
	Username   string `json:"username"`
	Href       string `json:"href,omitempty"`
	Departures int    `json:"departures"`
	// FollowsNow reports whether they are in the latest follower list, i.e.
	// whether they are currently in a "back" phase.
	FollowsNow bool `json:"follows_now"`
}

// Cohort is everybody whose follow date falls in one calendar month, and how
// many of them still follow you.
type Cohort struct {
	Month    string `json:"month"`
	Followed int    `json:"followed"`
	Stayed   int    `json:"stayed"`
	// Percent is Stayed over Followed, rounded down.
	Percent int `json:"percent"`
	// BeforeTracking marks a month that began before the first execution.
	// People who followed and left before then were never seen, so the
	// retention of such a month is overstated: only the survivors are counted.
	BeforeTracking bool `json:"before_tracking"`
}

// PendingRequest is a follow request you sent that is still unanswered.
type PendingRequest struct {
	Username    string     `json:"username"`
	Href        string     `json:"href,omitempty"`
	RequestedAt *time.Time `json:"requested_at,omitempty"`
	// AgeDays is how long the request had waited when the latest export was
	// generated. Nil when the export did not date it.
	AgeDays *int `json:"age_days,omitempty"`
}

// insightExecution is one execution, reduced to what the insights read.
type insightExecution struct {
	id       int64
	takenAt  time.Time
	recorded map[string]bool
	// members maps list kind to user to follow timestamp (nil when undated).
	members map[string]map[int64]*int64
}

func (e *insightExecution) has(kind string, userID int64) bool {
	_, ok := e.members[kind][userID]
	return ok
}

type insightUser struct {
	username string
	href     string
}

// insightKinds are the lists the insights read, and the only ones loaded.
var insightKinds = []string{ListKindFollowers, ListKindFollowing, ListKindCloseFriends, ListKindPendingRequests}

// Insights computes every insight for an account.
//
// The whole history is read into memory once and reasoned about there. At the
// scale of one person's archive that is a few thousand rows per execution, and
// several of the questions (who left first, when a mutual ended) walk an
// account's membership across executions in a way that reads far more clearly
// as a loop than as SQL.
func (s *Store) Insights(ctx context.Context, accountID int64, quickDays int) (Insights, error) {
	if quickDays <= 0 {
		quickDays = DefaultQuickUnfollowDays
	}
	out := Insights{
		QuickUnfollowDays: quickDays,
		QuickUnfollows:    []QuickUnfollow{},
		EndedMutuals:      []EndedMutual{},
		RepeatLeavers:     []RepeatLeaver{},
		Cohorts:           []Cohort{},
	}

	latest, err := s.LatestCompletedUpload(ctx, accountID)
	if err != nil {
		return out, err
	}
	if latest == nil {
		return out, nil
	}
	out.Latest = latest

	execs, err := s.loadInsightExecutions(ctx, accountID)
	if err != nil {
		return out, err
	}
	out.Executions = len(execs)
	if len(execs) == 0 {
		return out, nil
	}
	users, err := s.loadInsightUsers(ctx, accountID)
	if err != nil {
		return out, err
	}
	departures, err := s.loadFollowerDepartures(ctx, accountID)
	if err != nil {
		return out, err
	}

	for _, e := range execs {
		if e.recorded[ListKindFollowing] {
			out.FollowingRecorded = true
			break
		}
	}

	out.QuickUnfollows = quickUnfollows(execs, users, departures, quickDays)
	out.EndedMutuals = endedMutuals(execs, users, departures)
	out.RepeatLeavers = repeatLeavers(execs, users, departures)
	out.Cohorts, out.UndatedFollowers = cohorts(execs, departures)
	last := execs[len(execs)-1]
	out.PendingRequests = pendingRequests(last, users)
	out.PendingRecorded = last.recorded[ListKindPendingRequests]
	out.CloseFriendsNotFollowing = closeFriendsNotFollowing(last, users)
	out.CloseFriendsRecorded = last.recorded[ListKindCloseFriends]
	return out, nil
}

func (s *Store) loadInsightExecutions(ctx context.Context, accountID int64) ([]*insightExecution, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(snapshot_taken_at, uploaded_at) FROM uploads
		WHERE account_id = ? AND status = 'completed' AND sequence_no IS NOT NULL
		ORDER BY sequence_no`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list executions for insights: %w", err)
	}
	defer rows.Close()

	var execs []*insightExecution
	byID := map[int64]*insightExecution{}
	for rows.Next() {
		var (
			e       insightExecution
			takenAt int64
		)
		if err := rows.Scan(&e.id, &takenAt); err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		e.takenAt = time.Unix(takenAt, 0).UTC()
		e.recorded = map[string]bool{}
		e.members = map[string]map[int64]*int64{}
		execs = append(execs, &e)
		byID[e.id] = &e
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read executions: %w", err)
	}

	lists, err := s.db.QueryContext(ctx, `
		SELECT ul.upload_id, ul.list_kind FROM upload_lists ul
		JOIN uploads u ON u.id = ul.upload_id
		WHERE u.account_id = ? AND u.status = 'completed'
		  AND ul.list_kind IN (?, ?, ?, ?)`,
		accountID, insightKinds[0], insightKinds[1], insightKinds[2], insightKinds[3])
	if err != nil {
		return nil, fmt.Errorf("list recorded kinds: %w", err)
	}
	defer lists.Close()
	for lists.Next() {
		var (
			uploadID int64
			kind     string
		)
		if err := lists.Scan(&uploadID, &kind); err != nil {
			return nil, fmt.Errorf("scan recorded kind: %w", err)
		}
		if e, ok := byID[uploadID]; ok {
			e.recorded[kind] = true
			e.members[kind] = map[int64]*int64{}
		}
	}
	if err := lists.Err(); err != nil {
		return nil, fmt.Errorf("read recorded kinds: %w", err)
	}

	members, err := s.db.QueryContext(ctx, `
		SELECT sm.upload_id, sm.list_kind, sm.user_id, sm.followed_at
		FROM snapshot_members sm
		JOIN uploads u ON u.id = sm.upload_id
		WHERE u.account_id = ? AND u.status = 'completed'
		  AND sm.list_kind IN (?, ?, ?, ?)`,
		accountID, insightKinds[0], insightKinds[1], insightKinds[2], insightKinds[3])
	if err != nil {
		return nil, fmt.Errorf("list members for insights: %w", err)
	}
	defer members.Close()
	for members.Next() {
		var (
			uploadID, userID int64
			kind             string
			followedAt       *int64
		)
		if err := members.Scan(&uploadID, &kind, &userID, &followedAt); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		e, ok := byID[uploadID]
		if !ok || e.members[kind] == nil {
			continue
		}
		e.members[kind][userID] = followedAt
	}
	if err := members.Err(); err != nil {
		return nil, fmt.Errorf("read members: %w", err)
	}

	return execs, nil
}

func (s *Store) loadInsightUsers(ctx context.Context, accountID int64) (map[int64]insightUser, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, href FROM users WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	out := map[int64]insightUser{}
	for rows.Next() {
		var (
			id int64
			u  insightUser
		)
		if err := rows.Scan(&id, &u.username, &u.href); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out[id] = u
	}
	return out, rows.Err()
}

// followerDeparture is one recorded departure from the follower list.
type followerDeparture struct {
	userID   int64
	uploadID int64
	reason   DepartureReason
}

func (s *Store) loadFollowerDepartures(ctx context.Context, accountID int64) ([]followerDeparture, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, upload_id, COALESCE(departure_reason, '') FROM changes
		WHERE account_id = ? AND list_kind = 'followers' AND change_type = 'unfollowed'`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list departures: %w", err)
	}
	defer rows.Close()

	var out []followerDeparture
	for rows.Next() {
		var (
			d      followerDeparture
			reason string
		)
		if err := rows.Scan(&d.userID, &d.uploadID, &reason); err != nil {
			return nil, fmt.Errorf("scan departure: %w", err)
		}
		d.reason = DepartureReason(reason)
		out = append(out, d)
	}
	return out, rows.Err()
}

// quickUnfollows finds follow-for-follow departures.
//
// Three things have to hold, each read from data the export actually carried:
// the follow is dated (JSON exports only), you followed them back in the last
// execution they were seen in, and the execution that noticed them gone is
// close enough to the follow date to bound the stay within the window. A long
// gap between exports therefore hides quick unfollows rather than inventing
// them: the bound is too loose to claim anything.
func quickUnfollows(execs []*insightExecution, users map[int64]insightUser, departures []followerDeparture, days int) []QuickUnfollow {
	index := map[int64]int{}
	for i, e := range execs {
		index[e.id] = i
	}
	latest := execs[len(execs)-1]
	window := time.Duration(days) * 24 * time.Hour

	out := []QuickUnfollow{}
	for _, d := range departures {
		// Renames and your own blocks are not somebody choosing to leave.
		if d.reason == DepartureRenamed || d.reason == DepartureYouBlocked {
			continue
		}
		i, ok := index[d.uploadID]
		if !ok || i == 0 {
			continue
		}
		prev, cur := execs[i-1], execs[i]
		followedAt := prev.members[ListKindFollowers][d.userID]
		if followedAt == nil || !prev.has(ListKindFollowing, d.userID) {
			continue
		}

		followed := time.Unix(*followedAt, 0).UTC()
		stay := cur.takenAt.Sub(followed)
		if stay < 0 || stay > window {
			continue
		}

		u := users[d.userID]
		out = append(out, QuickUnfollow{
			Username:         u.username,
			Href:             u.href,
			FollowedAt:       followed,
			LastSeen:         prev.takenAt,
			GoneBy:           cur.takenAt,
			StayedAtMostDays: ceilDays(stay),
			YouStillFollow:   latest.has(ListKindFollowing, d.userID),
			FollowsAgain:     latest.has(ListKindFollowers, d.userID),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].GoneBy.Equal(out[j].GoneBy) {
			return out[i].GoneBy.After(out[j].GoneBy)
		}
		return out[i].Username < out[j].Username
	})
	return out
}

// endedMutuals finds follows that used to go both ways and no longer do, and
// works out which side ended them first where the history can say.
//
// Each side's departure is bracketed by the last execution that still showed it
// and the first that did not, counting only executions that carried that list.
// When one side's bracket closes before the other's opens, the order is known.
// When the brackets overlap, both happened in the same gap and the export
// cannot tell them apart.
func endedMutuals(execs []*insightExecution, users map[int64]insightUser, departures []followerDeparture) []EndedMutual {
	latest := execs[len(execs)-1]
	// Without both lists in the latest execution, "no longer mutual" cannot be
	// established.
	if !latest.recorded[ListKindFollowers] || !latest.recorded[ListKindFollowing] {
		return []EndedMutual{}
	}

	// A rename ends nothing: the same person carries on under the new name.
	renamed := renamedUsers(departures)

	lastMutual := map[int64]int{}
	for i, e := range execs {
		if !e.recorded[ListKindFollowing] {
			continue
		}
		for userID := range e.members[ListKindFollowers] {
			if e.has(ListKindFollowing, userID) && !renamed[userID] {
				lastMutual[userID] = i
			}
		}
	}

	out := []EndedMutual{}
	for userID, m := range lastMutual {
		followsYou := latest.has(ListKindFollowers, userID)
		youFollow := latest.has(ListKindFollowing, userID)
		if followsYou && youFollow {
			continue
		}

		theirLastSeen, theirGone := bracket(execs, m, ListKindFollowers, userID)
		yourLastSeen, yourGone := bracket(execs, m, ListKindFollowing, userID)

		var ending MutualEnding
		switch {
		case theirGone >= 0 && (yourGone < 0 || theirGone <= yourLastSeen):
			ending = EndedTheyLeftFirst
		case yourGone >= 0 && (theirGone < 0 || yourGone <= theirLastSeen):
			ending = EndedYouLeftFirst
		default:
			ending = EndedSameGap
		}

		u := users[userID]
		out = append(out, EndedMutual{
			Username:        u.username,
			Href:            u.href,
			Ending:          ending,
			Label:           ending.Label(),
			LastMutual:      execs[m].takenAt,
			StillFollowsYou: followsYou,
			YouStillFollow:  youFollow,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastMutual.Equal(out[j].LastMutual) {
			return out[i].LastMutual.After(out[j].LastMutual)
		}
		return out[i].Username < out[j].Username
	})
	return out
}

// bracket walks forward from execution from, where the user is known to be in
// the list, and returns the index of the last execution still showing them and
// of the first that did not. gone is -1 when they never left. Executions that
// did not carry the list are skipped: they say nothing either way.
func bracket(execs []*insightExecution, from int, kind string, userID int64) (lastSeen, gone int) {
	lastSeen = from
	for i := from + 1; i < len(execs); i++ {
		if !execs[i].recorded[kind] {
			continue
		}
		if !execs[i].has(kind, userID) {
			return lastSeen, i
		}
		lastSeen = i
	}
	return lastSeen, -1
}

// repeatLeavers lists people who left the follower list at least twice.
// Departures that were renames or your own blocks are not counted.
func repeatLeavers(execs []*insightExecution, users map[int64]insightUser, departures []followerDeparture) []RepeatLeaver {
	counts := map[int64]int{}
	for _, d := range departures {
		if d.reason == DepartureRenamed || d.reason == DepartureYouBlocked {
			continue
		}
		counts[d.userID]++
	}

	latest := execs[len(execs)-1]
	out := []RepeatLeaver{}
	for userID, n := range counts {
		if n < 2 {
			continue
		}
		u := users[userID]
		out = append(out, RepeatLeaver{
			Username:   u.username,
			Href:       u.href,
			Departures: n,
			FollowsNow: latest.has(ListKindFollowers, userID),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Departures != out[j].Departures {
			return out[i].Departures > out[j].Departures
		}
		return out[i].Username < out[j].Username
	})
	return out
}

// cohorts groups every follower ever seen by the month they first followed, and
// counts how many of each month are in the latest follower list.
//
// A person's earliest recorded follow date is used, so somebody who left and
// came back stays in their original month. Names that turned out to be renames
// are left out, since the same person is counted under the new name, which
// keeps the follow date across a rename.
func cohorts(execs []*insightExecution, departures []followerDeparture) (out []Cohort, undated int) {
	renamed := renamedUsers(departures)

	earliest := map[int64]int64{}
	seen := map[int64]bool{}
	for _, e := range execs {
		for userID, followedAt := range e.members[ListKindFollowers] {
			if renamed[userID] {
				continue
			}
			seen[userID] = true
			if followedAt == nil {
				continue
			}
			if at, ok := earliest[userID]; !ok || *followedAt < at {
				earliest[userID] = *followedAt
			}
		}
	}
	undated = len(seen) - len(earliest)

	latest := execs[len(execs)-1]
	firstTaken := execs[0].takenAt
	byMonth := map[string]*Cohort{}
	for userID, at := range earliest {
		t := time.Unix(at, 0).UTC()
		month := t.Format("2006-01")
		c, ok := byMonth[month]
		if !ok {
			start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
			c = &Cohort{Month: month, BeforeTracking: start.Before(firstTaken)}
			byMonth[month] = c
		}
		c.Followed++
		if latest.has(ListKindFollowers, userID) {
			c.Stayed++
		}
	}

	out = make([]Cohort, 0, len(byMonth))
	for _, c := range byMonth {
		c.Percent = c.Stayed * 100 / c.Followed
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Month > out[j].Month })
	return out, undated
}

// pendingRequests lists the latest execution's unanswered follow requests,
// oldest first, so the ones worth withdrawing lead.
func pendingRequests(latest *insightExecution, users map[int64]insightUser) []PendingRequest {
	if !latest.recorded[ListKindPendingRequests] {
		return nil
	}
	out := []PendingRequest{}
	for userID, at := range latest.members[ListKindPendingRequests] {
		u := users[userID]
		p := PendingRequest{Username: u.username, Href: u.href}
		if at != nil {
			requested := time.Unix(*at, 0).UTC()
			age := int(latest.takenAt.Sub(requested) / (24 * time.Hour))
			if age < 0 {
				age = 0
			}
			p.RequestedAt = &requested
			p.AgeDays = &age
		}
		out = append(out, p)
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].RequestedAt, out[j].RequestedAt
		switch {
		case a != nil && b != nil && !a.Equal(*b):
			return a.Before(*b)
		case (a == nil) != (b == nil):
			return a != nil
		default:
			return out[i].Username < out[j].Username
		}
	})
	return out
}

// closeFriendsNotFollowing lists close friends who are not in the latest
// follower list.
func closeFriendsNotFollowing(latest *insightExecution, users map[int64]insightUser) []Follower {
	if !latest.recorded[ListKindCloseFriends] {
		return nil
	}
	out := []Follower{}
	for userID := range latest.members[ListKindCloseFriends] {
		if latest.has(ListKindFollowers, userID) {
			continue
		}
		u := users[userID]
		out = append(out, Follower{Username: u.username, Href: u.href})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

// renamedUsers is every name that was recognised as having been renamed away.
func renamedUsers(departures []followerDeparture) map[int64]bool {
	out := map[int64]bool{}
	for _, d := range departures {
		if d.reason == DepartureRenamed {
			out[d.userID] = true
		}
	}
	return out
}

func ceilDays(d time.Duration) int {
	day := 24 * time.Hour
	return int((d + day - 1) / day)
}
