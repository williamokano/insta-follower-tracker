package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

const uploadColumns = `u.id, u.account_id, a.handle, u.sequence_no, u.original_filename,
	u.stored_path, u.sha256, u.size_bytes, u.status, u.error_message, u.uploaded_at,
	u.started_at, u.processed_at, u.follower_count, u.added_count, u.removed_count,
	u.allow_partial, u.snapshot_taken_at, u.snapshot_source, u.snapshot_date_override`

func scanUpload(sc interface{ Scan(...any) error }) (Upload, error) {
	var (
		up          Upload
		uploadedAt  int64
		startedAt   *int64
		processedAt *int64
		snapshotAt  *int64
		overrideAt  *int64
	)
	err := sc.Scan(&up.ID, &up.AccountID, &up.AccountHandle, &up.SequenceNo, &up.OriginalFilename,
		&up.StoredPath, &up.SHA256, &up.SizeBytes, &up.Status, &up.ErrorMessage, &uploadedAt,
		&startedAt, &processedAt, &up.FollowerCount, &up.AddedCount, &up.RemovedCount,
		&up.AllowPartial, &snapshotAt, &up.SnapshotSource, &overrideAt)
	if err != nil {
		return Upload{}, err
	}
	up.UploadedAt = time.Unix(uploadedAt, 0).UTC()
	up.StartedAt = unixPtr(startedAt)
	up.ProcessedAt = unixPtr(processedAt)
	up.SnapshotTakenAt = unixPtr(snapshotAt)
	if overrideAt != nil {
		up.SnapshotDate = time.Unix(*overrideAt, 0).UTC()
	}
	up.IsBaseline = up.SequenceNo != nil && *up.SequenceNo == 1
	return up, nil
}

// NormalizeHandle trims decoration users habitually type around a handle.
func NormalizeHandle(handle string) string {
	h := strings.ToLower(strings.TrimSpace(handle))
	h = strings.TrimPrefix(h, "@")
	return strings.TrimSuffix(h, "/")
}

// EnsureAccount returns the account for handle, creating it when absent.
func (s *Store) EnsureAccount(ctx context.Context, handle string) (Account, error) {
	h := NormalizeHandle(handle)
	if h == "" {
		return Account{}, errors.New("account handle is required")
	}

	now := time.Now().UTC().Unix()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO accounts (handle, created_at) VALUES (?, ?) ON CONFLICT(handle) DO NOTHING`,
		h, now); err != nil {
		return Account{}, fmt.Errorf("insert account: %w", err)
	}
	return s.AccountByHandle(ctx, h)
}

// AccountByHandle looks up a single account.
func (s *Store) AccountByHandle(ctx context.Context, handle string) (Account, error) {
	var (
		acc       Account
		createdAt int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, handle, created_at FROM accounts WHERE handle = ?`, NormalizeHandle(handle)).
		Scan(&acc.ID, &acc.Handle, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("select account: %w", err)
	}
	acc.CreatedAt = time.Unix(createdAt, 0).UTC()
	return acc, nil
}

// ListAccounts returns every tracked account with headline counts.
func (s *Store) ListAccounts(ctx context.Context) ([]AccountSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.handle, a.created_at,
		       COUNT(u.id),
		       COALESCE(SUM(CASE WHEN u.status = 'completed' THEN 1 ELSE 0 END), 0),
		       COALESCE((
		           SELECT lu.follower_count FROM uploads lu
		           WHERE lu.account_id = a.id AND lu.status = 'completed'
		           ORDER BY lu.sequence_no DESC LIMIT 1
		       ), 0),
		       MAX(u.uploaded_at)
		FROM accounts a
		LEFT JOIN uploads u ON u.account_id = a.id
		GROUP BY a.id
		ORDER BY a.handle`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	var out []AccountSummary
	for rows.Next() {
		var (
			sum          AccountSummary
			createdAt    int64
			lastUploadAt *int64
		)
		if err := rows.Scan(&sum.ID, &sum.Handle, &createdAt, &sum.UploadCount,
			&sum.CompletedCount, &sum.FollowerCount, &lastUploadAt); err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		sum.CreatedAt = time.Unix(createdAt, 0).UTC()
		sum.LastUploadAt = unixPtr(lastUploadAt)
		out = append(out, sum)
	}
	return out, rows.Err()
}

// NewUpload describes a file that has been stored and is waiting to be
// processed.
type NewUpload struct {
	AccountID    int64
	Filename     string
	StoredPath   string
	SHA256       string
	SizeBytes    int64
	AllowPartial bool
	// SnapshotDate overrides the date read from the archive. Zero for none.
	SnapshotDate time.Time
}

// CreateUpload records a freshly received file as pending work.
func (s *Store) CreateUpload(ctx context.Context, in NewUpload) (int64, error) {
	var override *int64
	if !in.SnapshotDate.IsZero() {
		v := in.SnapshotDate.UTC().Unix()
		override = &v
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO uploads (account_id, original_filename, stored_path, sha256, size_bytes,
		                     status, uploaded_at, allow_partial, snapshot_date_override)
		VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?)`,
		in.AccountID, in.Filename, in.StoredPath, in.SHA256, in.SizeBytes,
		time.Now().UTC().Unix(), in.AllowPartial, override)
	if err != nil {
		return 0, fmt.Errorf("insert upload: %w", err)
	}
	return res.LastInsertId()
}

