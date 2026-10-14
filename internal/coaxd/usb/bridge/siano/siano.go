// Copyright (c) 2005-2009 Siano Mobile Silicon, Uri Shkolnik, Anatoly Greenblat;
// 2008 Michael Krufky; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from the Linux siano driver (smsusb.c, smscoreapi.c, smsdvb-main.c).

// Package siano drives the Siano SMS1xxx family of single-chip MDTV receivers.
//
// There is no I2C bus to a separate demodulator or tuner: the chip runs
// downloadable firmware driven by a message protocol over two bulk endpoints.
// Requests go out on endpoint 2, which the protocol fixes rather than the
// descriptors. Responses, unsolicited indications and the transport stream all
// come back interleaved on one IN endpoint, so a single reader fans them out by
// message type.
package siano

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/pkg/usbio"
)

// HeaderSize covers the header every message begins with: message type, source
// and destination task, total length and flags.
const HeaderSize = 8

// maxPayload is the largest firmware chunk one download message may carry.
const maxPayload = 240

// bufferSize is the read size the firmware expects for this device family.
const bufferSize = 0x2000

// outEndpoint carries requests, addressed by number rather than read from the
// interface descriptor.
const outEndpoint = 0x02

// hifTask is the firmware task every request is addressed to.
const hifTask = 11

// sendTimeoutMS bounds the bulk transfer carrying one outgoing message.
const sendTimeoutMS = 2000

// readPollTimeoutMS is how long each bulk read waits before the reader checks
// whether the device is being closed.
const readPollTimeoutMS = 100

// readDepth is how many reads are kept outstanding on the IN endpoint. With one
// read at a time the chip's FIFO holds the stream while the reader is away, and
// it does not hold much: measured on a MyGica S270 under Windows, a pause of 5
// to 35 ms between reads lost packets inside the device, a few times a minute.
// The vendor driver keeps ten reads outstanding for the same reason.
const readDepth = 16

// ControlMsgID is the source identifier tuning and filter requests carry, so the
// firmware routes their responses back as control messages.
const ControlMsgID = 201

// flagSplitMessage marks a response whose body was moved further into the
// buffer to keep it aligned with the endpoint's packet size.
const flagSplitMessage = 4

// Message types exchanged with the firmware.
const (
	MsgRFTuneReq            = 561
	MsgRFTuneRes            = 562
	MsgInitDeviceReq        = 578
	MsgInitDeviceRes        = 579
	MsgAddPIDFilterReq      = 601
	MsgAddPIDFilterRes      = 602
	MsgRemovePIDFilterReq   = 603
	MsgRemovePIDFilterRes   = 604
	MsgGetStatisticsReq     = 615
	MsgGetStatisticsRes     = 616
	MsgGetStatisticsExReq   = 653
	MsgGetStatisticsExRes   = 654
	MsgDataDownloadReq      = 660
	MsgDataDownloadRes      = 661
	MsgDataValidityReq      = 662
	MsgDataValidityRes      = 663
	MsgSWDownloadTriggerReq = 664
	MsgSWDownloadTriggerRes = 665
	MsgGetVersionExReq      = 668
	MsgGetVersionExRes      = 669
	MsgDVBTBDAData          = 693
	MsgSWReloadStartReq     = 702
	MsgSWReloadStartRes     = 703
	MsgSWReloadExecReq      = 704
	MsgISDBTTuneReq         = 776
	MsgISDBTTuneRes         = 777
	MsgSignalDetectedInd    = 827
	MsgNoSignalInd          = 828
)

// PIDAll asks for the whole transport stream rather than one elementary stream,
// the same value DVB uses for a full-TS feed.
const PIDAll = 0x2000

// Bandwidth codes the tuning requests carry. This is the firmware's own
// enumeration, so the DVB-T widths and the ISDB-T segment counts share it.
const (
	Bandwidth8MHz       = 0
	Bandwidth7MHz       = 1
	Bandwidth6MHz       = 2
	BandwidthISDBT1Seg  = 4
	BandwidthISDBT3Seg  = 5
	BandwidthISDBT13Seg = 8
)

// ErrClosed reports that the device was closed while a request was in flight.
var ErrClosed = errors.New("the Siano device is closed")

// Device is an opened Siano receiver.
type Device struct {
	usb        *usbio.Transport
	inEndpoint byte
	// ring keeps the IN endpoint's reads outstanding; see readDepth.
	ring *usbio.Stream
	// alignment is where a split response's body starts, derived from the IN
	// endpoint's packet size.
	alignment int

	// exchangeMu serialises request/response pairs: a response is matched to
	// its request by message type alone, so only one may be in flight.
	exchangeMu sync.Mutex

	mu      sync.Mutex
	waiters map[uint16]chan []byte
	locked  bool
	closed  bool

	data chan []byte
	stop chan struct{}
	done chan struct{}
}

