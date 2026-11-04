// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asicen

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
	"github.com/Till0196/coax/pkg/win32"
)

type protocol struct {
	writes []uint32
	tsid   []byte
}

func (p *protocol) Get(_ win32.GUID, id uint32, b []byte) (uint32, error) {
	switch id {
	case propFlags:
		binary.LittleEndian.PutUint32(b, 0)
	case propSeed:
		copy(b, transportSeed[:])
	default:
		return 0, fmt.Errorf("unexpected property read: %d", id)
	}
	return uint32(len(b)), nil
}
func (p *protocol) Set(_ win32.GUID, id uint32, b []byte) error {
	p.writes = append(p.writes, id)
	switch id {
	case propInit:
	case propTSID:
		p.tsid = append([]byte(nil), b...)
	default:
		return fmt.Errorf("unexpected property write: %d", id)
	}
	return nil
}
func packets(n int) []byte {
	b := bytes.Repeat([]byte{0x22}, 188*n)
	for i := 0; i < len(b); i += 188 {
		b[i] = 0xc7
	}
	return b
}

type fragments struct {
	data  []byte
	chunk int
}

func (s *fragments) Read(b []byte) (int, error) {
	if len(s.data) == 0 {
		return 0, io.EOF
	}
	n := copy(b, s.data[:min(len(s.data), s.chunk)])
	s.data = s.data[n:]
	return n, nil
}
func TestReaderRetainsFragments(t *testing.T) {
	raw := packets(3)
	want := append([]byte(nil), raw...)
	for i := 0; i < len(want); i += 188 {
		want[i] = 0x47
	}
	p := new(protocol)
	c, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	r := &Reader{Source: &fragments{data: raw, chunk: 73}, Control: c}
	var got []byte
	buf := make([]byte, 29)
	for i := 0; i < 100; i++ {
		n, e := r.Read(buf)
		got = append(got, buf[:n]...)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %d bytes want %d", len(got), len(want))
	}

}
func TestNoTransportPropertyCallsAndTSIDWidth(t *testing.T) {
	p := new(protocol)
	c, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.RestoreSync(packets(readSize/188 + 1)); e != nil {
		t.Fatal(e)
	}
	if len(p.writes) != 1 || p.writes[0] != propInit {
		t.Fatalf("stream caused property calls: %v", p.writes)
	}
	if e = c.Select(0x4010); e != nil {
		t.Fatal(e)
	}
	if len(p.tsid) != 8 || binary.LittleEndian.Uint64(p.tsid) != 0x4010 {
		t.Fatalf("TSID %x", p.tsid)
	}
}
func TestReaderRejectsTruncatedPacket(t *testing.T) {
	p := new(protocol)
	c, _ := Open(p)
	r := &Reader{Source: bytes.NewReader(append(packets(1), 0)), Control: c}
	b := make([]byte, 188)
	if n, e := r.Read(b); n != 188 || e != nil {
		t.Fatalf("%d %v", n, e)
	}
	if n, e := r.Read(b); n != 0 || e != io.ErrUnexpectedEOF {
		t.Fatalf("%d %v", n, e)
	}
}

type initialization struct {
	protocol
	nonce [16]byte
	key   []byte
	short bool
}

func (p *initialization) Get(g win32.GUID, id uint32, b []byte) (uint32, error) {
	switch id {
	case propFlags:
		binary.LittleEndian.PutUint32(b, 1)
		return 4, nil
	case propRandom:
		copy(b, p.nonce[:])
		if p.short {
			return 15, nil
		}
		return 16, nil
	}
	return p.protocol.Get(g, id, b)
}
func (p *initialization) Set(g win32.GUID, id uint32, b []byte) error {
	if id == propInit {
		p.key = append([]byte(nil), b...)
		return nil
	}
	return p.protocol.Set(g, id, b)
}
func TestInitializationChallengeWireOrder(t *testing.T) {
	seed := []byte{0x61, 0xd8, 0x56, 0x3d, 0xc1, 0x15, 0x46, 0x68, 0xb2, 0xec, 0x6f, 0xa9, 0xed, 0x45, 0x33, 0x81}
	p := new(initialization)
	for i := range p.nonce {
		p.nonce[i] = seed[i] ^ byte(i)
	}
	if _, e := Open(p); e != nil {
		t.Fatal(e)
	}
	want := []byte{0x7f, 0x1d, 0x11, 0x13, 0x17, 0x4a, 0x94, 0xe3, 0x8b, 0xa7, 0x07, 0xf3, 0xc5, 0x30, 0x2b, 0x4d}
	if len(p.key) != 32 || !bytes.Equal(p.key[:16], want[:16]) || !bytes.Equal(p.key[16:], streamSetup[:]) {
		t.Fatalf("initialization packet %x want %x", p.key, want)
	}
}
func TestIncompleteChallengeDoesNotInitialize(t *testing.T) {
	p := &initialization{short: true}
	if _, e := Open(p); e == nil {
		t.Fatal("accepted incomplete nonce")
	}
	if p.key != nil {
		t.Fatal("initialized using incomplete nonce")
	}
}

