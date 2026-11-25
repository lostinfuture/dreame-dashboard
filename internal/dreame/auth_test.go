package dreame

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPasswordGrantAndRefreshRotation(t *testing.T) {
	var hits atomic.Int32
	var sawRefreshAuth string
	var sawLoginAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		hits.Add(1)
		if r.URL.Path != "/dreame-auth/oauth/token" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != BasicAuth {
			t.Errorf("basic %s", r.Header.Get("Authorization"))
		}
		if strings.Contains(body, "grant_type=password") {
			sawLoginAuth = r.Header.Get("dreame-auth")
			if !strings.Contains(body, "password="+PasswordHash("secret")) {
				t.Fatalf("hash body %s", body)
			}
			if !strings.Contains(body, "country=US") {
				t.Fatal(body)
			}
			writeToken(w, "access-1", "refresh-1", 1_800_000_000)
			return
		}
		if strings.Contains(body, "grant_type=refresh_token") {
			sawRefreshAuth = r.Header.Get("dreame-auth")
			if !strings.Contains(body, "refresh_token=refresh-1") {
				t.Fatal(body)
			}
			writeToken(w, "access-2", "refresh-2", 1_800_003_600)
			return
		}
		t.Fatalf("body %s", body)
	}))
	defer srv.Close()
	dir := t.TempDir()
	exp := int64(1_800_000_000)
	now := time.Unix(exp-1000, 0)
	m := NewManager(filepath.Join(dir, "tokens.json"))
	m.HTTP = &http.Client{Transport: rewrite(srv.URL)}
	m.Now = func() time.Time { return now }
	m.Region = "us"
	m.Country = "US"
	m.Email = "a@b.c"
	m.Password = "secret"
	ctx := context.Background()
	access, tenant, err := m.Authorize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if access == "" || tenant != "000000" {
		t.Fatal(access, tenant)
	}
	if sawLoginAuth != "bearer" {
		t.Fatal(sawLoginAuth)
	}
	now = time.Unix(exp-100, 0)
	m.Now = func() time.Time { return now }
	access2, _, err := m.Authorize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if access2 == access {
		t.Fatal("token did not refresh")
	}
	if sawRefreshAuth != "bearer" {
		t.Fatal(sawRefreshAuth)
	}
	b, err := os.ReadFile(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "refresh-2") || strings.Contains(string(b), "secret") {
		t.Fatal(string(b))
	}
	if hits.Load() != 2 {
		t.Fatal(hits.Load())
	}
}

func TestRefreshKeepsOldTokenWhenOmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"` + jwt("acc", time.Now().Add(time.Hour).Unix()) + `","tenant_id":"000000"}`))
	}))
	defer srv.Close()
	m := NewManager("")
	m.HTTP = &http.Client{Transport: rewrite(srv.URL)}
	m.Region = "us"
	m.Country = "US"
	m.tokens = &Tokens{AccessToken: "old", RefreshToken: "keep-me", ExpiresAt: 1}
	m.Now = time.Now
	if err := m.ForceRefresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Current().RefreshToken != "keep-me" {
		t.Fatal(m.Current().RefreshToken)
	}
}

func TestInteractiveFailureStaysAuthRequired(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	m := NewManager("")
	m.HTTP = &http.Client{Transport: rewrite(srv.URL)}
	m.Region = "us"
	m.Country = "US"
	err := m.Login(context.Background(), "a@b.c", "nope", "us", "US")
	if err == nil {
		t.Fatal("expected error")
	}
	if m.State() != "auth_required" {
		t.Fatal(m.State())
	}
	if m.Password != "" {
		t.Fatal("password retained")
	}
	_ = m.Login(context.Background(), "a@b.c", "nope", "us", "US")
	if n.Load() != 2 {
		t.Fatal("interactive must not enter backoff")
	}
}

func TestUnattendedInvalidDoesNotBackoff(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_user"}`))
	}))
	defer srv.Close()
	m := NewManager("")
	m.HTTP = &http.Client{Transport: rewrite(srv.URL)}
	m.Region = "us"
	m.Country = "US"
	m.Email = "a@b.c"
	m.Password = "bad"
	if _, _, err := m.Authorize(context.Background()); err == nil {
		t.Fatal("expected")
	}
	if m.State() != "auth_required" {
		t.Fatal(m.State())
	}
	_, _, _ = m.Authorize(context.Background())
	if n.Load() != 2 {
		t.Fatalf("calls %d", n.Load())
	}
}

