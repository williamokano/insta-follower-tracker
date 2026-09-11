package instagram_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/williamokano/insta-follower-tracker/internal/instagram"
)

// datedExport renders a JSON follower list whose follow timestamps span the
// given number of days back from a fixed point.
func datedExport(t *testing.T, count int, spanDays int) []byte {
	t.Helper()

	type item struct {
		Href      string `json:"href"`
		Value     string `json:"value"`
		Timestamp int64  `json:"timestamp"`
	}
	type entry struct {
		StringListData []item `json:"string_list_data"`
	}

	const newest = int64(1_700_000_000)
	entries := make([]entry, 0, count)
	for i := range count {
		// Spread evenly across the span so the oldest sits exactly spanDays back.
		offset := int64(0)
		if count > 1 {
			offset = int64(spanDays) * 86400 * int64(i) / int64(count-1)
		}
		name := fmt.Sprintf("member%03d", i)
		entries = append(entries, entry{StringListData: []item{{
			Href:      "https://www.instagram.com/" + name,
			Value:     name,
			Timestamp: newest - offset,
		}}})
	}

	body, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

// TestDeclaredDateRangeMarksExportPartial is the signal that would have caught
// the real case: a download restricted to a year lists only the people who
// started following inside it.
func TestDeclaredDateRangeMarksExportPartial(t *testing.T) {
	archive := buildZip(t, map[string][]byte{
		"start_here.html": fixture(t, "start_here_windowed.html"),
		"connections/followers_and_following/followers_1.html": fixture(t, "followers_1.html"),
	})

	export, err := parseExport(t, archive)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !export.Coverage.Partial {
		t.Fatal("a download declaring a one-year window covers only part of the follower list")
	}
	if !strings.Contains(export.Coverage.Reason, "2023") || !strings.Contains(export.Coverage.Reason, "2024") {
		t.Fatalf("the reason should name the window, got: %s", export.Coverage.Reason)
	}
	// The followers themselves still parse; it is the coverage that is in doubt.
	assertNames(t, export.Followers, "alice", "bob", "carol_x.99")
}

func TestWideDateRangeIsNotPartial(t *testing.T) {
	archive := buildZip(t, map[string][]byte{
		"start_here.html": fixture(t, "start_here_full.html"),
		"connections/followers_and_following/followers_1.html": fixture(t, "followers_1.html"),
	})

	export, err := parseExport(t, archive)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if export.Coverage.Partial {
		t.Fatalf("a twelve-year range is a complete download, got: %s", export.Coverage.Reason)
	}
}

// TestExportWithoutBannerIsNotAssumedPartial matters because older downloads
// carry no metadata at all, and must not be rejected for lacking it.
func TestExportWithoutBannerIsNotAssumedPartial(t *testing.T) {
	archive := buildZip(t, map[string][]byte{
		"connections/followers_and_following/followers_1.html": fixture(t, "followers_1.html"),
	})

	export, err := parseExport(t, archive)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if export.Coverage.Partial {
		t.Fatalf("absence of a range banner is not evidence of a filter, got: %s", export.Coverage.Reason)
	}
}

// TestFollowDatesDoNotDecideCoverage records a deliberate choice. A filtered
// download's follow dates all sit inside its window, which looks like a signal
// until you notice a young or fast-growing account produces the same shape.
// Refusing those would cost more than the case it catches, so follow dates are
// not consulted and the follower count check carries that ground instead.
func TestFollowDatesDoNotDecideCoverage(t *testing.T) {
	// Sixty followers gained within a single week: plausible for a new account,
	// and indistinguishable from a week-long filtered export.
	export, err := parseExport(t, datedExport(t, 60, 7))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if export.Coverage.Partial {
		t.Fatalf("follow dates must not be treated as evidence of filtering, got: %s",
			export.Coverage.Reason)
	}
}

func TestHTMLExportHasNoCoverageSignalOfItsOwn(t *testing.T) {
	export, err := parseExport(t, fixture(t, "followers_1.html"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if export.Coverage.Partial {
		t.Fatalf("a bare html file declares nothing about coverage, got: %s", export.Coverage.Reason)
	}
}

func TestCoverageRangeIsReported(t *testing.T) {
	archive := buildZip(t, map[string][]byte{
		"start_here.html": fixture(t, "start_here_windowed.html"),
		"connections/followers_and_following/followers_1.html": fixture(t, "followers_1.html"),
	})

	export, err := parseExport(t, archive)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if export.Coverage.From.IsZero() || export.Coverage.To.IsZero() {
		t.Fatal("a partial finding should carry the window it observed")
	}
	if !export.Coverage.To.After(export.Coverage.From) {
		t.Fatal("window end should follow its start")
	}
	if days := export.Coverage.To.Sub(export.Coverage.From).Hours() / 24; days > instagram.NarrowWindowDays {
		t.Fatalf("a window of %.0f days should not have been flagged", days)
	}
}
