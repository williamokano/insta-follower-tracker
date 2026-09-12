package chart_test

import (
	"strings"
	"testing"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/chart"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

func executions() []chart.Execution {
	return []chart.Execution{
		{SequenceNo: 1, TakenAt: day(2024, time.January, 1),
			Totals: map[string]int{"followers": 100, "following": 80}},
		{SequenceNo: 2, TakenAt: day(2024, time.February, 1),
			Totals:  map[string]int{"followers": 120, "following": 90},
			Added:   map[string]int{"followers": 25, "following": 10},
			Removed: map[string]int{"followers": 5}},
		{SequenceNo: 3, TakenAt: day(2026, time.September, 1),
			Totals:  map[string]int{"followers": 90, "following": 95},
			Added:   map[string]int{"followers": 2, "following": 5},
			Removed: map[string]int{"followers": 32}},
	}
}

var lists = []chart.ListSeries{
	{Kind: "followers", Label: "Followers"},
	{Kind: "following", Label: "Following"},
}

// TestTrendPlacesExecutionsByDate is the property that matters once exports can
// be backfilled: executions are not evenly spaced, and drawing them so would
// misstate the history.
func TestTrendPlacesExecutionsByDate(t *testing.T) {
	trend := chart.BuildTrend(executions(), lists)
	if trend.Empty {
		t.Fatal("three executions should plot")
	}

	followers := trend.Series[0]
	if len(followers.Dots) != 3 {
		t.Fatalf("dots = %d, want 3", len(followers.Dots))
	}

	// January to February is one month; February to September 2026 is over
	// nineteen. The gaps on the axis must reflect that.
	first := followers.Dots[1].X - followers.Dots[0].X
	second := followers.Dots[2].X - followers.Dots[1].X
	if second <= first*5 {
		t.Fatalf("gaps are %.1f then %.1f; the second span is ~19x longer and must be drawn wider",
			first, second)
	}
}

// TestDeltaBarsSpanTheIntervalTheyCover is the honesty requirement. Nothing
// records when somebody left a list, only that they were present at one
// execution and absent at the next, so the bar covers that gap rather than
// standing at a point.
func TestDeltaBarsSpanTheIntervalTheyCover(t *testing.T) {
	trend := chart.BuildTrend(executions(), lists)
	delta := chart.BuildDelta(executions(), "followers")
	if delta.Empty {
		t.Fatal("two changes should plot")
	}
	if len(delta.Bars) != 2 {
		t.Fatalf("bars = %d, want 2 for three executions", len(delta.Bars))
	}

	// Each bar starts at its earlier execution and ends at its later one.
	dots := trend.Series[0].Dots
	for i, bar := range delta.Bars {
		wantStart, wantEnd := dots[i].X, dots[i+1].X
		if bar.X < wantStart-2 || bar.X > wantStart+2 {
			t.Fatalf("bar %d starts at %.1f, want ~%.1f", i, bar.X, wantStart)
		}
		if end := bar.X + bar.Width; end < wantEnd-3 || end > wantEnd+3 {
			t.Fatalf("bar %d ends at %.1f, want ~%.1f", i, end, wantEnd)
		}
	}

	// The long gap is drawn wide, which is the visible admission that the
	// timing within it is unknown.
	if delta.Bars[1].Width <= delta.Bars[0].Width*5 {
		t.Fatalf("widths %.1f then %.1f; the nineteen-month span must be far wider",
			delta.Bars[0].Width, delta.Bars[1].Width)
	}
}

func TestDeltaSplitsGainsAboveAndLossesBelow(t *testing.T) {
	delta := chart.BuildDelta(executions(), "followers")

	for _, bar := range delta.Bars {
		if bar.Gained > 0 && bar.GainY+bar.GainH > delta.ZeroY+0.01 {
			t.Fatalf("gains must sit above the baseline, got y=%.1f h=%.1f zero=%.1f",
				bar.GainY, bar.GainH, delta.ZeroY)
		}
		if bar.Lost > 0 && bar.LossY < delta.ZeroY-0.01 {
			t.Fatalf("losses must sit below the baseline, got y=%.1f zero=%.1f",
				bar.LossY, delta.ZeroY)
		}
	}

	// Position carries polarity, so the reading survives without colour.
	last := delta.Bars[1]
	if last.LossH <= last.GainH {
		t.Fatalf("the last execution lost 32 and gained 2; the loss must be drawn larger")
	}
}

// TestBrokenLineWhereAListIsAbsent: an execution that did not carry a list says
// nothing about it, so the line must break rather than imply a value.
func TestBrokenLineWhereAListIsAbsent(t *testing.T) {
	execs := executions()
	delete(execs[1].Totals, "following")

	trend := chart.BuildTrend(execs, lists)
	following := trend.Series[1]

	if len(following.Dots) != 2 {
		t.Fatalf("dots = %d, want 2: the middle execution has no following list", len(following.Dots))
	}
	if strings.Count(following.Path, "M") < 2 {
		t.Fatalf("path %q should lift the pen across the gap", following.Path)
	}
}

func TestEveryLineIsDirectlyLabelled(t *testing.T) {
	trend := chart.BuildTrend(executions(), lists)
	for _, s := range trend.Series {
		if s.Label == "" {
			t.Fatal("a series without a label leaves identity resting on colour alone")
		}
		if s.LabelX <= 0 {
			t.Fatalf("%s has no label position", s.Label)
		}
		if s.Class == "" {
			t.Fatalf("%s has no colour class", s.Label)
		}
	}
}

func TestTooFewExecutionsToPlot(t *testing.T) {
	one := executions()[:1]
	if !chart.BuildTrend(one, lists).Empty {
		t.Fatal("a single execution is not a trend")
	}
	if !chart.BuildDelta(one, "followers").Empty {
		t.Fatal("a single execution has no change to show")
	}
}

func TestAxisMaximaAreRoundNumbers(t *testing.T) {
	trend := chart.BuildTrend(executions(), lists)
	for _, tick := range trend.YTicks {
		if strings.Contains(tick.Label, ".") {
			t.Fatalf("axis label %q should be a round number", tick.Label)
		}
	}
}

// TestDirectLabelsDoNotCollide: two lines ending at similar heights would
// otherwise print their labels on top of each other, which is worse than no
// label at all.
func TestDirectLabelsDoNotCollide(t *testing.T) {
	execs := []chart.Execution{
		{SequenceNo: 1, TakenAt: day(2024, time.January, 1),
			Totals: map[string]int{"followers": 100, "following": 100}},
		{SequenceNo: 2, TakenAt: day(2024, time.June, 1),
			Totals: map[string]int{"followers": 150, "following": 151}},
	}

	trend := chart.BuildTrend(execs, lists)
	if len(trend.Series) != 2 {
		t.Fatalf("series = %d, want 2", len(trend.Series))
	}

	gap := trend.Series[0].LabelY - trend.Series[1].LabelY
	if gap < 0 {
		gap = -gap
	}
	if gap < 12 {
		t.Fatalf("labels are %.1f apart; they would overlap", gap)
	}
}

func TestAxisTicksAreWholeNumbers(t *testing.T) {
	// A maximum of 50 over four divisions would give 12.5 and be truncated.
	execs := []chart.Execution{
		{SequenceNo: 1, TakenAt: day(2024, time.January, 1), Totals: map[string]int{"followers": 10}},
		{SequenceNo: 2, TakenAt: day(2024, time.June, 1), Totals: map[string]int{"followers": 45},
			Added: map[string]int{"followers": 35}},
	}

	delta := chart.BuildDelta(execs, "followers")
	for _, tick := range delta.YTicks {
		clean := strings.TrimLeft(tick.Label, "+−")
		for _, r := range clean {
			if r < '0' || r > '9' {
				t.Fatalf("axis label %q is not a whole number", tick.Label)
			}
		}
	}
}
