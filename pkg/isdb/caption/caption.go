// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package caption reads captions and superimposed text (ARIB STD-B24) on both
// transmission layers.
//
// In a transport stream a caption rides the private data of a PES
// (data_identifier 0x80 a caption, 0x81 superimposed text) as data groups:
// management data (the languages and the shape of the screen) and statement
// data (the text and its DRCS). The text is part 2's eight-unit coding, with
// position, size and colour control codes between the characters.
//
//   - ParsePES: a PES's payload into a Group (management or statement, the
//     language, the time and the data units).
//   - Presenter: sets the screen up from the management data and turns
//     statement data into what to put on the screen (Layout), in screen
//     pixels; the drawer only scales.
//
// Superimposed text has no timing: it is shown as it arrives and cleared by
// a clear code or after three minutes.
//
// MMT captions are TTML documents carried in MPUs; the MFU header and the
// additional information of the data coding descriptor are read in mmt.go,
// and the documents themselves by package ttml.
package caption

import (
	"sort"

	"github.com/Till0196/coax/pkg/isdb"
)

// mostRepeat bounds what RPC repeats: the widest screen filled with the
// smallest characters.
const mostRepeat = 1920

// Kind is a caption or superimposed text (the PES's data_identifier).
type Kind uint8

const (
	KindCaption Kind = iota
	KindSuperimpose
)

// Group is one data group. Exactly one of Management and Statement is set.
type Group struct {
	Kind Kind
	// ID is data_group_id: 0x0/0x20 management, 0x1-0x8/0x21-0x28 statement
	// (the 0x20 bit is group B).
	ID         byte
	Management *Management
	Statement  *Statement
}

// IsManagement reports whether the group is management data.
func (g Group) IsManagement() bool { return g.ID&0x0f == 0 }

// Language is the language number (1 to 8) of statement data.
func (g Group) Language() (byte, bool) {
	n := g.ID & 0x0f
	return n, n != 0
}

// Management is caption management data.
type Management struct {
	// TMD is the time control mode (0 free, 1 real time, 2 offset).
	TMD       byte
	Languages []Language
	Units     []Unit
}

// Language is one language's entry of management data.
type Language struct {
	// Tag is the language number (0 to 7, statement data's data_group_id
	// low bits minus one).
	Tag byte
	// DMF is the display mode: the top two bits on reception, the bottom
	// two on playback.
	DMF byte
	ISO [3]byte
	// Format is the display format (table 8-4).
	Format byte
	// TCS is the character coding (0 the eight-unit code).
	TCS    byte
	Rollup byte
}

// Statement is caption statement data.
type Statement struct {
	TMD byte
	// STM is when to present (milliseconds), only where TMD is 1 or 2.
	STM    uint64
	HasSTM bool
	Units  []Unit
}

// UnitKind says what a data unit holds.
type UnitKind uint8

const (
	// UnitText is text in the eight-unit code.
	UnitText UnitKind = iota
	// UnitDRCS is DRCS shapes (laid out as table D-1).
	UnitDRCS
	// UnitOther is anything else (bitmaps, geometric...).
	UnitOther
)

// Unit is one data unit.
type Unit struct {
	Kind UnitKind
	// Parameter is data_unit_parameter.
	Parameter byte
	// TwoByte, for DRCS, is 0x31 (the two-byte DRCS-0) rather than 0x30 (the
	// one-byte DRCS-1 to 15).
	TwoByte bool
	Data    []byte
}

// ParsePES reads a PES's payload (without the PES header, from
// data_identifier). One PES carries one data group. The CRC is not checked.
func ParsePES(payload []byte) (Group, bool) {
	if len(payload) < 3 {
		return Group{}, false
	}
	var kind Kind
	switch payload[0] {
	case 0x80:
		kind = KindCaption
	case 0x81:
		kind = KindSuperimpose
	default:
		return Group{}, false
	}
	if payload[1] != 0xff {
		return Group{}, false
	}
	header := int(payload[2] & 0x0f)
	if 3+header > len(payload) {
		return Group{}, false
	}
	group := payload[3+header:]
	if len(group) < 5 {
		return Group{}, false
	}
	id := group[0] >> 2
	size := int(group[3])<<8 | int(group[4])
	if 5+size > len(group) {
		return Group{}, false
	}
	data := group[5 : 5+size]
	out := Group{Kind: kind, ID: id}
	if id&0x0f == 0 {
		m, ok := parseManagement(data)
		if !ok {
			return Group{}, false
		}
		out.Management = &m
	} else {
		s, ok := parseStatement(data)
		if !ok {
			return Group{}, false
		}
		out.Statement = &s
	}
	return out, true
}

