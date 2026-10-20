// SPDX-License-Identifier: GPL-3.0-or-later

// File transfer, group 70 (contract 1.1).

package main

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dscsystems/go-dnp3"
	"github.com/dscsystems/go-dnp3/master"
	"github.com/dscsystems/go-dnp3/outstation"
)

var fileStatusNames = map[dnp3.FileStatus]string{
	dnp3.FileSuccess: "SUCCESS", dnp3.FilePermissionDenied: "PERMISSION_DENIED", dnp3.FileInvalidMode: "INVALID_MODE",
	dnp3.FileNotFound: "FILE_NOT_FOUND", dnp3.FileLocked: "FILE_LOCKED", dnp3.FileTooManyOpen: "TOO_MANY_OPEN",
	dnp3.FileInvalidHandle: "INVALID_HANDLE", dnp3.FileWriteBlockSize: "WRITE_BLOCK_SIZE", dnp3.FileCommLost: "COMM_LOST",
	dnp3.FileCannotAbort: "CANNOT_ABORT", dnp3.FileNotOpen: "NOT_OPENED", dnp3.FileHandleExpired: "HANDLE_EXPIRED",
	dnp3.FileBufferOverrun: "BUFFER_OVERRUN", dnp3.FileFatal: "FATAL", dnp3.FileBlockSequence: "BLOCK_SEQ",
}

func fileStatusName(s dnp3.FileStatus) string {
	if n, ok := fileStatusNames[s]; ok {
		return n
	}
	return "UNDEFINED"
}

// ---- outstation ----

// fileTree writes the files of the fixture into a fresh directory, which the
// library's directory handler then serves.
func fileTree(fx *fixture) (string, error) {
	root, err := os.MkdirTemp("", "dnp3-interop-files-")
	if err != nil {
		return "", err
	}
	for _, d := range fx.Directories {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			return "", err
		}
	}
	for name, content := range fx.Files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), content, 0o644); err != nil {
			return "", err
		}
	}
	return root, nil
}

func fileEvent(action, name string, status dnp3.FileStatus) {
	emit(object{{"event", "file"}, {"action", action}, {"name", name}, {"status", fileStatusName(status)}})
}

// fileEvents serves the directory and reports every answered file request.
// The library asks the handler for the descriptor both for GET_FILE_INFO and
// before opening a file for reading; the function code of the request tells
// which.
type fileEvents struct{ h *outstation.DirFileHandler }

func (f fileEvents) Info(name string) (dnp3.FileInfo, dnp3.FileStatus) {
	info, status := f.h.Info(name)
	switch {
	case requestFunction.Load() == fcGetFileInfo:
		fileEvent("info", name, status)
	case status != dnp3.FileSuccess:
		fileEvent("read", name, status) // the open for reading ends here
	}
	return info, status
}

func (f fileEvents) List(name string) ([]dnp3.FileInfo, dnp3.FileStatus) {
	entries, status := f.h.List(name)
	fileEvent("list", name, status)
	return entries, status
}

func (f fileEvents) OpenRead(name string) (io.ReadCloser, dnp3.FileInfo, dnp3.FileStatus) {
	r, info, status := f.h.OpenRead(name)
	fileEvent("read", name, status)
	return r, info, status
}

func (f fileEvents) OpenWrite(name string, mode dnp3.FileMode, size uint32) (io.WriteCloser, dnp3.FileStatus) {
	w, status := f.h.OpenWrite(name, mode, size)
	action := "write"
	if mode == dnp3.FileModeAppend {
		action = "append"
	}
	if status != dnp3.FileSuccess {
		fileEvent(action, name, status)
		return w, status
	}
	return &writeEvent{WriteCloser: w, name: name, action: action}, status
}

func (f fileEvents) Delete(name string) dnp3.FileStatus {
	status := f.h.Delete(name)
	fileEvent("delete", name, status)
	return status
}

// writeEvent reports a write when the file is closed: when it is complete.
type writeEvent struct {
	io.WriteCloser
	name   string
	action string
	once   sync.Once
}

func (w *writeEvent) Close() error {
	err := w.WriteCloser.Close()
	w.once.Do(func() {
		status := dnp3.FileSuccess
		if err != nil {
			status = dnp3.FileFatal
		}
		fileEvent(w.action, w.name, status)
	})
	return err
}

// ---- master ----

var fileOperations = []string{"file-get", "file-put", "file-ls", "file-info", "file-delete", "file-authenticate"}

