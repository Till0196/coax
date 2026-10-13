// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import "strings"

type DRCSResolver interface {
	DecodeDRCS(set byte, cell uint16) (rune, bool)
}

type Control struct {
	Code   byte
	Params []byte
	At     int
}

type Cell struct {
	Set   byte
	Bytes uint16
	At    int
}

type DecodeResult struct {
	Text        string
	Controls    []Control
	Undecodable []Cell
}

func (r DecodeResult) Lossless() bool { return len(r.Undecodable) == 0 }

type Decoder struct {
	DRCS DRCSResolver

	jis jisCodec
	g   [4]charset
	gl  int
	gr  int
	ss  int
	// normal is whether the characters are at the normal size, as a string
	// starts. SSZ and MSZ halve them, NSZ puts them back, SZX says which.
	normal bool

	text     strings.Builder
	runes    int
	controls []Control
	bad      []Cell
}

func NewDecoder() *Decoder {
	d := &Decoder{jis: newJISCodec()}
	d.reset()
	return d
}

func (d *Decoder) reset() {
	d.g = [4]charset{csKanji, csASCII, csHiragana, csKatakana}
	d.gl, d.gr, d.ss = 0, 2, 0
}

func (d *Decoder) Decode(b []byte) DecodeResult { return d.decode(b, true) }

func (d *Decoder) Continue(b []byte) DecodeResult { return d.decode(b, false) }

func DecodeString(b []byte) DecodeResult { return NewDecoder().Decode(b) }

func (d *Decoder) decode(b []byte, reset bool) DecodeResult {
	if reset {
		d.reset()
		d.normal = true
	}
	d.text.Reset()
	d.runes = 0
	d.controls = nil
	d.bad = nil

	for i := 0; i < len(b); {
		i += d.step(b, i)
	}
	return DecodeResult{
		Text:        d.text.String(),
		Controls:    d.controls,
		Undecodable: d.bad,
	}
}

func (d *Decoder) step(b []byte, i int) int {
	c := b[i]
	switch {
	case c == 0x00 || c == 0x7f || c == 0xff:
		return 1
	case c == CodeESC:
		return d.escape(b, i)
	case c < 0x20 || (c >= 0x80 && c <= 0x9f):
		return d.control(b, i)
	case c == 0x20 || c == 0xa0:
		// A space at the normal size occupies a full-width cell, and is
		// read as the ideographic space, as Mirakurun (aribts) reads it: a
		// name has to come out as the same string there and here.
		if d.normal {
			d.emit('\u3000')
		} else {
			d.emit(' ')
		}
		d.ss = 0
		return 1
	default:
		return d.graphic(b, i)
	}
}

func (d *Decoder) emit(r rune) {
	d.text.WriteRune(r)
	d.runes++
}

func (d *Decoder) graphic(b []byte, i int) int {
	c := b[i]
	idx := d.gl
	if d.ss != 0 {
		idx = d.ss
	} else if c&0x80 != 0 {
		idx = d.gr
	}
	d.ss = 0
	set := d.g[idx]

	if !set.twoByte() {
		d.character(set, uint16(c&0x7f), 1)
		return 1
	}
	if i+1 >= len(b) {
		d.bad = append(d.bad, Cell{Set: byte(set), Bytes: uint16(c & 0x7f), At: d.runes})
		return 1
	}
	if (b[i+1]^c)&0x80 != 0 {
		d.bad = append(d.bad, Cell{Set: byte(set), Bytes: uint16(c&0x7f) << 8, At: d.runes})
		return 1
	}
	cell := uint16(c&0x7f)<<8 | uint16(b[i+1]&0x7f)
	d.character(set, cell, 2)
	return 2
}

func (d *Decoder) character(set charset, cell uint16, width int) {
	if set.drcs() {
		if d.DRCS != nil {
			if r, ok := d.DRCS.DecodeDRCS(byte(set), cell); ok {
				d.emit(r)
				return
			}
		}
		d.bad = append(d.bad, Cell{Set: byte(set), Bytes: cell, At: d.runes})
		return
	}
	c1, c2 := byte(cell>>8), byte(cell)
	if width == 1 {
		c1, c2 = byte(cell), 0
	}
	if r, ok := set.decode(&d.jis, c1, c2); ok {
		// The alphanumerics are full-width at the normal size too, for the
		// same reason as the space.
		if width == 1 && d.normal {
			r = fullWidth(r)
		}
		d.emit(r)
		return
	}
	d.bad = append(d.bad, Cell{Set: byte(set), Bytes: cell, At: d.runes})
}