func parseManagement(data []byte) (Management, bool) {
	if len(data) < 1 {
		return Management{}, false
	}
	tmd := data[0] >> 6
	at := 1
	if tmd == 2 {
		// OTM, the offset time: five bytes.
		at += 5
	}
	if at >= len(data) {
		return Management{}, false
	}
	count := int(data[at])
	at++
	languages := make([]Language, 0, count)
	for range count {
		if at >= len(data) {
			return Management{}, false
		}
		first := data[at]
		tag, dmf := first>>5, first&0x0f
		at++
		if dmf == 0b1100 || dmf == 0b1101 || dmf == 0b1110 {
			// DC, the display condition: one byte.
			at++
		}
		if at+4 > len(data) {
			return Management{}, false
		}
		iso := [3]byte{data[at], data[at+1], data[at+2]}
		at += 3
		last := data[at]
		at++
		languages = append(languages, Language{
			Tag: tag, DMF: dmf, ISO: iso,
			Format: last >> 4, TCS: (last >> 2) & 3, Rollup: last & 3,
		})
	}
	units, ok := parseUnits(data, at)
	if !ok {
		return Management{}, false
	}
	return Management{TMD: tmd, Languages: languages, Units: units}, true
}

func parseStatement(data []byte) (Statement, bool) {
	if len(data) < 1 {
		return Statement{}, false
	}
	s := Statement{TMD: data[0] >> 6}
	at := 1
	if s.TMD == 1 || s.TMD == 2 {
		if at+5 > len(data) {
			return Statement{}, false
		}
		t := data[at : at+5]
		at += 5
		// HHMMSS.mmm in BCD.
		bcd := func(b byte) uint64 { return uint64(b>>4)*10 + uint64(b&0x0f) }
		hours, minutes, seconds := bcd(t[0]), bcd(t[1]), bcd(t[2])
		millis := bcd(t[3])*10 + uint64(t[4]>>4)
		s.STM = ((hours*60+minutes)*60+seconds)*1000 + millis
		s.HasSTM = true
	}
	units, ok := parseUnits(data, at)
	if !ok {
		return Statement{}, false
	}
	s.Units = units
	return s, true
}

// parseUnits reads the data units, after their three-byte total length.
func parseUnits(data []byte, at int) ([]Unit, bool) {
	if at+3 > len(data) {
		return nil, false
	}
	total := int(data[at])<<16 | int(data[at+1])<<8 | int(data[at+2])
	at += 3
	end := at + total
	if end > len(data) {
		end = len(data)
	}
	var out []Unit
	for at+5 <= end {
		if data[at] != 0x1f {
			break
		}
		parameter := data[at+1]
		size := int(data[at+2])<<16 | int(data[at+3])<<8 | int(data[at+4])
		bodyEnd := at + 5 + size
		if bodyEnd > end {
			bodyEnd = end
		}
		body := append([]byte(nil), data[at+5:bodyEnd]...)
		switch parameter {
		case 0x20:
			out = append(out, Unit{Kind: UnitText, Parameter: parameter, Data: body})
		case 0x30, 0x31:
			out = append(out, Unit{Kind: UnitDRCS, Parameter: parameter, TwoByte: parameter == 0x31, Data: body})
		default:
			out = append(out, Unit{Kind: UnitOther, Parameter: parameter, Data: body})
		}
		at += 5 + size
	}
	return out, true
}

// Color is a colour; A is 0 for transparent.
type Color struct {
	R, G, B, A uint8
}

var (
	Transparent = Color{}
	White       = Color{255, 255, 255, 255}
	Black       = Color{0, 0, 0, 255}
)

// Indexed is the default CLUT colour of an index (0 to 127, table 8-8; the
// values are a receiver's).
func Indexed(index uint8) Color { return clut[index&0x7f] }

