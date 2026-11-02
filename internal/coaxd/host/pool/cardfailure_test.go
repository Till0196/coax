// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"errors"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/pkg/usbio"
)

func TestCardFailuresRetireOnlyUnusableHandles(t *testing.T) {
	operations := map[string]func(device.CardReader) error{
		"status":   func(r device.CardReader) error { _, err := r.CardStatus(); return err },
		"reset":    func(r device.CardReader) error { _, err := r.ResetCard(); return err },
		"transmit": func(r device.CardReader) error { _, err := r.TransmitAPDU([]byte{0, 1}); return err },
	}
	for name, call := range operations {
		for _, cause := range []error{usbio.ErrGone, usbio.ErrTimeout, errors.New("invalid card response")} {
			t.Run(name+"/"+cause.Error(), func(t *testing.T) {
				pool, registry, board := newTestPool(t, Options{Linger: LingerForever})
				source := pool.Cards()
				listed, err := source.Boards()
				if err != nil {
					t.Fatal(err)
				}
				borrow, err := source.Open(listed[0].Backend, listed[0].Selector)
				if err != nil {
					t.Fatal(err)
				}
				defer borrow.Release()
				board.mutex.Lock()
				board.cardErr = cause
				before := board.cardCalls
				board.mutex.Unlock()
				if err := call(borrow.Board.(device.CardReader)); err == nil {
					t.Fatal("failure lost")
				}
				gone := errors.Is(cause, usbio.ErrGone)
				if borrow.Valid() == gone {
					t.Fatalf("validity after %v = %v", cause, borrow.Valid())
				}
				board.mutex.Lock()
				after := board.cardCalls
				board.cardErr = nil
				board.mutex.Unlock()
				if after != before+1 {
					t.Fatal("card command was retried")
				}
				if gone {
					if _, err := borrow.Board.(device.CardReader).CardStatus(); !errors.Is(err, ErrGone) {
						t.Fatal("old reader was not retired")
					}
					board.mutex.Lock()
					calls := board.cardCalls
					board.mutex.Unlock()
					if calls != after {
						t.Fatal("closed reader was accessed")
					}
				}
				fresh, err := source.Open(listed[0].Backend, listed[0].Selector)
				if err != nil {
					t.Fatal(err)
				}
				defer fresh.Release()
				want := 1
				if gone {
					want = 2
				}
				if registry.openCount() != want {
					t.Fatalf("opens = %d, want %d", registry.openCount(), want)
				}
				if _, err := fresh.Board.(device.CardReader).CardStatus(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