// Upload fetches a single execution by id.
func (s *Store) Upload(ctx context.Context, id int64) (Upload, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+uploadColumns+` FROM uploads u JOIN accounts a ON a.id = u.account_id WHERE u.id = ?`, id)
	up, err := scanUpload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, fmt.Errorf("select upload: %w", err)
	}
	return up, nil
}

// ListUploads returns an account's executions, newest first.
func (s *Store) ListUploads(ctx context.Context, accountID int64) ([]Upload, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+uploadColumns+` FROM uploads u JOIN accounts a ON a.id = u.account_id
		 WHERE u.account_id = ?
		 ORDER BY u.sequence_no IS NULL DESC, u.sequence_no DESC, u.uploaded_at DESC`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list uploads: %w", err)
	}
	defer rows.Close()

	out := []Upload{}
	for rows.Next() {
		up, err := scanUpload(rows)
		if err != nil {
			return nil, fmt.Errorf("scan upload: %w", err)
		}
		out = append(out, up)
	}
	return out, rows.Err()
}

// ClaimNextPending atomically moves the oldest pending upload into the
// processing state and returns it. It reports ErrNotFound when the queue is
// empty.
func (s *Store) ClaimNextPending(ctx context.Context) (Upload, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Upload{}, fmt.Errorf("begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM uploads WHERE status = 'pending' ORDER BY uploaded_at, id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, fmt.Errorf("select pending upload: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE uploads SET status = 'processing', started_at = ? WHERE id = ?`,
		time.Now().UTC().Unix(), id); err != nil {
		return Upload{}, fmt.Errorf("claim upload: %w", err)
	}

	row := tx.QueryRowContext(ctx,
		`SELECT `+uploadColumns+` FROM uploads u JOIN accounts a ON a.id = u.account_id WHERE u.id = ?`, id)
	up, err := scanUpload(row)
	if err != nil {
		return Upload{}, fmt.Errorf("select claimed upload: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Upload{}, fmt.Errorf("commit claim: %w", err)
	}
	return up, nil
}

// RequeueProcessing returns uploads abandoned by a crash to the pending queue.
func (s *Store) RequeueProcessing(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE uploads SET status = 'pending', started_at = NULL WHERE status = 'processing'`)
	if err != nil {
		return 0, fmt.Errorf("requeue processing: %w", err)
	}
	return res.RowsAffected()
}

// FailUpload marks an execution as failed with the given reason.
func (s *Store) FailUpload(ctx context.Context, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE uploads SET status = 'failed', error_message = ?, processed_at = ? WHERE id = ?`,
		reason, time.Now().UTC().Unix(), id)
	if err != nil {
		return fmt.Errorf("fail upload: %w", err)
	}
	return nil
}

// PendingCount reports how many uploads are queued or in flight.
func (s *Store) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM uploads WHERE status IN ('pending', 'processing')`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count pending: %w", err)
	}
	return n, nil
}

// LatestCompletedUpload returns an account's most recent processed execution,
// or nil when it has none yet.
func (s *Store) LatestCompletedUpload(ctx context.Context, accountID int64) (*Upload, error) {
	up, err := s.BoundaryUpload(ctx, accountID, true)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &up, nil
}

// PrecedingCompletedUpload returns the execution immediately before takenAt in
// snapshot-date order, or nil when this would be the oldest.
//
// excludeID keeps an execution from being compared against itself when it is
// reprocessed.
func (s *Store) PrecedingCompletedUpload(ctx context.Context, accountID int64, takenAt time.Time, excludeID int64) (*Upload, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+uploadColumns+` FROM uploads u JOIN accounts a ON a.id = u.account_id
		 WHERE u.account_id = ? AND u.status = 'completed' AND u.id <> ?
		   AND u.snapshot_taken_at IS NOT NULL AND u.snapshot_taken_at <= ?
		 ORDER BY u.snapshot_taken_at DESC, u.uploaded_at DESC, u.id DESC LIMIT 1`,
		accountID, excludeID, takenAt.UTC().Unix())

	up, err := scanUpload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select preceding upload: %w", err)
	}
	return &up, nil
}

