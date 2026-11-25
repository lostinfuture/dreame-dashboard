package dreame

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"dreame-dashboard/internal/filex"
	"dreame-dashboard/internal/logx"
)

// ErrAuthRequired means the operator must use the login gate.
var ErrAuthRequired = errors.New("auth required")

// Tokens are the persisted OAuth pair. The password is never stored here.
type Tokens struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TenantID         string `json:"tenant_id"`
	UID              string `json:"uid"`
	Region           string `json:"region"`
	ExpiresAt        int64  `json:"expires_at"`
	RefreshExpiresAt int64  `json:"refresh_expires_at"`
}

// Manager serializes login and refresh and persists tokens atomically.
type Manager struct {
	mu        sync.Mutex
	Region    string
	Country   string
	Tenant    string
	Email     string
	Password  string
	SendRLC   bool
	Path      string
	HTTP      *http.Client
	Now       func() time.Time
	OnRefresh func()
	tokens    *Tokens
	state     string
	lastErr   string
	backoff   time.Duration
	nextTry   time.Time
	hook      bool
	loaded    bool
}

var defaultHTTP = &http.Client{Timeout: 20 * time.Second}

// NewManager loads tokens.json when it exists.
func NewManager(path string) *Manager {
	m := &Manager{Path: path, state: "boot", Now: time.Now, Tenant: "000000", Region: "us", Country: "US"}
	if path == "" {
		return m
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	var t Tokens
	if json.Unmarshal(b, &t) != nil || t.AccessToken == "" && t.RefreshToken == "" {
		logx.Warn("auth", "tokens.json ignored", "err", "invalid")
		return m
	}
	m.tokens = &t
	if t.TenantID != "" {
		m.Tenant = t.TenantID
	}
	if t.Region != "" {
		m.Region = t.Region
		if c := regionCountry(t.Region); c != "" {
			m.Country = c
		}
	}
	m.loaded = true
	return m
}

// SetDefaultLocale applies configured defaults when tokens.json did not record a region.
func (m *Manager) SetDefaultLocale(region, country string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokens != nil && m.tokens.Region != "" {
		if m.Region == "" {
			m.Region = m.tokens.Region
		}
		if c := regionCountry(m.Region); c != "" {
			m.Country = c
		} else if country != "" {
			m.Country = country
		}
		return
	}
	if region != "" {
		m.Region = region
	}
	if country != "" {
		m.Country = country
	}
}

// SetDefaultTenant applies a configured tenant when tokens.json did not record one.
func (m *Manager) SetDefaultTenant(tenant string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokens != nil && m.tokens.TenantID != "" {
		return
	}
	if tenant != "" {
		m.Tenant = tenant
	}
}

// Locale returns the active region and country.
func (m *Manager) Locale() (region, country string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Region, m.Country
}

// State is the auth state machine name.
func (m *Manager) State() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == "" || m.state == "boot" {
		if m.tokens == nil && m.Password == "" {
			return "auth_required"
		}
	}
	return m.state
}

// LastError is a safe message for the login gate.
func (m *Manager) LastError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

// Current returns the in-memory pair.
func (m *Manager) Current() Tokens {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokens == nil {
		return Tokens{}
	}
	return *m.tokens
}

// Authorize returns a usable access token, refreshing or logging in when needed.
func (m *Manager) Authorize(ctx context.Context) (string, string, error) {
	m.mu.Lock()
	err := m.ensureLocked(ctx)
	hook := m.hook
	fn := m.OnRefresh
	m.hook = false
	access, tenant := "", m.tenantLocked()
	if m.tokens != nil {
		access = m.tokens.AccessToken
	}
	m.mu.Unlock()
	if hook && fn != nil {
		fn()
	}
	if err != nil {
		return "", "", err
	}
	return access, tenant, nil
}

