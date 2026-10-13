// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbfs

import "testing"

// TestPackFramesDropsEmptyFrames: empty frames take up no space in the result.
func TestPackFramesDropsEmptyFrames(t *testing.T) {
	const packetSize = 4
	source := []byte{
		1, 2, 3, 4,
		9, 9, 9, 9,
		5, 6, 0, 0,
	}
	frames := []isoPacketDescriptor{{ActualLength: 4}, {ActualLength: 0}, {ActualLength: 2}}
	destination := make([]byte, len(source))
	got := packFrames(destination, source, frames, packetSize)
	if got != 6 {
		t.Fatalf("packFrames returned %d, want 6", got)
	}
	want := []byte{1, 2, 3, 4, 5, 6}
	for index, value := range want {
		if destination[index] != value {
			t.Fatalf("packed = %v, want %v", destination[:got], want)
		}
	}
}

// TestPackFramesClampsAnOverlongFrame guards against a device reporting more
// than it was asked for, which would read past the frame.
func TestPackFramesClampsAnOverlongFrame(t *testing.T) {
	const packetSize = 2
	source := []byte{1, 2, 3, 4}
	frames := []isoPacketDescriptor{{ActualLength: 99}, {ActualLength: 2}}
	destination := make([]byte, len(source))
	if got := packFrames(destination, source, frames, packetSize); got != 4 {
		t.Fatalf("packFrames returned %d, want 4", got)
	}
}