// clut is the default CLUT (STD-B24 part 3, table 8-8): 0-7 the basic
// colours, 8 transparent, 9-15 half brightness, 16-64 the combinations in
// steps of 85, and 65-127 those half transparent.
var clut = [128]Color{
	{0, 0, 0, 255}, {255, 0, 0, 255}, {0, 255, 0, 255}, {255, 255, 0, 255},
	{0, 0, 255, 255}, {255, 0, 255, 255}, {0, 255, 255, 255}, {255, 255, 255, 255},
	{0, 0, 0, 0}, {170, 0, 0, 255}, {0, 170, 0, 255}, {170, 170, 0, 255},
	{0, 0, 170, 255}, {170, 0, 170, 255}, {0, 170, 170, 255}, {170, 170, 170, 255},
	{0, 0, 85, 255}, {0, 85, 0, 255}, {0, 85, 85, 255}, {0, 85, 170, 255},
	{0, 85, 255, 255}, {0, 170, 85, 255}, {0, 170, 255, 255}, {0, 255, 85, 255},
	{0, 255, 170, 255}, {85, 0, 0, 255}, {85, 0, 85, 255}, {85, 0, 170, 255},
	{85, 0, 255, 255}, {85, 85, 0, 255}, {85, 85, 85, 255}, {85, 85, 170, 255},
	{85, 85, 255, 255}, {85, 170, 0, 255}, {85, 170, 85, 255}, {85, 170, 170, 255},
	{85, 170, 255, 255}, {85, 255, 0, 255}, {85, 255, 85, 255}, {85, 255, 170, 255},
	{85, 255, 255, 255}, {170, 0, 85, 255}, {170, 0, 255, 255}, {170, 85, 0, 255},
	{170, 85, 85, 255}, {170, 85, 170, 255}, {170, 85, 255, 255}, {170, 170, 85, 255},
	{170, 170, 255, 255}, {170, 255, 0, 255}, {170, 255, 85, 255}, {170, 255, 170, 255},
	{170, 255, 255, 255}, {255, 0, 85, 255}, {255, 0, 170, 255}, {255, 85, 0, 255},
	{255, 85, 85, 255}, {255, 85, 170, 255}, {255, 85, 255, 255}, {255, 170, 0, 255},
	{255, 170, 85, 255}, {255, 170, 170, 255}, {255, 170, 255, 255}, {255, 255, 85, 255},
	{255, 255, 170, 255}, {0, 0, 0, 128}, {255, 0, 0, 128}, {0, 255, 0, 128},
	{255, 255, 0, 128}, {0, 0, 255, 128}, {255, 0, 255, 128}, {0, 255, 255, 128},
	{255, 255, 255, 128}, {170, 0, 0, 128}, {0, 170, 0, 128}, {170, 170, 0, 128},
	{0, 0, 170, 128}, {170, 0, 170, 128}, {0, 170, 170, 128}, {170, 170, 170, 128},
	{0, 0, 85, 128}, {0, 85, 0, 128}, {0, 85, 85, 128}, {0, 85, 170, 128},
	{0, 85, 255, 128}, {0, 170, 85, 128}, {0, 170, 255, 128}, {0, 255, 85, 128},
	{0, 255, 170, 128}, {85, 0, 0, 128}, {85, 0, 85, 128}, {85, 0, 170, 128},
	{85, 0, 255, 128}, {85, 85, 0, 128}, {85, 85, 85, 128}, {85, 85, 170, 128},
	{85, 85, 255, 128}, {85, 170, 0, 128}, {85, 170, 85, 128}, {85, 170, 170, 128},
	{85, 170, 255, 128}, {85, 255, 0, 128}, {85, 255, 85, 128}, {85, 255, 170, 128},
	{85, 255, 255, 128}, {170, 0, 85, 128}, {170, 0, 255, 128}, {170, 85, 0, 128},
	{170, 85, 85, 128}, {170, 85, 170, 128}, {170, 85, 255, 128}, {170, 170, 85, 128},
	{170, 170, 255, 128}, {170, 255, 0, 128}, {170, 255, 85, 128}, {170, 255, 170, 128},
	{170, 255, 255, 128}, {255, 0, 85, 128}, {255, 0, 170, 128}, {255, 85, 0, 128},
	{255, 85, 85, 128}, {255, 85, 170, 128}, {255, 85, 255, 128}, {255, 170, 0, 128},
	{255, 170, 85, 128}, {255, 170, 170, 128}, {255, 170, 255, 128}, {255, 255, 85, 128},
}

// DRCSGlyph is one shape a broadcast sends for a character.
type DRCSGlyph struct {
	Width, Height uint8
	// Levels is the number of grey levels (depth + 2).
	Levels uint8
	// Bitmap is each pixel's level, row by row: 0 transparent, Levels-1
	// the character's colour.
	Bitmap []uint8
}

// DRCSRune is the private-use character a DRCS character is mapped to. A
// two-byte one (DRCS-0) by row and cell; a one-byte one (DRCS-1 to 15) by set
// and code.
func DRCSRune(set byte, code uint16, width int) rune {
	sub := func(v uint32, by uint32) uint32 {
		if v < by {
			return 0
		}
		return v - by
	}
	var index uint32
	if width == 2 || set == 0 {
		row := sub(uint32(byte(code>>8)), 0x21)
		cell := sub(uint32(byte(code)), 0x21)
		index = row*94 + cell
	} else {
		s := uint32(set)
		if s > 15 {
			s = 15
		}
		index = 0x3000 + s*0x60 + sub(uint32(byte(code)), 0x21)
	}
	return rune(0xf0000 + index)
}

// IsDRCS reports whether a private-use character is a DRCS one.
func IsDRCS(r rune) bool { return r >= 0xf0000 && r < 0xf4000 }

// DRCSEntry is one shape of one character.
type DRCSEntry struct {
	Rune  rune
	Glyph DRCSGlyph
}

