package drives

import (
	"context"
	"io"
	"strings"
)

func localList(ctx context.Context, root, id, query string, limit int) (Listing, error) {
	out := Listing{Items: []Item{}, Scope: "immediate children"}
	if _, err := localID(id); err != nil {
		return out, err
	}
	if id == "root" || id == "." {
		id = ""
	}
	queue := []string{id}
	scanned := 0
	if query != "" {
		out.Scope = "recursive name search; at most 2000 entries, no link traversal"
	}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		f, err := openLocal(root, dir)
		if err != nil {
			if dir == id {
				return out, err
			}
			out.Skipped++
			continue
		}
		for {
			if ctx.Err() != nil {
				f.Close()
				return out, ctx.Err()
			}
			entries, e := f.ReadDir(32)
			if e != nil && e != io.EOF {
				f.Close()
				return out, errLocal
			}
			for _, v := range entries {
				scanned++
				if scanned > maxScan {
					f.Close()
					out.HasMore = true
					return out, nil
				}
				child := v.Name()
				if dir != "" {
					child = dir + "/" + child
				}
				// Open through the same no-follow path policy, not DirEntry.Info alone.
				cf, ce := openLocal(root, child)
				if ce != nil {
					out.Skipped++
					continue
				}
				st, ce := cf.Stat()
				cf.Close()
				if ce != nil {
					out.Skipped++
					continue
				}
				if query != "" && st.IsDir() {
					queue = append(queue, child)
				}
				if query == "" || strings.Contains(strings.ToLower(v.Name()), strings.ToLower(query)) {
					out.Items = append(out.Items, Item{ID: child, Name: v.Name(), Folder: st.IsDir(), Size: st.Size()})
					if len(out.Items) >= limit {
						out.HasMore = true
						f.Close()
						return out, nil
					}
				}
			}
			if e == io.EOF {
				break
			}
		}
		f.Close()
	}
	return out, nil
}

func localContent(root, id string, limit int) ([]byte, bool, error) {
	f, err := openLocal(root, id)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, false, errLocal
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, false, errLocal
	}
	if len(b) > limit {
		return b[:limit], true, nil
	}
	return b, false, nil
}
