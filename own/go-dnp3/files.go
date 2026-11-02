// SPDX-License-Identifier: MIT

// File transfer, group 70 (contract 1.1; append and AUTHENTICATE_FILE 1.3).

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	dnp3 "github.com/otfabric/go-dnp3"
	"github.com/otfabric/go-dnp3/app"
	"github.com/otfabric/go-dnp3/client"
	"github.com/otfabric/go-dnp3/server"
)

var fileStatusNames = map[app.FileStatus]string{
	app.FileSuccess: "SUCCESS", app.FilePermissionDenied: "PERMISSION_DENIED", app.FileInvalidMode: "INVALID_MODE",
	app.FileNotFound: "FILE_NOT_FOUND", app.FileLocked: "FILE_LOCKED", app.FileTooManyOpen: "TOO_MANY_OPEN",
	app.FileInvalidHandle: "INVALID_HANDLE", app.FileWriteBlockSize: "WRITE_BLOCK_SIZE", app.FileCommLost: "COMM_LOST",
	app.FileCannotAbort: "CANNOT_ABORT", app.FileNotOpened: "NOT_OPENED", app.FileHandleExpired: "HANDLE_EXPIRED",
	app.FileBufferOverrun: "BUFFER_OVERRUN", app.FileFatal: "FATAL", app.FileBlockSequence: "BLOCK_SEQ",
}

func fileStatusName(s app.FileStatus) string {
	if n, ok := fileStatusNames[s]; ok {
		return n
	}
	return "UNDEFINED"
}

func fileEvent(action, name string, status app.FileStatus) {
	emit(object{{"event", "file"}, {"action", action}, {"name", name}, {"status", fileStatusName(status)}})
}

// ---- outstation ----

const (
	filePermissions = app.Permissions(0o644)
	dirPermissions  = app.Permissions(0o755)
)

// fileServer is the files of the fixture, in memory: the process's own, as
// the points are. It is shared by the outstations of a server.
type fileServer struct {
	mu      sync.Mutex
	files   map[string][]byte
	dirs    map[string]bool
	created app.Timestamp

	auth           bool
	user, password string
	keys           map[uint32]bool
}

func newFileServer(fx *fixture) *fileServer {
	s := &fileServer{files: map[string][]byte{}, dirs: map[string]bool{"/": true}, created: app.TimestampOf(time.Now())}
	for name, content := range fx.Files {
		s.files[name] = slices.Clone(content)
	}
	for _, d := range fx.Directories {
		s.dirs[d] = true
	}
	return s
}

// requireAuth makes every open and delete need a key from
// AUTHENTICATE_FILE with these credentials.
func (s *fileServer) requireAuth(user, password string) {
	s.auth, s.user, s.password, s.keys = true, user, password, map[uint32]bool{}
}

// forServer is the server.FileServer: with a FileAuthenticator only when
// the server requires authentication.
func (s *fileServer) forServer() server.FileServer {
	if s.auth {
		return authFileServer{s}
	}
	return s
}

type authFileServer struct{ *fileServer }

// Authenticate (server.FileAuthenticator) checks the credentials and
// reports the outcome; never the password.
func (s authFileServer) Authenticate(_ context.Context, user, password string) (uint32, bool) {
	ok := subtle.ConstantTimeCompare([]byte(user), []byte(s.user)) == 1 &&
		subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) == 1
	status := app.FileSuccess
	var key uint32
	if ok {
		var b [4]byte
		for key == 0 {
			_, _ = rand.Read(b[:])
			key = binary.LittleEndian.Uint32(b[:])
		}
		s.mu.Lock()
		s.keys[key] = true
		s.mu.Unlock()
	} else {
		status = app.FilePermissionDenied
	}
	fileEvent("authenticate", user, status)
	return key, ok
}

// allowed checks the key of an open or delete. The caller holds mu.
func (s *fileServer) allowed(key uint32) bool { return !s.auth || (key != 0 && s.keys[key]) }

func parent(name string) string {
	d := path.Dir(name)
	if d == "." {
		return "/"
	}
	return d
}

