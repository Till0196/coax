// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbfs

import (
	"errors"
	"time"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

// An isochronous ring is not taken down when its stream ends, only parked: left
// running with nobody reading it, so the next stream on the same endpoint picks
// it up as it is. The ring is withdrawn for good only when the device is closed
// or its interface is put in another setting.
//
// Withdrawing a ring is slow, and not by anything this program does. usbfs
// withdraws a URB with usb_kill_urb, which waits for the URB to be handed back,
// and the first one withdrawn from a running endpoint makes the host controller
// stop it. The controller does that at once and returns the URB, but the USB
// core hands it on from a tasklet, and measured under KVM that tasklet ran up
// to 1.25 s after the controller was done -- all of which the stream's owner
// sat through, holding the tuner. A ring left running needs no withdrawing:
// stopping a stream is telling the device to stop sending, and starting the
// next is telling it to start, with the endpoint read throughout, so its FIFO
// never fills either.
//
// While parked the ring is kept turning by a goroutine of its own, putting each
// transfer back as it comes in, so the chain of transfers never runs out. It
// costs a few hundred empty transfers a second for as long as the device stays
// open between streams.

// parkedRing is a ring kept running between streams, and the goroutine keeping
// it so.
type parkedRing struct {
	ring *usbfsIsoRing
	stop chan struct{}
	done chan struct{}
}

// keepReadTimeoutMS is how long the keeper waits on one transfer before checking
// whether it has been asked to stop. Transfers come back every two milliseconds,
// so this only bounds a ring that has stalled.
const keepReadTimeoutMS = 20

// keepMostFailures is how many reads in a row may fail before the keeper stops
// keeping the ring: a second or two of nothing but failures, which is far past
// a passing upset.
const keepMostFailures = 1000

// park leaves a ring running with nobody reading it.
func (opened *usbfsDevice) park(ring *usbfsIsoRing) {
	opened.parkMu.Lock()
	defer opened.parkMu.Unlock()
	if opened.closing {
		// The device is closed or being closed, so there is no next stream to
		// keep the ring for.
		_ = ring.withdraw()
		return
	}
	if opened.parked != nil && opened.parked.ring != ring {
		opened.dropParkedLocked()
	}
	parked := &parkedRing{ring: ring, stop: make(chan struct{}), done: make(chan struct{})}
	opened.parked = parked
	go parked.keep()
}

func (parked *parkedRing) keep() {
	defer close(parked.done)
	scratch := make([]byte, parked.ring.packetSize*parked.ring.packets)
	failures := 0
	for {
		select {
		case <-parked.stop:
			return
		default:
		}
		// A transfer that failed is put back like any other, but a ring that
		// does nothing but fail is not kept: a device that has gone, or one closed
		// under the ring, fails every read at once, and turning it would only
		// spin. A ring left alone is still withdrawn or taken up as usual; a
		// stream that takes it up puts its transfers back as it reads.
		_, err := parked.ring.Read(scratch, keepReadTimeoutMS)
		switch {
		case err == nil, errors.Is(err, backend.ErrTimeout):
			failures = 0
		case errors.Is(err, backend.ErrGone):
			return
		default:
			failures++
			if failures >= keepMostFailures {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
}

// halt stops the keeper and waits for it, after which the ring is the caller's.
func (parked *parkedRing) halt() {
	close(parked.stop)
	<-parked.done
}

// unpark hands back the parked ring if it is on this endpoint and of this shape,
// and nil otherwise. A ring parked on anything else is withdrawn, since a new
// one is about to take the endpoint.
func (opened *usbfsDevice) unpark(endpoint byte, packetSize, packets int) *usbfsIsoRing {
	opened.parkMu.Lock()
	defer opened.parkMu.Unlock()
	parked := opened.parked
	if parked == nil {
		return nil
	}
	ring := parked.ring
	if ring.endpoint != endpoint || ring.packetSize != packetSize || ring.packets != packets {
		opened.dropParkedLocked()
		return nil
	}
	parked.halt()
	opened.parked = nil
	return ring
}

// dropParked withdraws the parked ring for good, if there is one.
func (opened *usbfsDevice) dropParked() {
	opened.parkMu.Lock()
	defer opened.parkMu.Unlock()
	opened.dropParkedLocked()
}

func (opened *usbfsDevice) dropParkedLocked() {
	if opened.parked == nil {
		return
	}
	opened.parked.halt()
	_ = opened.parked.ring.withdraw()
	opened.parked = nil
}

// hasParked reports whether a ring is parked.
func (opened *usbfsDevice) hasParked() bool {
	opened.parkMu.Lock()
	defer opened.parkMu.Unlock()
	return opened.parked != nil
}
