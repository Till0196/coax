// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

const (
	CodeNUL  = 0x00
	CodeBEL  = 0x07
	CodeAPB  = 0x08
	CodeAPF  = 0x09
	CodeAPD  = 0x0a
	CodeAPU  = 0x0b
	CodeCS   = 0x0c
	CodeAPR  = 0x0d
	CodeLS1  = 0x0e
	CodeLS0  = 0x0f
	CodePAPF = 0x16
	CodeCAN  = 0x18
	CodeSS2  = 0x19
	CodeESC  = 0x1b
	CodeAPS  = 0x1c
	CodeSS3  = 0x1d
	CodeRS   = 0x1e
	CodeUS   = 0x1f

	CodeBKF   = 0x80
	CodeRDF   = 0x81
	CodeGRF   = 0x82
	CodeYLF   = 0x83
	CodeBLF   = 0x84
	CodeMGF   = 0x85
	CodeCNF   = 0x86
	CodeWHF   = 0x87
	CodeSSZ   = 0x88
	CodeMSZ   = 0x89
	CodeNSZ   = 0x8a
	CodeSZX   = 0x8b
	CodeCOL   = 0x90
	CodeFLC   = 0x91
	CodeCDC   = 0x92
	CodePOL   = 0x93
	CodeWMM   = 0x94
	CodeMACRO = 0x95
	CodeHLC   = 0x97
	CodeRPC   = 0x98
	CodeSPL   = 0x99
	CodeSTL   = 0x9a
	CodeCSI   = 0x9b
	CodeTIME  = 0x9d
)

const (
	CSISWF = 0x53
	CSISDF = 0x56
	CSISSM = 0x57
	CSISHS = 0x58
	CSISVS = 0x59
	CSISDP = 0x5f
	CSIORN = 0x63
)

const (
	ORNNone    = '0'
	ORNHemming = '1'
)

const (
	ls2Esc  = 0x6e
	ls3Esc  = 0x6f
	ls1REsc = 0x7e
	ls2REsc = 0x7d
	ls3REsc = 0x7c
)

func controlLength(b []byte, i int) int {
	rest := len(b) - i
	clamp := func(n int) int {
		if n > rest {
			return rest
		}
		return n
	}
	switch b[i] {
	case CodePAPF, CodeSZX, CodeFLC, CodePOL, CodeWMM, CodeHLC, CodeRPC:
		return clamp(2)
	case CodeAPS:
		return clamp(3)
	case CodeCOL, CodeCDC:
		if rest > 1 && b[i+1] == 0x20 {
			return clamp(3)
		}
		return clamp(2)
	case CodeMACRO:
		for n := 1; n < rest; n++ {
			if b[i+n] == CodeMACRO {
				return clamp(n + 2)
			}
		}
		return rest
	case CodeCSI:
		n := 1
		for n < rest && b[i+n] >= 0x20 && b[i+n] <= 0x3f {
			n++
		}
		if n < rest && b[i+n] >= 0x40 && b[i+n] <= 0x7e {
			n++
		}
		return n
	case CodeTIME:
		if rest > 1 && b[i+1] == 0x20 {
			return clamp(3)
		}
		n := 1
		for n < rest && b[i+n] >= 0x28 && b[i+n] <= 0x3b {
			if b[i+n] == 0x29 {
				n++
				break
			}
			n++
		}
		return n
	}
	return 1
}
