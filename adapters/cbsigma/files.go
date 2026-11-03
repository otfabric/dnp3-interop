// SPDX-License-Identifier: MIT

// File transfer, group 70 (contract 1.1 and 1.3): the outstation's store and
// the master's operations.

package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	dnp3 "github.com/cB-Sigma/dnp3"
)

var fileStatusNames = map[uint8]string{
	0: "SUCCESS", 1: "PERMISSION_DENIED", 2: "INVALID_MODE", 3: "FILE_NOT_FOUND", 4: "FILE_LOCKED",
	5: "TOO_MANY_OPEN", 6: "INVALID_HANDLE", 7: "WRITE_BLOCK_SIZE", 8: "COMM_LOST", 9: "CANNOT_ABORT",
	16: "NOT_OPENED", 17: "HANDLE_EXPIRED", 18: "BUFFER_OVERRUN", 19: "FATAL", 20: "BLOCK_SEQ",
}

func fileStatusName(s uint8) string {
	if n, ok := fileStatusNames[s]; ok {
		return n
	}
	return "UNDEFINED"
}

// ---- outstation ----

// The permissions of what the fixture holds and what is written: the
// library reports a file's permissions masked by the requestor's rights.
const (
	filePerms uint16 = 0o644
	dirPerms  uint16 = 0o755
)

type storeEntry struct {
	dir     bool
	data    []byte
	created time.Time
	perms   uint16
}

// memStore is the outstation's FileStore: the files of the fixture, in
// memory, for the life of the process. Paths have no leading separator;
// "" is the root.
type memStore struct {
	mu      sync.Mutex
	entries map[string]*storeEntry
}

func newStore(fx *fixture) *memStore {
	s := &memStore{entries: map[string]*storeEntry{"": {dir: true, perms: dirPerms}}}
	now := time.Now()
	for _, d := range fx.Directories {
		s.entries[strings.Trim(d, "/")] = &storeEntry{dir: true, created: now, perms: dirPerms}
	}
	for name, content := range fx.Files {
		p := strings.Trim(name, "/")
		for dir := path.Dir(p); dir != "." && s.entries[dir] == nil; dir = path.Dir(dir) {
			s.entries[dir] = &storeEntry{dir: true, created: now, perms: dirPerms}
		}
		s.entries[p] = &storeEntry{data: slices.Clone(content), created: now, perms: filePerms}
	}
	return s
}

func parentOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