func TestRejectedRefreshFallsBackToPassword(t *testing.T) {
	var grants []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		grants = append(grants, r.FormValue("grant_type"))
		if r.FormValue("grant_type") == "refresh_token" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		writeToken(w, "acc", "refresh-new", time.Now().Add(time.Hour).Unix())
	}))
	defer srv.Close()
	m := NewManager("")
	m.HTTP = &http.Client{Transport: rewrite(srv.URL)}
	m.Region = "us"
	m.Country = "US"
	m.Email = "a@b.c"
	m.Password = "file-secret"
	m.tokens = &Tokens{AccessToken: "old-access", RefreshToken: "stale", ExpiresAt: 1}
	m.Now = time.Now
	access, _, err := m.Authorize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 2 || grants[0] != "refresh_token" || grants[1] != "password" {
		t.Fatal(grants)
	}
	if m.State() != "ready" {
		t.Fatal(m.State())
	}
	cur := m.Current()
	if cur.RefreshToken != "refresh-new" || cur.AccessToken == "" || cur.AccessToken == "old-access" || access != cur.AccessToken {
		t.Fatalf("access %s refresh %s", access, cur.RefreshToken)
	}
	if m.Password != "file-secret" {
		t.Fatal("password cleared")
	}
}

func TestLoginRestoresRegionOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	m := NewManager("")
	m.HTTP = &http.Client{Transport: rewrite(srv.URL)}
	m.Region = "us"
	m.Country = "US"
	err := m.Login(context.Background(), "a@b.c", "nope", "eu", "DE")
	if err == nil {
		t.Fatal("expected error")
	}
	if m.Region != "us" || m.Country != "US" {
		t.Fatalf("region %s country %s", m.Region, m.Country)
	}
}

func TestInteractiveTransientKeepsRefresh(t *testing.T) {
	var grants []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		grants = append(grants, r.FormValue("grant_type"))
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	m := NewManager("")
	m.HTTP = &http.Client{Transport: rewrite(srv.URL)}
	m.Region = "us"
	m.Country = "US"
	m.Email = "a@b.c"
	m.tokens = &Tokens{AccessToken: "old-access", RefreshToken: "keep-me", ExpiresAt: 1, RefreshExpiresAt: time.Now().Add(24 * time.Hour).Unix()}
	if _, _, err := m.Authorize(context.Background()); err == nil {
		t.Fatal("expected refresh error")
	}
	if m.State() == "auth_required" || m.State() != "degraded" {
		t.Fatal(m.State())
	}
	if m.Current().RefreshToken != "keep-me" {
		t.Fatal(m.Current().RefreshToken)
	}
	if len(grants) != 1 || grants[0] != "refresh_token" {
		t.Fatal(grants)
	}
	// Backoff must stop tight retry loops on interactive degraded sessions.
	if _, _, err := m.Authorize(context.Background()); err == nil {
		t.Fatal("expected backoff error")
	}
	if len(grants) != 1 {
		t.Fatalf("interactive degraded hammered oauth: %d calls", len(grants))
	}
}

