// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbio

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

// The errors a caller sees, re-exported from the backend package.
var (
	ErrTimeout     = backend.ErrTimeout
	ErrUnsupported = backend.ErrUnsupported
	ErrGone        = backend.ErrGone
	ErrNoDevices   = backend.ErrNoDevices
)

var errClosed = errors.New("USB device is closed")

// ID is a USB vendor/product pair. Each board driver lists the IDs it claims.
type ID = backend.ID

// IDs builds the ID list a board driver claims from one vendor number and the
// product numbers keying its model map, so the two cannot drift apart.
func IDs[Model any](vendor uint16, models map[uint16]Model) []ID {
	ids := make([]ID, 0, len(models))
	for product := range models {
		ids = append(ids, ID{Vendor: vendor, Product: product})
	}
	return ids
}

// Transport is an opened USB device: the raw control, bulk and isochronous
// endpoints that bridge chips are driven through. Transfers may run
// concurrently, so one board's several tuners can stream at once; chips needing
// an atomic sequence serialise it themselves, as the bridge drivers do.
type Transport struct {
	device    backend.Device
	name      string
	vendorID  uint16
	productID uint16
	closed    bool
	// mu guards the closed flag only, not the transfers: serialising those would
	// stop a multi-tuner board working in parallel. Close takes the write lock so
	// it cannot run while a transfer is in flight.
	mu sync.RWMutex
}

// Open claims interface 0 of the first device matching one of the supported IDs.
// The selector is empty for "any", a "bus:address" string, or a match index.
func Open(selector string, supported []ID) (*Transport, error) {
	devices, err := enumerate()
	if err != nil {
		return nil, err
	}
	defer backend.Drop(devices)
	matchedIndex := 0
	for _, candidate := range devices {
		if !candidate.ID.In(supported) {
			continue
		}
		if candidate.Held != "" {
			// Left out as Devices leaves it out, and so out of the count an index
			// selector is taken against; named outright, it is told why.
			if strings.TrimSpace(selector) == candidate.Location {
				return nil, heldError(candidate)
			}
			continue
		}
		if !selected(selector, candidate.Location, matchedIndex) {
			matchedIndex++
			continue
		}
		opened, err := candidate.Open()
		if err != nil {
			return nil, fmt.Errorf("open USB device %s: %w", candidate.Location, err)
		}
		return &Transport{
			device:    opened,
			name:      candidate.Location,
			vendorID:  candidate.ID.Vendor,
			productID: candidate.ID.Product,
		}, nil
	}
	if selector == "" {
		return nil, errors.New("no supported USB device found")
	}
	return nil, fmt.Errorf("USB device %q was not found or is not supported", selector)
}

// heldError refuses a device another driver is bound to, before anything is
// sent to that driver.
func heldError(candidate backend.Found) error {
	return fmt.Errorf("USB device %s is bound to %s, not to a driver this program claims through; use the backend for that driver, or bind the device to WinUSB on Windows or unbind it on Linux (docs/setup/os.md)",
		candidate.Location, candidate.Held)
}

// Resolve finds the device a selector picks, by the same rule as Open, without
// opening it.
func Resolve(selector string, supported []ID) (Attached, error) {
	attached, err := Devices()
	if err != nil {
		return Attached{}, err
	}
	matchedIndex := 0
	for _, candidate := range attached {
		if !candidate.ID.In(supported) {
			continue
		}
		if !selected(selector, candidate.Location, matchedIndex) {
			matchedIndex++
			continue
		}
		return candidate, nil
	}
	if selector == "" {
		return Attached{}, errors.New("no supported USB device found")
	}
	return Attached{}, fmt.Errorf("USB device %q was not found or is not supported", selector)
}

