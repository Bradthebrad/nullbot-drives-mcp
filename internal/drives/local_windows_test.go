//go:build windows

package drives

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Junction creation needs no symlink privilege, so exercise the Windows reparse
// guard even when TestLocalSymlinkRejected must skip.
func TestWindowsJunctionRejected(t *testing.T) {
	_, root := localFixture(t)
	outside := t.TempDir()
	link := filepath.Join(root, "junction")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	script := "$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path " + quote(link) + " -Target " + quote(outside) + " | Out-Null"
	if output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput(); err != nil {
		t.Fatalf("create test junction: %v: %s", err, output)
	}
	defer os.Remove(link)
	if _, err := openLocal(root, "junction"); err == nil {
		t.Fatal("followed junction")
	}
	if _, err := openLocal(link, "."); err == nil {
		t.Fatal("followed junction root")
	}
	if err := os.Mkdir(filepath.Join(outside, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := openLocal(filepath.Join(link, "child"), "."); err == nil {
		t.Fatal("followed junction ancestor")
	}
	out, err := localList(context.Background(), root, "root", "", 50)
	if err != nil || out.Skipped != 1 {
		t.Fatalf("junction listing: %+v %v", out, err)
	}
}