func TestForceRefreshReturnsErrorOnTransientWithUnexpiredToken(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	now := time.Unix(1_800_000_000, 0)
	m := NewManager("")
	m.HTTP = &http.Client{Transport: rewrite(srv.URL)}
	m.Now = func() time.Time { return now }
	m.tokens = &Tokens{
		AccessToken:      "still-unexpired-by-clock",
		RefreshToken:     "keep-me",
		ExpiresAt:        now.Add(100 * time.Second).Unix(),
		RefreshExpiresAt: now.Add(24 * time.Hour).Unix(),
	}
	if err := m.ForceRefresh(context.Background()); err == nil {
		t.Fatal("ForceRefresh must propagate transient error even if access token is not expired by clock")
	}
	if m.State() != "degraded" || m.Current().RefreshToken != "keep-me" {
		t.Fatalf("state=%s refresh=%s", m.State(), m.Current().RefreshToken)
	}
	// Proactive Authorize inside the 5-minute window returns nil using the existing token,
	// and must not hammer OAuth again while inside backoff.
	m2 := NewManager("")
	m2.HTTP = &http.Client{Transport: rewrite(srv.URL)}
	m2.Now = func() time.Time { return now }
	m2.tokens = &Tokens{
		AccessToken:      "proactive-token",
		RefreshToken:     "keep-me",
		ExpiresAt:        now.Add(100 * time.Second).Unix(),
		RefreshExpiresAt: now.Add(24 * time.Hour).Unix(),
	}
	before := hits.Load()
	tok, _, err := m2.Authorize(context.Background())
	if err != nil || tok != "proactive-token" {
		t.Fatalf("proactive err=%v tok=%s", err, tok)
	}
	tok2, _, err := m2.Authorize(context.Background())
	if err != nil || tok2 != "proactive-token" {
		t.Fatalf("proactive 2nd err=%v tok=%s", err, tok2)
	}
	if hits.Load()-before != 1 {
		t.Fatalf("proactive refresh hammered oauth: %d calls", hits.Load()-before)
	}
}

func TestNewManagerRestoresSavedRegion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	raw, _ := json.Marshal(Tokens{AccessToken: "a", RefreshToken: "r", TenantID: "123456", Region: "eu", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(path)
	m.SetDefaultLocale("us", "US")
	m.SetDefaultTenant("000000")
	r, c := m.Locale()
	if r != "eu" || c != "DE" || m.Tenant != "123456" {
		t.Fatalf("locale=(%s,%s) tenant=%s", r, c, m.Tenant)
	}
}

func TestRefreshDialRefusedDoesNotPasswordGrant(t *testing.T) {
	var n atomic.Int32
	m := NewManager("")
	m.HTTP = &http.Client{Transport: refuseRT{n: &n}}
	m.Region = "us"
	m.Country = "US"
	m.Email = "a@b.c"
	m.Password = "pw"
	m.tokens = &Tokens{AccessToken: "old-access", RefreshToken: "keep-me", ExpiresAt: 1, RefreshExpiresAt: time.Now().Add(24 * time.Hour).Unix()}
	if _, _, err := m.Authorize(context.Background()); err == nil {
		t.Fatal("expected")
	}
	if m.State() != "backoff" {
		t.Fatal(m.State())
	}
	if n.Load() != 1 {
		t.Fatalf("grants %d", n.Load())
	}
	if m.Current().RefreshToken != "keep-me" || m.Password != "pw" {
		t.Fatal("session cleared")
	}
}

type refuseRT struct{ n *atomic.Int32 }

func (r refuseRT) RoundTrip(*http.Request) (*http.Response, error) {
	r.n.Add(1)
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
}

func TestUnattendedTransientBackoff(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	now := time.Unix(1_800_000_000, 0)
	m := NewManager("")
	m.HTTP = &http.Client{Transport: rewrite(srv.URL)}
	m.Region = "us"
	m.Country = "US"
	m.Email = "a@b.c"
	m.Password = "pw"
	m.Now = func() time.Time { return now }
	if _, _, err := m.Authorize(context.Background()); err == nil {
		t.Fatal("expected")
	}
	if m.State() != "backoff" {
		t.Fatal(m.State())
	}
	_, _, _ = m.Authorize(context.Background())
	if n.Load() != 1 {
		t.Fatalf("backoff hammered %d", n.Load())
	}
}

func jwt(sub string, exp int64) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	p, _ := json.Marshal(map[string]any{"sub": sub, "exp": exp, "uid": "U0000001"})
	return h + "." + base64.RawURLEncoding.EncodeToString(p) + ".x"
}

func writeToken(w http.ResponseWriter, access, refresh string, exp int64) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  jwt(access, exp),
		"refresh_token": refresh,
		"expires_in":    7200,
		"tenant_id":     "000000",
		"uid":           "U0000001",
	})
}

type rewriteRoundTripper struct{ base string }

func rewrite(base string) http.RoundTripper { return rewriteRoundTripper{base: base} }

func (r rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	target, _ := http.NewRequest(req.Method, r.base+u.Path, req.Body)
	target.Header = req.Header
	target.ContentLength = req.ContentLength
	return http.DefaultTransport.RoundTrip(target)
}
