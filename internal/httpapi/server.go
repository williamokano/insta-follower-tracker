// Package httpapi exposes the tracker over HTTP: a small JSON API and the web
// interface built on top of it.
package httpapi

import (
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/store"
	"github.com/williamokano/insta-follower-tracker/internal/tracker"
)

// Options configures a Server.
type Options struct {
	// MaxUploadBytes mirrors the tracker's cap so oversized requests are
	// rejected before the body is read.
	MaxUploadBytes int64
	// Version is reported by the health endpoint and shown in the UI footer.
	Version string
	// Logger receives request-level errors.
	Logger *slog.Logger
}

// Server routes HTTP requests to the tracker.
type Server struct {
	svc       *tracker.Service
	opts      Options
	log       *slog.Logger
	mux       *http.ServeMux
	templates map[string]*template.Template
}

// New builds a Server with all routes registered.
func New(svc *tracker.Service, opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.MaxUploadBytes <= 0 {
		opts.MaxUploadBytes = 100 << 20
	}

	s := &Server{svc: svc, opts: opts, log: opts.Logger, mux: http.NewServeMux()}
	if err := s.routes(); err != nil {
		return nil, err
	}
	return s, nil
}

// Handler returns the root handler, wrapped in request logging.
func (s *Server) Handler() http.Handler { return s.withLogging(s.mux) }

func (s *Server) routes() error {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)

	s.mux.HandleFunc("POST /api/uploads", s.handleCreateUpload)
	s.mux.HandleFunc("GET /api/uploads/{id}", s.handleGetUpload)
	s.mux.HandleFunc("GET /api/uploads/{id}/changes", s.handleUploadChanges)
	s.mux.HandleFunc("GET /api/uploads/{id}/followers", s.handleUploadFollowers)
	s.mux.HandleFunc("GET /api/uploads/{id}/relationships", s.handleUploadRelationships)
	s.mux.HandleFunc("GET /api/lists", s.handleLists)

	s.mux.HandleFunc("GET /api/accounts", s.handleListAccounts)
	s.mux.HandleFunc("GET /api/accounts/{handle}/uploads", s.handleAccountUploads)
	s.mux.HandleFunc("GET /api/accounts/{handle}/followers", s.handleAccountFollowers)
	s.mux.HandleFunc("GET /api/accounts/{handle}/diff", s.handleAccountDiff)
	s.mux.HandleFunc("GET /api/accounts/{handle}/unfollowers", s.handleAccountUnfollowers)
	s.mux.HandleFunc("POST /api/accounts/{handle}/reprocess", s.handleReprocess)

	return s.uiRoutes()
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		if rec.status >= http.StatusInternalServerError {
			s.log.Error("request failed",
				"method", r.Method, "path", r.URL.Path, "status", rec.status,
				"duration", time.Since(started))
			return
		}
		s.log.Debug("request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration", time.Since(started))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	pending, err := s.svc.Store().PendingCount(r.Context())
	if err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, err)
		return
	}
	rereading, err := s.svc.ReprocessPending(r.Context())
	if err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"status":    "ok",
		"version":   s.opts.Version,
		"pending":   pending,
		"rereading": rereading,
	})
}

// apiError is the JSON body returned for every failed request.
type apiError struct {
	Error string `json:"error"`
}

func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	if r.Method == http.MethodHead {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.log.Error("writing response failed", "path", r.URL.Path, "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, err error) {
	if status >= http.StatusInternalServerError {
		s.log.Error("request error", "path", r.URL.Path, "error", err)
	}
	s.writeJSON(w, r, status, apiError{Error: err.Error()})
}

// resolveAccount looks up the account named in the path.
func (s *Server) resolveAccount(w http.ResponseWriter, r *http.Request) (store.Account, bool) {
	handle := r.PathValue("handle")
	account, err := s.svc.Store().AccountByHandle(r.Context(), handle)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, errors.New("no account named "+store.NormalizeHandle(handle)))
		return store.Account{}, false
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, err)
		return store.Account{}, false
	}
	return account, true
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid execution id")
	}
	return id, nil
}