// Reset puts a device through a bus reset, restarting the silicon while it keeps
// its address on the bus. Closing the handle clears none of the bridge chip's
// state, so the next open walks into whatever the last one left -- on some
// boards a chip that no longer acknowledges its own I2C address. It is not a
// plugging cycle: a device whose USB core has stopped answering stays stuck (see
// docs/setup/os.md). Reports ErrUnsupported where the backend cannot ask for
// one, which is everywhere but macOS.
func Reset(selector string, supported []ID) error {
	devices, err := enumerate()
	if err != nil {
		return err
	}
	defer backend.Drop(devices)
	matchedIndex := 0
	for _, candidate := range devices {
		if !candidate.ID.In(supported) {
			continue
		}
		if !selected(selector, candidate.Location, matchedIndex) {
			matchedIndex++
			continue
		}
		if candidate.Reset == nil {
			return ErrUnsupported
		}
		if err := candidate.Reset(); err != nil {
			return err
		}
		// The device stays on the bus, but its firmware is not ready the instant
		// the call returns.
		time.Sleep(resetSettle)
		return nil
	}
	return fmt.Errorf("USB device %q was not found or is not supported", selector)
}

// resetSettle is how long a device is left alone after a bus reset: the USB
// core answers again long before the bridge chip behind it does.
const resetSettle = 250 * time.Millisecond

// Attached describes a USB device as reported without opening it.
type Attached struct {
	ID ID
	// Location is the "bus:address" name, which doubles as the selector.
	Location string
	// Serial is the device's serial number when the operating system could report
	// it unopened: Linux (sysfs), macOS (the IOKit registry) and Windows (the
	// device instance name) all know it. Empty where the device carries none, or
	// one made only of zeros (see [Meaningful]).
	Serial string
	// Port is the physical path from the root hub, "1-2.3". Unlike bus and
	// address it survives a re-enumeration and changes only when the device is
	// moved to another socket, so a stable name falls back to it without a serial.
	Port string
}

// Devices lists every USB device present that this program could claim.
// Nothing is opened and no interface is claimed, so this is safe against devices
// owned by other drivers; one that is so owned is left out, since the backend
// for that driver lists it already and claiming it here cannot work.
func Devices() ([]Attached, error) {
	devices, err := enumerate()
	if err != nil {
		return nil, err
	}
	attached := make([]Attached, 0, len(devices))
	for _, candidate := range devices {
		if candidate.Release != nil {
			candidate.Release()
		}
		if candidate.Held != "" {
			continue
		}
		attached = append(attached, Attached{
			ID:       candidate.ID,
			Location: candidate.Location,
			Serial:   Meaningful(candidate.Serial),
			Port:     candidate.Port,
		})
	}
	return attached, nil
}

// Meaningful gives a serial number back, or nothing where it is only zeros: a
// maker that writes "0" or "00000000" into every unit's descriptor has given
// them all the same serial, and a name built from it would not tell two boards
// apart. Such a board is named by the socket it is in instead.
func Meaningful(serial string) string {
	if strings.Trim(serial, "0") == "" {
		return ""
	}
	return serial
}

// Name reports the USB bus:address the device was found at.
func (transport *Transport) Name() string { return transport.name }

// ProductID reports the USB product number the device enumerated with.
func (transport *Transport) ProductID() uint16 { return transport.productID }

// VendorID reports the USB vendor number the device enumerated with.
func (transport *Transport) VendorID() uint16 { return transport.vendorID }

// ID reports the vendor and product pair the device enumerated with, for boards
// that share product numbers across vendors.
func (transport *Transport) ID() ID {
	return ID{Vendor: transport.vendorID, Product: transport.productID}
}

// Descriptor requests, as chapter 9 of the USB specification numbers them.
const (
	requestGetDescriptor  = 0x06
	descriptorTypeDevice  = 0x01
	descriptorTypeString  = 0x03
	descriptorTimeoutMS   = 500
	descriptorTypeShift   = 8
	serialIndexOffset     = 16
	deviceDescriptorBytes = 18
	// languageEnglishUS is the only language a tuner is worth asking for.
	languageEnglishUS = 0x0409
)

