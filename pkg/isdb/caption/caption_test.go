// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package caption

import (
	"slices"
	"testing"
)

func group(kind, id byte, data []byte) []byte {
	out := []byte{kind, 0xff, 0x00, id << 2, 0x00, 0x00, byte(len(data) >> 8), byte(len(data))}
	out = append(out, data...)
	return append(out, 0, 0)
}

func statementBytes(text []byte) []byte {
	data := []byte{0x00} // TMD free
	unit := []byte{0x1f, 0x20, byte(len(text) >> 16), byte(len(text) >> 8), byte(len(text))}
	unit = append(unit, text...)
	data = append(data, byte(len(unit)>>16), byte(len(unit)>>8), byte(len(unit)))
	return append(data, unit...)
}

func mustGroup(t *testing.T, b []byte) Group {
	t.Helper()
	g, ok := ParsePES(b)
	if !ok {
		t.Fatal("not read")
	}
	return g
}

func TestAManagementGroupNamesTheLanguageAndTheFormat(t *testing.T) {
	// TMD free, one language, tag 0 DMF 0, jpn, format 7 (960x540
	// horizontal).
	data := []byte{0x00, 0x01, 0x00, 'j', 'p', 'n', 0x70, 0x00, 0x00, 0x00}
	g := mustGroup(t, group(0x80, 0x00, data))
	if !g.IsManagement() || g.Management == nil {
		t.Fatalf("group %+v", g)
	}
	if g.Management.Languages[0].ISO != [3]byte{'j', 'p', 'n'} || g.Management.Languages[0].Format != 7 {
		t.Errorf("language %+v", g.Management.Languages[0])
	}
}

func TestAStatementIsLaidOutWhereAPSSays(t *testing.T) {
	// APS row 2 column 3, then あい (the kanji set a caption starts with).
	text := []byte{0x1c, 0x40 + 2, 0x40 + 3, 0x24, 0x22, 0x24, 0x24}
	g := mustGroup(t, group(0x81, 0x01, statementBytes(text)))
	if g.Kind != KindSuperimpose || g.Statement == nil {
		t.Fatalf("group %+v", g)
	}
	p := NewPresenter(KindSuperimpose)
	layout := p.Present(g.Statement)
	if layout.ScreenW != 960 || layout.ScreenH != 540 || len(layout.Texts) != 1 {
		t.Fatalf("layout %+v", layout)
	}
	tx := layout.Texts[0]
	if tx.Text != "あい" || tx.X != 3*40 || tx.Y != 2*60 || tx.CellW != 40 || tx.CellH != 60 || tx.Fg != White {
		t.Errorf("text %+v", tx)
	}
	if !layout.Superimpose {
		t.Error("not superimposed")
	}
}

func TestDRCSGlyphsAreReadAndPutWhereTheTextUsesThem(t *testing.T) {
	// DRCS-0 (two-byte): row and cell 0x21 0x21, one shape, mode 1, depth 2
	// (four levels, two bits), 2x2.
	drcsUnit := []byte{0x01, 0x21, 0x21, 0x01, 0x21, 0x02, 0x02, 0x02, 0b11_00_00_11}
	glyphs := ParseDRCS(true, drcsUnit)
	if len(glyphs) != 1 || !slices.Equal(glyphs[0].Glyph.Bitmap, []uint8{3, 0, 0, 3}) || glyphs[0].Rune != DRCSRune(0, 0x2121, 2) {
		t.Fatalf("glyphs %+v", glyphs)
	}
	// The text designates DRCS-0 into G0 (ESC 0x24 0x28 0x20 0x40), then
	// 0x21 0x21.
	data := []byte{0x00}
	unitD := append([]byte{0x1f, 0x31, 0, 0, byte(len(drcsUnit))}, drcsUnit...)
	text := []byte{0x1b, 0x24, 0x28, 0x20, 0x40, 0x21, 0x21}
	unitT := append([]byte{0x1f, 0x20, 0, 0, byte(len(text))}, text...)
	total := len(unitD) + len(unitT)
	data = append(data, 0, byte(total>>8), byte(total))
	data = append(data, unitD...)
	data = append(data, unitT...)
	g := mustGroup(t, group(0x81, 0x01, data))
	p := NewPresenter(KindSuperimpose)
	layout := p.Present(g.Statement)
	if len(layout.Texts) != 1 {
		t.Fatalf("texts %+v", layout.Texts)
	}
	r := []rune(layout.Texts[0].Text)[0]
	if !IsDRCS(r) || len(layout.DRCS[r]) != 1 {
		t.Errorf("drcs %q %+v", r, layout.DRCS)
	}
}

func TestColoursAndSizesSplitTheRuns(t *testing.T) {
	// Red (0x81), あ, MSZ (0x89), LS1 to G1 (the alphanumerics), A.
	text := []byte{0x81, 0x24, 0x22, 0x89, 0x0e, 0x41}
	g := mustGroup(t, group(0x80, 0x01, statementBytes(text)))
	p := NewPresenter(KindCaption)
	layout := p.Present(g.Statement)
	if len(layout.Texts) != 2 {
		t.Fatalf("texts %+v", layout.Texts)
	}
	if layout.Texts[0].Fg != Indexed(1) {
		t.Errorf("fg %+v", layout.Texts[0].Fg)
	}
	if layout.Texts[1].Text != "A" || layout.Texts[1].CellW != 20 || layout.Texts[1].CellH != 60 {
		t.Errorf("second %+v", layout.Texts[1])
	}
}

func TestACSICutOffAtTheEndOfTheTextIsPassedOver(t *testing.T) {
	text := []byte{0x24, 0x22, 0x9b}
	g := mustGroup(t, group(0x80, 0x01, statementBytes(text)))
	layout := NewPresenter(KindCaption).Present(g.Statement)
	if len(layout.Texts) != 1 {
		t.Fatalf("texts %+v", layout.Texts)
	}
}

// A wait followed by CS keeps what was put until the wait is over.
func TestTIMEThenCSKeepsTheTextUntilThen(t *testing.T) {
	text := []byte{0x24, 0x22, 0x9d, 0x20, 0x40 + 30, 0x0c}
	g := mustGroup(t, group(0x80, 0x01, statementBytes(text)))
	layout := NewPresenter(KindCaption).Present(g.Statement)
	if len(layout.Texts) != 1 || !layout.Texts[0].HasUntilMS || layout.Texts[0].UntilMS != 3000 {
		t.Fatalf("texts %+v", layout.Texts)
	}
	if !layout.HasClearAfterMS || layout.ClearAfterMS != 3000 || layout.Clear {
		t.Errorf("layout %+v", layout)
	}
}

// A statement with a time carries it in milliseconds.
func TestAStatementTimeIsRead(t *testing.T) {
	data := []byte{0x40, 0x01, 0x02, 0x03, 0x45, 0x60, 0, 0, 0}
	g := mustGroup(t, group(0x80, 0x01, data))
	if !g.Statement.HasSTM || g.Statement.STM != ((1*60+2)*60+3)*1000+456 {
		t.Fatalf("statement %+v", g.Statement)
	}
}
