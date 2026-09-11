package tracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/instagram"
	"github.com/williamokano/insta-follower-tracker/internal/store"
)

// Notify wakes the worker. A missed nudge is harmless: the worker also polls,
// and it always takes its work from the database rather than from a queue held
// in memory.
func (s *Service) Notify() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Run drains the upload queue until ctx is cancelled.
//
// A single worker is deliberate. Processing is cheap relative to how often
// exports arrive, and serialising it means an execution's sequence number is
// always assigned in processing order without any cross-request locking.
func (s *Service) Run(ctx context.Context) error {
	// Uploads left mid-flight by a crash are returned to the queue.
	if requeued, err := s.store.RequeueProcessing(ctx); err != nil {
		return fmt.Errorf("requeue interrupted uploads: %w", err)
	} else if requeued > 0 {
		s.log.Info("requeued uploads interrupted by a restart", "count", requeued)
	}

	ticker := time.NewTicker(s.opts.PollInterval)
	defer ticker.Stop()

	for {
		s.drain(ctx)

		select {
		case <-ctx.Done():
			return nil
		case <-s.notify:
		case <-ticker.C:
		}
	}
}

func (s *Service) drain(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		processed, err := s.ProcessNext(ctx)
		if err != nil {
			s.log.Error("processing upload failed unexpectedly", "error", err)
			return
		}
		if !processed {
			return
		}
	}
}

// ProcessNext claims and processes a single queued upload. It reports whether
// there was anything to do, which lets tests drive the queue deterministically
// instead of racing the background loop.
func (s *Service) ProcessNext(ctx context.Context) (bool, error) {
	up, err := s.store.ClaimNextPending(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if err := s.process(ctx, up); err != nil {
		s.log.Warn("upload could not be processed",
			"upload_id", up.ID, "account", up.AccountHandle, "error", err)
		if failErr := s.store.FailUpload(ctx, up.ID, err.Error()); failErr != nil {
			return true, fmt.Errorf("record failure for upload %d: %w", up.ID, failErr)
		}
	}
	return true, nil
}

func (s *Service) process(ctx context.Context, up store.Upload) error {
	f, err := os.Open(up.StoredPath)
	if err != nil {
		return fmt.Errorf("open stored upload: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat stored upload: %w", err)
	}

	export, err := instagram.Parse(f, info.Size())
	if err != nil {
		return err
	}

	// A partial export must never be recorded silently: diffed against a
	// complete one it manufactures an unfollow for everybody it leaves out.
	previous, err := s.store.LatestCompletedUpload(ctx, up.AccountID)
	if err != nil {
		return err
	}
	if err := s.checkCoverage(export, previous, up.AllowPartial); err != nil {
		return err
	}

	members := make([]store.Member, 0, len(export.Followers))
	for _, fl := range export.Followers {
		members = append(members, store.Member{
			Username:   fl.Username,
			Href:       fl.Href,
			FollowedAt: fl.FollowedAt,
		})
	}

	result, err := s.store.ApplySnapshot(ctx, up.ID, members)
	if err != nil {
		return err
	}

	if result.IsBaseline {
		s.log.Info("baseline execution recorded",
			"upload_id", up.ID, "account", up.AccountHandle, "followers", result.FollowerCount)
	} else {
		s.log.Info("execution processed",
			"upload_id", up.ID, "account", up.AccountHandle, "sequence", result.SequenceNo,
			"followers", result.FollowerCount, "followed", result.AddedCount, "unfollowed", result.RemovedCount)
	}

	if !s.opts.RetainUploads {
		if err := os.Remove(up.StoredPath); err != nil && !os.IsNotExist(err) {
			s.log.Warn("could not remove processed upload", "path", up.StoredPath, "error", err)
		}
	}
	return nil
}