// ParseDRCS reads a DRCS data unit (table D-1). A character comes in several
// shapes (faces, sizes), and all of them are returned.
func ParseDRCS(twoByte bool, b []byte) []DRCSEntry {
	var out []DRCSEntry
	if len(b) == 0 {
		return out
	}
	count := int(b[0])
	at := 1
	for range count {
		if at+3 > len(b) {
			break
		}
		first, second, fonts := b[at], b[at+1], int(b[at+2])
		at += 3
		var r rune
		if twoByte {
			r = DRCSRune(0, uint16(first)<<8|uint16(second), 2)
		} else {
			r = DRCSRune(first&0x0f, uint16(second), 1)
		}
		for range fonts {
			if at+4 > len(b) {
				return out
			}
			mode, depth, width, height := b[at], b[at+1], b[at+2], b[at+3]
			at += 4
			if mode&0x0f > 1 {
				// Geometric shapes are not used in operation.
				return out
			}
			levels := uint16(depth) + 2
			bits := 0
			for v := levels - 1; v > 0; v >>= 1 {
				bits++
			}
			pixels := int(width) * int(height)
			length := (pixels*bits + 7) / 8
			if at+length > len(b) {
				return out
			}
			data := b[at : at+length]
			at += length
			bitmap := make([]uint8, 0, pixels)
			bit := 0
			for range pixels {
				var value uint8
				for range bits {
					by := data[bit>>3]
					value = value<<1 | (by>>(7-(bit&7)))&1
					bit++
				}
				bitmap = append(bitmap, value)
			}
			out = append(out, DRCSEntry{Rune: r, Glyph: DRCSGlyph{Width: width, Height: height, Levels: uint8(levels), Bitmap: bitmap}})
		}
	}
	return out
}

// Text is one run of characters on the screen. The position and the sizes
// are screen pixels (Layout.Screen).
type Text struct {
	X, Y int
	// Cell is one character's box (width, height); half the width for a
	// half-width size.
	CellW, CellH uint32
	// SizeW and SizeH are the character's size (the box less the spacing).
	SizeW, SizeH uint32
	Text         string
	Fg, Bg       Color
	// Outline is the outline colour (ORN), where there is one.
	Outline    Color
	HasOutline bool
	// Flash is FLC: 0 none, 1 normal phase, 2 reverse phase.
	Flash uint8
	// Enclosure is HLC: bits 1 right, 2 below, 4 left, 8 above.
	Enclosure uint8
	// DelayMS is how long to wait before showing (TIME), from the start of
	// the statement.
	DelayMS uint32
	// UntilMS is when to clear (CS after TIME), from the start of the
	// statement; without it the text stays until the next statement.
	UntilMS    uint32
	HasUntilMS bool
	runes      int
}

// Motion is a movement (a caption's SCR): from the start of the statement,
// after DelayMS, from From to To over DurationMS.
type Motion struct {
	FromX, FromY int
	ToX, ToY     int
	DelayMS      uint32
	DurationMS   uint32
	// StepEnd moves at the end at once rather than smoothly.
	StepEnd bool
}

// Layout is one statement laid out.
type Layout struct {
	// Screen is the screen's size (SWF).
	ScreenW, ScreenH uint32
	// Area is the display area (SDF, SDP). Some receivers paint it with the
	// background.
	AreaX, AreaY int
	AreaW, AreaH uint32
	// Clear is set where the screen is to be cleared first (there was a CS).
	Clear bool
	// ClearAfterMS is set for a statement that waits with TIME and then
	// clears (milliseconds); Texts are shown until then.
	ClearAfterMS    uint32
	HasClearAfterMS bool
	Texts           []Text
	Superimpose     bool
	// DRCS are the shapes this statement uses, by the DRCSRune character.
	DRCS map[rune][]DRCSGlyph
	// Raster is the raster colour (RCS) the display area is painted with;
	// transparent paints nothing.
	Raster Color
	// Sound is the built-in sound to play (PRA, 1 to 13), or 0.
	Sound uint8
	// Motion is the whole statement's movement (a scroll), where there is
	// one.
	Motion *Motion
}

type area struct {
	x, y int
	w, h uint32
}

type setup struct {
	area     area
	ssmW     uint32
	ssmH     uint32
	shs, svs uint32
}

// Presenter sets the screen up from management data and lays statement data
// out, for one language.
type Presenter struct {
	Kind Kind
	// Language is which language this lays out (1 the first, 2 the second);
	// the screen is set up from that language's entry of management data.
	Language byte
	// CProfile is set for a one-segment (C profile) caption: the character
	// sets are fixed (GL is DRCS, GR kanji; TR-B14 part 3, 6.4.1) and the
	// display area is the receiver's (16 characters by 3 lines, 6.3.1).
	CProfile bool

	screenW, screenH uint32
	area             area
	ssmW, ssmH       uint32
	shs, svs         uint32
	init             setup
	inManagement     bool
	// moved is set once the active position has moved or a character has
	// been put since CS (SWF). A receiver ignores SWF, SDF, SDP and RCS after
	// that.
	moved bool
	// sizeMode is SZX: 1 normal, 2 middle (half width), 3 small, 4 tall,
	// 5 wide, 6 tall and wide.
	sizeMode   uint8
	fg, bg     Color
	outline    Color
	hasOutline bool
	// palette is COL's palette (0 to 7); sixteen times it is added to an
	// index.
	palette  uint8
	inverted bool
	// x is the left of the character and y its bottom: the standard's
	// active position is the character's bottom left.
	x, y   int
	hidden bool
	// lastHeight is the box height of the last character put. APD and APR
	// go down by this rather than by the current size, so lines do not
	// overlap when the size changes at the end of a line.
	lastHeight    uint32
	hasLastHeight bool
	vertical      bool
	drcs          map[rune][]DRCSGlyph
	raster        Color
	// repeat is RPC: how many times to repeat the next character (0 to the
	// end of the line); -1 for none.
	repeat    int
	flash     uint8
	enclosure uint8
	delayMS   uint32
}