func (s *fileServer) descriptor(name string) (app.FileDescriptor, bool) {
	base := path.Base(name)
	if s.dirs[name] {
		return app.FileDescriptor{Name: base, Type: app.FileTypeDirectory, Created: s.created, Permissions: dirPermissions}, true
	}
	if data, ok := s.files[name]; ok {
		return app.FileDescriptor{Name: base, Type: app.FileTypeSimple, Size: uint32(len(data)), Created: s.created,
			Permissions: filePermissions}, true
	}
	return app.FileDescriptor{}, false
}

// listing is the content of a directory opened for reading: the
// descriptors of its entries.
func (s *fileServer) listing(dir string) []byte {
	var names []string
	for n := range s.files {
		if parent(n) == dir {
			names = append(names, n)
		}
	}
	for n := range s.dirs {
		if n != "/" && parent(n) == dir {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	var b []byte
	for _, n := range names {
		d, _ := s.descriptor(n)
		if nb, err := app.AppendFileDescriptor(b, d); err == nil {
			b = nb
		}
	}
	return b
}

func (s *fileServer) Open(_ context.Context, req server.FileRequest) (server.File, app.FileStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := req.Name
	switch req.Mode {
	case app.FileModeRead:
		action := "read"
		if s.dirs[name] {
			action = "list"
		}
		status := app.FileSuccess
		var data []byte
		switch {
		case !s.allowed(req.AuthKey):
			status = app.FilePermissionDenied
		case s.dirs[name]:
			data = s.listing(name)
		case s.files[name] != nil:
			data = slices.Clone(s.files[name])
		default:
			status = app.FileNotFound
		}
		fileEvent(action, name, status)
		if status != app.FileSuccess {
			return nil, status
		}
		return &readFile{r: bytes.NewReader(data), size: uint32(len(data))}, status
	case app.FileModeWrite, app.FileModeAppend:
		action := "write"
		if req.Mode == app.FileModeAppend {
			action = "append"
		}
		status := app.FileSuccess
		switch {
		case !s.allowed(req.AuthKey):
			status = app.FilePermissionDenied
		case s.dirs[name] || name == "/":
			status = app.FilePermissionDenied
		case !s.dirs[parent(name)]:
			status = app.FileNotFound
		}
		if status != app.FileSuccess {
			fileEvent(action, name, status)
			return nil, status
		}
		return &writeFile{s: s, name: name, action: action, appendTo: req.Mode == app.FileModeAppend}, status
	}
	return nil, app.FileInvalidMode
}

func (s *fileServer) Delete(_ context.Context, name string, key uint32) app.FileStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := app.FileSuccess
	switch {
	case !s.allowed(key):
		status = app.FilePermissionDenied
	case s.files[name] != nil:
		delete(s.files, name)
	case s.dirs[name]:
		status = app.FilePermissionDenied
	default:
		status = app.FileNotFound
	}
	fileEvent("delete", name, status)
	return status
}

func (s *fileServer) Info(_ context.Context, name string) (app.FileDescriptor, app.FileStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.descriptor(name)
	status := app.FileSuccess
	if !ok {
		status = app.FileNotFound
	}
	fileEvent("info", name, status)
	return d, status
}

type readFile struct {
	r    *bytes.Reader
	size uint32
}

func (f *readFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *readFile) Write([]byte) (int, error)  { return 0, errors.New("opened for reading") }
func (f *readFile) Size() uint32               { return f.size }
func (f *readFile) Close(bool) error           { return nil }

// writeFile collects the blocks of a write and stores the file when it is
// closed: the write event reports it then.
type writeFile struct {
	s        *fileServer
	name     string
	action   string
	appendTo bool
	buf      bytes.Buffer
}

func (f *writeFile) Read([]byte) (int, error)    { return 0, errors.New("opened for writing") }
func (f *writeFile) Write(p []byte) (int, error) { return f.buf.Write(p) }
func (f *writeFile) Size() uint32                { return 0 }

func (f *writeFile) Close(abort bool) error {
	if abort {
		return nil
	}
	f.s.mu.Lock()
	data := f.buf.Bytes()
	if f.appendTo {
		data = append(slices.Clone(f.s.files[f.name]), data...)
	}
	f.s.files[f.name] = slices.Clone(data)
	if f.s.files[f.name] == nil {
		f.s.files[f.name] = []byte{}
	}
	f.s.mu.Unlock()
	fileEvent(f.action, f.name, app.FileSuccess)
	return nil
}

// ---- master ----

var fileOperations = []string{"file-get", "file-put", "file-ls", "file-info", "file-delete", "file-authenticate"}

type fileCredentials struct{ user, password string }

// parseFileCredentials reads --file-user and --file-password: both or
// neither, and both for file-authenticate.
func parseFileCredentials(op string, a args) (*fileCredentials, error) {
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
	return &fileCredentials{user: user, password: password}, nil
}

// fileResult is what a file operation adds to the result document.
type fileResult struct {
	status *string
	file   object
	files  []object
}

func entry(d app.FileDescriptor) object {
	kind := "file"
	if d.Type == app.FileTypeDirectory {
		kind = "directory"
	}
	return object{{"name", d.Name}, {"type", kind}, {"size", d.Size},
		{"permissions", permissionString(uint16(d.Permissions))}, {"time", formatTime(d.Created.Time())}}
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

// errPermissionDenied is a key 0 from AUTHENTICATE_FILE.
var errPermissionDenied = &dnp3.FileError{Op: "authenticate", Status: app.FilePermissionDenied}

type fileRequest func(ctx context.Context, c *client.Client, r *fileResult) error

// withKey runs AUTHENTICATE_FILE first when there are credentials and
// passes the key on.
func withKey(creds *fileCredentials, fn func(ctx context.Context, c *client.Client, o client.FileOptions, r *fileResult) error) fileRequest {
	return func(ctx context.Context, c *client.Client, r *fileResult) error {
		var o client.FileOptions
		if creds != nil {
			key, err := c.AuthenticateFile(ctx, creds.user, creds.password)
			if err != nil {
				return err
			}
			if key == 0 {
				return errPermissionDenied
			}
			o.AuthKey = key
		}
		return fn(ctx, c, o, r)
	}
}

func parseFileOperation(op string, a args, creds *fileCredentials) (fileRequest, error) {
	if op == "file-authenticate" {
		return withKey(creds, func(context.Context, *client.Client, client.FileOptions, *fileResult) error { return nil }), nil
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
		return withKey(creds, func(ctx context.Context, c *client.Client, o client.FileOptions, r *fileResult) error {
			var buf bytes.Buffer
			_, err := c.ReadFile(ctx, name, &buf, o)
			if err == nil {
				r.file = object{{"name", name}, {"size", buf.Len()}, {"content", hex.EncodeToString(buf.Bytes())}}
			}
			return err
		}), nil
	case "file-put":
		text, err := a.required("content")
		if err != nil {
			return nil, err
		}
		data, err := hex.DecodeString(text)
		if err != nil {
			return nil, usagef("--content must be hex")
		}
		appendTo := a.has("append")
		return withKey(creds, func(ctx context.Context, c *client.Client, o client.FileOptions, _ *fileResult) error {
			o.Append = appendTo
			_, err := c.WriteFile(ctx, name, bytes.NewReader(data), uint32(len(data)), o)
			return err
		}), nil
	case "file-ls":
		return withKey(creds, func(ctx context.Context, c *client.Client, o client.FileOptions, r *fileResult) error {
			entries, err := c.ListDirectory(ctx, name, o)
			if err == nil {
				r.files = []object{}
				for _, e := range entries {
					r.files = append(r.files, entry(e))
				}
			}
			return err
		}), nil
	case "file-info":
		return func(ctx context.Context, c *client.Client, r *fileResult) error {
			d, err := c.FileInfo(ctx, name)
			if err == nil {
				r.file = entry(d)
			}
			return err
		}, nil
	case "file-delete":
		return withKey(creds, func(ctx context.Context, c *client.Client, o client.FileOptions, _ *fileResult) error {
			return c.DeleteFile(ctx, name, o)
		}), nil
	}
	return nil, usagef("unknown operation '%s'", op)
}

// fileStatusOf is the file status of a refused file operation.
func fileStatusOf(err error) (app.FileStatus, bool) {
	var fe *dnp3.FileError
	if errors.As(err, &fe) {
		return fe.Status, true
	}
	return 0, false
}
