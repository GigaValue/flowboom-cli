// Flowboom CLI is a first-party public OAuth device client.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const clientID = "flowboom-cli"

var origins = map[string]string{"cn": "https://cloud.flowboom.cn", "global": "https://cloud.flowboom.io"}

const scopes = "flowboom.read flowboom.knowledge flowboom.drafts flowboom.publish"

type credentials struct {
	BaseURL      string    `json:"baseUrl"`
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
	TenantID     uint64    `json:"tenantId"`
	TenantIDs    []uint64  `json:"tenantIds,omitempty"`
	Scope        string    `json:"scope"`
}
type tokens struct {
	Access    string   `json:"access_token"`
	Refresh   string   `json:"refresh_token"`
	ExpiresIn int      `json:"expires_in"`
	TenantID  uint64   `json:"tenant_id"`
	TenantIDs []uint64 `json:"tenant_ids"`
	Scope     string   `json:"scope"`
}
type apiError struct {
	Status int
	Code   string
}

func (e *apiError) Error() string {
	if e.Code == "authorization_pending" || e.Code == "slow_down" || e.Code == "access_denied" || e.Code == "expired_token" {
		return e.Code
	}
	return fmt.Sprintf("request failed (HTTP %d); check authorization, workspace and permissions", e.Status)
}

type app struct {
	profile, path string
	workspace     uint64
	http          *http.Client
	out, errOut   io.Writer
}