// NewPresenter lays out the first language.
func NewPresenter(kind Kind) *Presenter { return ForLanguage(kind, 1) }

// ForLanguage lays out the language-th language (from 1).
func ForLanguage(kind Kind, language byte) *Presenter {
	if language < 1 {
		language = 1
	}
	p := &Presenter{
		Kind:     kind,
		Language: language,
		sizeMode: 1,
		fg:       White,
		drcs:     map[rune][]DRCSGlyph{},
		repeat:   -1,
	}
	p.reset(960, 540, false)
	return p
}

// ForOneSeg lays out a one-segment (C profile) caption. The broadcast writes
// statements that fit 16 characters by 3 lines (or 12 by 4 vertically), with
// APR for a new line and the receiver wrapping at the right edge (TR-B14
// part 3, 6.3.1). On a 960 by 540 plane the 16 by 3 lines go in a band at the
// bottom: characters at the standard 36 pixels, a line 16 x 40 = 640 pixels
// wide, with margins either side.
func ForOneSeg(kind Kind) *Presenter {
	p := NewPresenter(kind)
	p.SetOneSeg()
	return p
}

// SetOneSeg switches to the one-segment layout (ForOneSeg).
func (p *Presenter) SetOneSeg() {
	p.CProfile = true
	p.reset(960, 540, false)
}

// Manage sets the screen up from management data (the language entry's
// format, table 8-4).
func (p *Presenter) Manage(m *Management) {
	p.takeDRCS(m.Units)
	var language *Language
	for i := range m.Languages {
		if m.Languages[i].Tag+1 == p.Language {
			language = &m.Languages[i]
			break
		}
	}
	if language == nil && len(m.Languages) > 0 {
		language = &m.Languages[0]
	}
	if language != nil {
		// 9 is 960x540 vertical, 10 720x480 horizontal, 11 vertical,
		// anything else 960x540 horizontal.
		switch language.Format {
		case 9:
			p.reset(960, 540, true)
		case 10:
			p.reset(720, 480, false)
		case 11:
			p.reset(720, 480, true)
		default:
			p.reset(960, 540, false)
		}
	}
	// The control codes in management data set things up; nothing is drawn.
	scratch := p.layout()
	p.inManagement = true
	for _, u := range m.Units {
		if u.Kind == UnitText {
			p.text(u.Data, &scratch)
		}
	}
	p.inManagement = false
}

func (p *Presenter) layout() Layout {
	return Layout{
		ScreenW: p.screenW, ScreenH: p.screenH,
		AreaX: p.area.x, AreaY: p.area.y, AreaW: p.area.w, AreaH: p.area.h,
		Superimpose: p.Kind == KindSuperimpose,
		DRCS:        map[rune][]DRCSGlyph{},
		Raster:      p.raster,
	}
}

// reset sets the defaults from the screen's size and direction (tables 8-2
// and 8-3).
func (p *Presenter) reset(w, h uint32, vertical bool) {
	p.ssmW, p.ssmH = 36, 36
	switch {
	case w == 720 && h == 480 && !vertical:
		p.shs, p.svs = 4, 16
	case w == 720 && h == 480 && vertical:
		p.shs, p.svs = 8, 24
	case !vertical:
		p.shs, p.svs = 4, 24
	default:
		p.shs, p.svs = 12, 24
	}
	p.screenW, p.screenH = w, h
	p.area = area{0, 0, w, h}
	if p.CProfile {
		// 16 characters by 3 lines at the bottom of the plane; 60 x 3 = 180.
		p.area = area{160, 540 - 180, 640, 180}
	}
	p.init = setup{p.area, p.ssmW, p.ssmH, p.shs, p.svs}
	p.moved = false
	p.vertical = vertical
	p.sizeMode = 1
	p.home()
}

// home moves the active position to the start of the display area's first
// line.
func (p *Presenter) home() {
	p.x = p.area.x
	_, ch := p.cell()
	p.y = p.area.y + int(ch)
	p.hidden = false
	p.hasLastHeight = false
}

func scaled(w, h uint32, mode uint8) (uint32, uint32) {
	switch mode {
	case 2:
		return w / 2, h
	case 3:
		return w / 2, h / 2
	case 4:
		return w, h * 2
	case 5:
		return w * 2, h
	case 6:
		return w * 2, h * 2
	}
	return w, h
}

// cell is one character's box, size included.
func (p *Presenter) cell() (uint32, uint32) {
	return scaled(p.ssmW+p.shs, p.ssmH+p.svs, p.sizeMode)
}

func (p *Presenter) glyph() (uint32, uint32) {
	return scaled(p.ssmW, p.ssmH, p.sizeMode)
}

// position is the active position's top left, in pixels.
func (p *Presenter) position() (int, int) {
	_, ch := p.cell()
	return p.x, p.y - int(ch)
}

func (p *Presenter) color(n uint8) Color { return Indexed(p.palette*16 + n) }

