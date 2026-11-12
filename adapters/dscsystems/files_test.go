// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileTree: the fixture's names, which start with "/", land inside the
// served tree with the modes the master sees (rw-r--r--, rwxr-xr-x).
func TestFileTree(t *testing.T) {
	fx := &fixture{
		Files:       map[string][]byte{"/readme.txt": []byte("hi"), "/logs/a.txt": []byte("a")},
		Directories: []string{"/logs", "/empty"},
	}
	dir, err := fileTree(fx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	for name, want := range map[string]os.FileMode{"readme.txt": servedFileMode, "logs/a.txt": servedFileMode,
		"logs": servedDirMode | os.ModeDir, "empty": servedDirMode | os.ModeDir} {
		st, err := root.Stat(filepath.FromSlash(name))
		if err != nil {
			t.Fatal(err)
		}
		// The process umask may only take bits away.
		if got := st.Mode(); got&^want != 0 || got.IsDir() != want.IsDir() {
			t.Errorf("%s: mode %v, want %v", name, got, want)
		}
	}
	if b, err := root.ReadFile("readme.txt"); err != nil || string(b) != "hi" {
		t.Errorf("readme.txt: %q, %v", b, err)
	}
}

// TestFileTreeStaysInside: a name that would leave the tree is an error.
func TestFileTreeStaysInside(t *testing.T) {
	dir, err := fileTree(&fixture{Files: map[string][]byte{"/../escape.txt": []byte("x")}})
	if err == nil {
		_ = os.RemoveAll(dir)
		t.Fatal("a name outside the tree was written")
	}
	if dir != "" {
		t.Errorf("fileTree returned %q with the error %v", dir, err)
	}
}
