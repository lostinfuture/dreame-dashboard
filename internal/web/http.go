package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dreame-dashboard/internal/ai"
)

const csp = "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'"

// SSE is one server-sent event.
type SSE struct {
	Name string
	Data string
}

// ControlInput is one confirmed device write.
type ControlInput struct {
	Action   string
	SIID     int
	PIID     int
	Value    string
	Expected string
}

// SettingsForm is one /admin/settings post. Empty fields are left unchanged.
type SettingsForm struct {
	Poll     string
	TempUnit string
	ReadOnly string
	Reset    bool
}

// OpenAIForm is one /admin/openai post.
type OpenAIForm struct {
	APIKey string
	Model  string
	Delete bool
}

// AIRequest is one /api/ai/analyze post.
type AIRequest struct {
	Scope  string
	Metric string
	Span   string
	Force  bool
}

// Backend is the supervisor surface the HTTP server calls.
type Backend interface {
	Phase() string
	Page(name string, q url.Values) Snapshot
	Login(ctx context.Context, email, password, region string) error
	Logout(ctx context.Context) error
	Control(ctx context.Context, req ControlInput) error
	Poll(ctx context.Context) error
	CheckOTA(ctx context.Context) error
	StartOTAUpdate(ctx context.Context) error
	VerifyMap(ctx context.Context) error
	Settings(ctx context.Context, f SettingsForm) error
	SaveOpenAI(ctx context.Context, f OpenAIForm) error
	Analyze(ctx context.Context, req AIRequest) (ai.Report, error)
	CachedReport(scope, metric, span string) (ai.Report, bool)
	Subscribe() (<-chan SSE, func())
	Dismiss()
}

// Server is the dashboard HTTP server.
type Server struct {
	Token     string
	BasicUser string
	BasicPass string
	Backend   Backend
	mux       *http.ServeMux
}

// NewServer builds routes for the dashboard.
func NewServer(token string, b Backend) *Server {
	s := &Server{Token: token, Backend: b}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /page.css", s.asset("page.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /chart.js", s.asset("chart.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /app.js", s.asset("app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /{$}", s.page("overview"))
	mux.HandleFunc("GET /device", s.page("device"))
	mux.HandleFunc("GET /activity", s.page("activity"))
	mux.HandleFunc("GET /events", s.events)
	mux.HandleFunc("GET /api/history", s.history)
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("GET /api/ai/report", s.aiReport)
	mux.HandleFunc("POST /api/ai/analyze", s.aiAnalyze)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("POST /control", s.control)
	mux.HandleFunc("POST /poll", s.poll)
	mux.HandleFunc("POST /ota/check", s.ota)
	mux.HandleFunc("POST /ota/update", s.otaUpdate)
	mux.HandleFunc("POST /admin/verify-map", s.verify)
	mux.HandleFunc("POST /admin/settings", s.settings)
	mux.HandleFunc("POST /admin/openai", s.openai)
	mux.HandleFunc("POST /dismiss", s.dismiss)
	s.mux = mux
	return s
}

// Handler is the root handler.
func (s *Server) Handler() http.Handler { return s }

