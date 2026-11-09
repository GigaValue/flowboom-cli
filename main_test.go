package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T, profile string) *app {
	t.Helper()
	a, e := newApp(profile)
	if e != nil {
		t.Fatal(e)
	}
	a.path = filepath.Join(t.TempDir(), "private", profile+".json")
	a.out = &bytes.Buffer{}
	a.errOut = &bytes.Buffer{}
	return a
}
func validCred(base string) *credentials {
	return &credentials{BaseURL: base, AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh", TenantID: 7, Scope: scopes, ExpiresAt: time.Now().Add(time.Hour)}
}
func TestProfilesStayOnTheirOrigin(t *testing.T) {
	for _, profile := range []string{"cn", "global"} {
		t.Run(profile, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") != "Bearer synthetic-access" || r.Header.Get("X-Tenant-ID") != "7" {
					t.Error("missing credentials or workspace")
				}
				if r.URL.Path != "/api/v1/skills/sites" {
					t.Error(r.URL.Path)
				}
				io.WriteString(w, `{"data":[{"id":9,"name":"Demo"}]}`)
			}))
			defer server.Close()
			a := testApp(t, profile)
			if e := a.save(validCred(server.URL)); e != nil {
				t.Fatal(e)
			}
			if e := a.run(context.Background(), []string{"sites", "list"}); e != nil {
				t.Fatal(e)
			}
			if requests != 1 {
				t.Fatal(requests)
			}
			info, e := os.Stat(a.path)
			if e != nil || info.Mode().Perm() != 0600 {
				t.Fatal("credentials not private", e)
			}
			if strings.Contains(a.out.(*bytes.Buffer).String(), "synthetic-access") || strings.Contains(a.errOut.(*bytes.Buffer).String(), "synthetic-refresh") {
				t.Fatal("token printed")
			}
		})
	}
	cn, _ := newApp("cn")
	global, _ := newApp("global")
	if cn.path == global.path || origins["cn"] != "https://cloud.flowboom.cn" || origins["global"] != "https://cloud.flowboom.io" {
		t.Fatal("profile collision")
	}
}
func TestOriginValidationAndRedirects(t *testing.T) {
	for _, base := range []string{"http://example.com", "https://user:password@example.com", "https://example.com/api", "https://example.com?token=x", "https://example.com/#secret", "file:///tmp/x"} {
		if _, e := normalizeOrigin(base); e == nil {
			t.Fatal("unsafe origin", base)
		}
	}
	for _, base := range []string{"https://cloud.flowboom.cn", "https://cloud.flowboom.io/", "http://127.0.0.1:8080"} {
		if _, e := normalizeOrigin(base); e != nil {
			t.Fatal(base, e)
		}
	}
	leaked := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	a := testApp(t, "cn")
	a.save(validCred(redirect.URL))
	if e := a.run(context.Background(), []string{"sites", "list"}); e == nil || leaked {
		t.Fatal("followed credential-bearing redirect")
	}
}
func TestRefreshRotationAndWriteNoRetry(t *testing.T) {
	refreshed, posted := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/oauth/token":
			refreshed++
			r.ParseForm()
			if r.Form.Get("client_id") != clientID || r.Form.Get("refresh_token") != "synthetic-refresh" {
				t.Error("incorrect refresh")
			}
			io.WriteString(w, `{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600,"tenant_id":7,"scope":"flowboom.read flowboom.drafts"}`)
		case "/api/v1/skills/articles":
			posted++
			if r.Header.Get("Authorization") != "Bearer rotated-access" {
				t.Error("old token")
			}
			w.WriteHeader(503)
			io.WriteString(w, `{"error":"upstream password=NEVER-PRINT"}`)
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	a := testApp(t, "cn")
	c := validCred(server.URL)
	c.ExpiresAt = time.Now().Add(-time.Minute)
	if e := a.save(c); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(t.TempDir(), "article.json")
	os.WriteFile(file, []byte(`{"siteId":9,"title":"Draft","slug":"draft"}`), 0600)
	e := a.run(context.Background(), []string{"articles", "create", "--file", file})
	if e == nil || strings.Contains(e.Error(), "NEVER-PRINT") {
		t.Fatal("raw server error exposed", e)
	}
	if refreshed != 1 || posted != 1 {
		t.Fatal("write retried", refreshed, posted)
	}
	c, e = a.load()
	if e != nil || c.RefreshToken != "rotated-refresh" || c.BaseURL != server.URL {
		t.Fatal("rotation not persisted", e)
	}
}
func TestDeviceLoginAndDenial(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(fmt.Sprint(deny), func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				switch r.URL.Path {
				case "/api/v1/oauth/device/code":
					if r.Form.Get("client_id") != clientID {
						t.Error("client")
					}
					json.NewEncoder(w).Encode(map[string]any{"device_code": "synthetic-device", "user_code": "ABCD-EFGH", "verification_uri_complete": server.URL + "/oauth/device?user_code=ABCD-EFGH", "expires_in": 10, "interval": 1})
				case "/api/v1/oauth/token":
					if r.Form.Get("device_code") != "synthetic-device" || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
						t.Error("grant")
					}
					if deny {
						w.WriteHeader(400)
						io.WriteString(w, `{"error":"access_denied"}`)
					} else {
						io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"tenant_id":7,"scope":"flowboom.read"}`)
					}
				case "/api/v1/skills/identity":
					io.WriteString(w, `{"data":{"tenant":{"id":7,"name":"Demo"},"scope":"flowboom.read"}}`)
				default:
					t.Error(r.URL.Path)
				}
			}))
			defer server.Close()
			a := testApp(t, "cn")
			e := a.run(context.Background(), []string{"auth", "login", "--base-url", server.URL, "--no-browser"})
			if deny {
				if e == nil {
					t.Fatal("denial ignored")
				}
				if _, e = os.Stat(a.path); !os.IsNotExist(e) {
					t.Fatal("denied credentials saved")
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				c, e := a.load()
				if e != nil || c.Scope != "flowboom.read" {
					t.Fatal("reduced consent lost", e)
				}
			}
			output := a.out.(*bytes.Buffer).String() + a.errOut.(*bytes.Buffer).String()
			for _, secret := range []string{"new-access", "new-refresh", "synthetic-device"} {
				if strings.Contains(output, secret) {
					t.Fatal("secret printed")
				}
			}
		})
	}
}
func TestLogoutRetainsCredentialsUntilRevocation(t *testing.T) {
	fail := true
	revoked := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if fail {
			w.WriteHeader(503)
			io.WriteString(w, `{"error":"unavailable"}`)
			return
		}
		revoked = r.Form.Get("token")
		io.WriteString(w, `{}`)
	}))
	defer server.Close()
	a := testApp(t, "global")
	a.save(validCred(server.URL))
	if e := a.run(context.Background(), []string{"auth", "logout"}); e == nil {
		t.Fatal("failed revoke ignored")
	}
	if _, e := os.Stat(a.path); e != nil {
		t.Fatal("lost revoke credential")
	}
	fail = false
	if e := a.run(context.Background(), []string{"auth", "logout"}); e != nil {
		t.Fatal(e)
	}
	if revoked != "synthetic-refresh" {
		t.Fatal("wrong token")
	}
	if _, e := os.Stat(a.path); !os.IsNotExist(e) {
		t.Fatal("credential retained after revoke")
	}
}
func TestCommandContracts(t *testing.T) {
	var path, method, query string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		method = r.Method
		query = r.URL.RawQuery
		body = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"data":[]}`)
	}))
	defer server.Close()
	a := testApp(t, "cn")
	a.save(validCred(server.URL))
	if e := a.run(context.Background(), []string{"sources", "list", "--site", "9", "--page", "2"}); e != nil {
		t.Fatal(e)
	}
	q, _ := url.ParseQuery(query)
	if path != "/api/v1/skills/source-items" || q.Get("siteId") != "9" || q.Get("page") != "2" {
		t.Fatal(path, query)
	}
	if e := a.run(context.Background(), []string{"knowledge", "search", "--site", "9", "--query", "真实资料"}); e != nil {
		t.Fatal(e)
	}
	if method != "POST" || body["query"] != "真实资料" || body["siteId"] != float64(9) {
		t.Fatal(method, body)
	}
	if e := a.run(context.Background(), []string{"articles", "get", "../../admin"}); e == nil {
		t.Fatal("unsafe path ID")
	}
}