func TestSatelliteFrequencyUnits(t *testing.T) {
	for _, frequency := range []int32{1049480, 11727480} {
		p := Profile(tuning.TuneProfile{Delivery: tuning.DeliverySatellite, System: "ISDBS", FrequencyKHz: frequency, StreamID: 0x4010})
		if p.FrequencyKHz != 11727480 || p.Delivery != tuning.DeliveryTerrestrial || p.BandwidthMHz != 6 || p.StreamID != -1 {
			t.Fatalf("profile: %+v", p)
		}
	}
}
func TestCaptureCannotComeFromAnotherUSBInstance(t *testing.T) {
	var tunerGUID, captureGUID string
	for guid, known := range filters {
		tunerGUID, captureGUID = guid, known.capture
		break
	}
	path := "@device:pnp:usb#vid_0b06&pid_0006&mi_00#instance"
	tuner := vendors.Tuner{Path: path + "#{category}\\" + tunerGUID}
	capture := vendors.Tuner{Path: path + "#{capture}\\" + captureGUID}
	if !MatchCapture(tuner, capture) {
		t.Fatal("same instance did not match")
	}
	capture.Path = strings.Replace(capture.Path, "#instance#", "#instance1#", 1)
	if MatchCapture(tuner, capture) {
		t.Fatal("another instance matched")
	}
}

type blockingProtocol struct {
	protocol
	entered, release chan struct{}
}

func (p *blockingProtocol) Set(g win32.GUID, id uint32, b []byte) error {
	if id == propTSID {
		close(p.entered)
		<-p.release
	}
	return p.protocol.Set(g, id, b)
}
func TestCloseWaitsForPropertyExchange(t *testing.T) {
	p := &blockingProtocol{entered: make(chan struct{}), release: make(chan struct{})}
	c, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	decoded := make(chan error, 1)
	go func() { decoded <- c.Select(0x1234) }()
	<-p.entered
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
		close(p.release)
		t.Fatal("closed while a driver exchange was active")
	case <-time.After(25 * time.Millisecond):
	}
	close(p.release)
	if e := <-decoded; e != nil {
		t.Fatal(e)
	}
	<-closed
	if e := c.RestoreSync(packets(1)); e != io.ErrClosedPipe {
		t.Fatalf("after close: %v", e)
	}
}

type invalidProperty struct {
	protocol
	id    uint32
	short bool
}

func (p *invalidProperty) Get(g win32.GUID, id uint32, b []byte) (uint32, error) {
	n, e := p.protocol.Get(g, id, b)
	if id == p.id {
		if p.short {
			return n - 1, e
		}
		b[0] ^= 1
	}
	return n, e
}
func TestRejectsWrongSetup(t *testing.T) {
	for _, p := range []*invalidProperty{{id: propSeed, short: true}, {id: propSeed}} {
		if _, e := Open(p); e == nil {
			t.Fatalf("accepted invalid property %d", p.id)
		}
	}
}
func TestRestoreSyncRejectsMalformedPackets(t *testing.T) {
	c, e := Open(new(protocol))
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range [][]byte{make([]byte, 188), make([]byte, 189)} {
		if e := c.RestoreSync(b); e == nil {
			t.Fatal("accepted malformed packets")
		}
	}
}

// signalDriver answers the signal level item with a fixed value.
type signalDriver struct{ level uint32 }

func (d signalDriver) Get(_ win32.GUID, id uint32, b []byte) (uint32, error) {
	if id != propSignalLevel || len(b) != 4 {
		return 0, errors.New("unexpected item")
	}
	binary.LittleEndian.PutUint32(b, d.level)
	return 4, nil
}

func (signalDriver) Set(win32.GUID, uint32, []byte) error { return errors.New("unexpected set") }

func TestSignalLevelIsHundredthsOfADecibel(t *testing.T) {
	cnr, measured, err := (&Control{properties: signalDriver{level: 2581}}).SignalLevel()
	if err != nil || !measured || cnr != 25.81 {
		t.Fatalf("C/N %v, measured %v, %v", cnr, measured, err)
	}
	if _, measured, err := (&Control{properties: signalDriver{}}).SignalLevel(); err != nil || measured {
		t.Fatalf("a zero level counted as a measurement: %v", err)
	}
}
