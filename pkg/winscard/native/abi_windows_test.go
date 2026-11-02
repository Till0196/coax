// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package native

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/Till0196/coax/pkg/winscard"
)

func TestWindowsReaderAndRequestLayout(t *testing.T) {
	word := unsafe.Sizeof(uintptr(0))
	if got := unsafe.Sizeof(readerState{}); got != 48+2*word {
		t.Fatalf("reader state size=%d", got)
	}
	if got := unsafe.Offsetof(readerState{}.currentState); got != 2*word {
		t.Fatalf("current state offset=%d", got)
	}
	if got := unsafe.Offsetof(readerState{}.atr); got != 2*word+12 {
		t.Fatalf("ATR offset=%d", got)
	}
	if got := unsafe.Sizeof(ioRequest{}); got != 8 {
		t.Fatalf("PCI size=%d", got)
	}
	if got := unsafe.Offsetof(ioRequest{}.pciLength); got != 4 {
		t.Fatalf("PCI length offset=%d", got)
	}
}

func TestWindowsStatusKeepsIts32BitValue(t *testing.T) {
	status := uintptr(0x80100068)
	if !errors.Is(code(status), winscard.ErrResetCard) {
		t.Fatal("zero-extended status changed")
	}
	// A LONG return may also arrive sign-extended in the pointer-sized register.
	status |= ^uintptr(0xffffffff)
	if !errors.Is(code(status), winscard.ErrResetCard) {
		t.Fatal("sign-extended status changed")
	}
}