// Serial reads the device's serial-number string, empty when it carries none.
// An enclosure enumerating as several devices numbers them within one serial.
func (transport *Transport) Serial() (string, error) {
	descriptor := make([]byte, deviceDescriptorBytes)
	if _, err := transport.getDescriptor(descriptorTypeDevice, 0, 0, descriptor); err != nil {
		return "", fmt.Errorf("read the USB device descriptor: %w", err)
	}
	index := descriptor[serialIndexOffset]
	if index == 0 {
		return "", nil
	}
	// The first two bytes are the descriptor's own length and type; the rest
	// is UTF-16, which for a serial number is always plain ASCII in practice.
	buffer := make([]byte, 255)
	read, err := transport.getDescriptor(descriptorTypeString, index, languageEnglishUS, buffer)
	if err != nil {
		return "", fmt.Errorf("read the USB serial number: %w", err)
	}
	if read < 2 {
		return "", nil
	}
	if length := int(buffer[0]); length < read {
		read = length
	}
	var serial []rune
	for offset := 2; offset+1 < read; offset += 2 {
		serial = append(serial, rune(uint16(buffer[offset])|uint16(buffer[offset+1])<<8))
	}
	return strings.TrimSpace(string(serial)), nil
}

func (transport *Transport) getDescriptor(kind, index byte, language uint16, buffer []byte) (int, error) {
	value := uint16(kind)<<descriptorTypeShift | uint16(index)
	return transport.Control(0x80, requestGetDescriptor, value, language, buffer, descriptorTimeoutMS)
}

// checkOpen reports errClosed once Close has run. Every transfer method calls it
// under the read lock, so a transfer can never race the close.
func (transport *Transport) checkOpen() error {
	if transport.closed {
		return errClosed
	}
	return nil
}

// Control runs a transfer on the default control pipe.
func (transport *Transport) Control(requestType, request byte, value, index uint16, buffer []byte, timeoutMS uint32) (int, error) {
	transport.mu.RLock()
	defer transport.mu.RUnlock()
	if err := transport.checkOpen(); err != nil {
		return 0, err
	}
	return transport.device.Control(requestType, request, value, index, buffer, timeoutMS)
}

// Bulk runs a transfer on a bulk endpoint, whose direction the address decides.
func (transport *Transport) Bulk(endpoint byte, buffer []byte, timeoutMS uint32) (int, error) {
	transport.mu.RLock()
	defer transport.mu.RUnlock()
	if err := transport.checkOpen(); err != nil {
		return 0, err
	}
	return transport.device.Bulk(endpoint, buffer, timeoutMS)
}

// Isochronous runs one isochronous transfer of up to packets frames, packing
// what arrived to the front of the buffer so the caller sees a continuous
// stream.
func (transport *Transport) Isochronous(endpoint byte, buffer []byte, packetSize, packets int, timeoutMS uint32) (int, error) {
	transport.mu.RLock()
	defer transport.mu.RUnlock()
	if err := transport.checkOpen(); err != nil {
		return 0, err
	}
	if packetSize <= 0 || packets <= 0 || len(buffer) < packetSize {
		return 0, errors.New("invalid isochronous transfer size")
	}
	if maximum := len(buffer) / packetSize; packets > maximum {
		packets = maximum
	}
	return transport.device.Isochronous(endpoint, buffer, packetSize, packets, timeoutMS)
}

// Stream is a running endpoint that keeps several transfers outstanding, so it
// stays serviced while the reader is away with the data it last took.
//
// An isochronous endpoint has no queue and no retry: whatever the device sends
// while nothing is outstanding is lost, so reading one transfer at a time drops
// part of every lap. A bulk endpoint is flow-controlled instead, but the device
// behind it is not: a tuner fills a FIFO of a few kilobytes at the rate the
// multiplex arrives, and the host collects it only while a transfer is
// outstanding. Measured on a T230 carrying a 40 Mbps cable multiplex through
// Windows, reading one 256 KB transfer at a time lost a stretch of the stream
// once a minute, always two kilobytes into the next read: the FIFO's worth that
// had waited, then 0xff filler where the rest had overflowed.
type Stream struct {
	transport *Transport
	inner     backend.Stream
	closed    bool
	mu        sync.Mutex
}

