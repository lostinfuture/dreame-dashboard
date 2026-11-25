package web

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"dreame-dashboard/internal/ai"
)

type fakeBackend struct {
	phase      string
	snap       Snapshot
	sub        chan SSE
	ctrl       func(ControlInput) error
	logins     int
	outs       int
	polls      int
	otaChecks  int
	otaUpdates int
	openais    int
	analyze    func(AIRequest) (ai.Report, error)
	cached     *ai.Report
}

func (f *fakeBackend) Phase() string {
	if f.phase == "" {
		return "ready"
	}
	return f.phase
}
func (f *fakeBackend) Page(name string, q url.Values) Snapshot {
	s := f.snap
	s.Page = name
	s.Query = q
	if s.Name == "" {
		s.Name = "Air Purifier PM20"
	}
	return s
}
func (f *fakeBackend) Login(context.Context, string, string, string) error { f.logins++; return nil }
func (f *fakeBackend) Logout(context.Context) error                        { f.outs++; return nil }
func (f *fakeBackend) Control(_ context.Context, in ControlInput) error {
	if f.ctrl != nil {
		return f.ctrl(in)
	}
	return nil
}
func (f *fakeBackend) Poll(context.Context) error                   { f.polls++; return nil }
func (f *fakeBackend) CheckOTA(context.Context) error               { f.otaChecks++; return nil }
func (f *fakeBackend) StartOTAUpdate(context.Context) error         { f.otaUpdates++; return nil }
func (f *fakeBackend) VerifyMap(context.Context) error              { return nil }
func (f *fakeBackend) Settings(context.Context, SettingsForm) error { return nil }
func (f *fakeBackend) SaveOpenAI(context.Context, OpenAIForm) error { f.openais++; return nil }
func (f *fakeBackend) Analyze(_ context.Context, req AIRequest) (ai.Report, error) {
	if f.analyze != nil {
		return f.analyze(req)
	}
	return ai.Report{Scope: req.Scope, Metric: req.Metric, Span: req.Span, Status: "good", Headline: "OK"}, nil
}
func (f *fakeBackend) CachedReport(string, string, string) (ai.Report, bool) {
	if f.cached != nil {
		return *f.cached, true
	}
	return ai.Report{}, false
}
func (f *fakeBackend) Dismiss() {}
func (f *fakeBackend) Subscribe() (<-chan SSE, func()) {
	if f.sub == nil {
		f.sub = make(chan SSE, 1)
	}
	return f.sub, func() {}
}

func newTest(b *fakeBackend) *httptest.Server {
	s := NewServer("tok", b)
	return httptest.NewServer(s.Handler())
}

