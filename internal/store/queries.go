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
	u.started_at, u.processed_at, u.follower_count, u.added_count, u.removed_count`

func scanUpload(sc interface{ Scan(...any) error }) (Upload, error) {
	var (
		up          Upload
		uploadedAt  int64
		startedAt   *int64
		processedAt *int64
	)
	err := sc.Scan(&up.ID, &up.AccountID, &up.AccountHandle, &up.SequenceNo, &up.OriginalFilename,
		&up.StoredPath, &up.SHA256, &up.SizeBytes, &up.Status, &up.ErrorMessage, &uploadedAt,
		&startedAt, &processedAt, &up.FollowerCount, &up.AddedCount, &up.RemovedCount)
	if err != nil {
		return Upload{}, err
	}
	up.UploadedAt = time.Unix(uploadedAt, 0).UTC()
	up.StartedAt = unixPtr(startedAt)
	up.ProcessedAt = unixPtr(processedAt)
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

// CreateUpload records a freshly received file as pending work.
func (s *Store) CreateUpload(ctx context.Context, accountID int64, filename, storedPath, sha string, size int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO uploads (account_id, original_filename, stored_path, sha256, size_bytes, status, uploaded_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
		accountID, filename, storedPath, sha, size, time.Now().UTC().Unix())
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