// Serve runs until ctx is cancelled. A closed listener returns nil.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		if c, ok := s.Backend.(interface{ CloseHub() }); ok {
			c.CloseHub()
		}
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	err := srv.Serve(ln)
	if err == nil || err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	if !hostAllowed(r.Host) {
		http.Error(w, "misdirected", http.StatusMisdirectedRequest)
		return
	}
	if s.BasicUser != "" || s.BasicPass != "" {
		user, pass, ok := r.BasicAuth()
		if !ok || !constEq(user, s.BasicUser) || !constEq(pass, s.BasicPass) {
			w.Header().Set("WWW-Authenticate", `Basic realm="dreame"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if !csrfOK(r, s.Token) {
			http.Error(w, "rejected", http.StatusForbidden)
			return
		}
	}
	if !s.allow(w, r) {
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) allow(w http.ResponseWriter, r *http.Request) bool {
	phase := s.Backend.Phase()
	path := r.URL.Path
	blocked := r.Method == http.MethodPost || path == "/events" || strings.HasPrefix(path, "/api/")
	if phase == "boot" && blocked {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "booting"})
		return false
	}
	if phase == "auth_required" || phase == "auth" {
		if r.Method == http.MethodPost && path == "/login" {
			return true
		}
		if blocked {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auth_required"})
			return false
		}
		if path == "/device" || path == "/activity" {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return false
		}
	}
	return true
}

func (s *Server) asset(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := assets.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		_, _ = w.Write(b)
	}
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		phase := s.Backend.Phase()
		var snap Snapshot
		switch phase {
		case "boot":
			snap = Snapshot{Phase: "boot", Token: s.Token}
		case "auth", "auth_required":
			snap = s.Backend.Page("gate", r.URL.Query())
			snap.Phase = "auth_required"
			snap.Token = s.Token
		default:
			snap = s.Backend.Page(name, r.URL.Query())
			snap.Phase = "ready"
			snap.Page = name
			snap.Token = s.Token
		}
		b, err := RenderHTML(snap)
		if err != nil {
			http.Error(w, "page", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream", http.StatusInternalServerError)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch, cancel := s.Backend.Subscribe()
	defer cancel()
	fmt.Fprintf(w, ": hello\n\n")
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", sanitizeEvent(ev.Name), sanitizeEvent(ev.Data)); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	snap := s.Backend.Page("overview", r.URL.Query())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(HistoryJSON(snap))
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	snap := s.Backend.Page("state", r.URL.Query())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(StateJSON(snap))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	phase := s.Backend.Phase()
	if phase == "auth" || phase == "auth_required" {
		_ = s.Backend.Login(r.Context(), r.FormValue("email"), r.FormValue("password"), r.FormValue("region"))
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("confirmed") != "1" {
		http.Error(w, "rejected", http.StatusForbidden)
		return
	}
	_ = s.Backend.Logout(r.Context())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("confirmed") != "1" {
		http.Error(w, "rejected", http.StatusForbidden)
		return
	}
	siid, _ := strconv.Atoi(r.FormValue("siid"))
	piid, _ := strconv.Atoi(r.FormValue("piid"))
	err := s.Backend.Control(r.Context(), ControlInput{
		Action: r.FormValue("action"), SIID: siid, PIID: piid,
		Value: r.FormValue("value"), Expected: r.FormValue("expected_old"),
	})
	jsonMode := strings.Contains(r.Header.Get("Accept"), "application/json")
	if err != nil {
		code := statusOf(err)
		if jsonMode {
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		http.Error(w, err.Error(), code)
		return
	}
	if jsonMode {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	target := "/device"
	if r.FormValue("page") == "overview" {
		target = overviewBack(r)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) poll(w http.ResponseWriter, r *http.Request) {
	_ = s.Backend.Poll(r.Context())
	target := "/"
	if r.FormValue("page") == "device" {
		target = "/device"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) ota(w http.ResponseWriter, r *http.Request) {
	_ = s.Backend.CheckOTA(r.Context())
	http.Redirect(w, r, "/device#ota", http.StatusSeeOther)
}

func (s *Server) otaUpdate(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("confirmed") != "1" {
		http.Error(w, "rejected", http.StatusForbidden)
		return
	}
	_ = s.Backend.StartOTAUpdate(r.Context())
	http.Redirect(w, r, "/device#ota", http.StatusSeeOther)
}

func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(3 * time.Minute))
	_ = s.Backend.VerifyMap(r.Context())
	http.Redirect(w, r, "/device", http.StatusSeeOther)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	_ = s.Backend.Settings(r.Context(), SettingsForm{
		Poll: r.FormValue("poll"), TempUnit: r.FormValue("temp_unit"),
		ReadOnly: r.FormValue("read_only"), Reset: r.FormValue("reset") == "1",
	})
	http.Redirect(w, r, "/device", http.StatusSeeOther)
}

func (s *Server) openai(w http.ResponseWriter, r *http.Request) {
	_ = s.Backend.SaveOpenAI(r.Context(), OpenAIForm{
		APIKey: r.FormValue("api_key"),
		Model:  r.FormValue("model"),
		Delete: r.FormValue("delete") == "1",
	})
	http.Redirect(w, r, "/device#ai", http.StatusSeeOther)
}

func (s *Server) aiReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rep, ok := s.Backend.CachedReport(q.Get("scope"), q.Get("metric"), q.Get("span"))
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"cached": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cached": true, "report": rep})
}

func (s *Server) aiAnalyze(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(90 * time.Second))
	rep, err := s.Backend.Analyze(r.Context(), AIRequest{
		Scope:  r.FormValue("scope"),
		Metric: r.FormValue("metric"),
		Span:   r.FormValue("span"),
		Force:  r.FormValue("force") == "1",
	})
	if err != nil {
		writeJSON(w, statusOf(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "report": rep})
}

func (s *Server) dismiss(w http.ResponseWriter, r *http.Request) {
	s.Backend.Dismiss()
	target := "/"
	switch r.FormValue("page") {
	case "device":
		target = "/device"
	case "activity":
		target = "/activity"
	case "overview":
		target = overviewBack(r)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func overviewBack(r *http.Request) string {
	rawMetric := r.FormValue("metric")
	rawSpan := r.FormValue("span")
	if rawMetric == "" && rawSpan == "" {
		return "/"
	}
	span := allowQuery(rawSpan, "6h", "24h", "7d", "30d")
	if rawMetric == "" {
		return "/?span=" + url.QueryEscape(span)
	}
	metric := allowQuery(rawMetric, "pm25", "pm10", "pm1", "aq", "t", "rh", "hcho", "tvoc", "particles", "climate", "gas")
	return "/?metric=" + url.QueryEscape(metric) + "&span=" + url.QueryEscape(span)
}

func allowQuery(v string, ok ...string) string {
	for _, item := range ok {
		if v == item {
			return v
		}
	}
	if len(ok) > 0 {
		return ok[0]
	}
	return ""
}

func sanitizeEvent(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func statusOf(err error) int {
	var c interface{ StatusCode() int }
	if errors.As(err, &c) {
		if n := c.StatusCode(); n != 0 {
			return n
		}
	}
	return http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func hostAllowed(host string) bool {
	h := host
	if strings.HasPrefix(h, "[") {
		if i := strings.LastIndex(h, "]"); i >= 0 {
			h = h[1:i]
		}
	} else if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[:i], ":") {
		h = h[:i]
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	return net.ParseIP(h) != nil
}

func csrfOK(r *http.Request, token string) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		return originMatches(origin, r.Host)
	}
	if token != "" && constEq(r.FormValue("token"), token) {
		return true
	}
	ref := r.Header.Get("Referer")
	if ref == "" {
		return false
	}
	return originMatches(ref, r.Host)
}

func originMatches(raw, host string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

func constEq(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}
