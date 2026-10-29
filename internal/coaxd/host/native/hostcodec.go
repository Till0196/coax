// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package native

import (
	"errors"
	"fmt"
	"net/rpc"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/internal/coaxd/receiver"
	"github.com/Till0196/coax/pkg/usbio"
)

// The errors a caller tells apart cross the process boundary as a code in front
// of the message, since net/rpc carries an error as its text alone.
var hostErrors = []struct {
	code   string
	target error
}{
	{"busy", device.ErrTunerBusy},
	{"range", receiver.ErrOutOfRange},
	{"gone", pool.ErrGone},
}

const codeMark = "\x00"

// toHost is how the host returns an error.
func toHost(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range hostErrors {
		if errors.Is(err, known.target) || (known.target == pool.ErrGone && errors.Is(err, usbio.ErrGone)) {
			return errors.New(codeMark + known.code + codeMark + err.Error())
		}
	}
	return err
}

// fromHost turns what the host returned back into an error the caller can test.
func fromHost(err error) error {
	if err == nil {
		return nil
	}
	var answered rpc.ServerError
	if !errors.As(err, &answered) {
		// The connection, not the call: the host is gone, and every tuner it held
		// with it.
		return fmt.Errorf("%w: the BDA host process ended (%v)", pool.ErrGone, err)
	}
	text := string(answered)
	if strings.HasPrefix(text, codeMark) {
		if code, message, ok := strings.Cut(text[len(codeMark):], codeMark); ok {
			for _, known := range hostErrors {
				if known.code == code {
					return hostError{message: message, target: known.target}
				}
			}
			return errors.New(message)
		}
	}
	return errors.New(text)
}

// hostError is an error the host reported, still recognisable as what it was.
type hostError struct {
	message string
	target  error
}

func (err hostError) Error() string { return err.message }
func (err hostError) Unwrap() error { return err.target }
