// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Command fakecardd serves one made-up card slot over the pcsc-lite client protocol, so
// that this package can be checked against a real libpcsclite rather than only against a
// client written here. It lives under testdata so that the ordinary build never sees it.
//
//	CGO_ENABLED=0 go build -o fakecardd ./internal/coaxd/pcscd/testdata/fakecardd
//	docker run -d --name pcsctest debian:stable-slim sleep 3600
//	docker cp fakecardd pcsctest:/usr/local/bin/
//	docker exec pcsctest apt-get install -y pcsc-tools
//	docker exec pcsctest sh -c 'fakecardd /run/pcscd.comm &
//	  PCSCLITE_CSOCK_NAME=/run/pcscd.comm pcsc_scan -n'
//
// pcsc_scan should list the reader, report the card and print the ATR.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/Till0196/coax/internal/pcscd"
	"github.com/Till0196/coax/internal/winscard"
)

type slots struct{}

func (slots) Readers() ([]string, error) {
	return []string{"PLEX PX-W3U4 series Smart Card Reader (ID: 0123456789ABCDEF)"}, nil
}
func (slots) Open(string) (pcscd.Slot, error) { return slot{}, nil }

type slot struct{}

func (slot) Present() (bool, error) { return true, nil }
func (slot) ATR() []byte {
	return []byte{0x3B, 0xF0, 0x12, 0x00, 0xFF, 0x91, 0x81, 0x71, 0xFC, 0x47, 0x00, 0x00, 0x00}
}
func (slot) Sharing() int { return 0 }
func (slot) Close() error { return nil }

type provider struct{}

func (provider) Name() string { return "fake" }
func (provider) EstablishContext(winscard.Scope) (winscard.ProviderContext, error) {
	return pcontext{}, nil
}

type pcontext struct{}

func (pcontext) Readers([]string) ([]string, error) {
	return slots{}.Readers()
}
func (pcontext) ReaderGroups() ([]string, error) { return []string{winscard.DefaultReaderGroup}, nil }
func (pcontext) Release() error                  { return nil }
func (pcontext) Connect(string, winscard.ShareMode, winscard.Protocol) (winscard.Card, winscard.Protocol, error) {
	return card{}, winscard.ProtocolT1, nil
}
func (pcontext) GetStatusChange(ctx context.Context, _ time.Duration, _ []winscard.ReaderState) ([]winscard.ReaderState, error) {
	<-ctx.Done()
	return nil, winscard.ErrCancelled
}

type card struct{}

func (card) Reconnect(winscard.ShareMode, winscard.Protocol, winscard.Disposition) (winscard.Protocol, error) {
	return winscard.ProtocolT1, nil
}
func (card) Disconnect(winscard.Disposition) error     { return nil }
func (card) BeginTransaction() error                   { return nil }
func (card) EndTransaction(winscard.Disposition) error { return nil }
func (card) Control(uint32, []byte) ([]byte, error)    { return nil, winscard.ErrUnsupportedFeature }
func (card) SetAttrib(winscard.Attribute, []byte) error {
	return winscard.ErrUnsupportedFeature
}
func (card) Status() (winscard.Status, error) {
	return winscard.Status{State: winscard.CardSpecific, Protocol: winscard.ProtocolT1, ATR: slot{}.ATR()}, nil
}
func (card) GetAttrib(winscard.Attribute) ([]byte, error) {
	return nil, winscard.ErrUnsupportedFeature
}
func (card) Transmit(send []byte, _ winscard.Protocol) ([]byte, winscard.Protocol, error) {
	return append(append([]byte(nil), send...), 0x90, 0x00), winscard.ProtocolT1, nil
}

func main() {
	path := os.Args[1]
	table := pcscd.NewTable(slots{}, 200*time.Millisecond, true)
	service := winscard.NewService(provider{})
	server := pcscd.New(service, table, func(line string) { fmt.Fprintln(os.Stderr, "cardd:", line) })
	table.OnEvent(server.Notify)
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		panic(err)
	}
	_ = os.Chmod(path, 0o666)
	fmt.Fprintln(os.Stderr, "serving", path)
	panic(server.Serve(listener))
}
