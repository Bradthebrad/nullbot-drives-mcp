package drives

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Item struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Folder bool   `json:"folder"`
	Size   int64  `json:"size,omitempty"`
	MIME   string `json:"mime_type,omitempty"`
}
type Listing struct {
	Items   []Item `json:"items"`
	HasMore bool   `json:"has_more"`
	Scope   string `json:"scope"`
	Skipped int    `json:"skipped,omitempty"`
}

func (d *Drives) request(ctx context.Context, a Account, path string, q url.Values, cap int, partial bool) ([]byte, bool, error) {
	host := "www.googleapis.com"
	if a.Service == "onedrive" {
		host = "graph.microsoft.com"
	}
	u := url.URL{Scheme: "https", Host: host, Path: path, RawQuery: q.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, false, errUpstream
	}
	req.Header.Set("Authorization", "Bearer "+a.Fields["access_token"])
	req.Header.Set("Accept", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, false, errUpstream
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, false, errors.New("provider download redirect blocked; use a locally synced root for this content")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, errUpstream
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(cap)+1))
	if err != nil {
		return nil, false, errUpstream
	}
	if len(b) > cap {
		if !partial {
			return nil, false, errors.New("provider response exceeds safety limit")
		}
		return b[:cap], true, nil
	}
	return b, false, nil
}

func validCloudID(id string) bool { return safeID.MatchString(id) }
func (d *Drives) cloudList(ctx context.Context, a Account, id, query string, limit int) (Listing, error) {
	out := Listing{Items: []Item{}, Scope: "first provider page; no automatic pagination"}
	if !validCloudID(id) {
		return out, errors.New("invalid cloud item ID")
	}
	q := url.Values{}
	var path string
	if a.Service == "google_drive" {
		filter := "trashed = false"
		if query == "" {
			filter += " and '" + id + "' in parents"
		} else {
			escaped := strings.ReplaceAll(strings.ReplaceAll(query, `\`, `\\`), "'", `\'`)
			filter += " and name contains '" + escaped + "'"
			out.Scope = "name search across account; first provider page"
		}
		path = "/drive/v3/files"
		q.Set("q", filter)
		q.Set("pageSize", strconv.Itoa(limit))
		q.Set("fields", "nextPageToken,files(id,name,mimeType,size)")
	} else {
		path = "/v1.0/me/drive/root/children"
		if id != "root" {
			path = "/v1.0/me/drive/items/" + id + "/children"
		}
		if query != "" {
			// Search is scoped to root and uses an escaped OData string literal.
			path = "/v1.0/me/drive/root/search(q='" + strings.ReplaceAll(query, "'", "''") + "')"
			out.Scope = "provider search across account; first provider page"
		}
		q.Set("$top", strconv.Itoa(limit))
		q.Set("$select", "id,name,size,folder,file")
	}
	b, _, err := d.request(ctx, a, path, q, maxJSON, false)
	if err != nil {
		return out, err
	}
	if a.Service == "google_drive" {
		var r struct {
			Next  string `json:"nextPageToken"`
			Files []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				MIME string `json:"mimeType"`
				Size string `json:"size"`
			} `json:"files"`
		}
		if json.Unmarshal(b, &r) != nil {
			return out, errUpstream
		}
		out.HasMore = r.Next != ""
		for _, v := range r.Files {
			s, _ := strconv.ParseInt(v.Size, 10, 64)
			out.Items = append(out.Items, Item{ID: v.ID, Name: v.Name, MIME: v.MIME, Size: s, Folder: v.MIME == "application/vnd.google-apps.folder"})
		}
	} else {
		var r struct {
			Next  string `json:"@odata.nextLink"`
			Value []struct {
				ID     string           `json:"id"`
				Name   string           `json:"name"`
				Size   int64            `json:"size"`
				Folder *json.RawMessage `json:"folder"`
				File   *struct {
					MIME string `json:"mimeType"`
				} `json:"file"`
			} `json:"value"`
		}
		if json.Unmarshal(b, &r) != nil {
			return out, errUpstream
		}
		out.HasMore = r.Next != ""
		for _, v := range r.Value {
			mime := ""
			if v.File != nil {
				mime = v.File.MIME
			}
			out.Items = append(out.Items, Item{ID: v.ID, Name: v.Name, Size: v.Size, Folder: v.Folder != nil, MIME: mime})
		}
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.HasMore = true
	}
	for i := range out.Items {
		v := &out.Items[i]
		if !validCloudID(v.ID) || len(v.Name) > 1024 || len(v.MIME) > 256 {
			return Listing{}, errUpstream
		}
	}
	return out, nil
}

func (d *Drives) cloudContent(ctx context.Context, a Account, id string, limit int) ([]byte, bool, error) {
	if !validCloudID(id) || id == "root" {
		return nil, false, errors.New("invalid file item ID")
	}
	path := "/drive/v3/files/" + id
	q := url.Values{"alt": []string{"media"}}
	if a.Service == "onedrive" {
		path = "/v1.0/me/drive/items/" + id + "/content"
		q = nil
	}
	return d.request(ctx, a, path, q, limit, true)
}
