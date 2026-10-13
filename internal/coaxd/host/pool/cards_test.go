// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"testing"
	"time"

	"github.com/Till0196/coax/internal/winscard"
	"github.com/Till0196/coax/internal/winscard/usbcard"
)

// TestCardAndTunersShareOneHandle is what the whole seam exists for: on a
// PX-W3U4 the card slot and the four tuners are one USB device whose interface
// can be claimed once, yet a recording and a decoder must run at the same time.
func TestCardAndTunersShareOneHandle(t *testing.T) {
	pool, registry, _ := newTestPool(t, Options{Linger: LingerForever})
	provider := usbcard.New(usbcard.Config{Boards: pool.Cards()})

	context, err := provider.EstablishContext(winscard.ScopeSystem)
	if err != nil {
		t.Fatalf("establish a card context: %v", err)
	}
	defer context.Release()
	readers, err := context.Readers(nil)
	if err != nil {
		t.Fatalf("list readers: %v", err)
	}
	if len(readers) != 1 {
		t.Fatalf("listed %d readers, want the one board's: %v", len(readers), readers)
	}

	// A recording starts.
	owner := pool.NewOwner()
	lease, err := pool.Lease(Want{DeliverySystem: "ISDB-T", Owner: owner})
	if err != nil {
		t.Fatalf("take a tuner: %v", err)
	}
	defer lease.Release()

	// And a decoder opens the card on the same enclosure.
	card, _, err := context.Connect(readers[0], winscard.ShareShared, winscard.ProtocolAny)
	if err != nil {
		t.Fatalf("connect to the card while a tuner is running: %v", err)
	}
	defer card.Disconnect(winscard.LeaveCard)

	if got := registry.openCount(); got != 1 {
		t.Errorf("opened the board %d times; a recording and a card session share one handle", got)
	}
	if _, _, err := card.Transmit([]byte{0x90, 0x30, 0x00, 0x00, 0x00}, winscard.ProtocolT1); err != nil {
		t.Errorf("send an APDU while a tuner is running: %v", err)
	}
}

// TestCardHoldKeepsTheBoardForItself checks the other direction: a card session
// with no recording keeps the board open, and giving it up releases it.
func TestCardHoldKeepsTheBoardForItself(t *testing.T) {
	pool, _, board := newTestPool(t, Options{Linger: 20 * time.Millisecond})
	provider := usbcard.New(usbcard.Config{Boards: pool.Cards()})
	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()
	readers, _ := context.Readers(nil)

	handle, err := provider.OpenReader(readers[0])
	if err != nil {
		t.Fatalf("open the reader: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if board.closed.Get() != 0 {
		t.Error("the board was closed while a card session held it")
	}

	if err := handle.Close(); err != nil {
		t.Fatalf("close the handle: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for board.closed.Get() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the board was never closed once the card let it go")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
