// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

// PieceKind says what one Piece is.
type PieceKind uint8

const (
	// PieceChar is a character, with Width 2 for a two-byte set (a
	// full-width cell) and 1 for a one-byte set.
	PieceChar PieceKind = iota
	// PieceSpace is a space.
	PieceSpace
	// PieceDRCS is a character of a set of shapes the broadcast sends itself:
	// DRCSSet is the set's number (0 to 15), DRCSCode the code within it
	// (one byte or two) and Width as for PieceChar.
	PieceDRCS
	// PieceControl is a control code, with the bytes it takes with it in
	// Control (the code first).
	PieceControl
	// PieceLost is something that was not a character.
	PieceLost
)

// Piece is one thing the eight-unit coding said: a character, or a control
// code with its parameters. What a caption decoder reads the stream as.
type Piece struct {
	Kind     PieceKind
	Rune     rune
	Width    int
	DRCSSet  byte
	DRCSCode uint16
	Control  []byte
}

// defaultMacros are what codes 0x60 to 0x6F of the macro set expand to
// (STD-B24 part 2, table 7-13). Each designates the first three places and
// the macro set into the fourth, then LS0 and LS2R.
var defaultMacros = [16][]byte{
	{0x1b, 0x24, 0x42, 0x1b, 0x29, 0x4a, 0x1b, 0x2a, 0x30, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x24, 0x42, 0x1b, 0x29, 0x31, 0x1b, 0x2a, 0x30, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x24, 0x42, 0x1b, 0x29, 0x20, 0x41, 0x1b, 0x2a, 0x30, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x28, 0x32, 0x1b, 0x29, 0x34, 0x1b, 0x2a, 0x35, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x28, 0x32, 0x1b, 0x29, 0x33, 0x1b, 0x2a, 0x35, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x28, 0x32, 0x1b, 0x29, 0x20, 0x41, 0x1b, 0x2a, 0x35, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x28, 0x20, 0x41, 0x1b, 0x29, 0x20, 0x42, 0x1b, 0x2a, 0x20, 0x43, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x28, 0x20, 0x44, 0x1b, 0x29, 0x20, 0x45, 0x1b, 0x2a, 0x20, 0x46, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x28, 0x20, 0x47, 0x1b, 0x29, 0x20, 0x48, 0x1b, 0x2a, 0x20, 0x49, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x28, 0x20, 0x4a, 0x1b, 0x29, 0x20, 0x4b, 0x1b, 0x2a, 0x20, 0x4c, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x28, 0x20, 0x4d, 0x1b, 0x29, 0x20, 0x4e, 0x1b, 0x2a, 0x20, 0x4f, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x24, 0x42, 0x1b, 0x29, 0x20, 0x42, 0x1b, 0x2a, 0x30, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x24, 0x42, 0x1b, 0x29, 0x20, 0x43, 0x1b, 0x2a, 0x30, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x24, 0x42, 0x1b, 0x29, 0x20, 0x44, 0x1b, 0x2a, 0x30, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x28, 0x31, 0x1b, 0x29, 0x30, 0x1b, 0x2a, 0x4a, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
	{0x1b, 0x28, 0x4a, 0x1b, 0x29, 0x32, 0x1b, 0x2a, 0x20, 0x41, 0x1b, 0x2b, 0x20, 0x70, 0x0f, 0x1b, 0x7d},
}

// CaptionPieces breaks a caption's text into what it says, in order. The sets
// and the shifts are followed here; what the control codes mean for the
// layout is the reader's. A caption starts with the macro set in the fourth
// place, and the documents lean on it: SS3 61 is the usual way of switching
// the second place to katakana.
func CaptionPieces(b []byte) []Piece {
	d := newPieceDecoder()
	d.g[3] = csMacro
	return d.run(b)
}

// CaptionPiecesCProfile is CaptionPieces for a one-segment (C profile)
// caption: the low half is fixed to the first one-byte DRCS set and the high
// half to the kanji set, and nothing in the stream changes them (ARIB TR-B14
// part 3, 6.4.1). Read as an ordinary caption, where the high half starts as
// hiragana, every kanji would come out as two hiragana.
func CaptionPiecesCProfile(b []byte) []Piece {
	d := newPieceDecoder()
	d.initial = pieceState{g: [4]charset{csDRCS0 + 1, csASCII, csKanji, csMacro}, gl: 0, gr: 2}
	d.g, d.gl, d.gr = d.initial.g, d.initial.gl, d.initial.gr
	return d.run(b)
}

// WideCell is one character of a two-byte set, by cell: the two bytes on the
// low half of the range, as JIS numbers them. EUC-JP is the same cells with
// the high bit set, so a data broadcast's documents (written in EUC-JP with
// the added rows) can be read through here too.
func WideCell(first, second byte) (rune, bool) {
	j := newJISCodec()
	return decodeTwoByte(&j, first, second)
}

type pieceState struct {
	g      [4]charset
	gl, gr int
}

// pieceDecoder follows the sets for a caption. Unlike Decoder (the one names
// are read with), a new line does not put the sets back; a clear screen does.
type pieceDecoder struct {
	jis     jisCodec
	g       [4]charset
	gl, gr  int
	ss      int
	initial pieceState
	depth   int
}

func newPieceDecoder() *pieceDecoder {
	return &pieceDecoder{
		jis:     newJISCodec(),
		g:       [4]charset{csKanji, csASCII, csHiragana, csKatakana},
		gl:      0,
		gr:      2,
		initial: pieceState{g: [4]charset{csKanji, csASCII, csHiragana, csMacro}, gl: 0, gr: 2},
	}
}

func (d *pieceDecoder) run(b []byte) []Piece {
	var out []Piece
	d.runInto(b, &out)
	return out
}

func (d *pieceDecoder) runInto(b []byte, out *[]Piece) {
	for i := 0; i < len(b); {
		i += d.step(b, i, out)
	}
}

func (d *pieceDecoder) step(b []byte, i int, out *[]Piece) int {
	c := b[i]
	switch {
	case c == 0x00 || c == 0x7f || c == 0xff:
		return 1
	case c == CodeESC:
		return d.escape(b, i)
	case c == 0x20 || c == 0xa0:
		*out = append(*out, Piece{Kind: PieceSpace})
		d.ss = 0
		return 1
	case c < 0x20 || (c >= 0x80 && c <= 0x9f):
		return d.control(b, i, out)
	}
	return d.graphic(b, i, out)
}

func (d *pieceDecoder) graphic(b []byte, i int, out *[]Piece) int {
	c := b[i]
	idx := d.gl
	switch {
	case d.ss != 0:
		idx = d.ss
	case c&0x80 != 0:
		idx = d.gr
	}
	d.ss = 0
	set := d.g[idx]
	if set == csMacro {
		d.macro(c & 0x7f)
		return 1
	}
	if !set.twoByte() {
		*out = append(*out, d.character(set, uint16(c&0x7f), 1))
		return 1
	}
	if i+1 >= len(b) {
		// The last byte is the first half of a character: the text was cut
		// short.
		*out = append(*out, Piece{Kind: PieceLost})
		return 1
	}
	// Both halves come from the same side of the byte range.
	if (b[i+1]^c)&0x80 != 0 {
		*out = append(*out, Piece{Kind: PieceLost})
		return 1
	}
	cell := uint16(c&0x7f)<<8 | uint16(b[i+1]&0x7f)
	*out = append(*out, d.character(set, cell, 2))
	return 2
}

// macro runs what a code of the macro set stands for. Only the default macros
// are known (a broadcast can define its own with MACRO 0x95; those are skipped
// as a control code). What a macro leaves behind is state, not text.
func (d *pieceDecoder) macro(code byte) {
	n := int(code) - 0x60
	if n < 0 || n >= len(defaultMacros) || d.depth > 0 {
		return
	}
	d.depth++
	var scratch []Piece
	d.runInto(defaultMacros[n], &scratch)
	d.depth--
}

func (d *pieceDecoder) character(set charset, cell uint16, width int) Piece {
	if set.drcs() {
		return Piece{Kind: PieceDRCS, DRCSSet: byte(set) & 0x0f, DRCSCode: cell, Width: width}
	}
	c1, c2 := byte(cell>>8), byte(cell)
	if width == 1 {
		c1, c2 = byte(cell), 0
	}
	if r, ok := set.decode(&d.jis, c1, c2); ok {
		return Piece{Kind: PieceChar, Rune: r, Width: width}
	}
	return Piece{Kind: PieceLost}
}

func (d *pieceDecoder) escape(b []byte, i int) int {
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

func (d *pieceDecoder) designateSingle(b []byte, i, place, at int) int {
	if i+at >= len(b) {
		return len(b) - i
	}
	f := b[i+at]
	if f == 0x20 {
		if i+at+1 >= len(b) {
			return len(b) - i
		}
		if n := b[i+at+1]; n == 0x70 || (n >= 0x41 && n <= 0x4f) {
			d.g[place] = charset(n) | 0x80
		}
		return at + 2
	}
	if singleByteSet(charset(f)) {
		d.g[place] = charset(f)
	}
	return at + 1
}

func (d *pieceDecoder) designateMulti(b []byte, i int) int {
	if i+2 >= len(b) {
		return len(b) - i
	}
	f := b[i+2]
	if twoByteSet(charset(f)) {
		d.g[0] = charset(f)
		return 3
	}
	if f < 0x28 || f > 0x2b {
		return 3
	}
	place := int(f - 0x28)
	if i+3 >= len(b) {
		return len(b) - i
	}
	if n := b[i+3]; n == 0x20 {
		if i+4 < len(b) && b[i+4] == 0x40 {
			d.g[place] = csDRCS0
		}
		return 5
	} else if twoByteSet(charset(n)) {
		d.g[place] = charset(n)
	}
	return 4
}

func (d *pieceDecoder) control(b []byte, i int, out *[]Piece) int {
	switch b[i] {
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
	case CodeCS:
		// A clear screen puts the sets back to what a caption starts in.
		d.g, d.gl, d.gr = d.initial.g, d.initial.gl, d.initial.gr
	}
	d.ss = 0
	n := controlLength(b, i)
	*out = append(*out, Piece{Kind: PieceControl, Control: b[i : i+n]})
	return n
}