func TestWorkspaceSwitchOverrideAndRefresh(t *testing.T) {
	ctx := context.Background()
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path+":"+r.Header.Get("X-Tenant-ID"))
		switch r.URL.Path {
		case "/api/v1/oauth/token":
			io.WriteString(w, `{"access_token":"next","refresh_token":"next-refresh","expires_in":3600,"tenant_id":7,"tenant_ids":[7,8],"scope":"flowboom.read"}`)
		case "/api/v1/skills/workspaces":
			if r.Header.Get("X-Tenant-ID") != "" {
				t.Error("workspace list must survive removal of default membership")
			}
			io.WriteString(w, `{"data":[{"id":7,"name":"A"},{"id":8,"name":"B"}]}`)
		default:
			io.WriteString(w, `{"data":[]}`)
		}
	}))
	defer server.Close()
	a := testApp(t, "cn")
	c := validCred(server.URL)
	c.TenantIDs = []uint64{7, 8}
	if e := a.save(c); e != nil {
		t.Fatal(e)
	}
	if e := a.run(ctx, []string{"workspaces", "list"}); e != nil {
		t.Fatal(e)
	}
	if e := a.run(ctx, []string{"workspaces", "use", "8"}); e != nil {
		t.Fatal(e)
	}
	c, _ = a.load()
	if c.TenantID != 8 {
		t.Fatal("default not saved")
	}
	c.ExpiresAt = time.Now().Add(-time.Minute)
	a.save(c)
	if e := a.run(ctx, []string{"sites", "list"}); e != nil {
		t.Fatal(e)
	}
	c, _ = a.load()
	if c.TenantID != 8 {
		t.Fatal("refresh reset chosen workspace")
	}
	if e := a.run(ctx, []string{"sites", "list", "--workspace", "7"}); e != nil {
		t.Fatal(e)
	}
	c, _ = a.load()
	if c.TenantID != 8 {
		t.Fatal("override changed persistent default")
	}
	before := len(calls)
	if e := a.run(ctx, []string{"sites", "list", "--workspace", "9"}); e == nil || len(calls) != before {
		t.Fatal("unauthorized workspace sent request")
	}
	a.workspace = 0
	if e := a.run(ctx, []string{"workspaces", "use", "9"}); e == nil {
		t.Fatal("unauthorized default accepted")
	}
	if !strings.HasSuffix(calls[len(calls)-1], ":7") {
		t.Fatal(calls)
	}
}

