package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ApplySnapshot records the full membership of an execution and then rebuilds
// the account's history around it.
//
// The rebuild is what makes backfilling work. An export generated in 2024 but
// uploaded after one from 2026 belongs between its neighbours in time, not at
// the end of the queue, and inserting it changes which executions the ones on
// either side should be compared against. Because every execution's complete
// member set is stored, the whole history can simply be recomputed from
// scratch, which is exact and, at the scale of a personal archive, free.
func (s *Store) ApplySnapshot(ctx context.Context, uploadID int64, members []Member, takenAt time.Time, source string) (SnapshotResult, error) {
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
		`INSERT INTO snapshot_members (upload_id, user_id, followed_at) VALUES (?, ?, ?)
		 ON CONFLICT(upload_id, user_id) DO NOTHING`)
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("prepare member insert: %w", err)
	}
	defer insertMember.Close()

	for _, m := range members {
		var userID int64
		if err := upsertUser.QueryRowContext(ctx, accountID, m.Username, m.Href).Scan(&userID); err != nil {
			return SnapshotResult{}, fmt.Errorf("upsert user %q: %w", m.Username, err)
		}
		if _, err := insertMember.ExecContext(ctx, uploadID, userID, m.FollowedAt); err != nil {
			return SnapshotResult{}, fmt.Errorf("insert member %q: %w", m.Username, err)
		}
	}

	var followerCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM snapshot_members WHERE upload_id = ?`, uploadID).Scan(&followerCount); err != nil {
		return SnapshotResult{}, fmt.Errorf("count members: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE uploads SET status = 'completed', processed_at = ?, follower_count = ?,
		       snapshot_taken_at = ?, snapshot_source = ?, error_message = ''
		WHERE id = ?`,
		time.Now().UTC().Unix(), followerCount, takenAt.UTC().Unix(), source, uploadID); err != nil {
		return SnapshotResult{}, fmt.Errorf("complete upload: %w", err)
	}

	if err := recomputeAccount(ctx, tx, accountID); err != nil {
		return SnapshotResult{}, err
	}

	// Read back what the rebuild decided about this execution.
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
// and rebuilds every diff between them.
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

		if i == 0 {
			// The oldest execution is the baseline: nothing precedes it.
			if _, err := tx.ExecContext(ctx,
				`UPDATE uploads SET added_count = 0, removed_count = 0 WHERE id = ?`, uploadID); err != nil {
				return fmt.Errorf("reset baseline counts: %w", err)
			}
			continue
		}

		prev := ordered[i-1]
		added, err := recordChanges(ctx, tx, accountID, uploadID, prev, ChangeFollowed, now)
		if err != nil {
			return err
		}
		removed, err := recordChanges(ctx, tx, accountID, uploadID, prev, ChangeUnfollowed, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE uploads SET added_count = ?, removed_count = ? WHERE id = ?`,
			added, removed, uploadID); err != nil {
			return fmt.Errorf("update execution counts: %w", err)
		}
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

// recordChanges writes the one-sided set difference between two snapshots.
// For ChangeFollowed that is (current \ previous); for ChangeUnfollowed it is
// (previous \ current).
func recordChanges(ctx context.Context, tx *sql.Tx, accountID, uploadID, prevUploadID int64, kind ChangeType, now int64) (int, error) {
	presentIn, absentFrom := uploadID, prevUploadID
	if kind == ChangeUnfollowed {
		presentIn, absentFrom = prevUploadID, uploadID
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO changes (account_id, upload_id, prev_upload_id, user_id, change_type, created_at)
		SELECT ?, ?, ?, sm.user_id, ?, ?
		FROM snapshot_members sm
		WHERE sm.upload_id = ?
		  AND NOT EXISTS (
		      SELECT 1 FROM snapshot_members other
		      WHERE other.upload_id = ? AND other.user_id = sm.user_id
		  )`,
		accountID, uploadID, prevUploadID, string(kind), now, presentIn, absentFrom)
	if err != nil {
		return 0, fmt.Errorf("record %s changes: %w", kind, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count %s changes: %w", kind, err)
	}
	return int(affected), nil
}
