package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ListSnapshot is one relationship list as an execution recorded it.
type ListSnapshot struct {
	Kind    string
	Members []Member
}

// ApplySnapshot records every list an execution carried and then rebuilds the
// account's history around it.
//
// The rebuild is what makes backfilling work. An export generated in 2024 but
// uploaded after one from 2026 belongs between its neighbours in time, not at
// the end of the queue, and inserting it changes which executions the ones on
// either side should be compared against. Because every execution stores its
// complete membership, the whole history can simply be recomputed from scratch,
// which is exact and, at the scale of a personal archive, free.
func (s *Store) ApplySnapshot(ctx context.Context, uploadID int64, lists []ListSnapshot, takenAt time.Time, source string) (SnapshotResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var accountID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT account_id FROM uploads WHERE id = ?`, uploadID).Scan(&accountID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SnapshotResult{}, ErrNotFound
		}
		return SnapshotResult{}, fmt.Errorf("load upload: %w", err)
	}

	// Clear any partial state from an earlier attempt at this upload.
	if _, err := tx.ExecContext(ctx, `DELETE FROM snapshot_members WHERE upload_id = ?`, uploadID); err != nil {
		return SnapshotResult{}, fmt.Errorf("clear snapshot members: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM upload_lists WHERE upload_id = ?`, uploadID); err != nil {
		return SnapshotResult{}, fmt.Errorf("clear upload lists: %w", err)
	}

	upsertUser, err := tx.PrepareContext(ctx, `
		INSERT INTO users (account_id, username, href) VALUES (?, ?, ?)
		ON CONFLICT(account_id, username) DO UPDATE SET href = CASE
			WHEN excluded.href <> '' THEN excluded.href ELSE users.href END
		RETURNING id`)
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("prepare user upsert: %w", err)
	}
	defer upsertUser.Close()

	insertMember, err := tx.PrepareContext(ctx,
		`INSERT INTO snapshot_members (upload_id, list_kind, user_id, followed_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(upload_id, list_kind, user_id) DO NOTHING`)
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("prepare member insert: %w", err)
	}
	defer insertMember.Close()

	for _, list := range lists {
		for _, m := range list.Members {
			var userID int64
			if err := upsertUser.QueryRowContext(ctx, accountID, m.Username, m.Href).Scan(&userID); err != nil {
				return SnapshotResult{}, fmt.Errorf("upsert user %q: %w", m.Username, err)
			}
			if _, err := insertMember.ExecContext(ctx, uploadID, list.Kind, userID, m.FollowedAt); err != nil {
				return SnapshotResult{}, fmt.Errorf("insert member %q: %w", m.Username, err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO upload_lists (upload_id, list_kind, member_count) VALUES (?, ?, 0)
			 ON CONFLICT(upload_id, list_kind) DO NOTHING`, uploadID, list.Kind); err != nil {
			return SnapshotResult{}, fmt.Errorf("register list %q: %w", list.Kind, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE uploads SET status = 'completed', processed_at = ?,
		       snapshot_taken_at = ?, snapshot_source = ?, error_message = ''
		WHERE id = ?`,
		time.Now().UTC().Unix(), takenAt.UTC().Unix(), source, uploadID); err != nil {
		return SnapshotResult{}, fmt.Errorf("complete upload: %w", err)
	}

	if err := recomputeAccount(ctx, tx, accountID); err != nil {
		return SnapshotResult{}, err
	}

	var (
		result       SnapshotResult
		sequenceNo   int64
		prevUploadID *int64
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT sequence_no, follower_count, added_count, removed_count FROM uploads WHERE id = ?`,
		uploadID).Scan(&sequenceNo, &result.FollowerCount, &result.AddedCount, &result.RemovedCount); err != nil {
		return SnapshotResult{}, fmt.Errorf("reload upload: %w", err)
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT prev_upload_id FROM changes WHERE upload_id = ? LIMIT 1`, uploadID).Scan(&prevUploadID); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return SnapshotResult{}, fmt.Errorf("reload predecessor: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return SnapshotResult{}, fmt.Errorf("commit snapshot: %w", err)
	}

	result.UploadID = uploadID
	result.SequenceNo = sequenceNo
	result.PrevUploadID = prevUploadID
	result.IsBaseline = sequenceNo == 1
	return result, nil
}

// recomputeAccount renumbers an account's executions into snapshot-date order
// and rebuilds every diff between them, for every list they recorded.
//
// Rebuilding wholesale rather than patching around the inserted execution is
// deliberate: the set of affected pairs is easy to get subtly wrong, and a
// wrong diff is invisible until somebody trusts it.
func recomputeAccount(ctx context.Context, tx *sql.Tx, accountID int64) error {
	ordered, err := executionsInDateOrder(ctx, tx, accountID)
	if err != nil {
		return err
	}

	// Release the sequence numbers first: they are unique per account, so
	// renumbering in place would collide with the numbering still in force.
	if _, err := tx.ExecContext(ctx,
		`UPDATE uploads SET sequence_no = NULL WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("clear sequence numbers: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM changes WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("clear changes: %w", err)
	}

	now := time.Now().UTC().Unix()
	for i, uploadID := range ordered {
		if _, err := tx.ExecContext(ctx,
			`UPDATE uploads SET sequence_no = ? WHERE id = ?`, i+1, uploadID); err != nil {
			return fmt.Errorf("assign sequence number: %w", err)
		}

		// Member counts do not depend on ordering, but are refreshed here so
		// one pass leaves every derived number consistent.
		if _, err := tx.ExecContext(ctx, `
			UPDATE upload_lists SET member_count = (
				SELECT COUNT(*) FROM snapshot_members sm
				WHERE sm.upload_id = upload_lists.upload_id AND sm.list_kind = upload_lists.list_kind
			), added_count = 0, removed_count = 0
			WHERE upload_id = ?`, uploadID); err != nil {
			return fmt.Errorf("refresh list counts: %w", err)
		}

		if i == 0 {
			// The oldest execution is the baseline: nothing precedes it.
			if err := syncFollowerColumns(ctx, tx, uploadID); err != nil {
				return err
			}
			continue
		}

		prev := ordered[i-1]
		if err := recomputeListsBetween(ctx, tx, accountID, uploadID, prev, now); err != nil {
			return err
		}
		if err := syncFollowerColumns(ctx, tx, uploadID); err != nil {
			return err
		}
	}
	return nil
}

// recomputeListsBetween diffs every list an execution shares with the one
// before it.
//
// A list absent from one of the two is skipped rather than treated as empty:
// an export that did not carry a list says nothing about it, and reading that
// silence as "everybody left" would invent changes, in the same way a
// date-limited export does.
func recomputeListsBetween(ctx context.Context, tx *sql.Tx, accountID, uploadID, prevUploadID, now int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT list_kind FROM upload_lists WHERE upload_id = ?
		INTERSECT
		SELECT list_kind FROM upload_lists WHERE upload_id = ?
		ORDER BY list_kind`, uploadID, prevUploadID)
	if err != nil {
		return fmt.Errorf("list shared kinds: %w", err)
	}
	defer rows.Close()

	var kinds []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return fmt.Errorf("scan kind: %w", err)
		}
		kinds = append(kinds, kind)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read kinds: %w", err)
	}

	for _, kind := range kinds {
		added, err := recordChanges(ctx, tx, accountID, uploadID, prevUploadID, kind, ChangeFollowed, now)
		if err != nil {
			return err
		}
		removed, err := recordChanges(ctx, tx, accountID, uploadID, prevUploadID, kind, ChangeUnfollowed, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE upload_lists SET added_count = ?, removed_count = ? WHERE upload_id = ? AND list_kind = ?`,
			added, removed, uploadID, kind); err != nil {
			return fmt.Errorf("update list counts: %w", err)
		}
	}

	// Why each departure happened, as far as the other lists in the same pair
	// of executions can say. It runs once the changes are in place, because it
	// reads them. A recompute clears every change for the account first, so
	// this reclassifies the whole history rather than only the newest pair.
	return classifyDepartures(ctx, tx, uploadID, prevUploadID)
}

// syncFollowerColumns mirrors the follower list's totals onto the upload row,
// where they have always lived and where the rest of the service reads them.
func syncFollowerColumns(ctx context.Context, tx *sql.Tx, uploadID int64) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE uploads SET
			follower_count = COALESCE((SELECT member_count  FROM upload_lists WHERE upload_id = uploads.id AND list_kind = 'followers'), 0),
			added_count    = COALESCE((SELECT added_count   FROM upload_lists WHERE upload_id = uploads.id AND list_kind = 'followers'), 0),
			removed_count  = COALESCE((SELECT removed_count FROM upload_lists WHERE upload_id = uploads.id AND list_kind = 'followers'), 0)
		WHERE id = ?`, uploadID); err != nil {
		return fmt.Errorf("mirror follower counts: %w", err)
	}
	return nil
}

// executionsInDateOrder lists an account's completed executions oldest first,
// which is the order the history is rebuilt in.
func executionsInDateOrder(ctx context.Context, tx *sql.Tx, accountID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM uploads
		WHERE account_id = ? AND status = 'completed'
		ORDER BY snapshot_taken_at, uploaded_at, id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list executions for recompute: %w", err)
	}
	defer rows.Close()

	var ordered []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		ordered = append(ordered, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read executions: %w", err)
	}
	return ordered, nil
}

// recordChanges writes the one-sided set difference between two snapshots of
// one list. For ChangeFollowed that is (current \ previous); for
// ChangeUnfollowed it is (previous \ current).
func recordChanges(ctx context.Context, tx *sql.Tx, accountID, uploadID, prevUploadID int64, listKind string, kind ChangeType, now int64) (int, error) {
	presentIn, absentFrom := uploadID, prevUploadID
	if kind == ChangeUnfollowed {
		presentIn, absentFrom = prevUploadID, uploadID
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO changes (account_id, upload_id, prev_upload_id, user_id, change_type, created_at, list_kind)
		SELECT ?, ?, ?, sm.user_id, ?, ?, ?
		FROM snapshot_members sm
		WHERE sm.upload_id = ? AND sm.list_kind = ?
		  AND NOT EXISTS (
		      SELECT 1 FROM snapshot_members other
		      WHERE other.upload_id = ? AND other.list_kind = ? AND other.user_id = sm.user_id
		  )`,
		accountID, uploadID, prevUploadID, string(kind), now, listKind,
		presentIn, listKind, absentFrom, listKind)
	if err != nil {
		return 0, fmt.Errorf("record %s changes: %w", kind, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count %s changes: %w", kind, err)
	}
	return int(affected), nil
}