// ForceRefresh refreshes once after an HTTP 401.
func (m *Manager) ForceRefresh(ctx context.Context) error {
	m.mu.Lock()
	err := m.refreshOrLogin(ctx, true)
	hook := m.hook
	fn := m.OnRefresh
	m.hook = false
	m.mu.Unlock()
	if hook && fn != nil {
		fn()
	}
	return err
}

// RefreshOnly rotates the refresh token and does not fall back to the password.
func (m *Manager) RefreshOnly(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokens == nil || m.tokens.RefreshToken == "" {
		return errors.New("no refresh token")
	}
	return m.refreshLocked(ctx)
}

// SetOnRefresh installs the reconnect hook.
func (m *Manager) SetOnRefresh(fn func()) {
	m.mu.Lock()
	m.OnRefresh = fn
	m.mu.Unlock()
}

// Login submits the interactive gate. The password is not retained.
func (m *Manager) Login(ctx context.Context, email, password, region, country string) error {
	m.mu.Lock()
	prevR, prevC := m.Region, m.Country
	if region != "" {
		m.Region = region
	}
	if country != "" {
		m.Country = country
	}
	err := m.passwordGrant(ctx, email, password, true)
	if err != nil {
		m.Region, m.Country = prevR, prevC
	}
	hook := m.hook
	fn := m.OnRefresh
	m.hook = false
	m.mu.Unlock()
	if err == nil && hook && fn != nil {
		fn()
	}
	return err
}