func newApp(profile string) (*app, error) {
	if profile != "cn" && profile != "global" {
		return nil, errors.New("profile must be cn or global")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return &app{profile: profile, path: filepath.Join(dir, "flowboom", profile+".json"), http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("HTTP redirects are disabled") }}, out: os.Stdout, errOut: os.Stderr}, nil
}
func normalizeOrigin(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("base URL must be an HTTPS origin")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("HTTPS is required except for loopback demos")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
func (a *app) load() (*credentials, error) {
	info, e := os.Lstat(a.path)
	if e != nil {
		return nil, errors.New("not logged in; run flowboom auth login for this profile")
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("credential file must be a regular private file (0600)")
	}
	raw, e := os.ReadFile(a.path)
	if e != nil {
		return nil, errors.New("cannot read credentials")
	}
	var c credentials
	if json.Unmarshal(raw, &c) != nil || c.AccessToken == "" || c.RefreshToken == "" || c.TenantID == 0 {
		return nil, errors.New("invalid credentials; log in again")
	}
	c.BaseURL, e = normalizeOrigin(c.BaseURL)
	if e != nil {
		return nil, e
	}
	if len(c.TenantIDs) == 0 {
		c.TenantIDs = []uint64{c.TenantID}
	}
	if !containsID(c.TenantIDs, c.TenantID) {
		return nil, errors.New("invalid default workspace; log in again")
	}
	return &c, nil
}
func (a *app) save(c *credentials) error {
	dir := filepath.Dir(a.path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return errors.New("cannot create credential directory")
	}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return errors.New("credential directory must be private (0700)")
	}
	raw, e := json.Marshal(c)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".credentials-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(raw); e != nil {
		f.Close()
		return errors.New("cannot save credentials")
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(name, a.path); e != nil {
		return errors.New("cannot replace credentials")
	}
	return nil
}
func (a *app) request(ctx context.Context, base, method, path string, body io.Reader, contentType, access string, tenant uint64) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, method, base+"/api/v1"+path, body)
	if e != nil {
		return nil, errors.New("invalid API request")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "flowboom-cli/0.1")
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	if tenant != 0 {
		req.Header.Set("X-Tenant-ID", strconv.FormatUint(tenant, 10))
	}
	res, e := a.http.Do(req)
	if e != nil {
		return nil, errors.New("API connection failed; check the selected site and network. Writes are not retried: inspect existing drafts before resubmitting")
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024+1))
	if e != nil || len(raw) > 8*1024*1024 {
		return nil, errors.New("invalid or oversized API response")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &msg)
		return nil, &apiError{Status: res.StatusCode, Code: msg.Error}
	}
	if !json.Valid(raw) {
		return nil, errors.New("API did not return JSON; check deployment")
	}
	return raw, nil
}
func (a *app) form(ctx context.Context, base, path string, values url.Values) ([]byte, error) {
	return a.request(ctx, base, "POST", path, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", "", 0)
}
func (a *app) exchange(ctx context.Context, base string, values url.Values) (*credentials, error) {
	values.Set("client_id", clientID)
	raw, e := a.form(ctx, base, "/oauth/token", values)
	if e != nil {
		return nil, e
	}
	var t tokens
	if json.Unmarshal(raw, &t) != nil || t.Access == "" || t.Refresh == "" || t.TenantID == 0 || t.ExpiresIn <= 0 {
		return nil, errors.New("invalid token response")
	}
	if len(t.TenantIDs) == 0 {
		t.TenantIDs = []uint64{t.TenantID}
	}
	if !containsID(t.TenantIDs, t.TenantID) {
		return nil, errors.New("invalid authorized workspaces")
	}
	return &credentials{TenantIDs: t.TenantIDs, BaseURL: base, AccessToken: t.Access, RefreshToken: t.Refresh, ExpiresAt: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second), TenantID: t.TenantID, Scope: t.Scope}, nil
}
func (a *app) ready(ctx context.Context) (*credentials, error) {
	c, e := a.load()
	if e != nil {
		return nil, e
	}
	if time.Until(c.ExpiresAt) > time.Minute {
		return c, nil
	}
	next, e := a.exchange(ctx, c.BaseURL, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}})
	if e != nil {
		return nil, errors.New("session refresh failed; retry once or run flowboom auth login")
	}
	for _, id := range next.TenantIDs {
		if !containsID(c.TenantIDs, id) {
			return nil, errors.New("refresh expanded workspaces; log in again")
		}
	}
	if containsID(next.TenantIDs, c.TenantID) {
		next.TenantID = c.TenantID
	}
	if e = a.save(next); e != nil {
		return nil, e
	}
	return next, nil
}
func (a *app) api(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	c, e := a.ready(ctx)
	if e != nil {
		return nil, e
	}
	id, e := a.selectedWorkspace(c)
	if e != nil {
		return nil, e
	}
	return a.request(ctx, c.BaseURL, method, "/skills"+path, bytes.NewReader(body), "application/json", c.AccessToken, id)
}
func openBrowser(link string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		cmd = exec.Command("xdg-open", link)
	}
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}
func (a *app) login(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("auth login", flag.ContinueOnError)
	flags.SetOutput(a.errOut)
	baseFlag := flags.String("base-url", origins[a.profile], "Flowboom HTTPS origin (local demo may use loopback HTTP)")
	noBrowser := flags.Bool("no-browser", false, "print browser URL without opening it")
	readOnly := flags.Bool("read-only", false, "request read access without draft creation")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected login arguments")
	}
	base, e := normalizeOrigin(*baseFlag)
	if e != nil {
		return e
	}
	requested := scopes
	if *readOnly {
		requested = "flowboom.read flowboom.knowledge"
	}
	raw, e := a.form(ctx, base, "/oauth/device/code", url.Values{"client_id": {clientID}, "scope": {requested}})
	if e != nil {
		return e
	}
	var d struct {
		DeviceCode string `json:"device_code"`
		Code       string `json:"user_code"`
		URI        string `json:"verification_uri_complete"`
		Expires    int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	if json.Unmarshal(raw, &d) != nil || d.DeviceCode == "" || d.Code == "" || d.Expires <= 0 || d.Expires > 900 || d.Interval < 1 {
		return errors.New("invalid device authorization response")
	}
	// Never open an arbitrary verification host supplied by a misconfigured API.
	link, e := url.Parse(d.URI)
	origin, _ := url.Parse(base)
	if e != nil || link.Scheme != origin.Scheme || link.Host != origin.Host || link.User != nil || link.Path != "/oauth/device" {
		return errors.New("verification URL does not match selected site")
	}
	fmt.Fprintf(a.errOut, "Profile: %s\nOpen: %s\nDevice code: %s\nChoose your workspaces and permissions in the browser. Waiting for authorization…\n", a.profile, d.URI, d.Code)
	if !*noBrowser {
		openBrowser(d.URI)
	}
	deadline := time.Now().Add(time.Duration(d.Expires) * time.Second)
	interval := time.Duration(d.Interval) * time.Second
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		c, e := a.exchange(ctx, base, url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {d.DeviceCode}})
		if e != nil {
			var ae *apiError
			if errors.As(e, &ae) {
				switch ae.Code {
				case "authorization_pending":
					continue
				case "slow_down":
					interval += 5 * time.Second
					continue
				case "access_denied":
					return errors.New("authorization denied; no credentials saved")
				case "expired_token":
					return errors.New("device code expired; run login again")
				}
			}
			return e
		}
		// Verify the resulting tenant-bound session before persisting it.
		if _, e = a.request(ctx, c.BaseURL, "GET", "/skills/identity", nil, "", c.AccessToken, c.TenantID); e != nil {
			return e
		}
		if e = a.save(c); e != nil {
			return e
		}
		fmt.Fprintf(a.errOut, "Authorized default workspace %d (%s). Credentials saved privately for profile %s.\n", c.TenantID, c.Scope, a.profile)
		return nil
	}
	return errors.New("device code expired; run login again")
}
func (a *app) auth(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("use auth login, status or logout")
	}
	switch args[0] {
	case "login":
		return a.login(ctx, args[1:])
	case "status":
		if len(args) != 1 {
			return errors.New("unexpected status arguments")
		}
		c, e := a.ready(ctx)
		if e != nil {
			return e
		}
		id, e := a.selectedWorkspace(c)
		if e != nil {
			return e
		}
		raw, e := a.request(ctx, c.BaseURL, "GET", "/skills/identity", nil, "", c.AccessToken, id)
		if e != nil {
			return e
		}
		var result map[string]any
		if json.Unmarshal(raw, &result) != nil {
			return errors.New("invalid identity response")
		}
		result["profile"] = a.profile
		result["baseUrl"] = c.BaseURL
		result["authorizedWorkspaceIds"] = c.TenantIDs
		result["defaultWorkspaceId"] = c.TenantID
		raw, _ = json.Marshal(result)
		return a.printJSON(raw)
	case "logout":
		if len(args) != 1 {
			return errors.New("unexpected logout arguments")
		}
		c, e := a.load()
		if e != nil {
			return e
		}
		if _, e = a.form(ctx, c.BaseURL, "/oauth/revoke", url.Values{"client_id": {clientID}, "token": {c.RefreshToken}}); e != nil {
			return errors.New("server revocation failed; local credentials retained so logout can be retried, or revoke in the browser")
		}
		if e = os.Remove(a.path); e != nil {
			return errors.New("revoked on server but local credential cleanup failed")
		}
		fmt.Fprintln(a.errOut, "Logged out; server authorization revoked.")
		return nil
	}
	return errors.New("unknown auth command")
}
func (a *app) printJSON(raw []byte) error {
	var dst bytes.Buffer
	if e := json.Indent(&dst, raw, "", "  "); e != nil {
		return e
	}
	_, e := fmt.Fprintln(a.out, dst.String())
	return e
}
func positiveID(raw string) (string, error) {
	n, e := strconv.ParseUint(raw, 10, 64)
	if e != nil || n == 0 {
		return "", errors.New("positive numeric ID required")
	}
	return strconv.FormatUint(n, 10), nil
}
func (a *app) run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprint(a.out, help)
		return nil
	}
	if args[0] == "auth" {
		return a.auth(ctx, args[1:])
	}
	if args[0] == "workspaces" {
		return a.workspaces(ctx, args[1:])
	}
	cmd := args[0]
	rest := args[1:]
	action := ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		action = rest[0]
		rest = rest[1:]
	}
	id := ""
	if (action == "get" || action == "submit-review" || action == "publish") && len(rest) > 0 {
		var e error
		id, e = positiveID(rest[0])
		if e != nil {
			return e
		}
		rest = rest[1:]
	}
	flags := flag.NewFlagSet(cmd, flag.ContinueOnError)
	flags.SetOutput(a.errOut)
	site := flags.Uint64("site", 0, "site ID")
	confirm := flags.Bool("confirm", false, "confirm publication of this exact article")
	workspace := flags.Uint64("workspace", a.workspace, "authorized workspace ID")
	file := flags.String("file", "", "UTF-8 JSON request file")
	query := flags.String("query", "", "search query")
	status := flags.String("status", "", "status filter")
	page := flags.Int("page", 1, "page number")
	size := flags.Int("page-size", 20, "page size")
	from := flags.String("from", "", "YYYY-MM-DD")
	to := flags.String("to", "", "YYYY-MM-DD")
	engine := flags.String("engine", "google", "google, bing or baidu")
	source := flags.String("source", "ga4", "ga4 or baidu_tongji")
	if e := flags.Parse(rest); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	a.workspace = *workspace
	if cmd == "articles" && (action == "submit-review" || action == "publish") {
		if id == "" {
			return errors.New("article ID required")
		}
		return a.articleAction(ctx, action, id, *confirm)
	}
	if *confirm {
		return errors.New("--confirm is only valid for articles publish")
	}
	method := "GET"
	path := ""
	values := url.Values{}
	if *site != 0 {
		values.Set("siteId", strconv.FormatUint(*site, 10))
	}
	var body []byte
	switch cmd {
	case "publish-tasks":
		if action != "get" || id == "" {
			return errors.New("use publish-tasks get ID")
		}
		path = "/publish-tasks/" + id
	case "sources":
		if action != "list" {
			return errors.New("use sources list [--site ID] [--page N]")
		}
		path = "/source-items"
		values.Set("page", strconv.Itoa(*page))
		if *status != "" {
			values.Set("status", *status)
		}
	case "sites":
		if action != "list" {
			return errors.New("use sites list")
		}
		path = "/sites"
	case "categories":
		if action != "list" || *site == 0 {
			return errors.New("use categories list --site ID")
		}
		path = "/categories"
	case "articles", "topics", "briefs":
		switch action {
		case "list":
			path = "/" + cmd
			values.Set("page", strconv.Itoa(*page))
			values.Set("pageSize", strconv.Itoa(*size))
			if *query != "" {
				values.Set("q", *query)
			}
			if *status != "" {
				values.Set("status", *status)
			}
		case "get":
			if id == "" || cmd == "topics" {
				return errors.New("get requires an article or Brief ID")
			}
			path = "/" + cmd + "/" + id
		case "create":
			if cmd == "briefs" {
				return errors.New("use briefs from-knowledge")
			}
			method = "POST"
			path = "/" + cmd
		case "from-knowledge":
			if cmd != "briefs" {
				return errors.New("use briefs from-knowledge")
			}
			method = "POST"
			path = "/knowledge/briefs"
		default:
			return errors.New("unknown content command")
		}
	case "knowledge":
		if action != "search" || strings.TrimSpace(*query) == "" {
			return errors.New("use knowledge search --query TEXT [--site ID]")
		}
		method = "POST"
		path = "/knowledge/search"
		body, _ = json.Marshal(map[string]any{"siteId": *site, "query": *query})
	case "diagnostics":
		if action != "" {
			return errors.New("use diagnostics [--site ID]")
		}
		path = "/diagnostics"
	case "seo":
		if *site == 0 {
			return errors.New("SEO requires --site ID")
		}
		path = "/sites/" + strconv.FormatUint(*site, 10) + "/seo/"
		values.Del("siteId")
		switch action {
		case "report":
			path += "overview"
			values.Set("engine", *engine)
			values.Set("source", *source)
			if *from != "" {
				values.Set("from", *from)
			}
			if *to != "" {
				values.Set("to", *to)
			}
		case "opportunities":
			path += "opportunities"
			if *status != "" {
				values.Set("status", *status)
			}
		default:
			return errors.New("use seo report or seo opportunities")
		}
	default:
		return errors.New("unknown command; run flowboom help")
	}
	if method == "POST" && body == nil {
		if *file == "" {
			return errors.New("--file is required for draft creation")
		}
		var e error
		body, e = os.ReadFile(*file)
		if e != nil {
			return errors.New("cannot read JSON file")
		}
		if len(body) > 1024*1024 || !json.Valid(body) {
			return errors.New("request file must contain valid JSON, at most 1 MiB")
		}
	}
	if method == "GET" && len(values) > 0 {
		path += "?" + values.Encode()
	}
	raw, e := a.api(ctx, method, path, body)
	if e != nil {
		return e
	}
	return a.printJSON(raw)
}