func TestHostAndBasic(t *testing.T) {
	srv := newTest(&fakeBackend{})
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Host = "evil.example"
	res, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMisdirectedRequest {
		t.Fatal(res.StatusCode)
	}
	locked := NewServer("tok", &fakeBackend{})
	locked.BasicUser = "ada"
	locked.BasicPass = "secret"
	ls := httptest.NewServer(locked.Handler())
	defer ls.Close()
	res, err = noFollow.Get(ls.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatal(res.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, ls.URL+"/", nil)
	req.SetBasicAuth("ada", "secret")
	res, err = noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatal(res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal(res.Header.Get("Content-Security-Policy"))
	}
}

func TestCSRFAndLogin(t *testing.T) {
	b := &fakeBackend{phase: "auth_required"}
	srv := newTest(b)
	defer srv.Close()
	res, err := noFollow.Post(srv.URL+"/login", "application/x-www-form-urlencoded", strings.NewReader("email=a@b.c"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || b.logins != 0 {
		t.Fatalf("status %d logins %d", res.StatusCode, b.logins)
	}
	res, err = noFollow.Post(srv.URL+"/login", "application/x-www-form-urlencoded", strings.NewReader("token=tok&email=a@b.c&password=x&region=us"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || b.logins != 1 {
		t.Fatalf("login %d %d", res.StatusCode, b.logins)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/poll", strings.NewReader("token=tok"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	res, err = noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatal(res.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/poll", strings.NewReader("token=tok"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://"+req.URL.Host)
	req.Host = req.URL.Host
	res, err = noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "auth_required") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	res, err = noFollow.Get(srv.URL + "/device")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Fatal(res.StatusCode, res.Header.Get("Location"))
	}
}

func TestBootBlocksWrites(t *testing.T) {
	srv := newTest(&fakeBackend{phase: "boot"})
	defer srv.Close()
	res, err := noFollow.Post(srv.URL+"/poll", "application/x-www-form-urlencoded", strings.NewReader("token=tok"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "booting") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	res, err = noFollow.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ = io.ReadAll(res.Body)
	if !strings.Contains(string(body), `http-equiv="refresh"`) {
		t.Fatal("boot page")
	}
}

func TestControlStatuses(t *testing.T) {
	b := &fakeBackend{snap: Snapshot{Mode: 0, ModeKnown: true, WritesOK: true, PM25Known: true, PM25: 4}}
	srv := newTest(b)
	defer srv.Close()
	res, err := noFollow.Get(srv.URL + "/device?stage=2.3:2")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "Change mode from AI to Sleep") {
		t.Fatalf("stage %d %s", res.StatusCode, body)
	}
	b.ctrl = func(ControlInput) error { return &codeErr{code: 403, msg: "read-only"} }
	res, err = noFollow.Post(srv.URL+"/control", "application/x-www-form-urlencoded", strings.NewReader("token=tok&confirmed=1&action=set&siid=2&piid=3&value=2"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatal(res.StatusCode)
	}
	b.ctrl = func(ControlInput) error { return &codeErr{code: 409, msg: "stale"} }
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/control", strings.NewReader("token=tok&confirmed=1&action=toggle_power&expected_old=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err = noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatal(res.StatusCode)
	}
	b.ctrl = nil
	res, err = noFollow.Post(srv.URL+"/control", "application/x-www-form-urlencoded", strings.NewReader("token=tok&confirmed=1&action=set&siid=2&piid=3&value=2"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatal(res.StatusCode)
	}
	res, err = noFollow.Post(srv.URL+"/logout", "application/x-www-form-urlencoded", strings.NewReader("token=tok"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || b.outs != 0 {
		t.Fatal(res.StatusCode, b.outs)
	}
	res, err = noFollow.Post(srv.URL+"/logout", "application/x-www-form-urlencoded", strings.NewReader("token=tok&confirmed=1"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || b.outs != 1 {
		t.Fatal(res.StatusCode, b.outs)
	}
}

func TestRefererAndSSE(t *testing.T) {
	b := &fakeBackend{}
	b.sub = make(chan SSE, 1)
	srv := newTest(b)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/poll", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", srv.URL+"/")
	res, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || b.polls != 1 {
		t.Fatal(res.StatusCode, b.polls)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		time.Sleep(30 * time.Millisecond)
		b.sub <- SSE{Name: "state", Data: `{"ok":true}`}
	}()
	rq, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	res, err = noFollow.Do(rq)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	sc := bufio.NewReader(res.Body)
	var got string
	for i := 0; i < 8; i++ {
		line, err := sc.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		got += line
		if strings.Contains(got, `data: {"ok":true}`) {
			break
		}
	}
	if !strings.Contains(got, `data: {"ok":true}`) {
		t.Fatal(got)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(res.Header.Get("Cache-Control"))
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/dismiss", strings.NewReader("token=tok&page=device"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", srv.URL+"/?stage=2.3:2")
	res, err = noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/device" {
		t.Fatalf("dismiss redirect %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestOpenAIAndAnalyzeRoutes(t *testing.T) {
	b := &fakeBackend{}
	srv := newTest(b)
	defer srv.Close()

	// Missing CSRF rejected on /admin/openai.
	res, err := noFollow.Post(srv.URL+"/admin/openai", "application/x-www-form-urlencoded", strings.NewReader("api_key=sk-test-1234567890abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || b.openais != 0 {
		t.Fatalf("status=%d openais=%d", res.StatusCode, b.openais)
	}

	res, err = noFollow.Post(srv.URL+"/admin/openai", "application/x-www-form-urlencoded", strings.NewReader("token=tok&api_key=sk-test-1234567890abcdef&model=gpt-4o-mini"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/device#ai" || b.openais != 1 {
		t.Fatalf("openai save status=%d loc=%q n=%d", res.StatusCode, res.Header.Get("Location"), b.openais)
	}

	res, err = noFollow.Get(srv.URL + "/api/ai/report?scope=general&span=6h")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"cached":false`) {
		t.Fatalf("report %d %s", res.StatusCode, body)
	}

	res, err = noFollow.Post(srv.URL+"/api/ai/analyze", "application/x-www-form-urlencoded", strings.NewReader("token=tok&scope=metric&metric=pm25&span=6h"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"headline":"OK"`) {
		t.Fatalf("analyze %d %s", res.StatusCode, body)
	}
}

func TestOTARoutes(t *testing.T) {
	b := &fakeBackend{}
	srv := newTest(b)
	defer srv.Close()

	res, err := noFollow.Post(srv.URL+"/ota/check", "application/x-www-form-urlencoded", strings.NewReader("token=tok"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/device#ota" || b.otaChecks != 1 {
		t.Fatalf("ota check status=%d loc=%q n=%d", res.StatusCode, res.Header.Get("Location"), b.otaChecks)
	}

	// Missing confirmed=1 must be rejected with 403.
	res, err = noFollow.Post(srv.URL+"/ota/update", "application/x-www-form-urlencoded", strings.NewReader("token=tok"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || b.otaUpdates != 0 {
		t.Fatalf("unconfirmed ota update status=%d n=%d", res.StatusCode, b.otaUpdates)
	}

	res, err = noFollow.Post(srv.URL+"/ota/update", "application/x-www-form-urlencoded", strings.NewReader("token=tok&confirmed=1"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/device#ota" || b.otaUpdates != 1 {
		t.Fatalf("confirmed ota update status=%d loc=%q n=%d", res.StatusCode, res.Header.Get("Location"), b.otaUpdates)
	}
}

var noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type codeErr struct {
	code int
	msg  string
}

func (e *codeErr) Error() string   { return e.msg }
func (e *codeErr) StatusCode() int { return e.code }
