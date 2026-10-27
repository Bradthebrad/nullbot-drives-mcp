//go:build linux

package drives

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Walk from / using directory handles and O_NOFOLLOW (including root ancestors).
func openLocal(root, id string) (*os.File, error) {
	rel, err := localID(id)
	if err != nil {
		return nil, err
	}
	full := root
	if rel != "." {
		full = filepath.Join(root, rel)
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, errLocal
	}
	parts := strings.FieldsFunc(full, func(r rune) bool { return r == '/' })
	for i, p := range parts {
		flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= syscall.O_DIRECTORY
		}
		next, e := syscall.Openat(fd, p, flags, 0)
		syscall.Close(fd)
		if e != nil {
			return nil, errLocal
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), "local item")
	st, err := f.Stat()
	if err != nil || (!st.IsDir() && !st.Mode().IsRegular()) {
		f.Close()
		return nil, errLocal
	}
	return f, nil
}
