package drives

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Bradthebrad/tinychain/mcp"
)

func configured(t *testing.T, accounts ...Account) *Drives {
	t.Helper()
	b, err := json.Marshal(accounts)
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func cloudAccount(service string) Account {
	return Account{ID: "test-account", Service: service, Label: "Test drive", Fields: map[string]string{"access_token": "synthetic-test-token-not-real"}, AllowWrite: true}
}
func invoke(t *testing.T, d *Drives, name string, args map[string]any) mcp.ToolResult {
	t.Helper()
	for _, tool := range d.Tools() {
		if tool.Name == name {
			r, err := tool.Handler(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
	}
	t.Fatalf("missing tool %s", name)
	return mcp.ToolResult{}
}
func decoded(t *testing.T, r mcp.ToolResult) map[string]any {
	t.Helper()
	if r.IsError {
		t.Fatalf("tool error: %+v", r)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(r.Content[0].Text), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestConfiguration(t *testing.T) {
	for _, raw := range []string{"", "[]", `[{"service":"gmail"}]`} {
		if _, err := New(raw); err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
	}
	for _, raw := range []string{"null", "{}", "[", strings.Repeat(" ", maxJSON+1)} {
		if _, err := New(raw); err == nil {
			t.Fatalf("accepted invalid configuration")
		}
	}
	d := configured(t, cloudAccount("google_drive"), Account{ID: "one", Service: "onedrive", Label: "One", Fields: map[string]string{"access_token": "another-synthetic-token"}})
	if len(d.accounts) != 2 {
		t.Fatal("accounts missing")
	}
	tr := d.client.Transport.(*http.Transport)
	if tr.Proxy != nil || d.client.Timeout.Seconds() != 20 {
		t.Fatal("unsafe client policy")
	}
	if d.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirects allowed")
	}
	for _, modify := range []func(*Account){
		func(a *Account) { a.ID = "bad!" }, func(a *Account) { a.ID = strings.Repeat("a", 65) },
		func(a *Account) { a.Label = "" }, func(a *Account) { a.Label = "bad\nlabel" },
		func(a *Account) { a.Fields = nil }, func(a *Account) { a.Fields["access_token"] = " \t" },
		func(a *Account) { a.Fields["access_token"] = "bad\x00token" },
		func(a *Account) { a.Fields["root"] = "relative" }, func(a *Account) { a.Service = "icloud_drive" },
	} {
		a := cloudAccount("google_drive")
		modify(&a)
		b, _ := json.Marshal([]Account{a})
		if _, err := New(string(b)); err != errConfig {
			t.Fatalf("want sanitized config error, got %v", err)
		}
	}
	a := cloudAccount("google_drive")
	b, _ := json.Marshal([]Account{a, a})
	if _, err := New(string(b)); err == nil {
		t.Fatal("duplicate accepted")
	}
	t.Setenv("NULLBOT_SERVICE_ACCOUNTS", "[]")
	if _, err := FromEnv(); err != nil {
		t.Fatal(err)
	}
}
func TestToolValidationAndDiscovery(t *testing.T) {
	d := configured(t, cloudAccount("google_drive"))
	if len(d.Tools()) != 5 {
		t.Fatal("wrong tool count")
	}
	for _, tool := range d.Tools() {
		if tool.InputSchema["additionalProperties"] != false {
			t.Fatal("loose schema")
		}
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"drives_accounts", map[string]any{"extra": true}},
		{"drives_list", map[string]any{}},
		{"drives_list", map[string]any{"account_id": "unknown"}},
		{"drives_list", map[string]any{"account_id": 7}},
		{"drives_read", map[string]any{"account_id": "test-account"}},
		{"drives_search", map[string]any{"account_id": "test-account", "query": ""}},
		{"drives_search", map[string]any{"account_id": "test-account", "query": "a/b"}},
		{"drives_search", map[string]any{"account_id": "test-account", "query": "x", "item_id": "folder"}},
	} {
		if !invoke(t, d, tc.name, tc.args).IsError {
			t.Fatalf("accepted %+v", tc)
		}
	}
	for _, v := range []any{float64(0), float64(101), 1.5, math.NaN(), math.Inf(1), "2", nil} {
		if !invoke(t, d, "drives_list", map[string]any{"account_id": "test-account", "limit": v}).IsError {
			t.Fatalf("accepted limit %v", v)
		}
	}
	r := invoke(t, d, "drives_accounts", nil)
	if r.IsError || strings.Contains(r.Content[0].Text, "synthetic-test-token") || !strings.Contains(r.Content[0].Text, `"read_only":true`) {
		t.Fatalf("unsafe metadata: %+v", r)
	}
}
func TestLocalIDPolicy(t *testing.T) {
	for _, id := range []string{"../secret", "a/../b", "/absolute", `C:\secret`, `a\b`, "a//b", "a/./b", "a/", "a.", "a ", "file:stream", "NUL.txt", "CON", "COM1", "LPT²", "a\x00b", "a?b"} {
		if _, err := localID(id); err == nil {
			t.Errorf("accepted %q", id)
		}
	}
	for _, id := range []string{"", "root", ".", "folder/file.txt", "résumé.txt"} {
		if _, err := localID(id); err != nil {
			t.Errorf("rejected %q", id)
		}
	}
}
func localFixture(t *testing.T) (*Drives, string) {
	t.Helper()
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("local roots unsupported on this platform")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"hello.txt": []byte("hello € world"), "binary.bin": {0xff, 0xfe, 0, 1}, "nested/match.txt": []byte("match")} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return configured(t, Account{ID: "local", Service: "icloud_drive", Label: "Local", Fields: map[string]string{"root": root}, AllowWrite: true}), root
}
func TestLocalTools(t *testing.T) {
	d, root := localFixture(t)
	r := invoke(t, d, "drives_accounts", nil)
	if strings.Contains(r.Content[0].Text, root) || strings.Contains(r.Content[0].Text, "root") {
		t.Fatal("root leaked")
	}
	v := decoded(t, invoke(t, d, "drives_list", map[string]any{"account_id": "local"}))
	if len(v["items"].([]any)) != 3 {
		t.Fatalf("listing: %+v", v)
	}
	v = decoded(t, invoke(t, d, "drives_search", map[string]any{"account_id": "local", "query": "MATCH"}))
	if len(v["items"].([]any)) != 1 {
		t.Fatalf("search: %+v", v)
	}
	v = decoded(t, invoke(t, d, "drives_list", map[string]any{"account_id": "local", "limit": float64(1)}))
	if len(v["items"].([]any)) != 1 || v["has_more"] != true {
		t.Fatalf("cap: %+v", v)
	}
	v = decoded(t, invoke(t, d, "drives_read", map[string]any{"account_id": "local", "item_id": "hello.txt", "max_bytes": float64(8)}))
	if v["data"] != "hello " || v["truncated"] != true || v["bytes"] != float64(6) {
		t.Fatalf("UTF-8 cap: %+v", v)
	}
	for _, id := range []string{"binary.bin", "nested", "../outside"} {
		if !invoke(t, d, "drives_read", map[string]any{"account_id": "local", "item_id": id, "max_bytes": float64(2)}).IsError {
			t.Fatalf("read accepted %s", id)
		}
	}
	v = decoded(t, invoke(t, d, "drives_download", map[string]any{"account_id": "local", "item_id": "binary.bin", "max_bytes": float64(2)}))
	b, err := base64.StdEncoding.DecodeString(v["data"].(string))
	if err != nil || string(b) != string([]byte{0xff, 0xfe}) || v["truncated"] != true {
		t.Fatalf("download: %+v", v)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := localList(ctx, root, "root", "", 50); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
func TestLocalSymlinkRejected(t *testing.T) {
	_, root := localFixture(t)
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
	if _, err := openLocal(root, "link"); err == nil {
		t.Fatal("followed link")
	}
	if _, err := openLocal(link, "."); err == nil {
		t.Fatal("followed linked root")
	}
	out, err := localList(context.Background(), root, "root", "", 50)
	if err != nil || out.Skipped != 1 {
		t.Fatalf("link listing: %+v %v", out, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestCloudRequests(t *testing.T) {
	for _, service := range []string{"google_drive", "onedrive"} {
		t.Run(service, func(t *testing.T) {
			a := cloudAccount(service)
			d := configured(t, a)
			calls := 0
			d.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				host := "www.googleapis.com"
				if service == "onedrive" {
					host = "graph.microsoft.com"
				}
				if r.URL.Scheme != "https" || r.URL.Host != host || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+a.Fields["access_token"] {
					t.Fatalf("unsafe request %v", r.URL)
				}
				if service == "google_drive" {
					if r.URL.Path != "/drive/v3/files" || r.URL.Query().Get("pageSize") != "1" {
						t.Fatalf("bad URL %s", r.URL)
					}
					return response(200, `{"nextPageToken":"more","files":[{"id":"abc","name":"Doc","mimeType":"text/plain","size":"42"}]}`), nil
				}
				if r.URL.Path != "/v1.0/me/drive/root/children" || r.URL.Query().Get("$top") != "1" {
					t.Fatalf("bad URL %s", r.URL)
				}
				return response(200, `{"@odata.nextLink":"https://untrusted.invalid/ignored","value":[{"id":"ABC!123","name":"Folder","folder":{}}]}`), nil
			})
			out, err := d.cloudList(context.Background(), a, "root", "", 1)
			if err != nil || len(out.Items) != 1 || !out.HasMore || calls != 1 {
				t.Fatalf("listing: %+v %v calls=%d", out, err, calls)
			}
			d.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if service == "google_drive" {
					if !strings.Contains(r.URL.Query().Get("q"), `name contains 'O\'Brien'`) {
						t.Fatalf("escaping: %s", r.URL)
					}
				} else if r.URL.Path != "/v1.0/me/drive/root/search(q='O''Brien')" {
					t.Fatalf("escaping: %s", r.URL)
				}
				if service == "google_drive" {
					return response(200, `{"files":[]}`), nil
				}
				return response(200, `{"value":[]}`), nil
			})
			if _, err := d.cloudList(context.Background(), a, "root", "O'Brien", 10); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestCloudFailuresAndCaps(t *testing.T) {
	a := cloudAccount("onedrive")
	d := configured(t, a)
	for _, tc := range []struct {
		code int
		body string
	}{{401, "sensitive provider error"}, {302, "redirect"}, {200, "{"}, {200, strings.Repeat("x", maxJSON+1)}, {200, `{"value":[{"id":"../bad"}]}`}} {
		d.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(tc.code, tc.body), nil })
		if _, err := d.cloudList(context.Background(), a, "root", "", 10); err == nil || strings.Contains(err.Error(), "sensitive") {
			t.Fatalf("unsafe response error: %v", err)
		}
	}
	d.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1.0/me/drive/items/ABC!123/content" {
			t.Fatalf("content path %s", r.URL)
		}
		return response(200, "abcdef"), nil
	})
	b, trunc, err := d.cloudContent(context.Background(), a, "ABC!123", 3)
	if err != nil || string(b) != "abc" || !trunc {
		t.Fatalf("cap: %q %v %v", b, trunc, err)
	}
	d.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("invalid ID reached transport"); return nil, nil })
	for _, id := range []string{"../x", "root", "a/b", "https://evil.invalid"} {
		if _, _, err := d.cloudContent(context.Background(), a, id, 10); err == nil {
			t.Fatal("unsafe ID accepted")
		}
	}
}
func TestCredentialOutputBlocked(t *testing.T) {
	a := cloudAccount("google_drive")
	d := configured(t, a)
	if !d.result(map[string]any{"name": a.Fields["access_token"]}).IsError {
		t.Fatal("credential echoed")
	}
	// Previously substring replacement could make numeric JSON invalid.
	d.accounts[0].Fields["access_token"] = "1"
	if !d.result(map[string]any{"size": 123}).IsError {
		t.Fatal("short secret not blocked")
	}
	d.accounts[0].Fields["access_token"] = "synthetic-test-token-not-real"
	d.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, "synthetic-test-token-not-real"), nil
	})
	for _, name := range []string{"drives_read", "drives_download"} {
		if !invoke(t, d, name, map[string]any{"account_id": "test-account", "item_id": "file"}).IsError {
			t.Fatal("content credential escaped")
		}
	}
}
