package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"meshmonday/internal/checkins"
	"meshmonday/internal/config"
	"meshmonday/internal/leaderboard"
	"meshmonday/internal/models"
	"meshmonday/internal/storage"
)

type Server struct {
	cfg       config.Config
	store     *storage.SQLiteStore
	logger    *slog.Logger
	templates *template.Template
}

const weekDateLayout = "2006-01-02"

func NewServer(cfg config.Config, store *storage.SQLiteStore, logger *slog.Logger) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"observerTooltip":     observerTooltip,
		"leaderboardRingFill": leaderboardRingFill,
	}).ParseGlob(filepath.Join("web", "templates", "*.html"))
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:       cfg,
		store:     store,
		logger:    logger,
		templates: tmpl,
	}, nil
}

func leaderboardRingFill(checkins, trackedWeeks int) int {
	if trackedWeeks <= 0 {
		return 0
	}
	p := (checkins * 100) / trackedWeeks
	if p > 100 {
		return 100
	}
	return p
}

func observerTooltip(count int, names []string) string {
	if count <= 0 {
		return "No observers recorded yet."
	}
	if len(names) == 0 {
		return "Observer data unavailable."
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	if len(sorted) == 1 {
		return "1 observer: " + sorted[0]
	}
	return fmt.Sprintf("%d observers: %s", len(sorted), strings.Join(sorted, " • "))
}

func configuredHashtagChannels(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		channel := strings.TrimSpace(raw)
		if channel == "" {
			continue
		}
		if !strings.HasPrefix(channel, "#") {
			channel = "#" + channel
		}
		channel = strings.ToLower(channel)
		if channel == "#meshmonday" {
			continue
		}
		if _, exists := seen[channel]; exists {
			continue
		}
		seen[channel] = struct{}{}
		out = append(out, channel)
	}
	return out
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/api/checkins", s.handleAPIWeekCheckins)
	mux.HandleFunc("/api/leaderboard", s.handleAPILeaderboard)
	mux.HandleFunc("/leaderboard", s.handleLeaderboardPage)
	mux.HandleFunc("/", s.handleMondayPage)
	return loggingMiddleware(securityHeadersMiddleware(mux), s.logger)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleMondayPage(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	weekStart, err := parseWeekStart(r, s.cfg.TZ)
	if err != nil {
		http.Error(w, "Invalid week parameter.", http.StatusBadRequest)
		return
	}
	items, err := s.store.ListCheckinsByWeek(r.Context(), s.cfg.IATAFilters, weekStart)
	if err != nil {
		s.logger.Error("list checkins by week failed", "error", err.Error(), "week_start", weekStart.Format(weekDateLayout))
		http.Error(w, "Internal server error.", http.StatusInternalServerError)
		return
	}
	data := struct {
		WeekStart      string
		MeshName       string
		ListenChannels []string
		DiceBearStyle  string
		UIPollSeconds  int
		IATA           string
		Count          int
		Checkins       []models.Checkin
	}{
		WeekStart:      weekStart.Format(weekDateLayout),
		MeshName:       s.cfg.MeshName,
		ListenChannels: configuredHashtagChannels(s.cfg.HashtagChannels),
		DiceBearStyle:  s.cfg.DiceBearStyle,
		UIPollSeconds:  s.cfg.UIPollSeconds,
		IATA:           s.cfg.IATAFilterLabel(),
		Count:          len(items),
		Checkins:       items,
	}
	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		s.logger.Error("render monday page failed", "error", err.Error())
		http.Error(w, "Internal server error.", http.StatusInternalServerError)
	}
}

func (s *Server) handleLeaderboardPage(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	entries, err := s.computeLeaderboard(r.Context())
	if err != nil {
		s.logger.Error("compute leaderboard failed", "error", err.Error())
		http.Error(w, "Internal server error.", http.StatusInternalServerError)
		return
	}
	data := struct {
		Entries       []models.LeaderboardEntry
		MeshName      string
		TrackedFrom   string
		DiceBearStyle string
		UIPollSeconds int
	}{
		Entries:       entries,
		MeshName:      s.cfg.MeshName,
		TrackedFrom:   s.cfg.TrackFromDate.Format(weekDateLayout),
		DiceBearStyle: s.cfg.DiceBearStyle,
		UIPollSeconds: s.cfg.UIPollSeconds,
	}
	if err := s.templates.ExecuteTemplate(w, "leaderboard.html", data); err != nil {
		s.logger.Error("render leaderboard page failed", "error", err.Error())
		http.Error(w, "Internal server error.", http.StatusInternalServerError)
	}
}

func (s *Server) handleAPIWeekCheckins(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	weekStart, err := parseWeekStart(r, s.cfg.TZ)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid_week"})
		return
	}
	items, err := s.store.ListCheckinsByWeek(r.Context(), s.cfg.IATAFilters, weekStart)
	if err != nil {
		s.logger.Error("api list checkins by week failed", "error", err.Error(), "week_start", weekStart.Format(weekDateLayout))
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"week_start": weekStart.Format(weekDateLayout),
		"iata":       s.cfg.IATAFilterLabel(),
		"count":      len(items),
		"checkins":   items,
	})
}

func (s *Server) handleAPILeaderboard(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	entries, err := s.computeLeaderboard(r.Context())
	if err != nil {
		s.logger.Error("api compute leaderboard failed", "error", err.Error())
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"tracked_from": s.cfg.TrackFromDate.Format(weekDateLayout),
		"entries":      entries,
	})
}

func (s *Server) computeLeaderboard(ctx context.Context) ([]models.LeaderboardEntry, error) {
	checkinRows, err := s.store.ListCheckinsSince(ctx, s.cfg.IATAFilters, s.cfg.TrackFromDate)
	if err != nil {
		return nil, err
	}
	entries := leaderboard.Compute(checkinRows, s.cfg.TrackFromDate, time.Now(), s.cfg.TZ)
	if err := s.store.ReplaceLeaderboardSnapshots(ctx, s.cfg.TrackFromDate, entries, time.Now().UTC()); err != nil {
		s.logger.Warn("replace leaderboard snapshots failed", "error", err.Error())
	}
	return entries, nil
}

func loggingMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http_request", "path", r.URL.Path, "method", r.Method, "duration_ms", time.Since(start).Milliseconds())
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func allowReadMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
	return false
}

func parseWeekStart(r *http.Request, tz string) (time.Time, error) {
	weekStart := checkins.WeekStartMonday(time.Now(), tz)
	raw := strings.TrimSpace(r.URL.Query().Get("week"))
	if raw == "" {
		return weekStart, nil
	}
	parsed, err := time.Parse(weekDateLayout, raw)
	if err != nil {
		return time.Time{}, errors.New("invalid week")
	}
	return parsed, nil
}

func jsonResponse(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