func (p *Presenter) setFg(c Color) {
	if p.inverted {
		p.bg = c
	} else {
		p.fg = c
	}
}

func (p *Presenter) setBg(c Color) {
	if p.inverted {
		p.fg = c
	} else {
		p.bg = c
	}
}

// Present lays one statement out.
func (p *Presenter) Present(s *Statement) Layout {
	layout := p.layout()
	p.delayMS = 0
	// The shapes need not come before the text (any order within one
	// statement).
	p.takeDRCS(s.Units)
	for _, u := range s.Units {
		if u.Kind == UnitText {
			p.text(u.Data, &layout)
		}
	}
	// Only the DRCS shapes used go with the layout.
	for _, t := range layout.Texts {
		for _, r := range t.Text {
			if !IsDRCS(r) {
				continue
			}
			if glyphs, ok := p.drcs[r]; ok {
				if _, had := layout.DRCS[r]; !had {
					layout.DRCS[r] = append([]DRCSGlyph(nil), glyphs...)
				}
			}
		}
	}
	return layout
}

// takeDRCS keeps the DRCS of data units. A shape of the same character at the
// same size replaces the one before.
func (p *Presenter) takeDRCS(units []Unit) {
	for _, u := range units {
		if u.Kind != UnitDRCS {
			continue
		}
		for _, e := range ParseDRCS(u.TwoByte, u.Data) {
			had := p.drcs[e.Rune][:0]
			for _, g := range p.drcs[e.Rune] {
				if g.Width != e.Glyph.Width || g.Height != e.Glyph.Height {
					had = append(had, g)
				}
			}
			p.drcs[e.Rune] = append(had, e.Glyph)
		}
	}
}

func flushRun(run **Text, layout *Layout) {
	if *run != nil && (*run).Text != "" {
		layout.Texts = append(layout.Texts, **run)
	}
	*run = nil
}

func (p *Presenter) text(b []byte, layout *Layout) {
	var run *Text
	var pieces []isdb.Piece
	if p.CProfile {
		pieces = isdb.CaptionPiecesCProfile(b)
	} else {
		pieces = isdb.CaptionPieces(b)
	}
	for _, piece := range pieces {
		switch piece.Kind {
		case isdb.PieceChar:
			p.put(&run, layout, piece.Rune)
		case isdb.PieceSpace:
			p.put(&run, layout, ' ')
		case isdb.PieceDRCS:
			p.put(&run, layout, DRCSRune(piece.DRCSSet, piece.DRCSCode, piece.Width))
		case isdb.PieceControl:
			flushRun(&run, layout)
			p.control(piece.Control, layout)
		}
	}
	flushRun(&run, layout)
}

// put puts one character and moves the active position on. A one-byte
// (half-width) character takes a full box at the normal size (table 7-14:
// an alphanumeric is one box too) and half a box at the middle size.
func (p *Presenter) put(run **Text, layout *Layout, r rune) {
	if p.hidden {
		return
	}
	cw, ch := p.cell()
	gw, gh := p.glyph()
	x, y := p.position()
	same := *run != nil && func() bool {
		t := *run
		return t.Y == y && t.CellW == cw && t.CellH == ch && t.Fg == p.fg && t.Bg == p.bg &&
			t.HasOutline == p.hasOutline && (!t.HasOutline || t.Outline == p.outline) &&
			t.Flash == p.flash && t.Enclosure == p.enclosure && t.DelayMS == p.delayMS &&
			t.X+t.runes*int(cw) == x
	}()
	if !same {
		flushRun(run, layout)
		*run = &Text{
			X: x, Y: y, CellW: cw, CellH: ch, SizeW: gw, SizeH: gh,
			Fg: p.fg, Bg: p.bg, Outline: p.outline, HasOutline: p.hasOutline,
			Flash: p.flash, Enclosure: p.enclosure, DelayMS: p.delayMS,
		}
	}
	times := uint32(1)
	switch {
	case p.repeat == 0:
		// To the end of the line.
		left := p.area.x + int(p.area.w) - p.x
		if left < 0 {
			left = 0
		}
		step := cw
		if step == 0 {
			step = 1
		}
		times = max(uint32(left)/step, 1)
	case p.repeat > 0:
		times = uint32(p.repeat)
	}
	p.repeat = -1
	// A display area can be declared any size with CSI, so a repetition
	// stops at as many characters as the screen is wide.
	times = min(times, mostRepeat)
	t := *run
	for range times {
		t.Text += string(r)
		t.runes++
	}
	p.advance(times)
	p.moved = true
	p.lastHeight, p.hasLastHeight = ch, true
}

// down moves the active position one line down (APD, APR); past the bottom of
// the display area it goes to the top line.
func (p *Presenter) down() {
	_, ch := p.cell()
	by := ch
	if p.hasLastHeight {
		by = p.lastHeight
	}
	p.y += int(by)
	if p.y > p.area.y+int(p.area.h) {
		p.y = p.area.y + int(ch)
	}
}

