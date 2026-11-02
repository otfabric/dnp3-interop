// SPDX-License-Identifier: MIT

//go:build !linux

package main

import (
	"errors"
	"os"
)

// openSerial: the image is Linux; elsewhere there is no serial line.
func openSerial(serialConfig) (*os.File, error) {
	return nil, errors.New("serial lines need Linux")
}
