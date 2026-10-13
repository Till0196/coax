// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package svg

import (
	"math"
	"testing"
)

// A square filling its box is filled entirely at any size.
func TestASquareGlyphFillsItsBoxAtAnySize(t *testing.T) {
	glyphs := FontGlyphs(`<svg xmlns="http://www.w3.org/2000/svg"><defs><font><font-face units-per-em="1024" ascent="880" descent="144"/><glyph unicode="&#xe054;" horiz-adv-x="1024" d="M0,-144 L1024,-144 L1024,880 L0,880 Z"/></font></defs></svg>`)
	if len(glyphs) != 1 || glyphs[0].Rune != '' {
		t.Fatalf("glyphs %+v", glyphs)
	}
	for _, size := range [][2]int{{96, 96}, {144, 144}, {72, 144}} {
		bitmap := glyphs[0].Outline.Rasterise(size[0], size[1])
		full := 0
		for _, v := range bitmap {
			if v == 255 {
				full++
			}
		}
		if full != size[0]*size[1] {
			t.Errorf("%v: %d full", size, full)
		}
	}
}

// The upper half (written with curves) is filled at the top and clear at the
// bottom: a font's y is upwards, so nearer the ascent is higher.
func TestTheUpperHalfLandsInTheUpperRowsAndCurvesAreRead(t *testing.T) {
	outline := FontGlyphs(`<svg><font><font-face units-per-em="1024" ascent="880" descent="144"/><glyph unicode="A" d="M0,368 Q512,368 1024,368 L1024,880 Q512,880 0,880 Z"/></font></svg>`)[0].Outline
	side := 96
	bitmap := outline.Rasterise(side, side)
	if bitmap[10*side+side/2] != 255 || bitmap[(side-10)*side+side/2] != 0 {
		t.Fatal("halves")
	}
}

// A hole wound the other way is left open.
func TestAHoleWoundTheOtherWayIsLeftOpen(t *testing.T) {
	outline := FontGlyphs(`<svg><font><font-face units-per-em="1024" ascent="880" descent="144"/><glyph unicode="O" d="M0,-144 L1024,-144 L1024,880 L0,880 Z M256,112 L256,624 L768,624 L768,112 Z"/></font></svg>`)[0].Outline
	side := 96
	bitmap := outline.Rasterise(side, side)
	if bitmap[side/2*side+side/2] != 0 {
		t.Error("the hole")
	}
	if bitmap[4*side+4] != 255 {
		t.Error("the ring")
	}
}

// A button mark: the viewBox is mapped onto the square, y down. A rectangle
// over the top half is in the top rows.
func TestAnIconIsLaidInItsBoxTopDown(t *testing.T) {
	side := 32
	bitmap, ok := Icon(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect x="0" y="0" width="24" height="12"/><path fill="none" d="M0,0 L24,24"/></svg>`, side)
	if !ok || len(bitmap) != side*side {
		t.Fatalf("bitmap %d %v", len(bitmap), ok)
	}
	if bitmap[4*side+16] != 255 || bitmap[28*side+16] != 0 {
		t.Error("top and bottom")
	}
	bitmap, ok = Icon(`<svg viewBox="0 0 10 10"><circle cx="5" cy="5" r="4"/><polygon points="0,0 2,0 0,2"/></svg>`, 20)
	if !ok || bitmap[10*20+10] != 255 || bitmap[19*20+19] != 0 {
		t.Error("circle")
	}
}

// Colours, transforms and groups: a red square moved right in a g, and a half
// transparent blue over it.
func TestColoursTransformsAndGroupsMakeAPicture(t *testing.T) {
	d, ok := ParseDrawing(`<svg viewBox="0 0 20 10"><g transform="translate(10,0)" fill="#ff0000"><rect x="0" y="0" width="10" height="10"/></g><rect x="0" y="0" width="5" height="10" style="fill:blue;fill-opacity:0.5"/><rect x="0" y="5" width="5" height="5" fill="none"/></svg>`)
	if !ok || len(d.Shapes) != 2 {
		t.Fatalf("drawing %+v", d)
	}
	rgba := d.Rasterise(20, 10)
	px := func(x, y int) [4]uint8 {
		i := (y*20 + x) * 4
		return [4]uint8{rgba[i], rgba[i+1], rgba[i+2], rgba[i+3]}
	}
	if px(15, 5) != [4]uint8{255, 0, 0, 255} {
		t.Errorf("red %v", px(15, 5))
	}
	if px(2, 5) != [4]uint8{0, 0, 255, 128} {
		t.Errorf("blue %v", px(2, 5))
	}
	if px(7, 5)[3] != 0 {
		t.Error("where nothing is is transparent")
	}
}

func TestTransformsComposeLikeSVG(t *testing.T) {
	if p := ParseTransform("translate(10,0) scale(2)").Apply(Point{1, 1}); p != (Point{12, 2}) {
		t.Errorf("translate scale %v", p)
	}
	p := ParseTransform("rotate(90)").Apply(Point{1, 0})
	if math.Abs(float64(p.X)) > 1e-5 || math.Abs(float64(p.Y-1)) > 1e-5 {
		t.Errorf("rotate %v", p)
	}
	if p := ParseTransform("matrix(1 0 0 1 5 6)").Apply(Point{}); p != (Point{5, 6}) {
		t.Errorf("matrix %v", p)
	}
}

// Relative steps and H, V, T and S are read.
func TestRelativeAndShorthandStepsAreRead(t *testing.T) {
	steps := Path("m10,20 h30 v-5 l-10,10 q5,5 10,0 t10,0 c1,1 2,2 3,3 s1,1 2,2 z")
	want := []Step{
		moveTo(Point{10, 20}),
		lineTo(Point{40, 20}),
		lineTo(Point{40, 15}),
		lineTo(Point{30, 25}),
		quadTo(Point{35, 30}, Point{40, 25}),
		quadTo(Point{45, 20}, Point{50, 25}),
		cubicTo(Point{51, 26}, Point{52, 27}, Point{53, 28}),
		cubicTo(Point{54, 29}, Point{54, 29}, Point{55, 30}),
		{Kind: StepClose},
	}
	if len(steps) != len(want) {
		t.Fatalf("steps %+v", steps)
	}
	for i := range want {
		if steps[i] != want[i] {
			t.Errorf("step %d %+v want %+v", i, steps[i], want[i])
		}
	}
	// 1.5.3 is two numbers.
	if s := Path("M1.5.3L2 2"); s[0] != moveTo(Point{1.5, 0.3}) {
		t.Errorf("1.5.3 %+v", s[0])
	}
}