// New wraps an opened USB device and starts reading from it; Close releases the
// transport too.
func New(transport *usbio.Transport) (*Device, error) {
	endpoints, err := transport.Endpoints(0)
	if err != nil {
		return nil, fmt.Errorf("read Siano endpoints: %w", err)
	}
	device := &Device{
		usb:     transport,
		waiters: make(map[uint16]chan []byte),
		data:    make(chan []byte, 64),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	for _, endpoint := range endpoints {
		if endpoint.In() && endpoint.Type == usbio.TransferBulk {
			device.inEndpoint = endpoint.Address
			// A split message repeats its header one packet in, less the
			// header the firmware already wrote at the head of the buffer.
			device.alignment = int(endpoint.MaxPacketSize) - HeaderSize
			break
		}
	}
	if device.inEndpoint == 0 {
		return nil, errors.New("the Siano device exposes no bulk IN endpoint")
	}
	device.ring, err = transport.OpenBulkStream(device.inEndpoint, bufferSize, readDepth, readPollTimeoutMS)
	if err != nil {
		return nil, fmt.Errorf("keep reads outstanding on the Siano IN endpoint: %w", err)
	}
	go device.read()
	return device, nil
}

// read pulls messages from the IN endpoint until the device is closed.
//
// Each read is handed a buffer of exactly one transfer, so the ring gives back
// one transfer and with it one message: a message ends in a short packet, and
// dispatch has no other way of telling where one stops and the next begins.
func (device *Device) read() {
	defer close(device.done)
	defer device.ring.Close()
	buffer := make([]byte, bufferSize)
	for {
		select {
		case <-device.stop:
			return
		default:
		}
		count, err := device.ring.Read(buffer, readPollTimeoutMS)
		if count > 0 {
			device.dispatch(buffer[:count])
		}
		if err != nil && !errors.Is(err, usbio.ErrTimeout) {
			// The device went away. Requests in flight fail on their timeout.
			return
		}
	}
}

// dispatch routes one received message to whoever is waiting for it.
func (device *Device) dispatch(raw []byte) {
	if len(raw) < HeaderSize {
		return
	}
	messageType := binary.LittleEndian.Uint16(raw)
	length := int(binary.LittleEndian.Uint16(raw[4:]))
	flags := binary.LittleEndian.Uint16(raw[6:])
	if length < HeaderSize || length > len(raw) {
		return
	}
	offset := 0
	if device.alignment > 0 && flags&flagSplitMessage != 0 {
		offset = device.alignment + int((flags>>8)&3)
		if offset+length > len(raw) {
			return
		}
	}
	body := raw[offset+HeaderSize : offset+length]

	switch messageType {
	case MsgDVBTBDAData:
		select {
		case device.data <- append([]byte(nil), body...):
		default: // The consumer is behind; dropping keeps the reader moving.
		}
		return
	case MsgSignalDetectedInd:
		device.setLocked(true)
		return
	case MsgNoSignalInd:
		device.setLocked(false)
		return
	}

	device.mu.Lock()
	waiter, waiting := device.waiters[messageType]
	if waiting {
		delete(device.waiters, messageType)
	}
	device.mu.Unlock()
	if waiting {
		waiter <- append([]byte(nil), body...)
	}
}

func (device *Device) setLocked(locked bool) {
	device.mu.Lock()
	device.locked = locked
	device.mu.Unlock()
}

// Locked reports the lock state the firmware last announced. It sends these
// unprompted after a tune, and again whenever the signal comes or goes.
func (device *Device) Locked() bool {
	device.mu.Lock()
	defer device.mu.Unlock()
	return device.locked
}

// ClearLock forgets the announced lock state, so a fresh tune is not credited
// with the previous channel's signal.
func (device *Device) ClearLock() { device.setLocked(false) }

// Send writes one message and does not wait for an answer.
func (device *Device) Send(messageType uint16, sourceID byte, payload []byte) error {
	message := make([]byte, HeaderSize+len(payload))
	binary.LittleEndian.PutUint16(message, messageType)
	message[2] = sourceID
	message[3] = hifTask
	binary.LittleEndian.PutUint16(message[4:], uint16(len(message)))
	copy(message[HeaderSize:], payload)
	written, err := device.usb.Bulk(outEndpoint, message, sendTimeoutMS)
	if err != nil {
		return fmt.Errorf("send Siano message %d: %w", messageType, err)
	}
	if written != len(message) {
		return fmt.Errorf("send Siano message %d: wrote %d of %d bytes", messageType, written, len(message))
	}
	return nil
}

// Exchange sends a request and returns the body of the matching response.
func (device *Device) Exchange(request, response uint16, sourceID byte, payload []byte, timeout time.Duration) ([]byte, error) {
	device.exchangeMu.Lock()
	defer device.exchangeMu.Unlock()

	waiter := make(chan []byte, 1)
	device.mu.Lock()
	if device.closed {
		device.mu.Unlock()
		return nil, ErrClosed
	}
	device.waiters[response] = waiter
	device.mu.Unlock()
	defer func() {
		device.mu.Lock()
		delete(device.waiters, response)
		device.mu.Unlock()
	}()

	if err := device.Send(request, sourceID, payload); err != nil {
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case body := <-waiter:
		return body, nil
	case <-timer.C:
		return nil, fmt.Errorf("timed out waiting for Siano message %d", response)
	case <-device.stop:
		return nil, ErrClosed
	}
}

// Data is the stream of transport-stream blocks the firmware sends once a PID
// filter is in place.
func (device *Device) Data() <-chan []byte { return device.data }

// DrainData discards blocks buffered from a previous channel.
func (device *Device) DrainData() {
	for {
		select {
		case <-device.data:
		default:
			return
		}
	}
}

// Name reports the USB bus:address the device was found at.
func (device *Device) Name() string { return device.usb.Name() }

// ID reports the USB vendor and product pair the device enumerated with.
func (device *Device) ID() usbio.ID { return device.usb.ID() }

// Close stops the reader and releases the USB device.
func (device *Device) Close() error {
	device.mu.Lock()
	if device.closed {
		device.mu.Unlock()
		return nil
	}
	device.closed = true
	device.mu.Unlock()
	close(device.stop)
	<-device.done
	return device.usb.Close()
}

// Words packs a payload of 32-bit words, the form most requests take.
func Words(values ...uint32) []byte {
	payload := make([]byte, 4*len(values))
	for index, value := range values {
		binary.LittleEndian.PutUint32(payload[4*index:], value)
	}
	return payload
}
