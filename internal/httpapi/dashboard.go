package httpapi

import (
	"net/http"

	"github.com/williamokano/insta-follower-tracker/internal/chart"
	"github.com/williamokano/insta-follower-tracker/internal/instagram"
	"github.com/williamokano/insta-follower-tracker/internal/store"
)

// StatTile is one headline number, which is a better form than a chart for a
// single current value.
type StatTile struct {
	Label    string
	Value    int
	Delta    int
	HasDelta bool
	Note     string
}

// handleTrends renders the charts for one account.
func (s *Server) handleTrends(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	handle := store.NormalizeHandle(r.PathValue("handle"))
	account, err := s.svc.Store().AccountByHandle(ctx, handle)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data := pageData{Page: "dashboard", Title: "Trends", Account: &account}

	accounts, err := s.svc.Store().ListAccounts(ctx)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.Accounts = accounts

	history, err := s.svc.Store().AccountHistory(ctx, account.ID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	executions, present := toChartExecutions(history)

	// Only lists this account actually recorded, in presentation order.
	var series []chart.ListSeries
	for _, info := range instagram.Lists() {
		if info.Tracked && present[string(info.Kind)] {
			series = append(series, chart.ListSeries{Kind: string(info.Kind), Label: info.Label})
			data.Lists = append(data.Lists, info)
		}
	}

	listKind, err := listKindParam(r)
	if err != nil || !present[listKind] {
		listKind = store.DefaultListKind
	}
	data.ListKind = listKind
	if info, ok := instagram.LookupList(listKind); ok {
		data.ListLabel = info.Label
	}

	data.Trend = chart.BuildTrend(executions, series)
	data.Delta = chart.BuildDelta(executions, listKind)
	data.Stats = buildStats(executions, series)
	data.History = executions
	data.ExecutionCount = len(executions)

	s.render(w, r, "dashboard.html", data)
}

// toChartExecutions reshapes stored history for plotting, and reports which
// lists appear anywhere in it.
func toChartExecutions(history []store.ExecutionTotals) ([]chart.Execution, map[string]bool) {
	present := map[string]bool{}
	out := make([]chart.Execution, 0, len(history))

	for _, h := range history {
		e := chart.Execution{
			Totals:  map[string]int{},
			Added:   map[string]int{},
			Removed: map[string]int{},
		}
		if h.Upload.SequenceNo != nil {
			e.SequenceNo = *h.Upload.SequenceNo
		}
		if h.Upload.SnapshotTakenAt != nil {
			e.TakenAt = *h.Upload.SnapshotTakenAt
		} else {
			e.TakenAt = h.Upload.UploadedAt
		}
		for _, t := range h.Lists {
			e.Totals[t.Kind] = t.MemberCount
			e.Added[t.Kind] = t.AddedCount
			e.Removed[t.Kind] = t.RemovedCount
			present[t.Kind] = true
		}
		out = append(out, e)
	}
	return out, present
}

// buildStats produces the headline row: where each list stands now, and how far
// it has moved across the whole history.
//
// The comparison is first against last rather than a sum of the per-execution
// changes, for the same reason the overall diff is: somebody who leaves and
// returns nets to nothing, and adding up the departures would count them as
// gone.
func buildStats(executions []chart.Execution, series []chart.ListSeries) []StatTile {
	if len(executions) == 0 {
		return nil
	}
	first, last := executions[0], executions[len(executions)-1]

	out := make([]StatTile, 0, len(series))
	for _, l := range series {
		current, ok := last.Totals[l.Kind]
		if !ok {
			continue
		}
		tile := StatTile{Label: l.Label, Value: current}
		if start, ok := first.Totals[l.Kind]; ok && len(executions) > 1 {
			tile.Delta = current - start
			tile.HasDelta = true
			tile.Note = "since " + first.TakenAt.Format("Jan 2006")
		}
		out = append(out, tile)
	}
	return out
}
