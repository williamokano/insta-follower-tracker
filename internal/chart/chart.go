// Package chart lays out the dashboard's plots.
//
// Geometry is computed here and emitted as inline SVG by the templates, so the
// interface keeps its property of shipping no third-party JavaScript and the
// charts render with scripting switched off.
package chart

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Palette slots, validated against this application's light and dark chart
// surfaces with the data-visualisation validator.
//
// The categorical slots carry list identity in the totals plot. The diverging
// pair carries polarity in the change plot, and is blue against red rather than
// the green against red used in the rest of the interface: where colour is the
// encoding it has to survive colour blindness, and that green and red separate
// by only a few units under simulated deuteranopia.
var (
	categoricalLight = []string{"#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4"}
	categoricalDark  = []string{"#3987e5", "#d95926", "#199e70", "#c98500", "#d55181"}
)

// seriesClass names the CSS class carrying a slot's colour.
//
// A class rather than an inline style, because the template layer refuses to
// interpolate a custom property into a style attribute and silently drops it,
// which leaves a line with no stroke at all. Keeping colour in the stylesheet
// also lets one definition serve both themes.
func seriesClass(i int) string { return fmt.Sprintf("s%d", (i%len(categoricalLight))+1) }

// CategoricalLight and CategoricalDark expose the validated slots so the
// stylesheet can declare them.
func CategoricalLight() []string { return categoricalLight }

// CategoricalDark exposes the dark steps of the same hues.
func CategoricalDark() []string { return categoricalDark }

// Geometry of the plots, in user units. The SVG scales to its container.
const (
	width       = 720.0
	trendHeight = 220.0
	deltaHeight = 190.0
	padLeft     = 46.0
	padRight    = 86.0 // room for the direct labels at the line ends
	padTop      = 14.0
	padBottom   = 30.0
)

// Point is one plotted observation.
type Point struct {
	X, Y  float64
	Label string // tooltip text
}

// Series is one list's trajectory.
type Series struct {
	Label string
	// Class is the CSS class carrying this series' colour.
	Class string
	Path  string
	Dots  []Point
	// LabelX and LabelY position the direct label at the end of the line, so
	// identity never rests on colour alone.
	LabelX, LabelY float64
	Last           int
}

// Tick is one axis gradation.
type Tick struct {
	Pos   float64
	Label string
}

// Trend is the totals-over-time plot.
type Trend struct {
	Width, Height       float64
	PlotLeft, PlotRight float64
	PlotTop, PlotBottom float64
	Series              []Series
	XTicks, YTicks      []Tick
	Empty               bool
}

// Execution is one processed export, as the charts need it.
type Execution struct {
	SequenceNo int64
	TakenAt    time.Time
	// Totals holds each list's member count, keyed by list kind.
	Totals map[string]int
	// Added and Removed hold each list's change against the previous
	// execution, keyed by list kind.
	Added, Removed map[string]int
}

// ListSeries names a list to plot.
type ListSeries struct {
	Kind  string
	Label string
}

// BuildTrend lays out one line per list across the executions' real dates.
//
// The horizontal axis is time rather than execution number, because executions
// are not evenly spaced: a backfilled history can have years between
// neighbours, and drawing them equidistant would misstate how the account
// actually changed.
func BuildTrend(executions []Execution, lists []ListSeries) Trend {
	t := Trend{
		Width: width, Height: trendHeight,
		PlotLeft: padLeft, PlotRight: width - padRight,
		PlotTop: padTop, PlotBottom: trendHeight - padBottom,
	}
	if len(executions) < 2 || len(lists) == 0 {
		t.Empty = true
		return t
	}

	minT, maxT := timeRange(executions)
	maxV := 0
	for _, e := range executions {
		for _, l := range lists {
			if v, ok := e.Totals[l.Kind]; ok && v > maxV {
				maxV = v
			}
		}
	}
	if maxV == 0 {
		t.Empty = true
		return t
	}

	top := niceCeiling(maxV)
	xOf := func(at time.Time) float64 { return scaleTime(at, minT, maxT, t.PlotLeft, t.PlotRight) }
	yOf := func(v int) float64 {
		return t.PlotBottom - (float64(v)/float64(top))*(t.PlotBottom-t.PlotTop)
	}

	for i, l := range lists {
		s := Series{Label: l.Label, Class: seriesClass(i)}
		path := ""
		for _, e := range executions {
			v, ok := e.Totals[l.Kind]
			if !ok {
				// An execution that did not carry this list says nothing about
				// it, so the line breaks rather than implying a value.
				path += " M"
				continue
			}
			x, y := xOf(e.TakenAt), yOf(v)
			verb := "L"
			if path == "" || hasSuffix(path, " M") {
				verb = "M"
				path = trimSuffix(path, " M")
			}
			path += fmt.Sprintf("%s%.1f %.1f ", verb, x, y)
			s.Dots = append(s.Dots, Point{X: x, Y: y, Label: fmt.Sprintf(
				"%s · %s · %d", l.Label, e.TakenAt.Format("2 Jan 2006"), v)})
			s.LabelX, s.LabelY, s.Last = x+8, y+4, v
		}
		s.Path = trimSuffix(path, " M")
		if len(s.Dots) > 0 {
			t.Series = append(t.Series, s)
		}
	}

	for _, tick := range valueTicks(top) {
		t.YTicks = append(t.YTicks, Tick{Pos: yOf(tick), Label: formatCount(tick)})
	}
	for _, e := range boundaryExecutions(executions) {
		t.XTicks = append(t.XTicks, Tick{Pos: xOf(e.TakenAt), Label: e.TakenAt.Format("Jan 2006")})
	}
	separateLabels(t.Series)
	return t
}

