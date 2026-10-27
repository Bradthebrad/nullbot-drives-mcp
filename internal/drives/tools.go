package drives

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/Bradthebrad/tinychain/mcp"
)

func prop(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}
func (d *Drives) Tools() []mcp.Tool {
	account := prop("string", "Configured drive account ID (not a credential).")
	id := prop("string", "Provider item ID or slash-separated path relative to synced root. Default root for list/search.")
	limit := map[string]any{"type": "integer", "minimum": 1, "maximum": maxItems, "description": "Maximum items; default 50. Results may be incomplete."}
	bytes := map[string]any{"type": "integer", "minimum": 1, "maximum": maxContent, "description": "Maximum raw content bytes; default 65536. Prefix only if truncated."}
	makeTool := func(name, desc string, properties map[string]any, required []string) mcp.Tool {
		return mcp.Tool{Name: name, Description: desc, InputSchema: map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}, Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			for k := range args {
				if _, ok := properties[k]; !ok {
					return failure(errors.New("unknown argument")), nil
				}
			}
			v, err := d.call(ctx, name, args)
			if err != nil {
				return failure(err), nil
			}
			return d.result(v), nil
		}}
	}
	return []mcp.Tool{
		makeTool("drives_accounts", "List configured drive accounts without credentials or local absolute roots. All tools are read-only.", map[string]any{}, []string{}),
		makeTool("drives_list", "List a bounded first page of folder children. No automatic provider pagination.", map[string]any{"account_id": account, "item_id": id, "limit": limit}, []string{"account_id"}),
		makeTool("drives_search", "Search names locally (bounded recursive walk), Google names, or OneDrive provider search. Cloud search is account-wide.", map[string]any{"account_id": account, "item_id": id, "query": prop("string", "Search text, 1-200 bytes; cloud queries cannot contain slash, backslash, or control characters."), "limit": limit}, []string{"account_id", "query"}),
		makeTool("drives_read", "Read a bounded UTF-8 text prefix; binary content is rejected. Provider redirects and Google-native document exports are not supported.", map[string]any{"account_id": account, "item_id": id, "max_bytes": bytes}, []string{"account_id", "item_id"}),
		makeTool("drives_download", "Return a bounded file prefix as base64 inline, never write a file. Check truncated before decoding; redirected OneDrive downloads are blocked.", map[string]any{"account_id": account, "item_id": id, "max_bytes": bytes}, []string{"account_id", "item_id"}),
	}
}

func failure(err error) mcp.ToolResult { r := mcp.Text(err.Error()); r.IsError = true; return r }
func textArg(args map[string]any, key, def string, required bool) (string, error) {
	v, ok := args[key]
	if !ok {
		if required {
			return "", errors.New("missing required argument")
		}
		return def, nil
	}
	s, ok := v.(string)
	if !ok || len(s) > 2048 || (required && s == "") {
		return "", errors.New("invalid string argument")
	}
	return s, nil
}
func intArg(args map[string]any, key string, def, max int) (int, error) {
	v, ok := args[key]
	if !ok {
		return def, nil
	}
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 1 || n > float64(max) || n != math.Trunc(n) {
		return 0, errors.New("invalid numeric limit")
	}
	return int(n), nil
}

// Defense-in-depth: refuse output containing known credentials. Do not replace
// substrings in serialized JSON: short tokens can corrupt keys or numeric values.
func (d *Drives) result(v any) mcp.ToolResult {
	b, err := json.Marshal(v)
	if err != nil || len(b) > 2*1024*1024 {
		return failure(errors.New("result exceeds output safety limit"))
	}
	s := string(b)
	for _, a := range d.accounts {
		for _, key := range []string{"access_token", "refresh_token", "client_secret"} {
			secret := a.Fields[key]
			if secret == "" {
				continue
			}
			encoded, _ := json.Marshal(secret)
			needle := string(encoded[1 : len(encoded)-1])
			if strings.Contains(s, needle) {
				return failure(errors.New("result contains a configured credential and cannot be returned"))
			}
		}
	}
	return mcp.Text(s)
}

func (d *Drives) call(ctx context.Context, name string, args map[string]any) (any, error) {
	if name == "drives_accounts" {
		out := []map[string]any{}
		for _, a := range d.accounts {
			mode := "cloud_token"
			if a.Fields["root"] != "" {
				mode = "local_synced_folder"
			}
			out = append(out, map[string]any{"id": a.ID, "service": a.Service, "label": a.Label, "mode": mode, "read_only": true})
		}
		return out, nil
	}
	accountID, err := textArg(args, "account_id", "", true)
	if err != nil {
		return nil, err
	}
	a, err := d.account(accountID)
	if err != nil {
		return nil, err
	}
	content := name == "drives_read" || name == "drives_download"
	id, err := textArg(args, "item_id", "root", content)
	if err != nil {
		return nil, err
	}
	if !content {
		limit, err := intArg(args, "limit", 50, maxItems)
		if err != nil {
			return nil, err
		}
		query := ""
		if name == "drives_search" {
			query, err = textArg(args, "query", "", true)
			if err != nil {
				return nil, err
			}
			if len(query) > 200 {
				return nil, errors.New("query too long")
			}
			for _, r := range query {
				if r < 32 || r == 127 || (a.Fields["root"] == "" && (r == '/' || r == '\\')) {
					return nil, errors.New("unsafe search query")
				}
			}
		}
		if a.Fields["root"] != "" {
			return localList(ctx, a.Fields["root"], id, query, limit)
		}
		if name == "drives_search" && id != "root" {
			return nil, errors.New("cloud search requires item_id root (account-wide)")
		}
		return d.cloudList(ctx, a, id, query, limit)
	}
	limit, err := intArg(args, "max_bytes", 65536, maxContent)
	if err != nil {
		return nil, err
	}
	var b []byte
	var truncated bool
	if a.Fields["root"] != "" {
		b, truncated, err = localContent(a.Fields["root"], id, limit)
	} else {
		b, truncated, err = d.cloudContent(ctx, a, id, limit)
	}
	if err != nil {
		return nil, err
	}
	// Do not return configured credentials verbatim in content or encoded downloads.
	for _, acct := range d.accounts {
		for _, key := range []string{"access_token", "refresh_token", "client_secret"} {
			if secret := acct.Fields[key]; secret != "" && strings.Contains(string(b), secret) {
				return nil, errors.New("content contains a configured credential and cannot be returned")
			}
		}
	}
	encoding := "utf-8"
	data := ""
	if name == "drives_download" {
		encoding = "base64"
		data = base64.StdEncoding.EncodeToString(b)
	} else {
		// Only discard an incomplete terminal rune, never malformed bytes.
		if truncated {
			for i := 0; i < len(b); {
				if !utf8.FullRune(b[i:]) {
					b = b[:i]
					break
				}
				r, size := utf8.DecodeRune(b[i:])
				if r == utf8.RuneError && size == 1 {
					break
				}
				i += size
			}
		}
		if !utf8.Valid(b) {
			return nil, errors.New("content is not UTF-8 text; use drives_download for bounded binary data")
		}
		for _, r := range string(b) {
			if (r < 32 && r != '\n' && r != '\r' && r != '\t') || r == 127 {
				return nil, errors.New("content appears binary; use drives_download")
			}
		}
		data = string(b)
	}
	return map[string]any{"encoding": encoding, "data": data, "bytes": len(b), "truncated": truncated, "read_only": true}, nil
}
