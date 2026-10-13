// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import "testing"

func TestDecodeBroadcastCharacterSets(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"kanji", []byte{0x46, 0x7c}, "日"},
		{"initial hiragana GR", []byte{0xa2, 0xa4, 0xa6}, "あいう"},
		{"additional symbols", []byte{CodeESC, 0x24, 0x3b, 0x7a, 0x5d, 0x7a, 0x56}, "🅍🈑"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := DecodeString(test.in)
			if result.Text != test.want || !result.Lossless() {
				t.Fatalf("DecodeString(% x) = %q, undecodable=%+v; want %q", test.in, result.Text, result.Undecodable, test.want)
			}
		})
	}
}

// TestDecodeNames covers the reading a name gets: the sets a string starts in,
// the shifts and designations, and the full-width forms at the normal size.
func TestDecodeNames(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		// 衛星 out of the two-byte set the string starts with, then あ from
		// the single-byte set on the high half.
		{"starting sets", []byte{0x31, 0x52, 0x40, 0x31, 0xa2}, "衛星あ"},
		// LS1 for the alphabet, LS0 back, then 衛星.
		{"alphabet moved to", []byte{0x0e, 'S', 'A', 'M', 'P', 'L', 'E', 0x0f, 0x31, 0x52, 0x40, 0x31}, "ＳＡＭＰＬＥ衛星"},
		// ESC ( 0 designates hiragana into the place the low half reads.
		{"designation part way", []byte{0x1b, 0x28, 0x30, 0x24, 0x22}, "いあ"},
		// SS2 reaches the third place for one character only.
		{"single shift", []byte{0x19, 0x22, 0x31, 0x52}, "あ衛"},
		{"added row arrow", []byte{0x7c, 0x21}, "➡"},
		{"added row number", []byte{0x7a, 0x4d}, "⒑"},
		{"added row standing for a kanji", []byte{0x7c, 0x27}, "年"},
		// A colour change takes its parameter with it.
		{"control parameters", []byte{0x0e, 0x90, 0x40, 'A', 'B'}, "ＡＢ"},
		{"padding", []byte{0x0e, 'A', 0x00, 0xff, 'B'}, "ＡＢ"},
		// A space is full-width at the normal size and half-width after MSZ.
		{"space at the normal size", []byte("\x0eA B"), "Ａ　Ｂ"},
		{"space after MSZ and NSZ", []byte("\x0e\x89A B\x8aC D"), "A BＣ　Ｄ"},
		{"alphanumerics at the normal size", []byte("\x0e(A)"), "（Ａ）"},
		{"alphanumerics after MSZ", []byte("\x0e\x89(A)"), "(A)"},
		// The five whose full-width form is not at U+FF01 + n.
		{"irregular full-width forms", []byte("\x0e\x22\x27\x5c\x60\x7e"), "”’￥‘￣"},
		// SZX with the tiny parameter halves them, any other keeps them normal.
		{"SZX tiny", []byte("\x0e\x8b\x60A"), "A"},
		{"SZX other", []byte("\x0e\x89\x8b\x41A"), "Ａ"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := DecodeString(test.in)
			if result.Text != test.want || !result.Lossless() {
				t.Fatalf("DecodeString(% x) = %q, undecodable=%+v; want %q", test.in, result.Text, result.Undecodable, test.want)
			}
		})
	}
}

// TestANameStartsAtTheNormalSize: a size left by one string does not carry
// into the next.
func TestANameStartsAtTheNormalSize(t *testing.T) {
	decoder := NewDecoder()
	decoder.Decode([]byte("\x0e\x89A"))
	if got := decoder.Decode([]byte("\x0eA")).Text; got != "Ａ" {
		t.Errorf("the second name read %q", got)
	}
}

// Rows 9 to 15 are read through the JIS X 0208 table: row 13 carries the
// circled numbers receivers show, and a cell the table leaves empty stays
// undecodable.
func TestKanjiRowsNineToFifteen(t *testing.T) {
	if got := DecodeString([]byte{0x2d, 0x21, 0x2d, 0x35}); got.Text != "①Ⅰ" || !got.Lossless() {
		t.Errorf("row 13 read as %q, undecodable=%+v", got.Text, got.Undecodable)
	}
	empty := DecodeString([]byte{0x29, 0x21})
	if empty.Lossless() || len(empty.Undecodable) != 1 {
		t.Errorf("an empty cell of row 9 read as %q, undecodable=%+v", empty.Text, empty.Undecodable)
	}
}
