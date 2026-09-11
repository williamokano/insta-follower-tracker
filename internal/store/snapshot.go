package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ApplySnapshot records the full membership of an execution and, unless it is
// the account's first, materialises the follow/unfollow deltas against the
// previous execution. Everything happens in one transaction so an execution is
// either fully recorded or not recorded at all.
func (s *Store) ApplySnapshot(ctx context.Context, uploadID int64, members []Member) (SnapshotResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		accountID   int64
		existingSeq sql.NullInt64
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT account_id, sequence_no FROM uploads WHERE id = ?`, uploadID).Scan(&accountID, &existingSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SnapshotResult{}, ErrNotFound
		}
		return SnapshotResult{}, fmt.Errorf("load upload: %w", err)
	}

	// sequence_no is assigned here, at processing time, so ordering always
	// reflects the order executions were actually processed. Reprocessing an
	// execution keeps the slot it already holds rather than taking a new one,
	// which would otherwise leave it diffed against itself.
	sequenceNo := existingSeq.Int64
	if !existingSeq.Valid {
		var maxSeq sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(sequence_no) FROM uploads WHERE account_id = ?`, accountID).Scan(&maxSeq); err != nil {
			return SnapshotResult{}, fmt.Errorf("load max sequence: %w", err)
		}
		sequenceNo = maxSeq.Int64 + 1
	}

	var prevUploadID *int64
	if sequenceNo > 1 {
		var prev int64
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM uploads WHERE account_id = ? AND sequence_no = ?`, accountID, sequenceNo-1).Scan(&prev)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return SnapshotResult{}, fmt.Errorf("load previous upload: %w", err)
		}
		if err == nil {
			prevUploadID = &prev
		}
	}

	// Clear any partial state from an earlier failed attempt at this upload.
	if _, err := tx.ExecContext(ctx, `DELETE FROM snapshot_members WHERE upload_id = ?`, uploadID); err != nil {
		return SnapshotResult{}, fmt.Errorf("clear snapshot members: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM changes WHERE upload_id = ?`, uploadID); err != nil {
		return SnapshotResult{}, fmt.Errorf("clear changes: %w", err)
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

	var added, removed int
	now := time.Now().UTC().Unix()
	if prevUploadID != nil {
		if added, err = recordChanges(ctx, tx, accountID, uploadID, *prevUploadID, ChangeFollowed, now); err != nil {
			return SnapshotResult{}, err
		}
		if removed, err = recordChanges(ctx, tx, accountID, uploadID, *prevUploadID, ChangeUnfollowed, now); err != nil {
			return SnapshotResult{}, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE uploads SET status = 'completed', sequence_no = ?, processed_at = ?,
		       follower_count = ?, added_count = ?, removed_count = ?, error_message = ''
		WHERE id = ?`,
		sequenceNo, now, followerCount, added, removed, uploadID); err != nil {
		return SnapshotResult{}, fmt.Errorf("complete upload: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return SnapshotResult{}, fmt.Errorf("commit snapshot: %w", err)
	}

	return SnapshotResult{
		UploadID:      uploadID,
		SequenceNo:    sequenceNo,
		PrevUploadID:  prevUploadID,
		FollowerCount: followerCount,
		AddedCount:    added,
		RemovedCount:  removed,
		IsBaseline:    prevUploadID == nil,
	}, nil
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