const help = `Flowboom CLI — content operations for Codex and WorkBuddy

flowboom [--profile cn|global] [--workspace ID] COMMAND

Profiles: cn=https://cloud.flowboom.cn (default)
          global=https://cloud.flowboom.io (deployment pending)
Credentials and workspace authorization are isolated per profile.

  auth login [--no-browser] [--read-only] [--base-url HTTPS_ORIGIN]
  auth status | auth logout
  workspaces list | workspaces use ID
  sites list
  sources list [--site ID] [--page N] [--status STATUS]
  categories list --site ID
  articles list [--site ID] [--status STATUS] [--query TEXT] [--page N]
  articles get ID
  articles create --file article.json
  articles submit-review ID
  articles publish ID [--confirm]
  publish-tasks get ID
  topics list [--site ID] [--status STATUS]
  topics create --file topic.json
  briefs list [--site ID] [--page N]
  briefs get ID
  briefs from-knowledge --file brief.json
  knowledge search --query TEXT [--site ID]
  diagnostics [--site ID]
  seo report --site ID [--from DATE --to DATE --engine google|bing|baidu --source ga4|baidu_tongji]
  seo opportunities --site ID [--status STATUS]

API results are JSON. Tokens are never printed. Writes are never retried.
Publish without --confirm previews the exact target; --confirm queues an approved article.
Review approval, paid generation and customer outreach remain in the web app.
`

