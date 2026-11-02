// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var bauds = map[int]uint32{
	50: syscall.B50, 75: syscall.B75, 110: syscall.B110, 134: syscall.B134, 150: syscall.B150, 200: syscall.B200,
	300: syscall.B300, 600: syscall.B600, 1200: syscall.B1200, 1800: syscall.B1800, 2400: syscall.B2400,
	4800: syscall.B4800, 9600: syscall.B9600, 19200: syscall.B19200, 38400: syscall.B38400, 57600: syscall.B57600,
	115200: syscall.B115200, 230400: syscall.B230400, 460800: syscall.B460800, 500000: syscall.B500000,
	576000: syscall.B576000, 921600: syscall.B921600, 1000000: syscall.B1000000, 1152000: syscall.B1152000,
	1500000: syscall.B1500000, 2000000: syscall.B2000000, 2500000: syscall.B2500000, 3000000: syscall.B3000000,
	3500000: syscall.B3500000, 4000000: syscall.B4000000,
}

// openSerial opens a serial device in raw mode with the line settings of
// cfg and no flow control.
func openSerial(cfg serialConfig) (*os.File, error) {
	speed, ok := bauds[cfg.baud]
	if !ok {
		return nil, fmt.Errorf("baud rate %d is not a standard rate", cfg.baud)
	}
	f, err := os.OpenFile(cfg.device, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	t := syscall.Termios{
		Cflag:  speed | syscall.CREAD | syscall.CLOCAL,
		Ispeed: speed,
		Ospeed: speed,
	}
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0
	if cfg.dataBits == 7 {
		t.Cflag |= syscall.CS7
	} else {
		t.Cflag |= syscall.CS8
	}
	switch cfg.parity {
	case "even":
		t.Cflag |= syscall.PARENB
	case "odd":
		t.Cflag |= syscall.PARENB | syscall.PARODD
	}
	if cfg.stopBits == 2 {
		t.Cflag |= syscall.CSTOPB
	}
	raw, err := f.SyscallConn()
	if err == nil {
		cerr := raw.Control(func(fd uintptr) {
			if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TCSETS),
				uintptr(unsafe.Pointer(&t))); errno != 0 {
				err = errno
			}
		})
		if cerr != nil {
			err = cerr
		}
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s: cannot set the line: %w", cfg.device, err)
	}
	return f, nil
}
