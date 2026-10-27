package drives

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxContent = 256 * 1024
const maxJSON = 1024 * 1024
const maxItems = 100
const maxScan = 2000

var safeID = regexp.MustCompile(`^[A-Za-z0-9_!-]{1,256}$`)
var accountIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var deviceName = regexp.MustCompile(`^(COM|LPT)[0-9¹²³]$`)
var errConfig = errors.New("invalid service accounts configuration (check supported account IDs, credentials, and absolute local roots)")
var errLocal = errors.New("local item unavailable or unsafe; symlinks, reparse points, and special files are not supported")
var errUpstream = errors.New("provider request failed; check token, permissions, and item availability")

type Account struct {
	ID         string            `json:"id"`
	Service    string            `json:"service"`
	Label      string            `json:"label"`
	Fields     map[string]string `json:"fields"`
	AllowWrite bool              `json:"allow_write"`
}

type Drives struct {
	accounts []Account
	client   *http.Client
}

func FromEnv() (*Drives, error) { return New(os.Getenv("NULLBOT_SERVICE_ACCOUNTS")) }

// New does not connect to providers. Unrelated service accounts are ignored.
func New(raw string) (*Drives, error) {
	var all []Account
	if len(raw) > 1024*1024 {
		return nil, errConfig
	}
	if strings.TrimSpace(raw) != "" {
		if !strings.HasPrefix(strings.TrimSpace(raw), "[") || json.Unmarshal([]byte(raw), &all) != nil {
			return nil, errConfig
		}
	}
	if len(all) > 100 {
		return nil, errConfig
	}
	d := &Drives{client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	// No environment proxy: credentials only go to the fixed HTTPS provider origin.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	d.client.Transport = tr
	seen := map[string]bool{}
	for _, a := range all {
		switch a.Service {
		case "google_drive", "onedrive", "icloud_drive":
		default:
			continue
		}
		if !accountIDPattern.MatchString(a.ID) || seen[a.ID] || len(a.Label) > 120 || strings.TrimSpace(a.Label) == "" || strings.ContainsAny(a.Label, "\r\n\x00") {
			return nil, errConfig
		}
		seen[a.ID] = true
		root, token := a.Fields["root"], a.Fields["access_token"]
		if (root == "") == (token == "") || len(token) > 16384 || (token != "" && strings.TrimSpace(token) == "") {
			return nil, errConfig
		}
		for _, c := range token {
			if c < 32 || c == 127 {
				return nil, errConfig
			}
		}
		if root != "" {
			if !filepath.IsAbs(root) || strings.HasPrefix(root, `\\`) {
				return nil, errConfig
			}
			root = filepath.Clean(root)
			a.Fields["root"] = root
			f, err := openLocal(root, ".")
			if err != nil {
				return nil, errConfig
			}
			info, err := f.Stat()
			f.Close()
			if err != nil || !info.IsDir() {
				return nil, errConfig
			}
		} else if a.Service == "icloud_drive" {
			return nil, errConfig
		}
		d.accounts = append(d.accounts, a)
	}
	return d, nil
}

func (d *Drives) account(id string) (Account, error) {
	for _, a := range d.accounts {
		if a.ID == id {
			return a, nil
		}
	}
	return Account{}, errors.New("unknown drive account_id")
}

func localID(id string) (string, error) {
	if id == "" || id == "root" || id == "." {
		return ".", nil
	}
	if len(id) > 2048 || strings.ContainsAny(id, `\:`) || strings.HasPrefix(id, "/") {
		return "", errLocal
	}
	for _, p := range strings.Split(id, "/") {
		if p == "" || p == "." || p == ".." || strings.TrimRight(p, " .") != p {
			return "", errLocal
		}
		for _, c := range p {
			if c < 32 || strings.ContainsRune(`<>"|?*`, c) {
				return "", errLocal
			}
		}
		base := strings.ToUpper(strings.SplitN(p, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || deviceName.MatchString(base) {
			return "", errLocal
		}
	}
	return filepath.FromSlash(id), nil
}
