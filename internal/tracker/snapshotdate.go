package tracker

import (
	"context"
	"os"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/instagram"
	"github.com/williamokano/insta-follower-tracker/internal/store"
)

// Sources a snapshot date can be established from, weakest last.
const (
	// SourceManual is a date typed by the person uploading.
	SourceManual = "entered manually"
	// SourceFilename is the date in the archive's file name.
	SourceFilename = "archive file name"
	// SourceUpload is the fallback when nothing else is available.
	SourceUpload = "upload time"
)

// resolveSnapshotDate decides when an export was generated.
//
// Order matters, and is ordered by how easily each source is wrong rather than
// by how precise it looks. A date typed by a person overrides everything, since
// it is the only one that can correct a mistake. The export's own statement
// comes next, then the timestamps inside the archive. The file name is last:
// it reads as authoritative but a browser or phone rewrites download names
// freely. Upload time is the floor, and is what the service used before
// snapshot dates existed.
func resolveSnapshotDate(export *instagram.Export, filename string, manual time.Time, uploadedAt time.Time) (time.Time, string) {
	if !manual.IsZero() {
		return manual.UTC(), SourceManual
	}
	if !export.TakenAt.IsZero() {
		return export.TakenAt.UTC(), export.TakenAtSource
	}
	if t, ok := instagram.FilenameDate(filename); ok {
		return t.UTC(), SourceFilename
	}
	return uploadedAt.UTC(), SourceUpload
}

// backfillSnapshotDates re-reads the export date of executions recorded before
// dates were tracked, using the raw files still on disk.
//
// Migrating those rows to their processing time keeps the order they already
// had, which is correct on its own but mixes badly with a genuine backfill: an
// export really generated in 2024 would sort after a legacy row stamped with
// the day the database was upgraded. Since the uploads are retained, the real
// dates can simply be read, and the ordering becomes right rather than merely
// unchanged.
//
// Files that are gone, because retention was turned off, keep the date they
// have. Nothing here can fail in a way that should stop the service starting.
func (s *Service) backfillSnapshotDates(ctx context.Context) {
	pending, err := s.store.UploadsWithUnknownSnapshotDate(ctx)
	if err != nil {
		s.log.Warn("could not look for executions with unknown export dates", "error", err)
		return
	}
	if len(pending) == 0 {
		return
	}

	corrected := 0
	for _, up := range pending {
		if ctx.Err() != nil {
			return
		}

		takenAt, source, ok := s.detectSnapshotDate(up)
		if !ok {
			continue
		}
		if err := s.store.SetSnapshotDate(ctx, up.ID, takenAt, source); err != nil {
			s.log.Warn("could not correct an export date",
				"upload_id", up.ID, "error", err)
			continue
		}
		corrected++
	}

	if corrected > 0 {
		s.log.Info("read export dates for executions recorded before they were tracked",
			"corrected", corrected, "examined", len(pending))
	}
}

// detectSnapshotDate re-opens a stored upload and reads its export date.
func (s *Service) detectSnapshotDate(up store.Upload) (time.Time, string, bool) {
	f, err := os.Open(up.StoredPath)
	if err != nil {
		// The file was not retained; its recorded date is the best there is.
		return time.Time{}, "", false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return time.Time{}, "", false
	}

	export, err := instagram.Parse(f, info.Size())
	if err != nil {
		return time.Time{}, "", false
	}

	if !export.TakenAt.IsZero() {
		return export.TakenAt.UTC(), export.TakenAtSource, true
	}
	if t, ok := instagram.FilenameDate(up.OriginalFilename); ok {
		return t.UTC(), SourceFilename, true
	}
	return time.Time{}, "", false
}
