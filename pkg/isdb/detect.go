// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

// DetectFormat says what a capture is: transport packets, TLV packets, or
// something this has no reading for.
func DetectFormat(buffer []byte) string {
	const packetSize = tsPacketSize
	const minimumPackets = 8
	for offset := 0; offset+packetSize*minimumPackets <= len(buffer); offset++ {
		if buffer[offset] != 0x47 {
			continue
		}
		valid := true
		for packet := 1; packet < minimumPackets; packet++ {
			if buffer[offset+packet*packetSize] != 0x47 {
				valid = false
				break
			}
		}
		if valid {
			return "ts"
		}
	}

	validTLVTypes := map[byte]bool{0x01: true, 0x02: true, 0x03: true, 0xfe: true, 0xff: true}
	for offset := 0; offset+4 <= len(buffer); offset++ {
		if buffer[offset] != 0x7f || !validTLVTypes[buffer[offset+1]] {
			continue
		}
		length := int(buffer[offset+2])<<8 | int(buffer[offset+3])
		next := offset + 4 + length
		if next+4 <= len(buffer) && buffer[next] == 0x7f && validTLVTypes[buffer[next+1]] {
			return "tlv"
		}
	}
	return "unknown"
}
