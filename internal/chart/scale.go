package chart

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// timeRange is the span the horizontal axis covers. A history whose executions
// all fall on one day is widened so the plot does not collapse to a line.
func timeRange(executions []Execution) (time.Time, time.Time) {
	minT, maxT := executions[0].TakenAt, executions[0].TakenAt
	for _, e := range executions {
		if e.TakenAt.Before(minT) {
			minT = e.TakenAt
		}
		if e.TakenAt.After(maxT) {
			maxT = e.TakenAt
		}
	}
	if !maxT.After(minT) {
		maxT = minT.Add(24 * time.Hour)
	}
	return minT, maxT
}

func scaleTime(at, minT, maxT time.Time, lo, hi float64) float64 {
	span := maxT.Sub(minT)
	if span <= 0 {
		return lo
	}
	return lo + (float64(at.Sub(minT))/float64(span))*(hi-lo)
}

// niceCeiling rounds an axis maximum up to a round number, so the gradations
// read as 200/400/600 rather than 187/374/561.
func niceCeiling(v int) int {
	if v <= 0 {
		return 1
	}
	magnitude := math.Pow(10, math.Floor(math.Log10(float64(v))))
	for _, step := range []float64{1, 2, 2.5, 5, 10} {
		if candidate := step * magnitude; candidate >= float64(v) {
			return int(candidate)
		}
	}
	return int(10 * magnitude)
}

// valueTicks returns gradations from zero to top inclusive.
//
// The number of divisions is chosen so every gradation lands on a whole number:
// four divisions of 50 would label the axis 12, 25, 37, which reads as noise.
func valueTicks(top int) []int {
	divisions := 4
	for _, candidate := range []int{4, 5, 2, 3} {
		if top%candidate == 0 {
			divisions = candidate
			break
		}
	}

	out := make([]int, 0, divisions+1)
	for i := 0; i <= divisions; i++ {
		out = append(out, top*i/divisions)
	}
	return out
}

// boundaryExecutions picks the executions to label on the time axis: the ends
// always, and a middle one when there is room, which keeps labels from
// colliding however many executions there are.
func boundaryExecutions(executions []Execution) []Execution {
	switch n := len(executions); {
	case n == 0:
		return nil
	case n <= 2:
		return executions
	default:
		return []Execution{executions[0], executions[n/2], executions[n-1]}
	}
}

func formatCount(v int) string {
	if v >= 1000 && v%1000 == 0 {
		return fmt.Sprintf("%dk", v/1000)
	}
	return fmt.Sprintf("%d", v)
}

func hasSuffix(s, suffix string) bool    { return strings.HasSuffix(s, suffix) }
func trimSuffix(s, suffix string) string { return strings.TrimSuffix(s, suffix) }