// parseFileCredentials reads --file-user and --file-password (contract 1.3):
// both or neither, and both for file-authenticate. The library then sends
// AUTHENTICATE_FILE before every open and delete.
func parseFileCredentials(op string, a args) (*master.FileCredentials, error) {
	user, hasUser := a["file-user"]
	password, hasPassword := a["file-password"]
	switch {
	case op == "file-info" && (hasUser || hasPassword):
		return nil, usagef("file-info takes no --file-user or --file-password: GET_FILE_INFO carries no key")
	case op == "file-authenticate" && (!hasUser || !hasPassword):
		return nil, usagef("file-authenticate needs --file-user and --file-password")
	case hasUser != hasPassword:
		return nil, usagef("--file-user and --file-password go together")
	case !hasUser:
		return nil, nil
	}
	return &master.FileCredentials{User: user, Password: password}, nil
}

// fileAuthenticator checks the credentials of AUTHENTICATE_FILE against the
// server's --file-user and --file-password and reports the outcome; never the
// password.
func fileAuthenticator(user, password string) outstation.FileAuthenticator {
	return func(u, p string) bool {
		ok := subtle.ConstantTimeCompare([]byte(u), []byte(user)) == 1 &&
			subtle.ConstantTimeCompare([]byte(p), []byte(password)) == 1
		status := dnp3.FileSuccess
		if !ok {
			status = dnp3.FilePermissionDenied
		}
		fileEvent("authenticate", u, status)
		return ok
	}
}

// fileResult is what a file operation adds to the result document.
type fileResult struct {
	status *string
	file   object
	files  []object
}

func entry(info dnp3.FileInfo) object {
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	return object{{"name", info.Name}, {"type", kind}, {"size", info.Size},
		{"permissions", permissionString(uint16(info.Permissions))}, {"time", formatTime(info.Created)}}
}

func permissionString(p uint16) string {
	var b strings.Builder
	for i := 8; i >= 0; i-- {
		if p&(1<<i) == 0 {
			b.WriteByte('-')
		} else {
			b.WriteByte("xwr"[i%3])
		}
	}
	return b.String()
}

// fileStatusOf finds the file status in an error of the library, which
// formats it as "<ErrFileTransfer>: <status>".
func fileStatusOf(err error) (dnp3.FileStatus, bool) {
	if !errors.Is(err, dnp3.ErrFileTransfer) {
		return 0, false
	}
	for s := range fileStatusNames {
		if strings.HasSuffix(err.Error(), ": "+s.String()) {
			return s, true
		}
	}
	return dnp3.FileUndefined, true
}

type fileRequest func(ctx context.Context, s *master.Session, r *fileResult) error

func parseFileOperation(op string, a args) (fileRequest, error) {
	if op == "file-authenticate" {
		return func(ctx context.Context, s *master.Session, _ *fileResult) error {
			_, err := s.AuthenticateFile(ctx, a["file-user"], a["file-password"])
			return err
		}, nil
	}
	name, err := a.required("name")
	if err != nil {
		return nil, err
	}
	if name != "/" && (!fileName.MatchString(name) || len(name) > 200) {
		return nil, usagef("--name must be an absolute file name of at most 200 characters, path elements of A-Za-z0-9._- not starting with '.'")
	}
	switch op {
	case "file-get":
		return func(ctx context.Context, s *master.Session, r *fileResult) error {
			data, err := s.ReadFileBytes(ctx, name)
			if err == nil {
				r.file = object{{"name", name}, {"size", len(data)}, {"content", hex.EncodeToString(data)}}
			}
			return err
		}, nil
	case "file-put":
		text, err := a.required("content")
		if err != nil {
			return nil, err
		}
		data, err := hex.DecodeString(text)
		if err != nil {
			return nil, usagef("--content must be hex")
		}
		return func(ctx context.Context, s *master.Session, _ *fileResult) error {
			return s.WriteFileBytes(ctx, name, data)
		}, nil
	case "file-ls":
		return func(ctx context.Context, s *master.Session, r *fileResult) error {
			entries, err := s.ReadDirectory(ctx, name)
			if err == nil {
				r.files = []object{}
				for _, e := range entries {
					r.files = append(r.files, entry(e))
				}
			}
			return err
		}, nil
	case "file-info":
		return func(ctx context.Context, s *master.Session, r *fileResult) error {
			info, err := s.FileInfo(ctx, name)
			if err == nil {
				r.file = entry(info)
			}
			return err
		}, nil
	case "file-delete":
		return func(ctx context.Context, s *master.Session, _ *fileResult) error {
			return s.DeleteFile(ctx, name)
		}, nil
	}
	return nil, usagef("unknown operation '%s'", op)
}
