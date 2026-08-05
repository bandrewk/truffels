package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"truffels-api/internal/store"
)

// Simple rate limiter: per-IP sliding window
var loginLimiter = struct {
	sync.Mutex
	attempts map[string][]time.Time
}{attempts: make(map[string][]time.Time)}

func checkLoginRate(ip string) bool {
	loginLimiter.Lock()
	defer loginLimiter.Unlock()

	now := time.Now()
	window := now.Add(-1 * time.Minute)

	// Clean old entries
	recent := loginLimiter.attempts[ip][:0]
	for _, t := range loginLimiter.attempts[ip] {
		if t.After(window) {
			recent = append(recent, t)
		}
	}
	loginLimiter.attempts[ip] = recent

	if len(recent) >= 5 {
		return false
	}
	loginLimiter.attempts[ip] = append(loginLimiter.attempts[ip], now)
	return true
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	setup, _ := s.auth.IsSetup()
	authenticated := s.auth.ValidateSession(r)
	writeJSON(w, http.StatusOK, map[string]bool{
		"setup":         setup,
		"authenticated": authenticated,
	})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	ip := r.RemoteAddr
	if !checkLoginRate(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "Too many login attempts. Try again in a minute.",
		})
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password required"})
		return
	}

	ok, err := s.auth.CheckPassword(req.Password)
	if err != nil {
		slog.Error("auth check", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if !ok {
		_ = s.store.LogAudit("login_failed", "", "", ip)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid password"})
		return
	}

	cookie, err := s.auth.CreateSession()
	if err != nil {
		slog.Error("create session", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	http.SetCookie(w, cookie)
	_ = s.store.LogAudit("login", "", "", ip)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	setup, _ := s.auth.IsSetup()
	if setup {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already configured"})
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}

	if err := s.auth.SetPassword(req.Password); err != nil {
		slog.Error("set password", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	cookie, err := s.auth.CreateSession()
	if err != nil {
		slog.Error("create session after setup", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	http.SetCookie(w, cookie)
	_ = s.store.LogAudit("setup", "", "initial password set", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, s.auth.ClearCookie())
	_ = s.store.LogAudit("logout", "", "", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// maxAuditLimit caps ?limit= so a client cannot ask the API to materialise
// the entire audit_log in one response.
const maxAuditLimit = 1000

// handleAuditLog serves GET /api/audit.
//
// ?limit= (default 100, capped at maxAuditLimit) and ?action= are both
// optional; omitting them keeps the previous unfiltered 100-row behaviour for
// existing callers. The action filter exists because the client used to pull
// an unfiltered page and scan it for a single rare action (auto_reclaim,
// which fires roughly weekly by design) — once 100 logins, service actions
// and update rows had accumulated, the row it was looking for had silently
// fallen off the end.
func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a positive integer"})
			return
		}
		if n > maxAuditLimit {
			n = maxAuditLimit
		}
		limit = n
	}

	var (
		entries []store.AuditEntry
		err     error
	)
	if action := r.URL.Query().Get("action"); action != "" {
		entries, err = s.store.GetAuditLogByAction(action, limit)
	} else {
		entries, err = s.store.GetAuditLog(limit)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
