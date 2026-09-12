package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// NetDiff is the comparison between two executions of the same account.
//
// The buckets exist because a naive union of per-execution unfollow events
// answers the wrong question: somebody who unfollowed at execution 3 and
// followed again at execution 7 is not actually lost. Lost is computed as a
// straight set difference between the two snapshots, so such a follower lands
// in Returned instead.
type NetDiff struct {
	// ListKind names which relationship list was compared.
	ListKind  string     `json:"list_kind"`
	From      Upload     `json:"from"`
	To        Upload     `json:"to"`
	Lost      []Follower `json:"lost"`
	Gained    []Follower `json:"gained"`
	Returned  []Follower `json:"returned"`
	Transient []Follower `json:"transient"`
}

const followerSelect = `SELECT u.username, u.href FROM `

func (s *Store) followers(ctx context.Context, what, query string, args ...any) ([]Follower, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer rows.Close()

	out := []Follower{}
	for rows.Next() {
		var f Follower
		if err := rows.Scan(&f.Username, &f.Href); err != nil {
			return nil, fmt.Errorf("scan %s: %w", what, err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CompletedUpload returns a processed execution by id, rejecting ones that are
// still queued or that failed.
func (s *Store) CompletedUpload(ctx context.Context, accountID, uploadID int64) (Upload, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+uploadColumns+` FROM uploads u JOIN accounts a ON a.id = u.account_id
		 WHERE u.id = ? AND u.account_id = ? AND u.status = 'completed'`, uploadID, accountID)
	up, err := scanUpload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, fmt.Errorf("select completed upload: %w", err)
	}
	return up, nil
}

// BoundaryUpload returns the account's first (oldest) or last (newest)
// completed execution.
func (s *Store) BoundaryUpload(ctx context.Context, accountID int64, last bool) (Upload, error) {
	order := "ASC"
	if last {
		order = "DESC"
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+uploadColumns+` FROM uploads u JOIN accounts a ON a.id = u.account_id
		 WHERE u.account_id = ? AND u.status = 'completed'
		 ORDER BY u.sequence_no `+order+` LIMIT 1`, accountID)
	up, err := scanUpload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, fmt.Errorf("select boundary upload: %w", err)
	}
	return up, nil
}

// Diff compares two completed executions and splits the result into the four
// buckets described on NetDiff.
func (s *Store) Diff(ctx context.Context, accountID int64, from, to Upload, listKind string) (NetDiff, error) {
	if listKind == "" {
		listKind = DefaultListKind
	}
	diff := NetDiff{From: from, To: to, ListKind: listKind}
	if from.SequenceNo == nil || to.SequenceNo == nil {
		return diff, errors.New("both executions must be processed before they can be compared")
	}
	fromSeq, toSeq := *from.SequenceNo, *to.SequenceNo

	// In `from` but not in `to`: genuinely gone.
	lost, err := s.followers(ctx, "lost followers", followerSelect+`snapshot_members sm
		JOIN users u ON u.id = sm.user_id
		WHERE sm.upload_id = ? AND sm.list_kind = ?
		  AND NOT EXISTS (SELECT 1 FROM snapshot_members o WHERE o.upload_id = ? AND o.list_kind = ? AND o.user_id = sm.user_id)
		ORDER BY u.username`, from.ID, listKind, to.ID, listKind)
	if err != nil {
		return diff, err
	}
	diff.Lost = lost

	// In `to` but not in `from`: net new.
	gained, err := s.followers(ctx, "gained followers", followerSelect+`snapshot_members sm
		JOIN users u ON u.id = sm.user_id
		WHERE sm.upload_id = ? AND sm.list_kind = ?
		  AND NOT EXISTS (SELECT 1 FROM snapshot_members o WHERE o.upload_id = ? AND o.list_kind = ? AND o.user_id = sm.user_id)
		ORDER BY u.username`, to.ID, listKind, from.ID, listKind)
	if err != nil {
		return diff, err
	}
	diff.Gained = gained

	// Present at both ends but recorded as unfollowing somewhere in between:
	// the left-and-came-back case that makes raw delta aggregation misleading.
	returned, err := s.followers(ctx, "returned followers", followerSelect+`snapshot_members sm
		JOIN users u ON u.id = sm.user_id
		WHERE sm.upload_id = ? AND sm.list_kind = ?
		  AND EXISTS (SELECT 1 FROM snapshot_members o WHERE o.upload_id = ? AND o.list_kind = ? AND o.user_id = sm.user_id)
		  AND EXISTS (
		      SELECT 1 FROM changes c
		      JOIN uploads cu ON cu.id = c.upload_id
		      WHERE c.user_id = sm.user_id
		        AND c.change_type = 'unfollowed'
		        AND c.list_kind = ?
		        AND cu.account_id = ?
		        AND cu.sequence_no > ? AND cu.sequence_no <= ?
		  )
		ORDER BY u.username`, from.ID, listKind, to.ID, listKind, listKind, accountID, fromSeq, toSeq)
	if err != nil {
		return diff, err
	}
	diff.Returned = returned

	// Seen only somewhere in the middle: absent at both ends, so invisible to a
	// plain first-versus-last comparison.
	transient, err := s.followers(ctx, "transient followers", `SELECT DISTINCT u.username, u.href
		FROM snapshot_members sm
		JOIN uploads mu ON mu.id = sm.upload_id
		JOIN users u ON u.id = sm.user_id
		WHERE mu.account_id = ? AND mu.status = 'completed' AND sm.list_kind = ?
		  AND mu.sequence_no > ? AND mu.sequence_no < ?
		  AND NOT EXISTS (SELECT 1 FROM snapshot_members o WHERE o.upload_id = ? AND o.list_kind = ? AND o.user_id = sm.user_id)
		  AND NOT EXISTS (SELECT 1 FROM snapshot_members o WHERE o.upload_id = ? AND o.list_kind = ? AND o.user_id = sm.user_id)
		ORDER BY u.username`, accountID, listKind, fromSeq, toSeq, from.ID, listKind, to.ID, listKind)
	if err != nil {
		return diff, err
	}
	diff.Transient = transient

	return diff, nil
}

// ChangesForUpload lists one execution's deltas. Passing an empty kind returns
// both follows and unfollows.
func (s *Store) ChangesForUpload(ctx context.Context, uploadID int64, listKind string, kind ChangeType) ([]Change, error) {
	if listKind == "" {
		listKind = DefaultListKind
	}
	query := `SELECT u.username, u.href, c.change_type, c.upload_id, cu.sequence_no, c.prev_upload_id, c.created_at
		FROM changes c
		JOIN users u ON u.id = c.user_id
		JOIN uploads cu ON cu.id = c.upload_id
		WHERE c.upload_id = ? AND c.list_kind = ?`
	args := []any{uploadID, listKind}
	if kind != "" {
		query += ` AND c.change_type = ?`
		args = append(args, string(kind))
	}
	query += ` ORDER BY c.change_type, u.username`

	return s.scanChanges(ctx, query, args...)
}

// AllUnfollowers lists every unfollow event ever recorded for an account,
// across all executions. This is deliberately the raw event log: a follower who
// later returned still appears here, which is why Diff exists.
func (s *Store) AllUnfollowers(ctx context.Context, accountID int64, listKind string) ([]Change, error) {
	if listKind == "" {
		listKind = DefaultListKind
	}
	return s.scanChanges(ctx, `SELECT u.username, u.href, c.change_type, c.upload_id, cu.sequence_no,
		       c.prev_upload_id, c.created_at
		FROM changes c
		JOIN users u ON u.id = c.user_id
		JOIN uploads cu ON cu.id = c.upload_id
		WHERE c.account_id = ? AND c.list_kind = ? AND c.change_type = 'unfollowed'
		ORDER BY cu.sequence_no, u.username`, accountID, listKind)
}

// NotFollowingBack returns accounts an execution shows you following that do
// not appear in its follower list.
//
// Unlike everything else here this needs only one execution: it compares two
// lists at the same moment rather than one list across time.
func (s *Store) NotFollowingBack(ctx context.Context, uploadID int64) ([]Follower, error) {
	return s.followers(ctx, "accounts not following back", followerSelect+`snapshot_members sm
		JOIN users u ON u.id = sm.user_id
		WHERE sm.upload_id = ? AND sm.list_kind = 'following'
		  AND NOT EXISTS (
		      SELECT 1 FROM snapshot_members o
		      WHERE o.upload_id = sm.upload_id AND o.list_kind = 'followers' AND o.user_id = sm.user_id
		  )
		ORDER BY u.username`, uploadID)
}

// Fans returns accounts an execution shows following you that you do not
// follow back.
func (s *Store) Fans(ctx context.Context, uploadID int64) ([]Follower, error) {
	return s.followers(ctx, "fans", followerSelect+`snapshot_members sm
		JOIN users u ON u.id = sm.user_id
		WHERE sm.upload_id = ? AND sm.list_kind = 'followers'
		  AND NOT EXISTS (
		      SELECT 1 FROM snapshot_members o
		      WHERE o.upload_id = sm.upload_id AND o.list_kind = 'following' AND o.user_id = sm.user_id
		  )
		ORDER BY u.username`, uploadID)
}

func (s *Store) scanChanges(ctx context.Context, query string, args ...any) ([]Change, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list changes: %w", err)
	}
	defer rows.Close()

	out := []Change{}
	for rows.Next() {
		var (
			c         Change
			createdAt int64
		)
		if err := rows.Scan(&c.Username, &c.Href, &c.ChangeType, &c.UploadID,
			&c.SequenceNo, &c.PrevUploadID, &createdAt); err != nil {
			return nil, fmt.Errorf("scan change: %w", err)
		}
		c.DetectedAt = time.Unix(createdAt, 0).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// MembersForUpload returns the complete follower list recorded by one
// execution.
//
// Every execution stores its whole membership rather than only its deltas, so
// this is a plain read: the list is as available for the first execution, which
// has nothing to diff against, as for any later one.
func (s *Store) MembersForUpload(ctx context.Context, uploadID int64, listKind string) ([]Follower, error) {
	if listKind == "" {
		listKind = DefaultListKind
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.username, u.href, sm.followed_at
		FROM snapshot_members sm
		JOIN users u ON u.id = sm.user_id
		WHERE sm.upload_id = ? AND sm.list_kind = ?
		ORDER BY u.username`, uploadID, listKind)
	if err != nil {
		return nil, fmt.Errorf("list execution members: %w", err)
	}
	defer rows.Close()

	out := []Follower{}
	for rows.Next() {
		var (
			f          Follower
			followedAt *int64
		)
		if err := rows.Scan(&f.Username, &f.Href, &followedAt); err != nil {
			return nil, fmt.Errorf("scan follower: %w", err)
		}
		f.FollowedAt = unixPtr(followedAt)
		out = append(out, f)
	}
	return out, rows.Err()
}

// CurrentFollowers returns the membership of the account's latest completed
// execution.
func (s *Store) CurrentFollowers(ctx context.Context, accountID int64) ([]Follower, error) {
	latest, err := s.BoundaryUpload(ctx, accountID, true)
	if errors.Is(err, ErrNotFound) {
		return []Follower{}, nil
	}
	if err != nil {
		return nil, err
	}
	return s.MembersForUpload(ctx, latest.ID, DefaultListKind)
}
