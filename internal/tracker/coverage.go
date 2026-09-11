package tracker

import (
	"fmt"

	"github.com/williamokano/insta-follower-tracker/internal/instagram"
	"github.com/williamokano/insta-follower-tracker/internal/store"
)

// PartialExportError reports that an upload holds only part of the follower
// list, so recording it would invent unfollows for everybody it omits.
type PartialExportError struct {
	Reason string
}

func (e *PartialExportError) Error() string {
	return e.Reason + ". Request the download again with the date range set to " +
		"\"All time\", or re-upload with the partial override if you are certain this is right"
}

// CollapseDropRatio is the share of an account's followers that may disappear
// between consecutive executions before the newer one is treated as partial.
//
// Losing half of a following overnight does not happen; an export truncated by
// a date filter looks exactly like that. Catching it here is what protects
// accounts whose export format states no date range at all.
const CollapseDropRatio = 0.5

// CollapseMinimumFollowers keeps the check away from very small accounts,
// where a large proportional swing is ordinary.
const CollapseMinimumFollowers = 25

// checkCoverage decides whether an export may be recorded.
//
// The two signals complement each other. What the export declares about itself
// is authoritative but only newer downloads carry it; the comparison against
// the previous execution needs no metadata at all and so covers every format,
// but needs an execution to compare against.
func (s *Service) checkCoverage(export *instagram.Export, previous *store.Upload, allowPartial bool) error {
	if allowPartial {
		return nil
	}

	if export.Coverage.Partial {
		return &PartialExportError{Reason: export.Coverage.Reason}
	}

	if previous == nil || previous.FollowerCount < CollapseMinimumFollowers {
		return nil
	}

	count := len(export.Followers)
	if float64(count) >= float64(previous.FollowerCount)*CollapseDropRatio {
		return nil
	}

	dropped := previous.FollowerCount - count
	return &PartialExportError{Reason: fmt.Sprintf(
		"this export lists %d followers where the previous execution had %d, a fall of %d (%.0f%%). "+
			"A drop that large is far more often a download restricted to a date range than a real loss",
		count, previous.FollowerCount, dropped,
		100*float64(dropped)/float64(previous.FollowerCount))}
}