// IsoStream is a running isochronous endpoint.
type IsoStream = Stream

// OpenIsoStream starts an isochronous endpoint with depth transfers of packets
// frames outstanding, returning ErrUnsupported where the backend has no ring.
// The caller closes the stream; reads take a buffer of at least
// packetSize*packets bytes, which is what one transfer can carry.
func (transport *Transport) OpenIsoStream(endpoint byte, packetSize, packets, depth int, timeoutMS uint32) (*IsoStream, error) {
	transport.mu.RLock()
	defer transport.mu.RUnlock()
	if err := transport.checkOpen(); err != nil {
		return nil, err
	}
	if packetSize <= 0 || packets <= 0 || depth <= 0 {
		return nil, errors.New("invalid isochronous stream size")
	}
	inner, err := transport.device.OpenIsoStream(endpoint, packetSize, packets, depth, timeoutMS)
	if err != nil {
		return nil, err
	}
	return &Stream{transport: transport, inner: inner}, nil
}

// OpenBulkStream starts reading a bulk endpoint with depth transfers of size
// bytes outstanding, returning ErrUnsupported where the backend has no ring.
// The caller closes the stream; reads take a buffer of at least size bytes.
func (transport *Transport) OpenBulkStream(endpoint byte, size, depth int, timeoutMS uint32) (*Stream, error) {
	transport.mu.RLock()
	defer transport.mu.RUnlock()
	if err := transport.checkOpen(); err != nil {
		return nil, err
	}
	if size <= 0 || depth <= 0 || endpoint&0x80 == 0 {
		return nil, errors.New("invalid bulk stream")
	}
	inner, err := transport.device.OpenBulkStream(endpoint, size, depth, timeoutMS)
	if err != nil {
		return nil, err
	}
	return &Stream{transport: transport, inner: inner}, nil
}

// Read packs what the oldest transfers carried to the front of the buffer.
// Returns ErrTimeout when nothing arrived.
func (stream *Stream) Read(buffer []byte, timeoutMS uint32) (int, error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		return 0, ErrGone
	}
	stream.transport.mu.RLock()
	defer stream.transport.mu.RUnlock()
	if err := stream.transport.checkOpen(); err != nil {
		return 0, err
	}
	return stream.inner.Read(buffer, timeoutMS)
}

// Close stops the endpoint and frees the transfers. A second call is harmless.
func (stream *Stream) Close() error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		return nil
	}
	stream.closed = true
	return stream.inner.Close()
}

// The shape of a bulk stream: sixteen transfers of 64 KB is a fifth of a second
// of the widest multiplex a USB tuner carries, and each transfer a whole number
// of high-speed packets.
const (
	bulkStreamSize  = 64 * 1024
	bulkStreamDepth = 16
)

// BulkReader reads a tuner's stream endpoint through a Stream where the backend
// has one, and a transfer at a time where it does not. The stream is opened by
// the first read and closed by Stop, so a bridge only has to say when its
// endpoint stops carrying anything.
type BulkReader struct {
	transport *Transport
	endpoint  byte

	mu     sync.Mutex
	stream *Stream
	// plain is set once the backend has said it has no ring.
	plain bool
	// carry holds what a transfer brought beyond a small caller's buffer.
	carry   []byte
	pending []byte
}

// BulkReader makes a reader for a stream endpoint. Nothing is opened yet.
func (transport *Transport) BulkReader(endpoint byte) *BulkReader {
	return &BulkReader{transport: transport, endpoint: endpoint}
}

// Start puts the reads in flight now rather than at the first Read. A bridge
// calls it just before it lets the stream go: the device's FIFO fills from that
// moment, and whatever arrives before anything is outstanding to take it is
// what the stream begins with a hole in.
func (reader *BulkReader) Start() error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.open(streamTimeoutMS)
}

