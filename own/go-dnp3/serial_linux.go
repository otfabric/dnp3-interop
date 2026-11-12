// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

var bauds = map[int]uint32{
	50: unix.B50, 75: unix.B75, 110: unix.B110, 134: unix.B134, 150: unix.B150, 200: unix.B200,
	300: unix.B300, 600: unix.B600, 1200: unix.B1200, 1800: unix.B1800, 2400: unix.B2400,
	4800: unix.B4800, 9600: unix.B9600, 19200: unix.B19200, 38400: unix.B38400, 57600: unix.B57600,
	115200: unix.B115200, 230400: unix.B230400, 460800: unix.B460800, 500000: unix.B500000,
	576000: unix.B576000, 921600: unix.B921600, 1000000: unix.B1000000, 1152000: unix.B1152000,
	1500000: unix.B1500000, 2000000: unix.B2000000, 2500000: unix.B2500000, 3000000: unix.B3000000,
	3500000: unix.B3500000, 4000000: unix.B4000000,
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
	t := unix.Termios{
		Cflag:  speed | unix.CREAD | unix.CLOCAL,
		Ispeed: speed,
		Ospeed: speed,
	}
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	if cfg.dataBits == 7 {
		t.Cflag |= unix.CS7
	} else {
		t.Cflag |= unix.CS8
	}
	switch cfg.parity {
	case "even":
		t.Cflag |= unix.PARENB
	case "odd":
		t.Cflag |= unix.PARENB | unix.PARODD
	}
	if cfg.stopBits == 2 {
		t.Cflag |= unix.CSTOPB
	}
	raw, err := f.SyscallConn()
	if err == nil {
		cerr := raw.Control(func(fd uintptr) {
			// TCSETS through x/sys, which keeps the unsafe pointer of the
			// ioctl out of this package.
			err = unix.IoctlSetTermios(int(fd), unix.TCSETS, &t)
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