// advance moves the active position right, to the start of the next line at
// the right edge of the display area; one character at a time, looking each
// time.
func (p *Presenter) advance(by uint32) {
	cw, ch := p.cell()
	for range by {
		p.x += int(cw)
		if p.x >= p.area.x+int(p.area.w) {
			p.x = p.area.x
			p.y += int(ch)
		}
	}
}

func (p *Presenter) control(code []byte, layout *Layout) {
	if len(code) == 0 {
		return
	}
	first := code[0]
	cw, ch := p.cell()
	param := func(i int) (byte, bool) {
		if i < len(code) {
			return code[i], true
		}
		return 0, false
	}
	switch first {
	case 0x08, 0x09, 0x0a, 0x0b, 0x0d, 0x16, 0x1c:
		p.moved = true
	}
	switch first {
	case 0x08:
		p.x -= int(cw)
	case 0x09:
		p.advance(1)
	case 0x0a:
		p.down()
	case 0x0b:
		p.y -= int(ch)
	case 0x0d:
		// One-segment: APR straight after wrapping at the right edge does not
		// go down again (TR-B14 part 3, 6.3.1, "once where they overlap").
		if p.CProfile && p.x == p.area.x && p.moved {
			return
		}
		p.x = p.area.x
		p.down()
	case 0x0c:
		// CS clears the screen, puts the display area, character size and
		// spacing back to the setup, and the colours, polarity, flashing,
		// enclosure, outline and raster to their defaults.
		p.area, p.ssmW, p.ssmH, p.shs, p.svs = p.init.area, p.init.ssmW, p.init.ssmH, p.init.shs, p.init.svs
		p.moved = false
		if p.delayMS > 0 {
			// CS after waiting with TIME: what was put before stays up until
			// then.
			for i := range layout.Texts {
				if !layout.Texts[i].HasUntilMS {
					layout.Texts[i].UntilMS, layout.Texts[i].HasUntilMS = p.delayMS, true
				}
			}
			layout.ClearAfterMS, layout.HasClearAfterMS = p.delayMS, true
		} else {
			layout.Clear = true
			layout.Texts = nil
		}
		p.sizeMode = 1
		p.home()
		p.fg, p.bg = White, Transparent
		p.palette = 0
		p.inverted = false
		p.hasOutline = false
		p.flash, p.enclosure = 0, 0
		p.raster = Transparent
		layout.Raster = Transparent
	case 0x91:
		// FLC: flashing (0x40 normal, 0x47 reverse, 0x4F stop).
		if v, ok := param(1); ok {
			switch v {
			case 0x40:
				p.flash = 1
			case 0x47:
				p.flash = 2
			default:
				p.flash = 0
			}
		}
	case 0x97:
		// HLC: enclosure (the low four bits right, below, left, above).
		if v, ok := param(1); ok {
			p.enclosure = v & 0x0f
		}
	case 0x9d:
		// TIME: a wait (9D 20 P is (P-0x40)/10 seconds) delays what follows.
		if v, ok := param(1); ok && v == 0x20 {
			if q, ok := param(2); ok && q >= 0x40 {
				p.delayMS += uint32(q-0x40) * 100
			}
		}
	case 0x98:
		// RPC: repeat the next character (0x40 to the end of the line).
		if v, ok := param(1); ok {
			p.repeat = int(v & 0x3f)
		}
	case 0x16:
		// PAPF: move forward so many.
		if v, ok := param(1); ok {
			p.advance(uint32(v & 0x3f))
		}
	case 0x1c:
		// APS: row and column (each with 0x40 added), counted at the current
		// size (table 7-9).
		row, ok1 := param(1)
		col, ok2 := param(2)
		if ok1 && ok2 {
			p.x = p.area.x + int(uint32(col&0x3f)*cw)
			p.y = p.area.y + int((uint32(row&0x3f)+1)*ch)
			p.hidden = false
		}
	case 0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87:
		p.setFg(p.color(first - 0x80))
	case 0x90:
		// COL: 90 20 4X is the palette; 4X foreground, 5X background (the low
		// four bits the index); 6X and 7X are the half tones, not drawn.
		if v, ok := param(1); ok {
			switch {
			case v == 0x20:
				if q, ok := param(2); ok && q&0xf0 == 0x40 {
					p.palette = q & 0x07
				}
			case v >= 0x40 && v <= 0x4f:
				p.setFg(p.color(v & 0x0f))
			case v >= 0x50 && v <= 0x5f:
				p.setBg(p.color(v & 0x0f))
			}
		}
	case 0x93:
		// POL: polarity (0x40 normal, 0x41 inverted, which swaps the
		// foreground and the background).
		if v, ok := param(1); ok {
			inverted := v == 0x41
			if inverted != p.inverted {
				p.fg, p.bg = p.bg, p.fg
				p.inverted = inverted
			}
		}
	case 0x88:
		p.sizeMode = 3
	case 0x89:
		p.sizeMode = 2
	case 0x8a:
		p.sizeMode = 1
	case 0x8b:
		if v, ok := param(1); ok {
			switch v {
			case 0x60:
				p.sizeMode = 3
			case 0x41:
				p.sizeMode = 4
			case 0x44:
				p.sizeMode = 5
			case 0x45:
				p.sizeMode = 6
			default:
				p.sizeMode = 1
			}
		}
	case 0x9b:
		p.csi(code, layout)
	}
}