func TestPublishPreviewConfirmationAndNoRetry(t *testing.T) {
	for _, state := range []string{"draft", "ready"} {
		t.Run(state, func(t *testing.T) {
			posts := 0
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Tenant-ID") != "7" {
					t.Error("wrong workspace")
				}
				switch {
				case r.Method == "GET" && r.URL.Path == "/api/v1/skills/articles/12":
					reads++
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": 12, "siteId": 9, "title": "Exact target", "status": state}})
				case r.Method == "POST" && r.URL.Path == "/api/v1/skills/articles/12/publish":
					posts++
					var in map[string]any
					json.NewDecoder(r.Body).Decode(&in)
					if in["confirm"] != true {
						t.Error("missing confirmation")
					}
					w.WriteHeader(503)
					io.WriteString(w, `{"error":"PRIVATE-QUEUE-SECRET"}`)
				default:
					t.Error(r.Method, r.URL.Path)
				}
			}))
			defer server.Close()
			a := testApp(t, "cn")
			a.save(validCred(server.URL))
			if e := a.run(context.Background(), []string{"articles", "publish", "12"}); e != nil {
				t.Fatal(e)
			}
			if posts != 0 || reads != 1 || !strings.Contains(a.out.(*bytes.Buffer).String(), `"confirmationRequired": true`) {
				t.Fatal("preview mutated or missing target")
			}
			e := a.run(context.Background(), []string{"articles", "publish", "12", "--confirm"})
			if e == nil || strings.Contains(e.Error(), "PRIVATE") {
				t.Fatal("unsafe failure", e)
			}
			want := 0
			if state == "ready" {
				want = 1
			}
			if posts != want {
				t.Fatal("publish unreviewed or retried", posts)
			}
		})
	}
}

func TestSubmitReviewAndTaskCommands(t *testing.T) {
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		io.WriteString(w, `{"data":{"id":12,"status":"reviewing"}}`)
	}))
	defer server.Close()
	a := testApp(t, "cn")
	a.save(validCred(server.URL))
	for _, args := range [][]string{{"articles", "submit-review", "12"}, {"publish-tasks", "get", "13"}} {
		if e := a.run(context.Background(), args); e != nil {
			t.Fatal(e)
		}
	}
	if strings.Join(seen, "|") != "POST /api/v1/skills/articles/12/submit-review|GET /api/v1/skills/publish-tasks/13" {
		t.Fatal(seen)
	}
}

func TestRefreshCannotEnlargeWorkspaceSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"next","refresh_token":"next-refresh","expires_in":3600,"tenant_id":9,"tenant_ids":[7,9],"scope":"flowboom.read"}`)
	}))
	defer server.Close()
	a := testApp(t, "cn")
	c := validCred(server.URL)
	c.ExpiresAt = time.Now().Add(-time.Minute)
	a.save(c)
	if _, e := a.ready(context.Background()); e == nil {
		t.Fatal("refresh expanded authorization")
	}
}

func TestScheduleCommandsRequireFlagNotFileConfirmation(t *testing.T) {
	for _, action := range []string{"schedule", "unschedule"} {
		t.Run(action, func(t *testing.T) {
			confirmations := []bool{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v1/skills/articles/"+action {
					t.Error("wrong plan endpoint")
				}
				var in map[string]any
				if json.NewDecoder(r.Body).Decode(&in) != nil {
					t.Error("invalid JSON")
				}
				value, ok := in["confirm"].(bool)
				if !ok {
					t.Error("missing confirmation")
				}
				confirmations = append(confirmations, value)
				io.WriteString(w, `{"data":{"confirmed":false,"items":[]}}`)
			}))
			defer server.Close()
			a := testApp(t, "cn")
			a.save(validCred(server.URL))
			file := filepath.Join(t.TempDir(), "plan.json")
			os.WriteFile(file, []byte(`{"siteId":9,"articleIds":[12],"startAt":"2030-01-01T09:00:00+08:00","intervalDays":1,"perDay":5,"confirm":true}`), 0600)
			for _, args := range [][]string{{"articles", action, "--file", file}, {"articles", action, "--file", file, "--confirm"}} {
				if e := a.run(context.Background(), args); e != nil {
					t.Fatal(e)
				}
			}
			if len(confirmations) != 2 || confirmations[0] || !confirmations[1] {
				t.Fatal("file bypassed preview", confirmations)
			}
		})
	}
}
