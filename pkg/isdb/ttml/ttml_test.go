// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package ttml

import (
	"slices"
	"testing"
)

func mustParse(t *testing.T, xml string, w, h uint32) *Document {
	t.Helper()
	doc, ok := Parse(xml, w, h)
	if !ok {
		t.Fatalf("not read: %s", xml)
	}
	return doc
}

func runText(doc *Document) []string {
	var out []string
	for _, p := range doc.Paragraphs {
		for _, r := range p.Runs {
			out = append(out, r.Text)
		}
	}
	return out
}

func eq64(p *uint64, v uint64) bool { return p != nil && *p == v }

func TestKeyframesBordersOutlinesAndAudioAreRead(t *testing.T) {
	xml := `<tt xmlns="http://www.w3.org/ns/ttml" xmlns:tts="http://www.w3.org/ns/ttml#styling" xmlns:arib-tt="http://www.arib.or.jp/ns/arib-tt"><head><styling><style xml:id="s1" tts:fontSize="144px 144px" tts:lineHeight="240px" tts:textOutline="#000000ff 4px 0px" tts:textDecoration="underline" arib-tt:text-shadow="4px 4px 0px #ff0000ff"/><arib-tt:keyframes animationName="slide"><keyframe position="0%" tts:origin="3840px 1800px"/><keyframe position="100%" tts:origin="0px 1800px"/></arib-tt:keyframes></styling><layout><region xml:id="r" tts:origin="100px 1800px" tts:extent="3640px 240px" arib-tt:border="solid 8px #ffffffff" arib-tt:animation="slide 20000ms linear 0ms 1 normal"/></layout></head><body><div begin="00:00:01.000" end="00:00:21.000"><p region="r"><span style="s1">流れる</span></p><div><arib-tt:audio arib-tt:src="chime.aiff"/></div></div></body></tt>`
	doc := mustParse(t, xml, 3840, 2160)
	p := doc.Paragraphs[0]
	r := p.Runs[0]
	if r.Style.Outline == nil || r.Style.Outline.Color != Black || r.Style.Outline.Width != 4 {
		t.Errorf("outline %+v", r.Style.Outline)
	}
	if !r.Style.Underline {
		t.Error("no underline")
	}
	if r.Style.Shadow == nil || r.Style.Shadow.DX != 4 || r.Style.Shadow.DY != 4 {
		t.Errorf("shadow %+v", r.Style.Shadow)
	}
	m := p.Animation
	if m == nil || m.DurationMS != 20000 {
		t.Fatalf("animation %+v", m)
	}
	if a := m.At(0); a.DX != 3740 || a.DY != 0 {
		t.Errorf("at 0 %+v", a)
	}
	if a := m.At(1); a.DX != -100 || a.DY != 0 {
		t.Errorf("at 1 %+v", a)
	}
	if a := m.At(0.5); a.DX != 1820 {
		t.Errorf("at 0.5 %+v", a)
	}
	if doc.Regions[0].Border[0] == nil || doc.Regions[0].Border[0].Width != 8 {
		t.Errorf("border %+v", doc.Regions[0].Border)
	}
	if len(doc.Audio) != 1 || doc.Audio[0].Src != "chime.aiff" || !eq64(doc.Audio[0].Begin, 1000) {
		t.Errorf("audio %+v", doc.Audio)
	}
	if !eq64(p.Begin, 1000) || !eq64(p.End, 21000) {
		t.Errorf("times %v %v", p.Begin, p.End)
	}
}

