// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package native

import (
	"errors"
	"fmt"
	"io"
	"net/rpc"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/internal/coaxd/receiver"
	"github.com/Till0196/coax/pkg/usbio"
)

// What the host returns reaches the daemon as an rpc.ServerError holding the text.
func acrossTheHost(err error) error {
	encoded := toHost(err)
	if encoded == nil {
		return fromHost(nil)
	}
	return fromHost(rpc.ServerError(encoded.Error()))
}

func TestHostErrorsKeepWhatTheyWere(t *testing.T) {
	for _, target := range []error{device.ErrTunerBusy, receiver.ErrOutOfRange, pool.ErrGone} {
		original := fmt.Errorf("tune: %w", target)
		got := acrossTheHost(original)
		if !errors.Is(got, target) {
			t.Errorf("%v came back as %v, which is not %v", original, got, target)
		}
		if got.Error() != original.Error() {
			t.Errorf("message %q, want %q", got.Error(), original.Error())
		}
	}
	if got := acrossTheHost(fmt.Errorf("read: %w", usbio.ErrGone)); !errors.Is(got, pool.ErrGone) {
		t.Errorf("a USB board that went is %v, want ErrGone", got)
	}
	plain := errors.New("the tuner did not lock to this channel")
	if got := acrossTheHost(plain); got.Error() != plain.Error() {
		t.Errorf("plain error came back as %q", got)
	}
	if acrossTheHost(nil) != nil {
		t.Error("no error came back as one")
	}
}

func TestALostHostIsAGoneBoard(t *testing.T) {
	for _, err := range []error{rpc.ErrShutdown, io.ErrUnexpectedEOF} {
		if got := fromHost(err); !errors.Is(got, pool.ErrGone) {
			t.Errorf("%v became %v, want ErrGone", err, got)
		}
	}
}