// csi is CSI: 9B P1 3B P2 ... F.
func (p *Presenter) csi(code []byte, layout *Layout) {
	// At least 9B and the final byte; shorter is a sequence cut off at the
	// end of the text.
	if len(code) < 2 {
		return
	}
	final := code[len(code)-1]
	var params []uint32
	var n uint32
	for _, b := range code[1 : len(code)-1] {
		if b == 0x3b {
			params = append(params, n)
			n = 0
			continue
		}
		if b >= 0x30 && b <= 0x39 {
			n = satAdd(satMul(n, 10), uint32(b-0x30))
		}
	}
	params = append(params, n)
	get := func(i int) (uint32, bool) {
		if i < len(params) {
			return params[i], true
		}
		return 0, false
	}
	switch final {
	case 0x53:
		// SWF: the screen's format (numbered differently from management
		// data's Format).
		v, ok := get(0)
		if !ok || p.moved {
			return
		}
		switch v {
		case 5:
			p.reset(1920, 1080, false)
		case 6:
			p.reset(1920, 1080, true)
		case 7:
			p.reset(960, 540, false)
		case 8:
			p.reset(960, 540, true)
		case 9:
			p.reset(720, 480, false)
		case 10:
			p.reset(720, 480, true)
		case 11:
			p.reset(1280, 720, false)
		case 12:
			p.reset(1280, 720, true)
		}
	case 0x5f:
		// SDP: the display area's position.
		x, ok1 := get(0)
		y, ok2 := get(1)
		if ok1 && ok2 && !p.moved {
			p.area.x, p.area.y = int(x), int(y)
			p.home()
			if p.inManagement {
				p.init.area = p.area
			}
		}
	case 0x56:
		// SDF: the display area's size.
		w, ok1 := get(0)
		h, ok2 := get(1)
		if ok1 && ok2 && !p.moved {
			p.area.w, p.area.h = w, h
			if p.inManagement {
				p.init.area = p.area
			}
		}
	case 0x57:
		// SSM: the character size.
		w, ok1 := get(0)
		h, ok2 := get(1)
		if ok1 && ok2 && w == h && (w == 16 || w == 20 || w == 24 || w == 30 || w == 36) {
			p.ssmW, p.ssmH = w, h
			if p.inManagement {
				p.init.ssmW, p.init.ssmH = w, h
			}
		}
	case 0x58:
		// SHS: the spacing between characters.
		if v, ok := get(0); ok {
			p.shs = v
			if p.inManagement {
				p.init.shs = v
			}
		}
	case 0x59:
		// SVS: the spacing between lines.
		if v, ok := get(0); ok {
			p.svs = v
			if p.inManagement {
				p.init.svs = v
			}
		}
	case 0x63:
		// ORN: outline (1 then the colour, written as set x 100 + index).
		if v, ok := get(0); ok && v == 1 {
			c, _ := get(1)
			p.outline, p.hasOutline = Indexed(uint8((c/100)*16+c%100)), true
		} else {
			p.hasOutline = false
		}
	case 0x61:
		// ACPS: the active position in pixels.
		x, ok1 := get(0)
		y, ok2 := get(1)
		if ok1 && ok2 {
			p.x, p.y = int(x), int(y)
			p.moved = true
			p.hidden = p.x > p.area.x+int(p.area.w) || p.y > p.area.y+int(p.area.h)
		}
	case 0x6e:
		// RCS: the raster colour (the display area's paint).
		if c, ok := get(0); ok && c < 128 && !p.moved {
			p.raster = Indexed(uint8(c))
			layout.Raster = p.raster
		}
	case 0x67:
		// SCR: scrolling (P1 1 and 2 by line, 3 and 4 by screen, the even
		// ones rolling out; P2 the speed). Text flows right to left.
		mode, ok1 := get(0)
		speed, ok2 := get(1)
		if ok1 && ok2 && mode >= 1 && mode <= 4 && speed > 0 {
			// The speed is taken as pixels a second, over the display area's
			// width.
			width := int(p.area.w)
			layout.Motion = &Motion{ToX: -width, DurationMS: uint32(width) * 1000 / speed}
		}
	case 0x68:
		// PRA: a built-in sound.
		if v, ok := get(0); ok && v >= 1 && v <= 13 {
			layout.Sound = uint8(v)
		}
	}
}

func satMul(a, b uint32) uint32 {
	if a != 0 && b > ^uint32(0)/a {
		return ^uint32(0)
	}
	return a * b
}

func satAdd(a, b uint32) uint32 {
	if a > ^uint32(0)-b {
		return ^uint32(0)
	}
	return a + b
}

// DRCSRunes are the characters a layout has shapes for, in order.
func (l Layout) DRCSRunes() []rune {
	out := make([]rune, 0, len(l.DRCS))
	for r := range l.DRCS {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
