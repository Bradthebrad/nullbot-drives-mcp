//go:build windows

package drives

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Lock every ancestor against rename/delete while opening the next component.
// OPEN_REPARSE_POINT prevents following junctions/cloud placeholders/symlinks.
func openLocal(root, id string) (*os.File, error) {
	rel, err := localID(id)
	if err != nil {
		return nil, err
	}
	full := root
	if rel != "." {
		full = filepath.Join(root, rel)
	}
	vol := filepath.VolumeName(full)
	if len(vol) != 2 || vol[1] != ':' {
		return nil, errLocal
	}
	parts := strings.Split(strings.TrimPrefix(full[len(vol):], `\`), `\`)
	paths := []string{vol + `\`}
	current := vol + `\`
	for _, p := range parts {
		if p != "" {
			current = filepath.Join(current, p)
			paths = append(paths, current)
		}
	}
	var held []*os.File
	defer func() {
		for _, f := range held {
			f.Close()
		}
	}()
	for i, p := range paths {
		ptr, e := syscall.UTF16PtrFromString(p)
		if e != nil {
			return nil, errLocal
		}
		h, e := syscall.CreateFile(ptr, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e != nil {
			return nil, errLocal
		}
		f := os.NewFile(uintptr(h), p)
		var info syscall.ByHandleFileInformation
		e = syscall.GetFileInformationByHandle(h, &info)
		if e != nil || info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (i < len(paths)-1 && info.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0) {
			f.Close()
			return nil, errLocal
		}
		st, e := f.Stat()
		if e != nil || (!st.IsDir() && !st.Mode().IsRegular()) {
			f.Close()
			return nil, errLocal
		}
		if i == len(paths)-1 {
			return f, nil
		}
		held = append(held, f)
	}
	return nil, errLocal
}