// separateLabels nudges direct labels apart when two lines end at similar
// heights, so the labels stay readable and identity never falls back to colour.
func separateLabels(series []Series) {
	const minGap = 14.0

	order := make([]int, len(series))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return series[order[a]].LabelY < series[order[b]].LabelY
	})

	for i := 1; i < len(order); i++ {
		prev, cur := &series[order[i-1]], &series[order[i]]
		if gap := cur.LabelY - prev.LabelY; gap < minGap {
			cur.LabelY = prev.LabelY + minGap
		}
	}
}

// Bar is one execution's change, drawn across the span it covers.
type Bar struct {
	X, Width     float64
	GainY, GainH float64
	LossY, LossH float64
	Gained, Lost int
	Label        string
	MidX         float64
	NarrowSpan   bool
}

// Delta is the change-per-execution plot.
type Delta struct {
	Width, Height       float64
	PlotLeft, PlotRight float64
	PlotTop, PlotBottom float64
	ZeroY               float64
	Bars                []Bar
	XTicks, YTicks      []Tick
	Empty               bool
}

// BuildDelta lays out gains above the baseline and losses below it.
//
// Each bar spans the interval between the two executions it compares, rather
// than standing at the later one. Nothing in an export records when somebody
// left a list; all that is known is that they were present at one execution and
// absent at the next. Drawing the bar across that gap states exactly that, and
// a wide bar is a visible admission that the timing is loose.
func BuildDelta(executions []Execution, kind string) Delta {
	d := Delta{
		Width: width, Height: deltaHeight,
		PlotLeft: padLeft, PlotRight: width - padRight,
		PlotTop: padTop, PlotBottom: deltaHeight - padBottom,
	}
	if len(executions) < 2 {
		d.Empty = true
		return d
	}

	minT, maxT := timeRange(executions)
	peak := 0
	for _, e := range executions[1:] {
		if v := e.Added[kind]; v > peak {
			peak = v
		}
		if v := e.Removed[kind]; v > peak {
			peak = v
		}
	}
	if peak == 0 {
		d.Empty = true
		return d
	}

	top := niceCeiling(peak)
	half := (d.PlotBottom - d.PlotTop) / 2
	d.ZeroY = d.PlotTop + half
	xOf := func(at time.Time) float64 { return scaleTime(at, minT, maxT, d.PlotLeft, d.PlotRight) }
	hOf := func(v int) float64 { return (float64(v) / float64(top)) * half }

	for i := 1; i < len(executions); i++ {
		prev, cur := executions[i-1], executions[i]
		gained, lost := cur.Added[kind], cur.Removed[kind]

		x0, x1 := xOf(prev.TakenAt), xOf(cur.TakenAt)
		// A 2px gap keeps neighbouring spans from touching, and a minimum
		// width keeps a same-day pair visible.
		barX, barW := x0+1, math.Max(x1-x0-2, 3)

		bar := Bar{
			X: barX, Width: barW, MidX: barX + barW/2,
			Gained: gained, Lost: lost,
			NarrowSpan: barW < 26,
			Label: fmt.Sprintf("Between %s and %s: %d joined, %d left",
				prev.TakenAt.Format("2 Jan 2006"), cur.TakenAt.Format("2 Jan 2006"), gained, lost),
		}
		if gained > 0 {
			bar.GainH = hOf(gained)
			bar.GainY = d.ZeroY - bar.GainH
		}
		if lost > 0 {
			bar.LossH = hOf(lost)
			bar.LossY = d.ZeroY
		}
		d.Bars = append(d.Bars, bar)
	}

	for _, tick := range valueTicks(top) {
		if tick == 0 {
			continue
		}
		d.YTicks = append(d.YTicks,
			Tick{Pos: d.ZeroY - hOf(tick), Label: "+" + formatCount(tick)},
			Tick{Pos: d.ZeroY + hOf(tick), Label: "−" + formatCount(tick)})
	}
	for _, e := range boundaryExecutions(executions) {
		d.XTicks = append(d.XTicks, Tick{Pos: xOf(e.TakenAt), Label: e.TakenAt.Format("Jan 2006")})
	}
	return d
}
