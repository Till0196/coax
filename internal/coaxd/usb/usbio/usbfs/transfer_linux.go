// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// The pieces of usbfs's transfers with no ioctl in them, kept apart from
// usbfs_linux.go because they are testable on their own.

package usbfs

import "unsafe"

// isoPacketDescriptor is one frame of an isochronous transfer: struct
// usbdevfs_iso_packet_desc as the kernel declares it.
type isoPacketDescriptor struct {
	Length       uint32
	ActualLength uint32
	Status       uint32
}

// packFrames copies the frames that carried data to the front of the caller's
// buffer, so empty frames take up no space and the caller sees a continuous
// stream. Source and destination may be the same slice, which is what a plain
// read passes: the kernel fills the caller's buffer directly.
func packFrames(destination []byte, source []byte, frames []isoPacketDescriptor, packetSize int) int {
	transferred := 0
	for index := range frames {
		length := int(frames[index].ActualLength)
		if length > packetSize {
			length = packetSize
		}
		if length != 0 {
			copy(destination[transferred:], source[index*packetSize:index*packetSize+length])
		}
		transferred += length
	}
	return transferred
}

// bufferPointer is the address of a slice's first byte, or nil when it is empty.
func bufferPointer(buffer []byte) unsafe.Pointer {
	if len(buffer) == 0 {
		return nil
	}
	return unsafe.Pointer(&buffer[0])
}