func TestABroadcastDocumentIsLaidOutInItsRegion(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?><tt xmlns="http://www.w3.org/ns/ttml" xmlns:tts="http://www.w3.org/ns/ttml#styling" xmlns:arib-tt="http://www.arib.or.jp/ns/arib-tt" xml:lang="ja"><head><styling><style xml:id="middleSize1" tts:fontSize="72px 144px" tts:lineHeight="240px" arib-tt:letter-spacing="8px"/><style xml:id="foreColor1" tts:color="#ffffffff"/><style xml:id="backColor1" tts:backgroundColor="#00000080"/><style xml:id="normalSize1" tts:fontSize="144px 144px" tts:lineHeight="240px" arib-tt:letter-spacing="16px"/></styling><layout><region xml:id="r690" tts:origin="1592px 1796px" tts:extent="640px 240px"/></layout></head><body><div begin="00:12:32.000" end="00:12:35.000"><p xml:id="P1880-1" region="r690"><span style="middleSize1 foreColor1 backColor1">（</span><span style="normalSize1 foreColor1 backColor1">落石音</span><span style="middleSize1 foreColor1 backColor1">）</span></p></div></body></tt>`
	doc := mustParse(t, xml, 3840, 2160)
	p := doc.Paragraphs[0]
	if len(p.Runs) != 3 {
		t.Fatalf("runs %+v", p.Runs)
	}
	if p.Runs[0].X != 1592 || p.Runs[0].Y != 1796 || p.Runs[0].CellW != 80 || p.Runs[0].CellH != 240 {
		t.Errorf("first %+v", p.Runs[0])
	}
	if p.Runs[1].X != 1592+80 || p.Runs[1].Text != "落石音" || p.Runs[1].CellW != 160 || p.Runs[1].CellH != 240 || p.Runs[1].Style.Bg.A != 0x80 {
		t.Errorf("second %+v", p.Runs[1])
	}
	if p.Clip == nil || *p.Clip != (Rect{1592, 1796, 640, 240}) {
		t.Errorf("clip %+v", p.Clip)
	}
	if doc.Begin() != 752_000 {
		t.Errorf("begin %d", doc.Begin())
	}
	if end, ok := doc.End(); !ok || end != 755_000 {
		t.Errorf("end %d %v", end, ok)
	}
	if len(doc.ShownAt(752_000)) == 0 || len(doc.ShownAt(755_000)) != 0 {
		t.Error("shown at")
	}
}

// A clear packet (TR-B39 8.11.5), <tt></tt> or a head alone, reads as a
// document with nothing to show; one that did not read would leave the
// previous caption up.
func TestAClearPacketIsReadAsADocumentWithNothingToShow(t *testing.T) {
	for _, xml := range []string{
		"<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<tt></tt>",
		"<tt/>",
		"<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<tt xmlns=\"http://www.w3.org/ns/ttml\" xmlns:tts=\"http://www.w3.org/ns/ttml#styling\">\n<head>\n</head>\n</tt>",
		"<tt xmlns=\"http://www.w3.org/ns/ttml\"><head><styling/></head><body/></tt>",
		"<?xml version=\"1.0\" encoding=\"utf-8\"?><tt xmlns=\"http://www.w3.org/ns/ttml\" xmlns:tts=\"http://www.w3.org/ns/ttml#styling\" xmlns:arib-tt=\"http://www.arib.or.jp/ns/arib-tt\" ><body><div><p><span/></p></div></body></tt>",
	} {
		if doc := mustParse(t, xml, 3840, 2160); !doc.IsClear() {
			t.Errorf("not clear: %s", xml)
		}
	}
}

// Each paragraph keeps its own times, relative to its parent's start (TR-B39
// 8.5.9.1).
func TestParagraphsKeepTheirOwnTimesRelativeToTheParent(t *testing.T) {
	xml := `<tt><head><styling><style xml:id="s" tts:fontSize="64px"/></styling><layout><region xml:id="r" tts:origin="0px 0px" tts:extent="640px 64px"/></layout></head><body><div begin="00:00:10.000"><p region="r" begin="00:00:01.000" end="00:00:03.000"><span style="s">a</span></p><p region="r" begin="00:00:02.000"><span style="s">b</span></p></div></body></tt>`
	doc := mustParse(t, xml, 1920, 1080)
	if !eq64(doc.Paragraphs[0].Begin, 11_000) || !eq64(doc.Paragraphs[0].End, 13_000) ||
		!eq64(doc.Paragraphs[1].Begin, 12_000) || doc.Paragraphs[1].End != nil {
		t.Fatalf("times %+v", doc.Paragraphs)
	}
	if doc.Begin() != 11_000 {
		t.Errorf("begin %d", doc.Begin())
	}
	if _, ok := doc.End(); ok {
		t.Error("a paragraph that never clears gave an end")
	}
	at := func(ms uint64) []string {
		var out []string
		for _, p := range doc.ShownAt(ms) {
			out = append(out, p.Runs[0].Text)
		}
		return out
	}
	for ms, want := range map[uint64][]string{10_500: nil, 11_000: {"a"}, 12_500: {"a", "b"}, 13_000: {"b"}} {
		if got := at(ms); !slices.Equal(got, want) {
			t.Errorf("at %d %q want %q", ms, got, want)
		}
	}
}

// Character frames (TR-B39 8.3.4) are width plus spacing by the line height.
// A span with its own region is its own area, placed from that origin.
func TestASpanWithItsOwnRegionIsItsOwnArea(t *testing.T) {
	xml := `<tt><head><styling><style xml:id="s" tts:fontSize="60px" arib-tt:letter-spacing="20px" tts:lineHeight="80px"/></styling><layout><region xml:id="p1" tts:origin="100px 100px" tts:extent="490px 170px"/><region xml:id="p2" tts:origin="185px 180px" tts:extent="490px 90px"/></layout></head><body><div><p region="p1"><span style="s">あい</span><span style="s" region="p2">うえ</span><span style="s">お</span></p></div></body></tt>`
	doc := mustParse(t, xml, 1920, 1080)
	if len(doc.Paragraphs) != 2 {
		t.Fatalf("paragraphs %+v", doc.Paragraphs)
	}
	first := doc.Paragraphs[0].Runs
	if first[0].X != 100 || first[0].Y != 100 || first[0].CellW != 80 || first[0].CellH != 80 {
		t.Errorf("first %+v", first[0])
	}
	if b := first[0].Bounds(); b != (Rect{100, 100, 160, 80}) {
		t.Errorf("bounds %+v", b)
	}
	if first[1].Text != "お" || first[1].X != 260 {
		t.Errorf("p's region carries on: %+v", first[1])
	}
	second := doc.Paragraphs[1]
	if second.Clip == nil || *second.Clip != (Rect{185, 180, 490, 90}) || second.Runs[0].X != 185 || second.Runs[0].Y != 180 {
		t.Errorf("second %+v", second)
	}
}

// A line is as thick as its largest line height.
func TestALineIsAsThickAsItsTallestRun(t *testing.T) {
	xml := `<tt><head><styling><style xml:id="a" tts:fontSize="60px" tts:lineHeight="80px"/><style xml:id="b" tts:fontSize="40px" tts:lineHeight="50px"/></styling><layout><region xml:id="r" tts:origin="0px 0px" tts:extent="600px 300px"/></layout></head><body><div><p region="r"><span style="b">小</span><span style="a">大</span><br/><span style="b">次</span></p></div></body></tt>`
	runs := mustParse(t, xml, 1920, 1080).Paragraphs[0].Runs
	if runs[0].CellW != 40 || runs[0].CellH != 80 || runs[1].CellW != 60 || runs[1].CellH != 80 {
		t.Errorf("cells %+v %+v", runs[0], runs[1])
	}
	if runs[2].Y != 80 || runs[2].CellW != 40 || runs[2].CellH != 50 {
		t.Errorf("next line %+v", runs[2])
	}
}

// A region's border is the display area's; a span's border-* the character
// frames'.
func TestBordersOnTheRegionAndOnTheSpanAreToldApart(t *testing.T) {
	xml := `<tt><head><styling><style xml:id="ss" tts:fontSize="60px" arib-tt:border="solid 5px #0000ff"/><style xml:id="s1" arib-tt:border-top="solid 5px #0000ff" arib-tt:border-left="solid 5px #0000ff"/></styling><layout><region xml:id="p1" style="ss" tts:origin="100px 100px" tts:extent="490px 90px"/></layout></head><body><div><p region="p1"><span>あ</span><span style="s1">い</span></p></div></body></tt>`
	doc := mustParse(t, xml, 1920, 1080)
	if doc.Regions[0].Border[0] == nil || doc.Regions[0].Border[0].Width != 5 {
		t.Errorf("region border %+v", doc.Regions[0].Border)
	}
	runs := doc.Paragraphs[0].Runs
	if runs[0].Style.Border != ([4]*Edge{}) {
		t.Error("the region's border went on the characters")
	}
	if runs[1].Style.Border[0] == nil || runs[1].Style.Border[3] == nil || runs[1].Style.Border[1] != nil {
		t.Errorf("span border %+v", runs[1].Style.Border)
	}
}

// Blinking (TR-B39 8.5.7.1) is opacity frames repeated with step-end.
func TestBlinkingIsAnOpacityAnimationSteppingBetweenFrames(t *testing.T) {
	xml := `<tt><head><styling><arib-tt:keyframes animationName="blink"><arib-tt:keyframe position="0%" tts:opacity="1"/><arib-tt:keyframe position="50%" tts:opacity="0"/><arib-tt:keyframe position="100%" tts:opacity="1"/></arib-tt:keyframes><style xml:id="s001" tts:fontSize="46px"/><style xml:id="s003" arib-tt:animation="blink 2000ms step-end 0ms 100 normal"/></styling><layout><region xml:id="r001" style="s001 s003" tts:origin="0px 40px" tts:extent="300px 90px"/></layout></head><body><div><p region="r001"><span>x</span></p></div></body></tt>`
	a := mustParse(t, xml, 1920, 1080).Paragraphs[0].Animation
	if a == nil || a.Iterations == nil || *a.Iterations != 100 {
		t.Fatalf("animation %+v", a)
	}
	for ms, want := range map[uint64]float32{0: 1, 500: 1, 1000: 0, 1999: 0, 2000: 1, 201_000: 1} {
		if got := a.MotionAt(ms).Opacity; got != want {
			t.Errorf("at %d opacity %v want %v", ms, got, want)
		}
	}
	if !a.Running(1000) || a.Running(200_000) {
		t.Error("running")
	}
}

// Vertically, tts:origin is the display area's top right (TR-B39 8.5.5.9).
// Columns go right to left, characters down.
func TestVerticalWritingHangsFromTheTopRightOrigin(t *testing.T) {
	xml := `<tt><head><styling><style xml:id="s" tts:fontSize="60px" tts:lineHeight="80px" tts:writingMode="tbrl"/></styling><layout><region xml:id="r" style="s" tts:origin="1160px 100px" tts:extent="160px 600px"/></layout></head><body><div><p region="r"><span>あい</span><br/><span>う</span></p></div></body></tt>`
	p := mustParse(t, xml, 1920, 1080).Paragraphs[0]
	if p.Clip == nil || *p.Clip != (Rect{1000, 100, 160, 600}) {
		t.Errorf("clip %+v", p.Clip)
	}
	runs := p.Runs
	if !runs[0].Vertical || runs[0].X != 1080 || runs[0].Y != 100 || runs[0].CellW != 80 || runs[0].CellH != 60 {
		t.Errorf("first %+v", runs[0])
	}
	if runs[1].X != 1000 || runs[1].Y != 100 {
		t.Errorf("second %+v", runs[1])
	}
	if c := runs[0].Cells()[1]; c != (Cell{'い', 1080, 160}) {
		t.Errorf("cell %+v", c)
	}
}

// Still pictures (smpte:backgroundImage), font declarations and pictures in
// the document.
func TestImagesFontsAndEmbeddedPicturesAreRead(t *testing.T) {
	xml := "<tt xmlns:smpte=\"http://www.smpte-ra.org/schemas/2052-1/2013/smpte-tt\"><head><metadata><smpte:image xml:id=\"Img1\" imageType=\"PNG\" encoding=\"Base64\">AQID\nBA==</smpte:image></metadata><styling><arib-tt:font-face font-family=\"Round gothic\" unicode-range=\"U+F000-F005, U+E000\"><arib-tt:src url=\"subt://1\" format=\"svg\"/></arib-tt:font-face></styling><layout><region xml:id=\"r\" tts:origin=\"10px 20px\" tts:extent=\"300px 200px\"/></layout></head><body><div><div region=\"r\" begin=\"00:00:05\" end=\"00:00:10\" smpte:backgroundImage=\"subt://2\"/><div region=\"r\" smpte:backgroundImage=\"#Img1\"/></div></body></tt>"
	doc := mustParse(t, xml, 1920, 1080)
	if len(doc.Images) != 2 || doc.Images[0].Src != "subt://2" || !eq64(doc.Images[0].Begin, 5000) || !eq64(doc.Images[0].End, 10_000) ||
		doc.Images[0].Rect != (Rect{10, 20, 300, 200}) {
		t.Errorf("images %+v", doc.Images)
	}
	if !slices.Equal(doc.Embedded["Img1"], []byte{1, 2, 3, 4}) {
		t.Errorf("embedded %v", doc.Embedded["Img1"])
	}
	if !slices.Equal(doc.Fonts[0].Ranges, [][2]uint32{{0xF000, 0xF005}, {0xE000, 0xE000}}) {
		t.Errorf("ranges %v", doc.Fonts[0].Ranges)
	}
	if !doc.Fonts[0].Covers('') || doc.Fonts[0].Covers('a') {
		t.Error("covers")
	}
	if doc.IsClear() {
		t.Error("clear")
	}
}

// Spaces collapse, but those around <br/> stay; new lines and tabs are spaces.
func TestSpacesCollapseButSurviveAroundABr(t *testing.T) {
	xml := "<tt><head><styling><style xml:id=\"s\" tts:fontSize=\"60px\"/></styling><layout><region xml:id=\"r\" tts:origin=\"0px 0px\" tts:extent=\"600px 200px\"/></layout></head><body><div><p region=\"r\"><span style=\"s\">あいうえお <br/>\n   かきくけこ</span></p></div></body></tt>"
	doc := mustParse(t, xml, 1920, 1080)
	if got := runText(doc); !slices.Equal(got, []string{"あいうえお ", " かきくけこ"}) {
		t.Errorf("runs %q", got)
	}
	if doc.Paragraphs[0].Runs[1].Y != int(normalLineHeight(60)) {
		t.Errorf("y %d", doc.Paragraphs[0].Runs[1].Y)
	}
}

// What does not fit goes to the next line: wrapped at the area's width,
// dropping the space before the wrap and the one starting the next line.
func TestTextWrapsAtTheAreaWidth(t *testing.T) {
	xml := `<tt><head><styling><style xml:id="s" tts:fontSize="60px" tts:lineHeight="80px"/></styling><layout><region xml:id="r" tts:origin="0px 0px" tts:extent="200px 400px"/></layout></head><body><div><p region="r"><span style="s">あいう えおか</span></p></div></body></tt>`
	doc := mustParse(t, xml, 1920, 1080)
	if got := runText(doc); !slices.Equal(got, []string{"あいう", "えおか"}) {
		t.Errorf("runs %q", got)
	}
	runs := doc.Paragraphs[0].Runs
	if runs[0].Y != 0 || runs[1].Y != 80 {
		t.Errorf("ys %d %d", runs[0].Y, runs[1].Y)
	}
	// A character bigger than the area is a line of its own.
	xml = `<tt><head><styling><style xml:id="s" tts:fontSize="300px"/></styling><layout><region xml:id="r" tts:origin="0px 0px" tts:extent="200px 900px"/></layout></head><body><div><p region="r"><span style="s">あい</span></p></div></body></tt>`
	if got := runText(mustParse(t, xml, 1920, 1080)); !slices.Equal(got, []string{"あ", "い"}) {
		t.Errorf("runs %q", got)
	}
}

// Paragraphs in one region at one time are one flow. The colour is the
// span's style, else the region's; the region's ground is the whole region.
func TestParagraphsInOneRegionAtOneTimeFlowTogether(t *testing.T) {
	xml := `<tt><head><styling><style xml:id="s" tts:fontSize="60px" tts:color="#ff0000" tts:backgroundColor="#00000080"/><style xml:id="g" tts:color="#00ff00"/></styling><layout><region xml:id="r" style="s" tts:origin="0px 0px" tts:extent="600px 200px"/></layout></head><body><div><p region="r"><span>あ</span></p><p region="r"><span style="g">い</span></p></div></body></tt>`
	doc := mustParse(t, xml, 1920, 1080)
	if len(doc.Paragraphs) != 1 {
		t.Fatalf("paragraphs %d", len(doc.Paragraphs))
	}
	runs := doc.Paragraphs[0].Runs
	if runs[0].Text != "あ" || runs[0].X != 0 || runs[1].Text != "い" || runs[1].X != 60 {
		t.Errorf("runs %+v", runs)
	}
	if runs[0].Style.Fg != (Rgba{255, 0, 0, 255}) || runs[1].Style.Fg != (Rgba{0, 255, 0, 255}) {
		t.Errorf("colours %+v %+v", runs[0].Style.Fg, runs[1].Style.Fg)
	}
	if runs[0].Style.Bg != Transparent {
		t.Error("the characters' ground is the span's style alone")
	}
	if doc.Regions[0].Bg.A != 0x80 {
		t.Errorf("region ground %+v", doc.Regions[0].Bg)
	}
}

// A span's border sits outside, and the next span moves by its width.
func TestASpanBorderSitsOutsideAndPushesTheNextSpan(t *testing.T) {
	xml := `<tt><head><styling><style xml:id="s" tts:fontSize="60px"/><style xml:id="b" arib-tt:border="solid 5px #0000ff"/></styling><layout><region xml:id="r" tts:origin="100px 100px" tts:extent="600px 200px"/></layout></head><body><div><p region="r"><span style="s b">あ</span><span style="s">い</span></p></div></body></tt>`
	runs := mustParse(t, xml, 1920, 1080).Paragraphs[0].Runs
	if runs[0].X != 105 {
		t.Errorf("x %d", runs[0].X)
	}
	if f := runs[0].Framed(); f != (Rect{100, 95, 70, normalLineHeight(60) + 10}) {
		t.Errorf("framed %+v", f)
	}
	if runs[1].X != 170 {
		t.Errorf("next x %d", runs[1].X)
	}
}

func TestUnderlinePosition(t *testing.T) {
	l80, l240 := uint32(80), uint32(240)
	for _, c := range []struct {
		vertical   bool
		w, h       uint32
		line       *uint32
		at, weight uint32
	}{
		{false, 60, 60, &l80, 67, 8},
		{false, 144, 144, &l240, 185, 18},
		{true, 60, 60, &l80, 4, 8},
	} {
		at, weight := underlineAt(c.vertical, c.w, c.h, c.line)
		if at != c.at || weight != c.weight {
			t.Errorf("%+v: got %d %d", c, at, weight)
		}
	}
	if normalLineHeight(60) != 73 {
		t.Errorf("normal %d", normalLineHeight(60))
	}
}

func TestClockForms(t *testing.T) {
	for in, want := range map[string]uint64{
		"00:12:32.000": 752_000, "01:00:00.5": 3_600_500, "00:00:01:15": 1_500, "2.5s": 2_500, "300ms": 300,
	} {
		if got, ok := Clock(in); !ok || got != want {
			t.Errorf("%s: %d %v", in, got, ok)
		}
	}
	if _, ok := Clock("nonsense"); ok {
		t.Error("nonsense read")
	}
}
