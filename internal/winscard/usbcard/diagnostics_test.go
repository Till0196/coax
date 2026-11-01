// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbcard

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/trace"
	"github.com/Till0196/coax/pkg/winscard"
)

func TestSessionFailureLogsCauseBeforeReturningCardStatus(t *testing.T) {
	var log bytes.Buffer
	trace.SetOutput(&log)
	defer trace.SetOutput(nil)
	target := newSlot(New(Config{}), cardDevice{name: "test reader"})
	target.reader = &fakeBoard{present: true}
	target.initialized = true
	cause := errors.New("initialize T=1: resynchronize: injected transport timeout")
	if got := target.fail(cause); got != winscard.ErrNotTransacted {
		t.Fatalf("failure status = %v, want SCARD_E_NOT_TRANSACTED", got)
	}
	if !strings.Contains(log.String(), "test reader") || !strings.Contains(log.String(), cause.Error()) {
		t.Fatalf("log lost the reader or original cause: %q", log.String())
	}
	if target.initialized {
		t.Fatal("failed session was not invalidated")
	}
}