func (d *Decoder) escape(b []byte, i int) int {
	if i+1 >= len(b) {
		return 1
	}
	switch f := b[i+1]; f {
	case ls2Esc, ls3Esc:
		d.gl = 2
		if f == ls3Esc {
			d.gl = 3
		}
		d.ss = 0
		return 2
	case ls1REsc, ls2REsc, ls3REsc:
		switch f {
		case ls1REsc:
			d.gr = 1
		case ls2REsc:
			d.gr = 2
		default:
			d.gr = 3
		}
		d.ss = 0
		return 2
	case 0x24:
		return d.designateMulti(b, i)
	case 0x28, 0x29, 0x2a, 0x2b:
		return d.designateSingle(b, i, int(f-0x28), 2)
	}
	return 2
}

func (d *Decoder) designateSingle(b []byte, i, gidx, at int) int {
	if i+at >= len(b) {
		return len(b) - i
	}
	f := b[i+at]
	if f == 0x20 {
		if i+at+1 >= len(b) {
			return len(b) - i
		}
		if n := b[i+at+1]; n == 0x70 || (n >= 0x41 && n <= 0x4f) {
			d.g[gidx] = charset(n) | 0x80
		}
		return at + 2
	}
	if singleByteSet(charset(f)) {
		d.g[gidx] = charset(f)
	}
	return at + 1
}

func (d *Decoder) designateMulti(b []byte, i int) int {
	if i+2 >= len(b) {
		return len(b) - i
	}
	switch f := b[i+2]; {
	case twoByteSet(charset(f)):
		d.g[0] = charset(f)
		return 3
	case f >= 0x28 && f <= 0x2b:
		gidx := int(f - 0x28)
		if i+3 >= len(b) {
			return len(b) - i
		}
		if n := b[i+3]; n == 0x20 {
			if i+4 < len(b) && b[i+4] == 0x40 {
				d.g[gidx] = csDRCS0
			}
			return 5
		} else if twoByteSet(charset(n)) {
			d.g[gidx] = charset(n)
		}
		return 4
	}
	return 3
}

func twoByteSet(c charset) bool {
	switch c {
	case csKanji, csJISX0213_1, csJISX0213_2, csAdditional:
		return true
	}
	return false
}

func singleByteSet(c charset) bool {
	switch c {
	case csASCII, csASCIIx, csJISX0201Kata:
		return true
	}
	return c >= csHiragana && c <= csKatakanaProp
}

func (d *Decoder) control(b []byte, i int) int {
	switch c := b[i]; c {
	case CodeLS0:
		d.gl, d.ss = 0, 0
		return 1
	case CodeLS1:
		d.gl, d.ss = 1, 0
		return 1
	case CodeSS2:
		d.ss = 2
		return 1
	case CodeSS3:
		d.ss = 3
		return 1
	case CodeAPD:
		d.emit('\n')
		d.reset()
		return 1
	case CodeAPR:
		d.emit('\r')
		d.ss = 0
		return 1
	}
	n := controlLength(b, i)
	switch b[i] {
	case CodeSSZ, CodeMSZ:
		d.normal = false
	case CodeNSZ:
		d.normal = true
	case CodeSZX:
		// Normal unless the parameter says tiny.
		d.normal = !(n > 1 && b[i+1] == 0x60)
	}
	d.controls = append(d.controls, Control{
		Code:   b[i],
		Params: append([]byte(nil), b[i+1:i+n]...),
		At:     d.runes,
	})
	d.ss = 0
	return n
}

// fullWidth is the full-width form of a printable ASCII character, as aribts's
// table (Shift-JIS 0x81xx and 0x82xx) reads it. Anything else is itself.
func fullWidth(r rune) rune {
	switch {
	case r == '"':
		return '\u201d'
	case r == '\'':
		return '\u2019'
	// The set's own yen sign and overline, where ASCII has the backslash
	// and the tilde.
	case r == '\\' || r == '\u00a5':
		return '\uffe5'
	case r == '`':
		return '\u2018'
	case r == '~' || r == '\u203e':
		return '\uffe3'
	case r >= '!' && r <= '}':
		return r - 0x21 + 0xff01
	}
	return r
}
