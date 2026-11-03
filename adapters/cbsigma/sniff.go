// SPDX-License-Identifier: MIT

// What the library does not tell the application, read off the frames of an
// outstation: broadcasts, file requests and their answers, the user of an
// authenticated request and the authentication errors the outstation sends.

package main

import (
	"encoding/binary"
	"sync"

	dnp3 "github.com/cB-Sigma/dnp3"
)

// fileExchange is a file request waiting for its answer.
type fileExchange struct {
	seq    uint8
	fc     byte
	name   string
	action string // of an open: read, list, write or append
	handle uint32 // of a close
}

type sniffer struct {
	st *station

	mu      sync.Mutex
	inRA    dnp3.Reassembler
	outRA   dnp3.Reassembler
	bcastRA dnp3.Reassembler
	pending *fileExchange
	writes  map[uint32]fileExchange // open handles of writes and appends
}

func userData(f dnp3.Frame) bool {
	return f.Primary() && (f.Func() == dnp3.LinkConfirmedUserData || f.Func() == dnp3.LinkUnconfirmedUserData) && len(f.Data) > 0
}

// in sees a frame from the master.
func (s *sniffer) in(f dnp3.Frame) {
	if !userData(f) {
		return
	}
	s.mu.Lock()
	ra := &s.inRA
	if dnp3.IsBroadcast(f.Dest) {
		ra = &s.bcastRA
	}
	apdu, err := ra.Add(f.Data)
	s.mu.Unlock()
	if err != nil || apdu == nil {
		return
	}
	req, err := dnp3.ParseFragment(apdu)
	if err != nil || req.IsResponse() {
		return
	}
	if dnp3.IsBroadcast(f.Dest) {
		if s.st.first && req.Func != dnp3.FCConfirm {
			emit(object{{"event", "broadcast"}, {"function", req.Func}})
		}
		return
	}
	switch req.Func {
	case dnp3.FCConfirm:
		return
	case dnp3.FCAuthRequest:
		// A challenge answered (g120v2: CSQ, user number, MAC): the request
		// it releases is reported when it is carried out.
		if v, d, err := dnp3.ParseAuthObject(req.Objects); err == nil && v == dnp3.AuthReply && len(d) >= 6 {
			user := binary.LittleEndian.Uint16(d[4:6])
			s.st.mu.Lock()
			s.st.authUser = &user
			s.st.mu.Unlock()
		}
		return
	}
	s.st.mu.Lock()
	s.st.authUser = nil
	s.st.mu.Unlock()
	s.fileRequest(req)
}

func (s *sniffer) fileRequest(req dnp3.Fragment) {
	if req.Func < dnp3.FCOpenFile || req.Func > dnp3.FCAuthenticateFile {
		return
	}
	v, raw, ok := groupSeventy(req.Objects)
	if !ok {
		return
	}
	x := &fileExchange{seq: req.Seq(), fc: req.Func}
	switch {
	case (req.Func == dnp3.FCOpenFile || req.Func == dnp3.FCDeleteFile) && v == 3:
		cmd, err := dnp3.ParseFileCommand(raw)
		if err != nil {
			return
		}
		x.name = cmd.Name
		switch {
		case req.Func == dnp3.FCDeleteFile:
			x.action = "delete"
		case cmd.Mode == dnp3.FileModeWrite:
			x.action = "write"
		case cmd.Mode == dnp3.FileModeAppend:
			x.action = "append"
		case s.st.store != nil && s.st.store.isDirectory(cmd.Name):
			x.action = "list"
		default:
			x.action = "read"
		}
	case req.Func == dnp3.FCCloseFile && v == 4:
		st, err := dnp3.ParseFileStatus(raw)
		if err != nil {
			return
		}
		x.handle = st.Handle
	case req.Func == dnp3.FCGetFileInfo && v == 7:
		d, err := dnp3.ParseFileDescriptor(raw)
		if err != nil {
			return
		}
		x.name, x.action = d.Name, "info"
	case req.Func == dnp3.FCAuthenticateFile && v == 2:
		a, err := dnp3.ParseFileAuth(raw)
		if err != nil {
			return
		}
		x.name, x.action = a.User, "authenticate"
	default:
		return
	}
	s.mu.Lock()
	s.pending = x
	s.mu.Unlock()
}

func fileEvent(action, name string, status uint8) {
	emit(object{{"event", "file"}, {"action", action}, {"name", name}, {"status", fileStatusName(status)}})
}

// out sees a frame the outstation sends.
func (s *sniffer) out(f dnp3.Frame) {
	if !userData(f) {
		return
	}
	s.mu.Lock()
	apdu, err := s.outRA.Add(f.Data)
	s.mu.Unlock()
	if err != nil || apdu == nil {
		return
	}
	resp, err := dnp3.ParseFragment(apdu)
	if err != nil {
		return
	}
	switch resp.Func {
	case dnp3.FCAuthResponse:
		// g120v7: CSQ, user number, association, error code, ...
		if v, d, err := dnp3.ParseAuthObject(resp.Objects); err == nil && v == dnp3.AuthErrorObj && len(d) >= 9 {
			emit(object{{"event", "auth-error"}, {"user", binary.LittleEndian.Uint16(d[4:6])}, {"code", d[8]}})
		}
	case dnp3.FCResponse:
		s.fileAnswer(resp)
	}
}

func (s *sniffer) fileAnswer(resp dnp3.Fragment) {
	s.mu.Lock()
	x := s.pending
	if x != nil && x.seq == resp.Seq() {
		s.pending = nil
	} else {
		x = nil
	}
	s.mu.Unlock()
	if x == nil {
		return
	}
	v, raw, ok := groupSeventy(resp.Objects)
	if !ok {
		return // refused (IIN2): not a file answer
	}
	var status uint8
	switch v {
	case 2:
		a, err := dnp3.ParseFileAuth(raw)
		if err != nil {
			return
		}
		if a.Key == 0 {
			status = 1 // PERMISSION_DENIED
		}
	case 4:
		st, err := dnp3.ParseFileStatus(raw)
		if err != nil {
			return
		}
		status = st.Status
		if x.fc == dnp3.FCCloseFile {
			s.mu.Lock()
			w, open := s.writes[x.handle]
			delete(s.writes, x.handle)
			s.mu.Unlock()
			if open {
				fileEvent(w.action, w.name, status)
			}
			return
		}
		if x.fc == dnp3.FCOpenFile && (x.action == "write" || x.action == "append") && status == 0 {
			s.mu.Lock()
			if s.writes == nil {
				s.writes = map[uint32]fileExchange{}
			}
			s.writes[st.Handle] = *x
			s.mu.Unlock()
			return
		}
	case 7:
	default:
		return
	}
	if x.action != "" {
		fileEvent(x.action, x.name, status)
	}
}