// Logout deletes the saved session.
func (m *Manager) Logout() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens = nil
	m.Password = ""
	m.state = "auth_required"
	m.lastErr = ""
	m.backoff = 0
	m.nextTry = time.Time{}
	if m.Path == "" {
		return nil
	}
	err := os.Remove(m.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (m *Manager) ensureLocked(ctx context.Context) error {
	now := m.now()
	if m.tokens != nil && m.tokens.AccessToken != "" && now.Unix() < m.tokens.ExpiresAt-300 {
		m.state = "ready"
		m.backoff = 0
		m.nextTry = time.Time{}
		return nil
	}
	if m.tokens != nil && m.tokens.AccessToken != "" && now.Unix() < m.tokens.ExpiresAt && now.Before(m.nextTry) {
		m.state = "ready"
		return nil
	}
	if (m.state == "backoff" || m.state == "degraded") && now.Before(m.nextTry) {
		if m.lastErr != "" {
			return fmt.Errorf("auth backoff: %s", m.lastErr)
		}
		return errors.New("auth backoff")
	}
	return m.refreshOrLogin(ctx, false)
}

func (m *Manager) refreshOrLogin(ctx context.Context, force bool) error {
	now := m.now()
	if m.tokens != nil && m.tokens.RefreshToken != "" && (m.tokens.RefreshExpiresAt == 0 || now.Unix() < m.tokens.RefreshExpiresAt) {
		err := m.refreshLocked(ctx)
		if err == nil {
			return nil
		}
		if isInvalid(err) {
			m.tokens.AccessToken = ""
			m.tokens.RefreshToken = ""
			if m.Password == "" {
				m.fail(err, true)
				return err
			}
			return m.passwordGrant(ctx, m.Email, m.Password, false)
		}
		if m.Password == "" {
			// A live refresh token must survive a blip. Only invalid_grant opens the gate.
			m.lastErr = safeAuthErr(err)
			m.bumpBackoff(now)
			if !force && m.tokens != nil && m.tokens.AccessToken != "" && now.Unix() < m.tokens.ExpiresAt {
				m.state = "ready"
				logx.Warn("auth", "auth refresh deferred", "auth_state", m.state, "err", m.lastErr)
				return nil
			}
			m.state = "degraded"
			logx.Warn("auth", "auth refresh deferred", "auth_state", m.state, "err", m.lastErr)
			return err
		}
		if isTemporary(err) {
			m.fail(err, false)
			return err
		}
		return m.passwordGrant(ctx, m.Email, m.Password, false)
	}
	if m.Password == "" {
		m.state = "auth_required"
		return ErrAuthRequired
	}
	return m.passwordGrant(ctx, m.Email, m.Password, false)
}

func (m *Manager) refreshLocked(ctx context.Context) error {
	m.state = "refreshing"
	old := m.tokens.RefreshToken
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", old)
	form.Set("scope", "all")
	form.Set("platform", "IOS")
	body, err := m.postForm(ctx, form, "bearer")
	if err != nil {
		return err
	}
	next, err := m.applyToken(body, old)
	if err != nil {
		return err
	}
	if err := m.persist(next); err != nil {
		return err
	}
	m.tokens = next
	m.state = "ready"
	m.backoff = 0
	m.nextTry = time.Time{}
	m.lastErr = ""
	m.hook = true
	logx.Info("auth", "auth refreshed", "token_tail", tail(next.AccessToken))
	return nil
}

func (m *Manager) passwordGrant(ctx context.Context, email, password string, interactive bool) error {
	m.state = "logging_in"
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("scope", "all")
	form.Set("platform", "IOS")
	form.Set("type", "account")
	form.Set("username", email)
	form.Set("password", PasswordHash(password))
	form.Set("country", m.Country)
	form.Set("lang", Lang)
	body, err := m.postForm(ctx, form, "bearer")
	if err != nil {
		m.fail(err, interactive || isInvalid(err) || m.Password == "")
		return err
	}
	var oldRefresh string
	if m.tokens != nil {
		oldRefresh = m.tokens.RefreshToken
	}
	next, err := m.applyToken(body, oldRefresh)
	if err != nil {
		m.fail(err, interactive || m.Password == "")
		return err
	}
	if err := m.persist(next); err != nil {
		m.fail(err, interactive)
		return err
	}
	m.tokens = next
	m.state = "ready"
	m.backoff = 0
	m.nextTry = time.Time{}
	m.lastErr = ""
	m.hook = true
	logx.Info("auth", "auth logged in", "token_tail", tail(next.AccessToken))
	return nil
}

func (m *Manager) applyToken(body []byte, oldRefresh string) (*Tokens, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if errName, _ := raw["error"].(string); errName != "" {
		return nil, &APIError{Op: "oauth", Msg: errName, Invalid: invalidName(errName)}
	}
	access, _ := raw["access_token"].(string)
	if access == "" {
		return nil, &APIError{Op: "oauth", Msg: "missing access_token", Invalid: true}
	}
	refresh, _ := raw["refresh_token"].(string)
	if refresh == "" {
		refresh = oldRefresh
	}
	tenant := stringField(raw["tenant_id"])
	if tenant == "" {
		tenant = m.Tenant
	}
	uid := stringField(raw["uid"])
	if uid == "" {
		uid = jwtString(access, "uid", "user_name", "userId")
	}
	now := m.now().Unix()
	exp := jwtExp(access)
	if exp == 0 {
		if n, ok := raw["expires_in"].(float64); ok && n > 0 {
			exp = now + int64(n)
		} else {
			exp = now + 7200
		}
	}
	var rexp int64
	if refresh != "" {
		rexp = jwtExp(refresh)
	}
	return &Tokens{
		AccessToken: access, RefreshToken: refresh, TenantID: tenant, UID: uid,
		Region: m.Region, ExpiresAt: exp, RefreshExpiresAt: rexp,
	}, nil
}

func (m *Manager) postForm(ctx context.Context, form url.Values, dreameAuth string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL(m.Region)+"/dreame-auth/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Authorization", BasicAuth)
	req.Header.Set("dreame-auth", dreameAuth)
	req.Header.Set("dreame-meta", MetaCV)
	tenant := m.Tenant
	if tenant == "" {
		tenant = "000000"
	}
	req.Header.Set("Tenant-Id", tenant)
	if m.SendRLC {
		h, err := RLCHeader(m.Region, Lang, m.Country)
		if err != nil {
			return nil, err
		}
		req.Header.Set("dreame-rlc", h)
	}
	resp, err := m.http().Do(req)
	if err != nil {
		return nil, &APIError{Op: "oauth", Msg: err.Error(), Temporary: !errors.Is(err, context.Canceled)}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, &APIError{Op: "oauth", Msg: err.Error(), Temporary: true, HTTP: resp.StatusCode}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest {
		msg := oauthMessage(b)
		return nil, &APIError{Op: "oauth", HTTP: resp.StatusCode, Msg: msg, Invalid: resp.StatusCode == http.StatusBadRequest || invalidName(msg)}
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return nil, &APIError{Op: "oauth", HTTP: resp.StatusCode, Msg: oauthMessage(b), Temporary: true}
	}
	if resp.StatusCode >= 300 {
		return nil, &APIError{Op: "oauth", HTTP: resp.StatusCode, Msg: oauthMessage(b)}
	}
	return b, nil
}

func (m *Manager) persist(t *Tokens) error {
	if m.Path == "" {
		return nil
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return filex.WriteAtomic(m.Path, b)
}

func (m *Manager) fail(err error, toGate bool) {
	m.lastErr = safeAuthErr(err)
	hasRefresh := m.tokens != nil && m.tokens.RefreshToken != "" && (m.tokens.RefreshExpiresAt == 0 || m.now().Unix() < m.tokens.RefreshExpiresAt)
	if toGate || isInvalid(err) || (m.Password == "" && !hasRefresh) {
		m.state = "auth_required"
		logx.Warn("auth", "auth failed", "auth_state", m.state, "err", m.lastErr)
		return
	}
	m.bumpBackoff(m.now())
	m.state = "backoff"
	logx.Warn("auth", "auth failed", "auth_state", m.state, "err", m.lastErr)
}

func (m *Manager) bumpBackoff(now time.Time) {
	if m.backoff < 30*time.Second {
		m.backoff = 30 * time.Second
	} else {
		m.backoff *= 2
		if m.backoff > 5*time.Minute {
			m.backoff = 5 * time.Minute
		}
	}
	m.nextTry = now.Add(m.backoff)
}

func regionCountry(region string) string {
	switch strings.ToLower(region) {
	case "us":
		return "US"
	case "eu":
		return "DE"
	case "cn":
		return "CN"
	case "sg":
		return "SG"
	case "kr":
		return "KR"
	case "ru":
		return "RU"
	default:
		return ""
	}
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) http() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return defaultHTTP
}

func (m *Manager) tenantLocked() string {
	if m.tokens != nil && m.tokens.TenantID != "" {
		return m.tokens.TenantID
	}
	if m.Tenant != "" {
		return m.Tenant
	}
	return "000000"
}

func oauthMessage(b []byte) string {
	var raw map[string]any
	if json.Unmarshal(b, &raw) != nil {
		return "oauth error"
	}
	if s, ok := raw["error"].(string); ok && s != "" {
		return s
	}
	if s, ok := raw["msg"].(string); ok && s != "" {
		return s
	}
	if s, ok := raw["error_description"].(string); ok && s != "" {
		return s
	}
	return "oauth error"
}

func invalidName(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "invalid_grant") || strings.Contains(l, "invalid_user") || strings.Contains(l, "bad password") || strings.Contains(l, "invalid_client")
}

func isInvalid(err error) bool {
	var e *APIError
	if errors.As(err, &e) {
		return e.Invalid || invalidName(e.Msg)
	}
	return invalidName(err.Error())
}

func isTemporary(err error) bool {
	var e *APIError
	if errors.As(err, &e) {
		return e.Temporary && !e.Invalid
	}
	return temporary(err)
}

func temporary(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var n net.Error
	if errors.As(err, &n) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func safeAuthErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 180 {
		s = s[:180]
	}
	return s
}

func tail(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

func stringField(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%.0f", t)
	default:
		return ""
	}
}

func jwtExp(token string) int64 {
	claims := jwtClaims(token)
	switch n := claims["exp"].(type) {
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func jwtString(token string, keys ...string) string {
	claims := jwtClaims(token)
	for _, k := range keys {
		if s, ok := claims[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		b, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}