// UploadsWithUnknownSnapshotDate lists completed executions whose date was
// inferred from processing order rather than read from the export, which is
// how executions recorded before snapshot dates existed were migrated.
func (s *Store) UploadsWithUnknownSnapshotDate(ctx context.Context) ([]Upload, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+uploadColumns+` FROM uploads u JOIN accounts a ON a.id = u.account_id
		 WHERE u.status = 'completed' AND u.snapshot_source = ?
		 ORDER BY u.id`, SourceProcessingOrder)
	if err != nil {
		return nil, fmt.Errorf("list executions with unknown dates: %w", err)
	}
	defer rows.Close()

	out := []Upload{}
	for rows.Next() {
		up, err := scanUpload(rows)
		if err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		out = append(out, up)
	}
	return out, rows.Err()
}

// SetSnapshotDate corrects an execution's export date and rebuilds the
// account's history around the new ordering.
func (s *Store) SetSnapshotDate(ctx context.Context, uploadID int64, takenAt time.Time, source string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin snapshot date update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var accountID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT account_id FROM uploads WHERE id = ?`, uploadID).Scan(&accountID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("load upload: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE uploads SET snapshot_taken_at = ?, snapshot_source = ? WHERE id = ?`,
		takenAt.UTC().Unix(), source, uploadID); err != nil {
		return fmt.Errorf("update snapshot date: %w", err)
	}

	if err := recomputeAccount(ctx, tx, accountID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit snapshot date update: %w", err)
	}
	return nil
}

// ListTotalsForUpload returns the per-list numbers an execution recorded.
func (s *Store) ListTotalsForUpload(ctx context.Context, uploadID int64) ([]ListTotals, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT list_kind, member_count, added_count, removed_count
		 FROM upload_lists WHERE upload_id = ? ORDER BY list_kind`, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list totals: %w", err)
	}
	defer rows.Close()

	out := []ListTotals{}
	for rows.Next() {
		var t ListTotals
		if err := rows.Scan(&t.Kind, &t.MemberCount, &t.AddedCount, &t.RemovedCount); err != nil {
			return nil, fmt.Errorf("scan list totals: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListKindsForAccount returns every list an account has ever recorded.
func (s *Store) ListKindsForAccount(ctx context.Context, accountID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT ul.list_kind
		FROM upload_lists ul
		JOIN uploads u ON u.id = ul.upload_id
		WHERE u.account_id = ? AND u.status = 'completed'
		ORDER BY ul.list_kind`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list kinds: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, fmt.Errorf("scan kind: %w", err)
		}
		out = append(out, kind)
	}
	return out, rows.Err()
}

// ExecutionTotals is one execution with every list's numbers attached, which is
// the shape the dashboard plots.
type ExecutionTotals struct {
	Upload Upload
	Lists  []ListTotals
}

// AccountHistory returns an account's completed executions oldest first, each
// with the totals of every list it recorded.
func (s *Store) AccountHistory(ctx context.Context, accountID int64) ([]ExecutionTotals, error) {
	uploads, err := s.ListUploads(ctx, accountID)
	if err != nil {
		return nil, err
	}

	out := make([]ExecutionTotals, 0, len(uploads))
	for i := len(uploads) - 1; i >= 0; i-- {
		up := uploads[i]
		if up.Status != StatusCompleted {
			continue
		}
		totals, err := s.ListTotalsForUpload(ctx, up.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ExecutionTotals{Upload: up, Lists: totals})
	}
	return out, nil
}

// MarkForReprocess queues every processed execution of an account to be read
// again from its retained file, and reports how many were queued.
func (s *Store) MarkForReprocess(ctx context.Context, accountID int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE uploads SET reprocess_at = ?
		WHERE account_id = ? AND status = 'completed' AND reprocess_at IS NULL`,
		time.Now().UTC().Unix(), accountID)
	if err != nil {
		return 0, fmt.Errorf("queue reprocessing: %w", err)
	}
	return res.RowsAffected()
}

// ClaimNextReprocess returns the next execution waiting to be read again.
//
// Unlike a new upload this does not change the execution's status: its recorded
// data stays in force and stays visible until the reread succeeds, so a file
// that has gone missing or stopped parsing costs nothing.
func (s *Store) ClaimNextReprocess(ctx context.Context) (Upload, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+uploadColumns+` FROM uploads u JOIN accounts a ON a.id = u.account_id
		 WHERE u.reprocess_at IS NOT NULL AND u.status = 'completed'
		 ORDER BY u.reprocess_at, u.id LIMIT 1`)

	up, err := scanUpload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, fmt.Errorf("select execution to reprocess: %w", err)
	}
	return up, nil
}

// FinishReprocess takes an execution off the reprocessing queue, whether the
// reread succeeded or not: a file that cannot be read now will not read any
// better on a retry, and retrying forever would spin.
func (s *Store) FinishReprocess(ctx context.Context, uploadID int64) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE uploads SET reprocess_at = NULL WHERE id = ?`, uploadID); err != nil {
		return fmt.Errorf("clear reprocessing flag: %w", err)
	}
	return nil
}

// ReprocessCount reports how many executions are waiting to be read again.
func (s *Store) ReprocessCount(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM uploads WHERE reprocess_at IS NOT NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count reprocessing: %w", err)
	}
	return n, nil
}
