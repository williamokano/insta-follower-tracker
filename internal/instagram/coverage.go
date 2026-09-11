package instagram

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Why there is no heuristic based on follow dates.
//
// A JSON export records when each person started following, and a filtered
// download's oldest entry necessarily falls inside its window. That looks like
// a usable signal, and it is not: an account two years old, or one that grew in
// a burst, has exactly the same shape as a filtered export of an old account.
// There is no threshold that separates them, and wrongly refusing a real export
// costs more than the case it would catch.
//
// What remains is the range an export declares about itself, which is exact,
// and the follower count check in the tracker, which needs no metadata and so
// works on every format. The gap they leave is a first upload, of a filtered
// download, in a format too old to declare its range: nothing can detect that
// from the file alone, so it is documented rather than guessed at.

// NarrowWindowDays is the widest declared date range still treated as a
// partial export.
//
// Instagram's limited downloads default to one year. An account that genuinely
// requests everything declares a range as old as the account itself, so a
// window at or under this bound is a deliberate filter rather than a short
// history. The slack above 365 covers leap years and the hours between
// requesting a download and it being generated.
const NarrowWindowDays = 400

// Coverage describes how much of the follower list an export actually holds.
//
// This matters more than it looks. A download restricted to a date range
// contains only the people who started following inside that window, but it is
// otherwise indistinguishable from a complete one. Diffed against a full
// snapshot it manufactures unfollows for everybody outside the window, so a
// partial export has to be recognised rather than quietly recorded.
type Coverage struct {
	// Partial is true when the export is known or strongly suspected to hold
	// only part of the follower list.
	Partial bool
	// Reason explains the finding in terms a person can act on.
	Reason string
	// From and To are the declared range, when the export states one.
	From, To time.Time
}

// declaredRangePattern matches the range banner newer exports carry, e.g.
// "Contains data that you requested from 11 September 2025 at 06:29 to
// 11 September 2026 at 06:29".
//
// Only the English phrasing is recognised. A miss costs nothing: the follower
// count check in the tracker catches a partial export regardless of language,
// and treating an unparsed banner as complete is better than guessing.
var declaredRangePattern = regexp.MustCompile(
	`(?i)contains data that you requested from\s+(\d{1,2}\s+\w+\s+\d{4})[^0-9]+(\d{1,2}:\d{2})\s+to\s+(\d{1,2}\s+\w+\s+\d{4})[^0-9]+(\d{1,2}:\d{2})`)

// coverageFromMetadata inspects an export's start_here page for a declared
// date range. Exports that predate the banner simply report nothing.
func coverageFromMetadata(startHere []byte) Coverage {
	text := textFromHTML(startHere)

	m := declaredRangePattern.FindStringSubmatch(text)
	if m == nil {
		return Coverage{}
	}

	from, okFrom := parseBannerTime(m[1], m[2])
	to, okTo := parseBannerTime(m[3], m[4])
	if !okFrom || !okTo || !to.After(from) {
		return Coverage{}
	}

	window := to.Sub(from)
	if window > NarrowWindowDays*24*time.Hour {
		// A wide range is what a complete download looks like.
		return Coverage{From: from, To: to}
	}

	return Coverage{
		Partial: true,
		From:    from,
		To:      to,
		Reason: fmt.Sprintf(
			"this download was restricted to %s through %s (%d days), so it only lists people who started following inside that window, not the full follower list",
			from.Format("2 January 2006"), to.Format("2 January 2006"), int(window.Hours()/24)),
	}
}

// bannerLayouts are the date shapes seen in the range banner.
var bannerLayouts = []string{"2 January 2006 15:04", "2 Jan 2006 15:04"}

func parseBannerTime(date, clock string) (time.Time, bool) {
	joined := strings.Join(strings.Fields(date), " ") + " " + clock
	for _, layout := range bannerLayouts {
		if t, err := time.Parse(layout, joined); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// textFromHTML reduces markup to its visible text, so prose can be matched
// without depending on the surrounding tags.
var (
	scriptStylePattern = regexp.MustCompile(`(?is)<(script|style)\b.*?</(script|style)>`)
	tagPattern         = regexp.MustCompile(`<[^>]*>`)
	whitespacePattern  = regexp.MustCompile(`\s+`)
)

func textFromHTML(body []byte) string {
	s := scriptStylePattern.ReplaceAllString(string(body), " ")
	s = tagPattern.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&#039;", "'")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return strings.TrimSpace(whitespacePattern.ReplaceAllString(s, " "))
}
