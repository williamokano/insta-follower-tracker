// Package tracker turns uploaded Instagram exports into follower snapshots and
// answers questions about how the follower list changed between them.
package tracker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/store"
)

// ErrUploadTooLarge is returned when an upload exceeds the configured cap.
var ErrUploadTooLarge = errors.New("upload is larger than the configured limit")

// Options configures a Service.
type Options struct {
	// UploadDir is where raw uploaded files are kept.
	UploadDir string
	// MaxUploadBytes caps a single upload. Zero means unlimited.
	MaxUploadBytes int64
	// RetainUploads keeps the raw file after successful processing, so an
	// execution can be reprocessed later.
	RetainUploads bool
	// Logger receives processing events. Defaults to slog.Default.
	Logger *slog.Logger
	// PollInterval bounds how long the worker sleeps between queue checks when
	// no upload arrives to wake it. Defaults to 5s.
	PollInterval time.Duration
}

// Service owns upload intake and the background processing queue.
type Service struct {
	store   *store.Store
	opts    Options
	log     *slog.Logger
	notify  chan struct{}
	nowFunc func() time.Time
}

// New builds a Service. It creates the upload directory if it does not exist.
func New(st *store.Store, opts Options) (*Service, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 5 * time.Second
	}
	if opts.UploadDir == "" {
		return nil, errors.New("tracker: upload directory is required")
	}
	if err := os.MkdirAll(opts.UploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("create upload dir: %w", err)
	}

	return &Service{
		store: st,
		opts:  opts,
		log:   opts.Logger,
		// Buffered so Accept never blocks on a busy worker; the worker always
		// drains the queue from the database, so a dropped nudge only delays
		// processing until the next poll.
		notify:  make(chan struct{}, 1),
		nowFunc: func() time.Time { return time.Now().UTC() },
	}, nil
}

// Store exposes the underlying store for read-only queries.
func (s *Service) Store() *store.Store { return s.store }

// Accept stores an uploaded export and queues it for background processing. It
// returns as soon as the file is on disk and the queue row exists: parsing
// happens later on the worker.
func (s *Service) Accept(ctx context.Context, handle, filename string, body io.Reader) (store.Upload, error) {
	account, err := s.store.EnsureAccount(ctx, handle)
	if err != nil {
		return store.Upload{}, err
	}

	dir := filepath.Join(s.opts.UploadDir, account.Handle)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return store.Upload{}, fmt.Errorf("create account upload dir: %w", err)
	}

	path := filepath.Join(dir, s.uploadFilename(filename))
	sum, size, err := s.writeUpload(path, body)
	if err != nil {
		_ = os.Remove(path)
		return store.Upload{}, err
	}

	id, err := s.store.CreateUpload(ctx, account.ID, sanitizeDisplayName(filename), path, sum, size)
	if err != nil {
		_ = os.Remove(path)
		return store.Upload{}, err
	}

	s.Notify()

	up, err := s.store.Upload(ctx, id)
	if err != nil {
		return store.Upload{}, err
	}
	s.log.Info("upload accepted",
		"upload_id", up.ID, "account", account.Handle, "filename", up.OriginalFilename, "bytes", size)
	return up, nil
}

// writeUpload streams body to path, hashing as it goes, and enforces the size
// cap without buffering the whole upload in memory.
func (s *Service) writeUpload(path string, body io.Reader) (string, int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", 0, fmt.Errorf("create upload file: %w", err)
	}
	defer f.Close()

	src := body
	if s.opts.MaxUploadBytes > 0 {
		src = io.LimitReader(body, s.opts.MaxUploadBytes+1)
	}

	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, hasher), src)
	if err != nil {
		return "", 0, fmt.Errorf("store upload: %w", err)
	}
	if s.opts.MaxUploadBytes > 0 && size > s.opts.MaxUploadBytes {
		return "", 0, ErrUploadTooLarge
	}
	if size == 0 {
		return "", 0, errors.New("uploaded file is empty")
	}
	if err := f.Sync(); err != nil {
		return "", 0, fmt.Errorf("flush upload: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// uploadFilename builds a collision-free name that keeps the original
// extension, so the stored file is still recognisable on disk.
func (s *Service) uploadFilename(original string) string {
	ext := strings.ToLower(filepath.Ext(sanitizeDisplayName(original)))
	if ext != ".zip" && ext != ".json" {
		ext = ".bin"
	}

	var nonce [8]byte
	// rand.Read from crypto/rand never fails on supported platforms.
	_, _ = rand.Read(nonce[:])
	return fmt.Sprintf("%s-%s%s", s.nowFunc().Format("20060102T150405Z"), hex.EncodeToString(nonce[:]), ext)
}

// sanitizeDisplayName strips any directory component a browser may have sent,
// so the recorded name can never be used as a path.
func sanitizeDisplayName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(filepath.Clean("/" + name))
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		return "upload"
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

// ResolveUpload turns an execution reference from a request into an execution.
// It accepts "first", "last", or a numeric upload id.
func (s *Service) ResolveUpload(ctx context.Context, accountID int64, ref string, fallbackLast bool) (store.Upload, error) {
	switch strings.ToLower(strings.TrimSpace(ref)) {
	case "":
		return s.store.BoundaryUpload(ctx, accountID, fallbackLast)
	case "first", "oldest":
		return s.store.BoundaryUpload(ctx, accountID, false)
	case "last", "latest", "newest":
		return s.store.BoundaryUpload(ctx, accountID, true)
	}

	id, err := strconv.ParseInt(strings.TrimSpace(ref), 10, 64)
	if err != nil {
		return store.Upload{}, fmt.Errorf("%q is not a valid execution reference", ref)
	}
	return s.store.CompletedUpload(ctx, accountID, id)
}
