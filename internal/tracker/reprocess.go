package tracker

import (
	"context"
	"errors"
	"fmt"

	"github.com/williamokano/insta-follower-tracker/internal/store"
)

// Reprocess queues an account's executions to be read again from the files kept
// on disk, and reports how many were queued.
//
// This exists because the service learns to read more than it once could. An
// export uploaded before other relationship lists were understood holds only
// its follower list, and one uploaded before export dates were tracked carries
// only the order it happened to be processed in. Both are sitting in the
// retained file, so neither needs asking for again.
func (s *Service) Reprocess(ctx context.Context, handle string) (int64, error) {
	account, err := s.store.AccountByHandle(ctx, handle)
	if err != nil {
		return 0, err
	}

	queued, err := s.store.MarkForReprocess(ctx, account.ID)
	if err != nil {
		return 0, err
	}
	if queued > 0 {
		s.log.Info("queued executions to be read again",
			"account", account.Handle, "count", queued)
		s.Notify()
	}
	return queued, nil
}

// reprocessNext rereads one queued execution, reporting whether there was one.
//
// Nothing is cleared up front. The execution keeps its recorded data and stays
// in the history until a fresh read succeeds, at which point the replacement is
// written in a single transaction. A file that has gone missing, or that no
// longer parses, therefore costs nothing: the execution is simply left as it
// was.
func (s *Service) reprocessNext(ctx context.Context) (bool, error) {
	up, err := s.store.ClaimNextReprocess(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if err := s.reread(ctx, up); err != nil {
		s.log.Warn("could not read an execution again; it keeps the data it had",
			"upload_id", up.ID, "account", up.AccountHandle, "error", err)
	}

	if err := s.store.FinishReprocess(ctx, up.ID); err != nil {
		return true, err
	}
	return true, nil
}

func (s *Service) reread(ctx context.Context, up store.Upload) error {
	export, err := s.parseStored(up)
	if err != nil {
		return err
	}

	takenAt, source := resolveSnapshotDate(export, up.OriginalFilename, up.SnapshotDate, up.UploadedAt)

	// The coverage guard is deliberately not applied here. It decides whether
	// an upload may be admitted, and this upload was admitted long ago; a
	// reread is about reading the same file better, not revisiting that.
	lists := listSnapshots(export)

	result, err := s.store.ApplySnapshot(ctx, up.ID, lists, takenAt, source)
	if err != nil {
		return err
	}

	s.log.Info("execution read again",
		"upload_id", up.ID, "account", up.AccountHandle, "sequence", result.SequenceNo,
		"lists", len(lists), "followers", result.FollowerCount,
		"snapshot_taken_at", takenAt, "date_source", source)
	return nil
}

// ReprocessPending reports how many executions are waiting to be read again, so
// the interface can show that work is still in flight.
func (s *Service) ReprocessPending(ctx context.Context) (int, error) {
	n, err := s.store.ReprocessCount(ctx)
	if err != nil {
		return 0, fmt.Errorf("count reprocessing: %w", err)
	}
	return n, nil
}