func main() {
	root := flag.NewFlagSet("flowboom", flag.ContinueOnError)
	root.SetOutput(os.Stderr)
	defaultProfile := os.Getenv("FLOWBOOM_PROFILE")
	if defaultProfile == "" {
		defaultProfile = "cn"
	}
	profile := root.String("profile", defaultProfile, "cn or global")
	workspace := root.Uint64("workspace", 0, "authorized workspace ID")
	if e := root.Parse(os.Args[1:]); e != nil {
		if e == flag.ErrHelp {
			fmt.Print(help)
			return
		}
		os.Exit(2)
	}
	a, e := newApp(*profile)
	if e == nil {
		a.workspace = *workspace
		ctx, cancel := context.WithTimeout(context.Background(), 16*time.Minute)
		defer cancel()
		e = a.run(ctx, root.Args())
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

func containsID(ids []uint64, id uint64) bool {
	for _, v := range ids {
		if v == id && id != 0 {
			return true
		}
	}
	return false
}
func (a *app) selectedWorkspace(c *credentials) (uint64, error) {
	id := c.TenantID
	if a.workspace != 0 {
		id = a.workspace
	}
	if !containsID(c.TenantIDs, id) {
		return 0, errors.New("workspace not authorized; choose an authorized workspace or log in again")
	}
	return id, nil
}
func (a *app) workspaces(ctx context.Context, args []string) error {
	if len(args) == 1 && args[0] == "list" {
		c, e := a.ready(ctx)
		if e != nil {
			return e
		}
		raw, e := a.request(ctx, c.BaseURL, "GET", "/skills/workspaces", nil, "", c.AccessToken, 0)
		if e != nil {
			return e
		}
		return a.printJSON(raw)
	}
	if len(args) != 2 || args[0] != "use" {
		return errors.New("use workspaces list or workspaces use ID")
	}
	rawID, e := positiveID(args[1])
	if e != nil {
		return e
	}
	id, _ := strconv.ParseUint(rawID, 10, 64)
	c, e := a.ready(ctx)
	if e != nil {
		return e
	}
	if !containsID(c.TenantIDs, id) {
		return errors.New("workspace not authorized; log in again to add it")
	}
	if _, e = a.request(ctx, c.BaseURL, "GET", "/skills/identity", nil, "", c.AccessToken, id); e != nil {
		return e
	}
	c.TenantID = id
	if e = a.save(c); e != nil {
		return e
	}
	fmt.Fprintf(a.errOut, "Default workspace set to %d for profile %s.\n", id, a.profile)
	return nil
}
func (a *app) articleAction(ctx context.Context, action, id string, confirm bool) error {
	if action == "submit-review" {
		raw, e := a.api(ctx, "POST", "/articles/"+id+"/submit-review", []byte(`{}`))
		if e != nil {
			return e
		}
		return a.printJSON(raw)
	}
	raw, e := a.api(ctx, "GET", "/articles/"+id, nil)
	if e != nil {
		return e
	}
	var article struct {
		Data struct {
			ID     uint64 `json:"id"`
			SiteID uint64 `json:"siteId"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &article) != nil || strconv.FormatUint(article.Data.ID, 10) != id {
		return errors.New("invalid article response")
	}
	c, e := a.load()
	if e != nil {
		return e
	}
	workspace, e := a.selectedWorkspace(c)
	if e != nil {
		return e
	}
	preview := map[string]any{"profile": a.profile, "workspaceId": workspace, "articleId": article.Data.ID, "siteId": article.Data.SiteID, "title": article.Data.Title, "status": article.Data.Status, "confirmationRequired": true}
	if !confirm {
		out, _ := json.Marshal(map[string]any{"data": preview})
		return a.printJSON(out)
	}
	if article.Data.Status != "ready" {
		return errors.New("article must pass review before publishing; approval remains in the web app")
	}
	fmt.Fprintf(a.errOut, "Publishing article %s in workspace %d, site %d.\n", id, workspace, article.Data.SiteID)
	raw, e = a.api(ctx, "POST", "/articles/"+id+"/publish", []byte(`{"confirm":true}`))
	if e != nil {
		return e
	}
	return a.printJSON(raw)
}
