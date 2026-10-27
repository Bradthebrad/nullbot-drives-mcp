//go:build !windows && !linux

package drives

import "os"

// Fail closed until a handle-relative, no-follow implementation is available.
func openLocal(root, id string) (*os.File, error) { return nil, errLocal }