// streamTimeoutMS is what a stream opened by Start is given for its transfers.
const streamTimeoutMS = 1000

func (reader *BulkReader) open(timeoutMS uint32) error {
	if reader.stream != nil || reader.plain {
		return nil
	}
	stream, err := reader.transport.OpenBulkStream(reader.endpoint, bulkStreamSize, bulkStreamDepth, timeoutMS)
	switch {
	case errors.Is(err, ErrUnsupported):
		reader.plain = true
	case err != nil:
		return err
	default:
		reader.stream = stream
	}
	return nil
}

// Read takes the next bytes the endpoint carried. Returns ErrTimeout when the
// device had nothing ready.
func (reader *BulkReader) Read(buffer []byte, timeoutMS uint32) (int, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if len(reader.pending) != 0 {
		moved := copy(buffer, reader.pending)
		reader.pending = reader.pending[moved:]
		return moved, nil
	}
	if err := reader.open(timeoutMS); err != nil {
		return 0, err
	}
	if reader.plain {
		return reader.transport.Bulk(reader.endpoint, buffer, timeoutMS)
	}
	if len(buffer) >= bulkStreamSize {
		return reader.stream.Read(buffer, timeoutMS)
	}
	if reader.carry == nil {
		reader.carry = make([]byte, bulkStreamSize)
	}
	read, err := reader.stream.Read(reader.carry, timeoutMS)
	moved := copy(buffer, reader.carry[:read])
	reader.pending = reader.carry[moved:read]
	return moved, err
}

// Stop closes the stream, withdrawing whatever is still outstanding. The next
// Read opens it again.
func (reader *BulkReader) Stop() error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.pending = nil
	if reader.stream == nil {
		return nil
	}
	err := reader.stream.Close()
	reader.stream = nil
	return err
}

// SetAlternate switches an interface to one of its alternate settings, which is
// how a bridge offering both bulk and isochronous streaming is told which to use.
func (transport *Transport) SetAlternate(interfaceNumber, alternate int) error {
	transport.mu.RLock()
	defer transport.mu.RUnlock()
	if err := transport.checkOpen(); err != nil {
		return err
	}
	return transport.device.SetAlternate(interfaceNumber, alternate)
}

// Close releases the interface and the device. A second call is harmless.
func (transport *Transport) Close() error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.closed {
		return nil
	}
	transport.closed = true
	return transport.device.Close()
}

// selected reports whether a selector picks this device: empty takes the first
// match, "bus:address" names one exactly, a bare number takes the nth match.
func selected(selector, location string, index int) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" || selector == location {
		return true
	}
	parsed, err := strconv.Atoi(selector)
	return err == nil && parsed == index
}

// EndpointReader reads a stream endpoint and is told when the stream starts
// and stops.
type EndpointReader interface {
	Start() error
	Read(buffer []byte, timeoutMS uint32) (int, error)
	Stop() error
}

// BulkDevice is anything that runs bulk transfers.
type BulkDevice interface {
	Bulk(endpoint byte, buffer []byte, timeoutMS uint32) (int, error)
}

// ReaderOf reads a stream endpoint through a BulkReader where the device is a
// Transport, and a transfer at a time where it is something standing in for
// one, as a test's fake is.
func ReaderOf(device BulkDevice, endpoint byte) EndpointReader {
	if transport, ok := device.(*Transport); ok {
		return transport.BulkReader(endpoint)
	}
	return plainReader{device: device, endpoint: endpoint}
}

type plainReader struct {
	device   BulkDevice
	endpoint byte
}

func (reader plainReader) Read(buffer []byte, timeoutMS uint32) (int, error) {
	return reader.device.Bulk(reader.endpoint, buffer, timeoutMS)
}

func (plainReader) Start() error { return nil }

func (plainReader) Stop() error { return nil }
