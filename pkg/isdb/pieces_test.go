// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import "testing"

func pieceText(pieces []Piece) string {
	var out []rune
	for _, p := range pieces {
		if p.Kind == PieceChar {
			out = append(out, p.Rune)
		}
	}
	return string(out)
}

// A one-segment caption reads its high half as kanji from the start.
func TestACProfileCaptionReadsKanjiWithoutADesignation(t *testing.T) {
	pieces := CaptionPiecesCProfile([]byte{0x0c, 0x87, 0x8a, 0xa4, 0xb7, 0xa4, 0xe3, 0xa4, 0xea})
	if got := pieceText(pieces); got != "しゃり" {
		t.Fatalf("text %q", got)
	}
}

// A caption starts with the macro set in the fourth place; SS3 61 runs the
// default macro that puts katakana in the second place.
func TestADefaultMacroChangesTheSets(t *testing.T) {
	// SS3 0x61 (macro 1: katakana in G1), LS1, then 0x22: katakana ア.
	pieces := CaptionPieces([]byte{0x1d, 0x61, 0x0e, 0x22})
	if got := pieceText(pieces); got != "ア" {
		t.Fatalf("text %q", got)
	}
}

// One-byte characters stay half-width in the pieces; the caption layout
// decides the cell, not the decoder.
func TestPiecesKeepTheWidthOfTheSet(t *testing.T) {
	pieces := CaptionPieces([]byte{0x0e, 'A', 0x0f, 0x24, 0x22})
	if len(pieces) != 2 {
		t.Fatalf("pieces %+v", pieces)
	}
	if pieces[0].Rune != 'A' || pieces[0].Width != 1 {
		t.Errorf("first %+v", pieces[0])
	}
	if pieces[1].Rune != 'あ' || pieces[1].Width != 2 {
		t.Errorf("second %+v", pieces[1])
	}
}

// A clear screen puts the sets back to what a caption starts in, and comes out
// as a control piece.
func TestAClearScreenPutsTheSetsBack(t *testing.T) {
	// LS1 to alphanumerics, CS, then 0x24 0x22 is read as kanji again: あ.
	pieces := CaptionPieces([]byte{0x0e, 0x0c, 0x24, 0x22})
	if len(pieces) != 2 || pieces[0].Kind != PieceControl || pieces[0].Control[0] != 0x0c {
		t.Fatalf("pieces %+v", pieces)
	}
	if pieceText(pieces) != "あ" {
		t.Fatalf("text %q", pieceText(pieces))
	}
}

// A DRCS character comes out with its set and code.
func TestADRCSCharacterIsAPiece(t *testing.T) {
	pieces := CaptionPieces([]byte{0x1b, 0x24, 0x28, 0x20, 0x40, 0x21, 0x21})
	if len(pieces) != 1 || pieces[0].Kind != PieceDRCS || pieces[0].DRCSSet != 0 || pieces[0].DRCSCode != 0x2121 || pieces[0].Width != 2 {
		t.Fatalf("pieces %+v", pieces)
	}
}

// A control code takes its parameters with it.
func TestAControlPieceCarriesItsParameters(t *testing.T) {
	pieces := CaptionPieces([]byte{0x1c, 0x42, 0x43, 0x24, 0x22})
	if len(pieces) != 2 || string(pieces[0].Control) != "\x1c\x42\x43" {
		t.Fatalf("pieces %+v", pieces)
	}
}

func TestWideCellReadsTheAddedRows(t *testing.T) {
	if r, ok := WideCell(0x7c, 0x21); !ok || r != '➡' {
		t.Errorf("got %q %v", r, ok)
	}
	if _, ok := WideCell(0x20, 0x21); ok {
		t.Error("a cell off the table read as a character")
	}
}

// A caption reads rows 9 to 15 the way a name does.
func TestCaptionPiecesReadRowThirteen(t *testing.T) {
	pieces := CaptionPieces([]byte{0x2d, 0x21, 0x29, 0x21})
	var runes []rune
	lost := 0
	for _, piece := range pieces {
		switch piece.Kind {
		case PieceChar:
			runes = append(runes, piece.Rune)
		case PieceLost:
			lost++
		}
	}
	if string(runes) != "①" || lost != 1 {
		t.Errorf("read %q with %d lost: %+v", string(runes), lost, pieces)
	}
}