func (s *memStore) children(dir string) []string {
	var out []string
	for p := range s.entries {
		if p != "" && parentOf(p) == dir {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (s *memStore) info(p string, e *storeEntry) dnp3.FileInfo {
	size := uint32(len(e.data))
	if e.dir {
		size = uint32(len(s.children(p)))
	}
	return dnp3.FileInfo{Name: path.Base("/" + p), Dir: e.dir, Size: size, Created: e.created, Permissions: e.perms}
}

func (s *memStore) Stat(_ context.Context, p string) (dnp3.FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[p]
	if e == nil {
		return dnp3.FileInfo{}, dnp3.ErrFileNotFound
	}
	return s.info(p, e), nil
}

func (s *memStore) List(_ context.Context, dir string) ([]dnp3.FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[dir]; e == nil || !e.dir {
		return nil, dnp3.ErrFileNotFound
	}
	var out []dnp3.FileInfo
	for _, p := range s.children(dir) {
		out = append(out, s.info(p, s.entries[p]))
	}
	return out, nil
}

func (s *memStore) Read(_ context.Context, p string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[p]
	if e == nil || e.dir {
		return nil, dnp3.ErrFileNotFound
	}
	return slices.Clone(e.data), nil
}

// Write creates or replaces a file in a directory that exists.
func (s *memStore) Write(_ context.Context, p string, data []byte, created time.Time, perms uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d := s.entries[parentOf(p)]; d == nil || !d.dir {
		return dnp3.ErrFileNotFound
	}
	if e := s.entries[p]; e != nil && e.dir {
		return dnp3.ErrFileDenied
	}
	if created.IsZero() || created.UnixMilli() == 0 {
		created = time.Now()
	}
	if perms == 0 {
		perms = filePerms
	}
	s.entries[p] = &storeEntry{data: slices.Clone(data), created: created, perms: perms}
	return nil
}

func (s *memStore) Delete(_ context.Context, p string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[p]
	switch {
	case e == nil:
		return dnp3.ErrFileNotFound
	case e.dir:
		return dnp3.ErrFileDenied
	}
	delete(s.entries, p)
	return nil
}

// isDirectory says whether the name a master sent is a directory of the
// store (for the file event of an open: list or read).
func (s *memStore) isDirectory(name string) bool {
	p, ok := dnp3.FilePath(name)
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[p]
	return e != nil && e.dir
}

// ---- master ----

var fileOperations = []string{"file-get", "file-put", "file-ls", "file-info", "file-delete", "file-authenticate"}

type fileRequest struct {
	op, name       string
	content        []byte
	append         bool
	user, password string
}

type fileResult struct {
	file   object
	files  []object
	status *uint8
}

// fileError is a file status other than SUCCESS (the error file-error).
type fileError struct{ status uint8 }

func (e fileError) Error() string { return "the outstation answered " + fileStatusName(e.status) }

func parseFileOperation(op string, a args) (fileRequest, error) {
	r := fileRequest{op: op}
	if a.has("file-user") != a.has("file-password") {
		return r, usagef("--file-user and --file-password go together")
	}
	r.user, r.password = a.str("file-user", ""), a.str("file-password", "")
	switch op {
	case "file-authenticate":
		if !a.has("file-user") {
			return r, usagef("file-authenticate needs --file-user and --file-password")
		}
		return r, nil
	case "file-info":
		if a.has("file-user") {
			return r, usagef("file-info takes no credentials: GET_FILE_INFO carries no key")
		}
	}
	name, err := a.required("name")
	if err != nil {
		return r, err
	}
	if name != "/" && (len(name) > 200 || !fileName.MatchString(name)) {
		return r, usagef("--name must be an absolute file name of path elements of A-Za-z0-9._- (at most 200 characters)")
	}
	r.name = name
	if op == "file-put" {
		text, err := a.required("content")
		if err != nil {
			return r, err
		}
		if r.content, err = hex.DecodeString(text); err != nil {
			return r, usagef("--content must be hex")
		}
		r.append = a.has("append")
	}
	return r, nil
}

// fileObject is the one group 70 object of a request.
func fileObject(variation uint8, data []byte) []byte {
	b, _ := dnp3.EncodeBlock(70, variation, dnp3.QFreeFormat, []dnp3.Point{{Raw: data}})
	return b
}

// fileAnswer runs a file request and returns the group 70 object of the
// answer: from the response, or, when the outstation answers later as an
// event, from polls of the event classes.
func (c *master) fileAnswer(ctx context.Context, fc byte, objs []byte) (uint8, []byte, error) {
	frags, err := c.request(ctx, fc, objs)
	if err != nil {
		return 0, nil, err
	}
	for deadline := time.Now().Add(c.timeout); ; {
		for _, f := range frags {
			if v, raw, ok := groupSeventy(f.Objects); ok {
				return v, raw, nil
			}
		}
		if time.Now().After(deadline) {
			return 0, nil, errors.New("the outstation sent no file object in answer")
		}
		time.Sleep(100 * time.Millisecond)
		if frags, err = c.request(ctx, dnp3.FCRead, dnp3.ClassRequest(1, 2, 3)); err != nil {
			return 0, nil, err
		}
	}
}

func groupSeventy(objs []byte) (uint8, []byte, bool) {
	blocks, _ := dnp3.ParseObjects(objs, true)
	for _, b := range blocks {
		if b.Header.Group == 70 && len(b.Points) > 0 {
			return b.Header.Var, b.Points[0].Raw, true
		}
	}
	return 0, nil, false
}

// status reads a g70v4 answer.
func (c *master) fileStatus(ctx context.Context, fc byte, objs []byte, r *fileResult) (dnp3.FileStatus, error) {
	v, raw, err := c.fileAnswer(ctx, fc, objs)
	if err != nil {
		return dnp3.FileStatus{}, err
	}
	if v != 4 {
		return dnp3.FileStatus{}, fmt.Errorf("the outstation answered g70v%d, not a file status", v)
	}
	st, err := dnp3.ParseFileStatus(raw)
	if err != nil {
		return st, err
	}
	r.status = &st.Status
	if st.Status != 0 {
		return st, fileError{st.Status}
	}
	return st, nil
}

// authenticate runs AUTHENTICATE_FILE and returns the key; key 0 is a
// refusal.
func (c *master) authenticate(ctx context.Context, req fileRequest, r *fileResult) (uint32, error) {
	v, raw, err := c.fileAnswer(ctx, dnp3.FCAuthenticateFile, fileObject(2, dnp3.FileAuth{User: req.user, Password: req.password}.Encode()))
	if err != nil {
		return 0, err
	}
	if v == 4 {
		st, err := dnp3.ParseFileStatus(raw)
		if err != nil {
			return 0, err
		}
		r.status = &st.Status
		return 0, fileError{st.Status}
	}
	answer, err := dnp3.ParseFileAuth(raw)
	if err != nil {
		return 0, err
	}
	if answer.Key == 0 {
		denied := uint8(1)
		r.status = &denied
		return 0, fileError{denied}
	}
	ok := uint8(0)
	r.status = &ok
	return answer.Key, nil
}

func permissions(p uint16) string {
	const letters = "rwxrwxrwx"
	out := []byte("---------")
	for i := range 9 {
		if p&(1<<(8-i)) != 0 {
			out[i] = letters[i]
		}
	}
	return string(out)
}

func entry(d dnp3.FileDescriptor) object {
	kind := "file"
	if d.Type == 0 {
		kind = "directory"
	}
	name := d.Name
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 && i < len(name)-1 {
		name = name[i+1:]
	}
	return object{{"name", name}, {"type", kind}, {"size", d.Size}, {"permissions", permissions(d.Permissions)},
		{"time", formatTime(d.Created)}}
}

// readFile opens a file or directory for reading, reads its blocks and
// closes it.
func (c *master) readFile(ctx context.Context, name string, key uint32, r *fileResult) ([]byte, error) {
	open, err := c.fileStatus(ctx, dnp3.FCOpenFile, fileObject(3, dnp3.FileCommand{Name: name, Mode: dnp3.FileModeRead,
		MaxBlock: 1024, RequestID: 1, AuthKey: key}.Encode()), r)
	if err != nil {
		return nil, err
	}
	var data []byte
	for block := uint32(0); ; block++ {
		v, raw, err := c.fileAnswer(ctx, dnp3.FCRead, fileObject(5, dnp3.FileTransport{Handle: open.Handle, Block: block}.Encode()))
		if err != nil {
			return nil, err
		}
		if v == 6 {
			st, err := dnp3.ParseFileTransportStatus(raw)
			if err != nil {
				return nil, err
			}
			r.status = &st.Status
			return nil, fileError{st.Status}
		}
		t, err := dnp3.ParseFileTransport(raw)
		if err != nil {
			return nil, err
		}
		data = append(data, t.Data...)
		if t.Last {
			break
		}
	}
	if _, err := c.fileStatus(ctx, dnp3.FCCloseFile, fileObject(4, dnp3.FileStatus{Handle: open.Handle, RequestID: 1}.Encode()), r); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *master) writeFile(ctx context.Context, req fileRequest, key uint32, r *fileResult) error {
	mode := uint16(dnp3.FileModeWrite)
	if req.append {
		mode = dnp3.FileModeAppend
	}
	open, err := c.fileStatus(ctx, dnp3.FCOpenFile, fileObject(3, dnp3.FileCommand{Name: req.name, Mode: mode,
		Size: uint32(len(req.content)), Permissions: filePerms, Created: time.Now(), MaxBlock: 1024, RequestID: 1, AuthKey: key}.Encode()), r)
	if err != nil {
		return err
	}
	size := int(open.MaxBlock)
	if size <= 0 || size > 1024 {
		size = 1024
	}
	data := req.content
	for block := uint32(0); ; block++ {
		n := min(size, len(data))
		last := n == len(data)
		v, raw, err := c.fileAnswer(ctx, dnp3.FCWrite, fileObject(5, dnp3.FileTransport{Handle: open.Handle, Block: block, Last: last, Data: data[:n]}.Encode()))
		if err != nil {
			return err
		}
		if v != 6 {
			return fmt.Errorf("the outstation answered a block with g70v%d", v)
		}
		st, err := dnp3.ParseFileTransportStatus(raw)
		if err != nil {
			return err
		}
		r.status = &st.Status
		if st.Status != 0 {
			return fileError{st.Status}
		}
		data = data[n:]
		if last {
			break
		}
	}
	_, err = c.fileStatus(ctx, dnp3.FCCloseFile, fileObject(4, dnp3.FileStatus{Handle: open.Handle, RequestID: 1}.Encode()), r)
	return err
}

// runFile runs a file operation.
func (c *master) runFile(ctx context.Context, req fileRequest, r *fileResult) error {
	var key uint32
	if req.user != "" {
		k, err := c.authenticate(ctx, req, r)
		if err != nil || req.op == "file-authenticate" {
			return err
		}
		key = k
	}
	switch req.op {
	case "file-get":
		data, err := c.readFile(ctx, req.name, key, r)
		if err != nil {
			return err
		}
		r.file = object{{"name", req.name}, {"size", len(data)}, {"content", hex.EncodeToString(data)}}
	case "file-ls":
		data, err := c.readFile(ctx, req.name, key, r)
		if err != nil {
			return err
		}
		list, err := dnp3.ParseDirectory(data)
		if err != nil {
			return err
		}
		r.files = []object{}
		for _, d := range list {
			r.files = append(r.files, entry(d))
		}
	case "file-put":
		return c.writeFile(ctx, req, key, r)
	case "file-info":
		v, raw, err := c.fileAnswer(ctx, dnp3.FCGetFileInfo, fileObject(7, dnp3.FileDescriptor{Name: req.name, RequestID: 1}.Encode()))
		if err != nil {
			return err
		}
		if v == 4 {
			st, err := dnp3.ParseFileStatus(raw)
			if err != nil {
				return err
			}
			r.status = &st.Status
			return fileError{st.Status}
		}
		d, err := dnp3.ParseFileDescriptor(raw)
		if err != nil {
			return err
		}
		ok := uint8(0)
		r.status = &ok
		r.file = entry(d)
	case "file-delete":
		_, err := c.fileStatus(ctx, dnp3.FCDeleteFile, fileObject(3, dnp3.FileCommand{Name: req.name, Mode: dnp3.FileModeNone,
			RequestID: 1, AuthKey: key}.Encode()), r)
		return err
	}
	return nil
}
