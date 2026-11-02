// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbcard

import (
	"errors"
	"testing"
	"time"

	"github.com/Till0196/coax/pkg/usbio"
	"github.com/Till0196/coax/pkg/winscard"
)

func reconnectedCards(t *testing.T) (winscard.Card, winscard.Card) {
	t.Helper()
	source := newLentSource()
	provider, reader := providerOver(t, source)
	ctx, _ := provider.EstablishContext(winscard.ScopeSystem)
	t.Cleanup(func() { _ = ctx.Release() })
	old, _, err := ctx.Connect(reader, winscard.ShareShared, winscard.ProtocolT1)
	if err != nil {
		t.Fatal(err)
	}
	source.replug()
	fresh, _, err := ctx.Connect(reader, winscard.ShareShared, winscard.ProtocolT1)
	if err != nil {
		t.Fatal(err)
	}
	return old, fresh
}

func TestOldDisconnectPreservesFreshSession(t *testing.T) {
	old, fresh := reconnectedCards(t)
	if err := old.Disconnect(winscard.LeaveCard); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fresh.Transmit([]byte{0, 1}, winscard.ProtocolT1); err != nil {
		t.Fatal(err)
	}
}

func TestOldConnectionCannotUseFreshSession(t *testing.T) {
	old, _ := reconnectedCards(t)
	if _, _, err := old.Transmit([]byte{0, 1}, winscard.ProtocolT1); err == nil {
		t.Fatal("old connection used the new generation")
	}
}

func TestOldResetDoesNotResetFreshSession(t *testing.T) {
	old, fresh := reconnectedCards(t)
	_ = old.Disconnect(winscard.ResetCard)
	if _, _, err := fresh.Transmit([]byte{0, 1}, winscard.ProtocolT1); err != nil {
		t.Fatal(err)
	}
}

func TestReconnectMovesToFreshGeneration(t *testing.T) {
	old, fresh := reconnectedCards(t)
	if _, err := old.Reconnect(winscard.ShareShared, winscard.ProtocolT1, winscard.ResetCard); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Disconnect(winscard.LeaveCard); err != nil {
		t.Fatal(err)
	}
	if _, _, err := old.Transmit([]byte{0, 1}, winscard.ProtocolT1); err != nil {
		t.Fatal(err)
	}
}

func TestUnpooledDisconnectErrorRetiresTheSlot(t *testing.T) {
	source := newLentSource()
	provider, reader := providerOver(t, source)
	ctx, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer ctx.Release()
	old, _, err := ctx.Connect(reader, winscard.ShareShared, winscard.ProtocolT1)
	if err != nil {
		t.Fatal(err)
	}
	source.board.mutex.Lock()
	source.board.transmitErr = usbio.ErrGone
	source.board.mutex.Unlock()
	if _, _, err := old.Transmit([]byte{0, 1}, winscard.ProtocolT1); !errors.Is(err, winscard.ErrReaderUnavailable) {
		t.Fatalf("disconnect was hidden by a later status probe: %v", err)
	}
	fresh, _, err := ctx.Connect(reader, winscard.ShareShared, winscard.ProtocolT1)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Disconnect(winscard.LeaveCard); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fresh.Transmit([]byte{0, 1}, winscard.ProtocolT1); err != nil {
		t.Fatal(err)
	}
	if borrows, _ := source.counts(); borrows != 2 {
		t.Fatalf("borrows=%d, want a fresh handle", borrows)
	}
}

func TestStatusObservationPreservesAnotherContextsTransaction(t *testing.T) {
	source := newLentSource()
	provider, reader := providerOver(t, source)
	firstContext, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer firstContext.Release()
	secondContext, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer secondContext.Release()
	first, _, err := firstContext.Connect(reader, winscard.ShareShared, winscard.ProtocolT1)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := secondContext.Connect(reader, winscard.ShareShared, winscard.ProtocolT1)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := provider.OpenReader(reader)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	if err := first.BeginTransaction(); err != nil {
		t.Fatal(err)
	}
	defer first.EndTransaction(winscard.LeaveCard)
	seen := make(chan error, 1)
	go func() { _, err := observer.Present(); seen <- err }()
	select {
	case err := <-seen:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("status was blocked by the transaction")
	}
	done := make(chan error, 1)
	go func() { _, _, err := second.Transmit([]byte{0, 2}, winscard.ProtocolT1); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("another request ran inside the transaction: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if _, _, err := first.Transmit([]byte{0, 1}, winscard.ProtocolT1); err != nil {
		t.Fatal(err)
	}
	if err := first.EndTransaction(winscard.LeaveCard); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting request did not resume")
	}
}
