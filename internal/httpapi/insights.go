package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/williamokano/insta-follower-tracker/internal/store"
)

// maxQuickUnfollowDays caps the window: past a year, "quick" stops meaning
// anything.
const maxQuickUnfollowDays = 365

// quickDaysParam reads the quick-unfollow window, defaulting when absent.
func quickDaysParam(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("days"))
	if raw == "" {
		return store.DefaultQuickUnfollowDays, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 1 || days > maxQuickUnfollowDays {
		return 0, fmt.Errorf("days must be a whole number from 1 to %d, got %q", maxQuickUnfollowDays, raw)
	}
	return days, nil
}

// handleAccountInsights returns every insight for an account.
func (s *Server) handleAccountInsights(w http.ResponseWriter, r *http.Request) {
	account, ok := s.resolveAccount(w, r)
	if !ok {
		return
	}

	days, err := quickDaysParam(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	insights, err := s.svc.Store().Insights(r.Context(), account.ID, days)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	if insights.Latest == nil {
		s.writeError(w, r, http.StatusNotFound,
			errors.New("this account has no processed executions yet"))
		return
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"account":  account,
		"insights": insights,
	})
}

// handleInsightsPage renders the insights for one account.
func (s *Server) handleInsightsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	handle := store.NormalizeHandle(r.PathValue("handle"))
	account, err := s.svc.Store().AccountByHandle(ctx, handle)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	data := pageData{Page: "insights", Title: "Insights", Account: &account}

	accounts, err := s.svc.Store().ListAccounts(ctx)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.Accounts = accounts

	days, err := quickDaysParam(r)
	if err != nil {
		data.Flash = err.Error()
		data.FlashKind = "error"
		days = store.DefaultQuickUnfollowDays
	}

	insights, err := s.svc.Store().Insights(ctx, account.ID, days)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.Insights = &insights

	s.render(w, r, "insights.html", data)
}
