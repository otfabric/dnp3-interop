// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"testing"

	dnp3 "github.com/cB-Sigma/dnp3"
)

// TestEntryKeepsTheName: an entry carries the name of the descriptor as the
// outstation sent it, a full path (the answer to GET_FILE_INFO, IEEE
// 1815-2012 4.4.18) as well as a last path element (a directory listing).
func TestEntryKeepsTheName(t *testing.T) {
	for _, name := range []string{"/readme.txt", "readme.txt", "/data/blocks.bin", "/"} {
		b, err := json.Marshal(entry(dnp3.FileDescriptor{Name: name, Type: 1, Size: 43, Permissions: 0o644}))
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Permissions string `json:"permissions"`
		}
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if got.Name != name || got.Type != "file" || got.Permissions != "rw-r--r--" {
			t.Errorf("entry of %q: %s", name, b)
		}
	}
}
