// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package svg is a small SVG reader: outlines are kept as vectors and turned
// into pixels at the size they are drawn.
//
// It reads what broadcasting uses (STD-B62 volume 1 part 2's fonts in 5.5 and
// pictures in 3.5, and a receiver's button marks): fills of <path d>, <rect>,
// <circle>, <ellipse>, <polygon>, <polyline> and <line>; fill, fill-opacity
// and opacity (in style too); transform (matrix, translate, scale, rotate,
// skewX, skewY) and its inheritance through <g>; and viewBox. Strokes are not
// read. An SVG font (<font><glyph unicode d>) becomes an outline per
// character.
//
// Pixels are made with a non-zero winding scan line, and curves are split for
// the size they are drawn at; drawing a bitmap once and stretching it blunts
// the edges of a character.
package svg

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Till0196/coax/pkg/isdb/xmltree"
)

// Rgba is a colour.
type Rgba [4]uint8

// Point is a position.
type Point struct{ X, Y float32 }

// StepKind says what one Step is.
type StepKind uint8

const (
	StepMove StepKind = iota
	StepLine
	StepQuad
	StepCubic
	StepClose
)

// Step is one move of an outline, in absolute coordinates. P is the end point,
// C1 and C2 the control points a curve takes.
type Step struct {
	Kind      StepKind
	C1, C2, P Point
}

func moveTo(p Point) Step          { return Step{Kind: StepMove, P: p} }
func lineTo(p Point) Step          { return Step{Kind: StepLine, P: p} }
func quadTo(c, p Point) Step       { return Step{Kind: StepQuad, C1: c, P: p} }
func cubicTo(c1, c2, p Point) Step { return Step{Kind: StepCubic, C1: c1, C2: c2, P: p} }

// Transform is a two-dimensional affine transform (a c e / b d f).
type Transform [6]float32

// Identity changes nothing.
var Identity = Transform{1, 0, 0, 1, 0, 0}

// Apply transforms a point.
func (t Transform) Apply(p Point) Point {
	a, b, c, d, e, f := t[0], t[1], t[2], t[3], t[4], t[5]
	return Point{a*p.X + c*p.Y + e, b*p.X + d*p.Y + f}
}

// Then is t followed by other (other times t).
func (t Transform) Then(o Transform) Transform {
	a1, b1, c1, d1, e1, f1 := t[0], t[1], t[2], t[3], t[4], t[5]
	a2, b2, c2, d2, e2, f2 := o[0], o[1], o[2], o[3], o[4], o[5]
	return Transform{
		a2*a1 + c2*b1,
		b2*a1 + d2*b1,
		a2*c1 + c2*d1,
		b2*c1 + d2*d1,
		a2*e1 + c2*f1 + e2,
		b2*e1 + d2*f1 + f2,
	}
}

func rotation(deg float32) Transform {
	r := float64(deg) * math.Pi / 180
	s, c := float32(math.Sin(r)), float32(math.Cos(r))
	return Transform{c, s, -s, c, 0, 0}
}

func tanDeg(deg float32) float32 { return float32(math.Tan(float64(deg) * math.Pi / 180)) }

// ParseTransform reads transform="translate(...) rotate(...) ...". The list
// is applied left as outermost (SVG applies the rightmost first).
func ParseTransform(text string) Transform {
	out := Identity
	rest := text
	for {
		open := strings.IndexByte(rest, '(')
		if open < 0 {
			break
		}
		name := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(rest[:open]), ","))
		close := strings.IndexByte(rest[open:], ')')
		if close < 0 {
			break
		}
		args := numbers(rest[open+1 : open+close])
		rest = rest[open+close+1:]
		var t Transform
		switch {
		case name == "matrix" && len(args) == 6:
			t = Transform{args[0], args[1], args[2], args[3], args[4], args[5]}
		case name == "translate" && len(args) == 1:
			t = Transform{1, 0, 0, 1, args[0], 0}
		case name == "translate" && len(args) == 2:
			t = Transform{1, 0, 0, 1, args[0], args[1]}
		case name == "scale" && len(args) == 1:
			t = Transform{args[0], 0, 0, args[0], 0, 0}
		case name == "scale" && len(args) == 2:
			t = Transform{args[0], 0, 0, args[1], 0, 0}
		case name == "rotate" && len(args) == 1:
			t = rotation(args[0])
		case name == "rotate" && len(args) == 3:
			cx, cy := args[1], args[2]
			t = Transform{1, 0, 0, 1, -cx, -cy}.Then(rotation(args[0])).Then(Transform{1, 0, 0, 1, cx, cy})
		case name == "skewX" && len(args) == 1:
			t = Transform{1, 0, tanDeg(args[0]), 1, 0, 0}
		case name == "skewY" && len(args) == 1:
			t = Transform{1, tanDeg(args[0]), 0, 1, 0, 0}
		default:
			continue
		}
		// The list's left is outermost: translate rotate rotates, then
		// translates.
		out = t.Then(out)
	}
	return out
}

// ViewBox is a box (x, y, width, height).
type ViewBox struct{ X, Y, W, H float32 }

// Outline is an area to fill. Coordinates are in ViewBox's units, y down.
type Outline struct {
	Steps []Step
	// ViewBox is mapped onto the whole rectangle drawn.
	ViewBox ViewBox
}

// Transformed is the outline with a transform applied.
func (o Outline) Transformed(t Transform) Outline {
	steps := make([]Step, len(o.Steps))
	for i, s := range o.Steps {
		switch s.Kind {
		case StepMove, StepLine:
			s.P = t.Apply(s.P)
		case StepQuad:
			s.C1, s.P = t.Apply(s.C1), t.Apply(s.P)
		case StepCubic:
			s.C1, s.C2, s.P = t.Apply(s.C1), t.Apply(s.C2), t.Apply(s.P)
		}
		steps[i] = s
	}
	return Outline{Steps: steps, ViewBox: o.ViewBox}
}

// Rasterise draws the outline into width x height pixels (coverage, 0 to
// 255). The box fills the rectangle (the aspect is not kept: a character's
// width and height are said separately).
func (o Outline) Rasterise(width, height int) []uint8 {
	if width <= 0 || height <= 0 {
		return nil
	}
	return Coverage(o.segments(width, height), width, height)
}

func (o Outline) segments(width, height int) []Segment {
	v := o.ViewBox
	sx := float32(width) / max(v.W, 1e-6)
	sy := float32(height) / max(v.H, 1e-6)
	toPixel := Transform{sx, 0, 0, sy, -v.X * sx, -v.Y * sy}
	return Flatten(o.Transformed(toPixel).Steps)
}

// Shape is one fill.
type Shape struct {
	Outline Outline
	Fill    Rgba
}

// Drawing is one picture.
type Drawing struct {
	ViewBox ViewBox
	Shapes  []Shape
}

// ParseDrawing reads an SVG document; the root if it is <svg>, else the first
// <svg>.
func ParseDrawing(text string) (*Drawing, bool) {
	root, ok := xmltree.Parse(text)
	if !ok {
		return nil, false
	}
	if root.Name != "svg" {
		found := root.FindAll("svg")
		if len(found) == 0 {
			return nil, false
		}
		root = found[0]
	}
	var vb ViewBox
	if v, ok := root.Attr("viewBox"); ok {
		if n := numbers(v); len(n) == 4 && n[2] > 0 && n[3] > 0 {
			vb = ViewBox{n[0], n[1], n[2], n[3]}
		}
	}
	if vb.W == 0 {
		size := func(name string, or float32) float32 {
			if v, ok := root.Attr(name); ok {
				if f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(v), "px"), 32); err == nil {
					return float32(f)
				}
			}
			return or
		}
		vb = ViewBox{0, 0, size("width", 24), size("height", 24)}
	}
	d := &Drawing{ViewBox: vb}
	black := Rgba{0, 0, 0, 255}
	collect(root, paint{fill: &black, opacity: 1, transform: Identity}, vb, &d.Shapes)
	return d, true
}

// Size is the box's size (when used as pixels).
func (d *Drawing) Size() (float32, float32) { return d.ViewBox.W, d.ViewBox.H }

// Rasterise draws into width x height RGBA, row by row from the top, alpha not
// premultiplied.
func (d *Drawing) Rasterise(width, height int) []uint8 {
	out := make([]uint8, width*height*4)
	for _, s := range d.Shapes {
		cover := s.Outline.Rasterise(width, height)
		for i, c := range cover {
			if c == 0 {
				continue
			}
			alpha := float32(c) / 255 * float32(s.Fill[3]) / 255
			blend(out[i*4:i*4+4], s.Fill[0], s.Fill[1], s.Fill[2], alpha)
		}
	}
	return out
}

// RasteriseAlpha draws the fills as coverage of one colour (button marks and
// the like; the drawer applies the colour).
func (d *Drawing) RasteriseAlpha(width, height int) []uint8 {
	out := make([]uint8, width*height)
	for _, s := range d.Shapes {
		cover := s.Outline.Rasterise(width, height)
		a := float32(s.Fill[3]) / 255
		for i, c := range cover {
			v := uint32(float32(c)*a) + uint32(out[i])
			out[i] = uint8(min(v, 255))
		}
	}
	return out
}

// blend lays a colour over a pixel (source over).
func blend(pixel []uint8, r, g, b uint8, alpha float32) {
	dstA := float32(pixel[3]) / 255
	outA := alpha + dstA*(1-alpha)
	if outA <= 0 {
		return
	}
	for k, src := range [3]uint8{r, g, b} {
		dst := float32(pixel[k]) * dstA
		v := (float32(src)*alpha + dst*(1-alpha)) / outA
		pixel[k] = clampByte(v)
	}
	pixel[3] = clampByte(outA * 255)
}

func clampByte(v float32) uint8 {
	return uint8(max(0, min(255, math.Round(float64(v)))))
}

// paint is the fill state that is inherited.
type paint struct {
	// fill is nil for fill="none".
	fill      *Rgba
	opacity   float32
	transform Transform
}

func parseF(v string) (float32, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 32)
	if err != nil {
		return 0, false
	}
	return float32(f), true
}

func collect(el *xmltree.Element, inherited paint, vb ViewBox, out *[]Shape) {
	p := inherited
	// style="fill:...; opacity:..." and attributes. An attribute is weaker
	// than style in CSS, but a broadcast's pictures use one or the other, so
	// both are taken alike.
	fillAttr, hasFill := el.Attr("fill")
	var fillOpacity, opacity *float32
	if v, ok := el.Attr("fill-opacity"); ok {
		if f, ok := parseF(v); ok {
			fillOpacity = &f
		}
	}
	if v, ok := el.Attr("opacity"); ok {
		if f, ok := parseF(v); ok {
			opacity = &f
		}
	}
	if style, ok := el.Attr("style"); ok {
		for _, decl := range strings.Split(style, ";") {
			name, value, ok := strings.Cut(decl, ":")
			if !ok {
				continue
			}
			switch strings.TrimSpace(name) {
			case "fill":
				fillAttr, hasFill = strings.TrimSpace(value), true
			case "fill-opacity":
				if f, ok := parseF(value); ok {
					fillOpacity = &f
				} else {
					fillOpacity = nil
				}
			case "opacity":
				if f, ok := parseF(value); ok {
					opacity = &f
				} else {
					opacity = nil
				}
			}
		}
	}
	if hasFill {
		fill := strings.TrimSpace(fillAttr)
		if fill == "none" {
			p.fill = nil
		} else if c, ok := Colour(fill); ok {
			p.fill = &c
		}
	}
	if opacity != nil {
		p.opacity *= max(0, min(1, *opacity))
	}
	if t, ok := el.Attr("transform"); ok {
		p.transform = ParseTransform(t).Then(p.transform)
	}
	switch el.Name {
	case "svg", "g", "defs", "symbol":
		for _, child := range el.Elements() {
			collect(child, p, vb, out)
		}
		return
	}
	steps, ok := shapeSteps(el)
	if !ok || len(steps) == 0 || p.fill == nil {
		return
	}
	fo := float32(1)
	if fillOpacity != nil {
		fo = max(0, min(1, *fillOpacity))
	}
	alpha := float32(p.fill[3]) / 255 * p.opacity * fo
	outline := Outline{Steps: steps, ViewBox: vb}.Transformed(p.transform)
	*out = append(*out, Shape{
		Outline: outline,
		Fill:    Rgba{p.fill[0], p.fill[1], p.fill[2], uint8(math.Round(float64(alpha * 255)))},
	})
}

// shapeSteps is a shape's outline.
func shapeSteps(el *xmltree.Element) ([]Step, bool) {
	get := func(name string) (float32, bool) {
		if v, ok := el.Attr(name); ok {
			return parseF(v)
		}
		return 0, false
	}
	or0 := func(name string) float32 {
		v, _ := get(name)
		return v
	}
	switch el.Name {
	case "path":
		d, ok := el.Attr("d")
		if !ok {
			return nil, false
		}
		return Path(d), true
	case "rect":
		x, y := or0("x"), or0("y")
		w, ok1 := get("width")
		h, ok2 := get("height")
		if !ok1 || !ok2 {
			return nil, false
		}
		// Either radius stands for the other where only one is given.
		rx, okx := get("rx")
		ry, oky := get("ry")
		if !okx {
			rx = ry
		}
		if !oky {
			ry, _ = get("rx")
		}
		rx = min(rx, w/2)
		ry = min(ry, h/2)
		if rx > 0 && ry > 0 {
			// Rounded corners: quarter circles (cubic approximations).
			const k = 0.5523
			return []Step{
				moveTo(Point{x + rx, y}),
				lineTo(Point{x + w - rx, y}),
				cubicTo(Point{x + w - rx + rx*k, y}, Point{x + w, y + ry - ry*k}, Point{x + w, y + ry}),
				lineTo(Point{x + w, y + h - ry}),
				cubicTo(Point{x + w, y + h - ry + ry*k}, Point{x + w - rx + rx*k, y + h}, Point{x + w - rx, y + h}),
				lineTo(Point{x + rx, y + h}),
				cubicTo(Point{x + rx - rx*k, y + h}, Point{x, y + h - ry + ry*k}, Point{x, y + h - ry}),
				lineTo(Point{x, y + ry}),
				cubicTo(Point{x, y + ry - ry*k}, Point{x + rx - rx*k, y}, Point{x + rx, y}),
				{Kind: StepClose},
			}, true
		}
		return []Step{
			moveTo(Point{x, y}), lineTo(Point{x + w, y}), lineTo(Point{x + w, y + h}), lineTo(Point{x, y + h}),
			{Kind: StepClose},
		}, true
	case "circle":
		r, ok := get("r")
		if !ok {
			return nil, false
		}
		return ellipse(or0("cx"), or0("cy"), r, r), true
	case "ellipse":
		rx, ok1 := get("rx")
		ry, ok2 := get("ry")
		if !ok1 || !ok2 {
			return nil, false
		}
		return ellipse(or0("cx"), or0("cy"), rx, ry), true
	case "polygon", "polyline":
		var points []float32
		if v, ok := el.Attr("points"); ok {
			points = numbers(v)
		}
		var out []Step
		for i := 0; i+1 < len(points); i += 2 {
			p := Point{points[i], points[i+1]}
			if i == 0 {
				out = append(out, moveTo(p))
			} else {
				out = append(out, lineTo(p))
			}
		}
		return append(out, Step{Kind: StepClose}), true
	}
	return nil, false
}

// ellipse is an ellipse as four cubic curves.
func ellipse(cx, cy, rx, ry float32) []Step {
	const k = 0.5523
	return []Step{
		moveTo(Point{cx + rx, cy}),
		cubicTo(Point{cx + rx, cy + ry*k}, Point{cx + rx*k, cy + ry}, Point{cx, cy + ry}),
		cubicTo(Point{cx - rx*k, cy + ry}, Point{cx - rx, cy + ry*k}, Point{cx - rx, cy}),
		cubicTo(Point{cx - rx, cy - ry*k}, Point{cx - rx*k, cy - ry}, Point{cx, cy - ry}),
		cubicTo(Point{cx + rx*k, cy - ry}, Point{cx + rx, cy - ry*k}, Point{cx + rx, cy}),
		{Kind: StepClose},
	}
}

// Glyph is one character of an SVG font.
type Glyph struct {
	Rune    rune
	Outline Outline
}

// FontGlyphs are the outlines of an SVG font's characters
// (<font><font-face/><glyph unicode d/></font>). The box is the advance by
// (ascent + descent), with y turned downwards.
func FontGlyphs(text string) []Glyph {
	root, ok := xmltree.Parse(text)
	if !ok {
		return nil
	}
	var out []Glyph
	for _, font := range root.FindAll("font") {
		var face *xmltree.Element
		if faces := font.FindAll("font-face"); len(faces) > 0 {
			face = faces[0]
		}
		number := func(name string, or float32) float32 {
			if face != nil {
				if v, ok := face.Attr(name); ok {
					if f, ok := parseF(v); ok {
						return f
					}
				}
			}
			return or
		}
		em := max(number("units-per-em", 1000), 1)
		ascent := number("ascent", em*0.8)
		descent := float32(math.Abs(float64(number("descent", em-ascent))))
		advance := em
		if v, ok := font.Attr("horiz-adv-x"); ok {
			if f, ok := parseF(v); ok {
				advance = f
			}
		}
		for _, glyph := range font.FindAll("glyph") {
			u, ok := glyph.Attr("unicode")
			if !ok {
				continue
			}
			runes := []rune(xmltree.Decode(u))
			if len(runes) == 0 {
				continue
			}
			width := advance
			if v, ok := glyph.Attr("horiz-adv-x"); ok {
				if f, ok := parseF(v); ok {
					width = f
				}
			}
			width = max(width, 1)
			d, ok := glyph.Attr("d")
			if !ok {
				continue
			}
			steps := Path(d)
			if len(steps) == 0 {
				continue
			}
			// A font's y is upwards (the baseline 0, up positive); turn it
			// downwards.
			flip := Transform{1, 0, 0, -1, 0, ascent}
			o := Outline{Steps: steps, ViewBox: ViewBox{0, 0, width, max(ascent+descent, 1)}}.Transformed(flip)
			out = append(out, Glyph{Rune: runes[0], Outline: o})
		}
	}
	return out
}

// Icon draws a small SVG picture (a receiver's button mark) as side x side
// coverage, centred in its box (the shorter way is padded).
func Icon(text string, side int) ([]uint8, bool) {
	d, ok := ParseDrawing(text)
	if !ok || len(d.Shapes) == 0 {
		return nil, false
	}
	v := d.ViewBox
	long := max(v.W, v.H)
	dx, dy := (long-v.W)/2, (long-v.H)/2
	centred := Drawing{ViewBox: ViewBox{v.X - dx, v.Y - dy, long, long}, Shapes: d.Shapes}
	return centred.RasteriseAlpha(side, side), true
}

// Colour reads #rgb, #rrggbb, #rrggbbaa, rgb(r,g,b) or a name.
func Colour(text string) (Rgba, bool) {
	v := strings.TrimSpace(text)
	if hex, ok := strings.CutPrefix(v, "#"); ok {
		n, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			return Rgba{}, false
		}
		switch len(hex) {
		case 3:
			r, g, b := uint8(n>>8&0xf), uint8(n>>4&0xf), uint8(n&0xf)
			return Rgba{r * 17, g * 17, b * 17, 255}, true
		case 6:
			return Rgba{uint8(n >> 16), uint8(n >> 8), uint8(n), 255}, true
		case 8:
			return Rgba{uint8(n >> 24), uint8(n >> 16), uint8(n >> 8), uint8(n)}, true
		}
		return Rgba{}, false
	}
	if inner, ok := strings.CutPrefix(v, "rgb("); ok {
		if inner, ok := strings.CutSuffix(inner, ")"); ok {
			if n := numbers(inner); len(n) >= 3 {
				return Rgba{uint8(n[0]), uint8(n[1]), uint8(n[2]), 255}, true
			}
		}
	}
	switch strings.ToLower(v) {
	case "black":
		return Rgba{0, 0, 0, 255}, true
	case "white":
		return Rgba{255, 255, 255, 255}, true
	case "red":
		return Rgba{255, 0, 0, 255}, true
	case "green":
		return Rgba{0, 128, 0, 255}, true
	case "lime":
		return Rgba{0, 255, 0, 255}, true
	case "blue":
		return Rgba{0, 0, 255, 255}, true
	case "yellow":
		return Rgba{255, 255, 0, 255}, true
	case "cyan", "aqua":
		return Rgba{0, 255, 255, 255}, true
	case "magenta", "fuchsia":
		return Rgba{255, 0, 255, 255}, true
	case "gray", "grey":
		return Rgba{128, 128, 128, 255}, true
	case "silver":
		return Rgba{192, 192, 192, 255}, true
	case "orange":
		return Rgba{255, 165, 0, 255}, true
	case "transparent":
		return Rgba{0, 0, 0, 0}, true
	}
	return Rgba{}, false
}

func numbers(text string) []float32 {
	var out []float32
	for _, v := range strings.FieldsFunc(text, func(c rune) bool {
		return c == ',' || c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
	}) {
		if f, ok := parseF(v); ok {
			out = append(out, f)
		}
	}
	return out
}

// Path reads an SVG path (d): M, L, H, V, Q, T, C, S, A and Z, and their
// relative forms. A (an arc) becomes a straight line to its end point (it
// appears in neither the fonts nor a broadcast's pictures).
func Path(d string) []Step {
	var out []Step
	var nums []float32
	var command rune
	haveCommand := false
	var current, start Point
	// The previous control points, which T and S reflect.
	var lastQuad, lastCubic *Point
	flush := func(command rune) {
		relative := command >= 'a' && command <= 'z'
		upper := command
		if relative {
			upper = command - 'a' + 'A'
		}
		at := 0
		take := func(n int) ([]float32, bool) {
			if at+n > len(nums) {
				return nil, false
			}
			v := nums[at : at+n]
			at += n
			return v, true
		}
		abs := func(cur Point, x, y float32) Point {
			if relative {
				return Point{cur.X + x, cur.Y + y}
			}
			return Point{x, y}
		}
		first := true
	loop:
		for {
			switch upper {
			case 'M':
				v, ok := take(2)
				if !ok {
					break loop
				}
				p := abs(current, v[0], v[1])
				if first {
					out = append(out, moveTo(p))
					start = p
				} else {
					out = append(out, lineTo(p))
				}
				current = p
				lastQuad, lastCubic = nil, nil
			case 'L':
				v, ok := take(2)
				if !ok {
					break loop
				}
				current = abs(current, v[0], v[1])
				out = append(out, lineTo(current))
				lastQuad, lastCubic = nil, nil
			case 'H':
				v, ok := take(1)
				if !ok {
					break loop
				}
				if relative {
					current.X += v[0]
				} else {
					current.X = v[0]
				}
				out = append(out, lineTo(current))
				lastQuad, lastCubic = nil, nil
			case 'V':
				v, ok := take(1)
				if !ok {
					break loop
				}
				if relative {
					current.Y += v[0]
				} else {
					current.Y = v[0]
				}
				out = append(out, lineTo(current))
				lastQuad, lastCubic = nil, nil
			case 'Q':
				v, ok := take(4)
				if !ok {
					break loop
				}
				c := abs(current, v[0], v[1])
				p := abs(current, v[2], v[3])
				out = append(out, quadTo(c, p))
				current = p
				lastQuad, lastCubic = &c, nil
			case 'T':
				v, ok := take(2)
				if !ok {
					break loop
				}
				c := current
				if lastQuad != nil {
					c = Point{2*current.X - lastQuad.X, 2*current.Y - lastQuad.Y}
				}
				p := abs(current, v[0], v[1])
				out = append(out, quadTo(c, p))
				current = p
				lastQuad, lastCubic = &c, nil
			case 'C':
				v, ok := take(6)
				if !ok {
					break loop
				}
				c1 := abs(current, v[0], v[1])
				c2 := abs(current, v[2], v[3])
				p := abs(current, v[4], v[5])
				out = append(out, cubicTo(c1, c2, p))
				current = p
				lastCubic, lastQuad = &c2, nil
			case 'S':
				v, ok := take(4)
				if !ok {
					break loop
				}
				c1 := current
				if lastCubic != nil {
					c1 = Point{2*current.X - lastCubic.X, 2*current.Y - lastCubic.Y}
				}
				c2 := abs(current, v[0], v[1])
				p := abs(current, v[2], v[3])
				out = append(out, cubicTo(c1, c2, p))
				current = p
				lastCubic, lastQuad = &c2, nil
			case 'A':
				v, ok := take(7)
				if !ok {
					break loop
				}
				current = abs(current, v[5], v[6])
				out = append(out, lineTo(current))
				lastQuad, lastCubic = nil, nil
			case 'Z':
				out = append(out, Step{Kind: StepClose})
				current = start
				lastQuad, lastCubic = nil, nil
				break loop
			default:
				break loop
			}
			first = false
			if at >= len(nums) {
				break
			}
		}
		nums = nums[:0]
	}
	var number []rune
	pushNumber := func() {
		if len(number) > 0 {
			if f, err := strconv.ParseFloat(string(number), 32); err == nil {
				nums = append(nums, float32(f))
			}
			number = number[:0]
		}
	}
	endsWithE := func() bool {
		return len(number) > 0 && (number[len(number)-1] == 'e' || number[len(number)-1] == 'E')
	}
	hasDot := func() bool {
		for _, c := range number {
			if c == '.' {
				return true
			}
		}
		return false
	}
	for _, c := range d {
		switch {
		case ((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) && c != 'e' && c != 'E':
			pushNumber()
			if haveCommand {
				flush(command)
			}
			command, haveCommand = c, true
			if c == 'Z' || c == 'z' {
				flush(c)
				haveCommand = false
			}
		case (c >= '0' && c <= '9') || c == 'e' || c == 'E':
			number = append(number, c)
		case c == '.':
			// 1.5.3 is 1.5 and .3.
			if hasDot() && !endsWithE() {
				pushNumber()
			}
			number = append(number, c)
		case c == '-' || c == '+':
			// A sign starts a number; an exponent's sign carries on.
			if !endsWithE() {
				pushNumber()
			}
			number = append(number, c)
		default:
			pushNumber()
		}
	}
	pushNumber()
	if haveCommand {
		flush(command)
	}
	return out
}

// Segment is a line from one point to another, in pixels.
type Segment [2]Point

// Flatten turns an outline into line segments. Curves are split by their
// length in pixels (one piece per three pixels, from four to sixty-four). An
// open loop is closed. Coordinates are pixels.
func Flatten(steps []Step) []Segment {
	var out []Segment
	var current, start Point
	open := false
	closeLoop := func() {
		if current != start {
			out = append(out, Segment{current, start})
		}
	}
	pieces := func(points ...Point) int {
		var length float64
		for i := 0; i+1 < len(points); i++ {
			dx := float64(points[i+1].X - points[i].X)
			dy := float64(points[i+1].Y - points[i].Y)
			length += math.Sqrt(dx*dx + dy*dy)
		}
		n := int(math.Ceil(length / 3))
		return max(4, min(64, n))
	}
	for _, s := range steps {
		switch s.Kind {
		case StepMove:
			if open {
				closeLoop()
			}
			current, start, open = s.P, s.P, true
		case StepLine:
			out = append(out, Segment{current, s.P})
			current = s.P
		case StepQuad:
			from := current
			n := pieces(from, s.C1, s.P)
			for i := 1; i <= n; i++ {
				t := float32(i) / float32(n)
				u := 1 - t
				p := Point{
					u*u*from.X + 2*u*t*s.C1.X + t*t*s.P.X,
					u*u*from.Y + 2*u*t*s.C1.Y + t*t*s.P.Y,
				}
				out = append(out, Segment{current, p})
				current = p
			}
		case StepCubic:
			from := current
			n := pieces(from, s.C1, s.C2, s.P)
			for i := 1; i <= n; i++ {
				t := float32(i) / float32(n)
				u := 1 - t
				p := Point{
					u*u*u*from.X + 3*u*u*t*s.C1.X + 3*u*t*t*s.C2.X + t*t*t*s.P.X,
					u*u*u*from.Y + 3*u*u*t*s.C1.Y + 3*u*t*t*s.C2.Y + t*t*t*s.P.Y,
				}
				out = append(out, Segment{current, p})
				current = p
			}
		case StepClose:
			closeLoop()
			current = start
			open = false
		}
	}
	if open {
		closeLoop()
	}
	return out
}

// Coverage draws a loop of segments into pixels (non-zero winding). Each row
// is split into four scan lines; on each, the crossings with the loop are
// gathered with the winding direction, and the spans where the winding is not
// zero are shared out over the pixels they cover. A pixel's value is 0 to
// 255.
func Coverage(segments []Segment, width, height int) []uint8 {
	const sub = 4
	cover := make([]float32, width*height)
	type crossing struct {
		x   float32
		dir int
	}
	var crossings []crossing
	// Which segments each row needs to look at (a character has hundreds, a
	// picture thousands).
	rows := make([][]int, height)
	for i, s := range segments {
		a, b := s[0], s[1]
		top, bottom := min(a.Y, b.Y), max(a.Y, b.Y)
		if top == bottom {
			continue
		}
		first := int(math.Max(0, math.Floor(float64(top))))
		last := min(int(math.Max(0, math.Ceil(float64(bottom)))), height)
		for r := first; r < last; r++ {
			rows[r] = append(rows[r], i)
		}
	}
	for row, listed := range rows {
		if len(listed) == 0 {
			continue
		}
		for s := 0; s < sub; s++ {
			y := float32(row) + (float32(s)+0.5)/sub
			crossings = crossings[:0]
			for _, i := range listed {
				a, b := segments[i][0], segments[i][1]
				top, bottom, dir := a, b, 1
				if a.Y >= b.Y {
					top, bottom, dir = b, a, -1
				}
				// The top end is in and the bottom end out (a vertex is not
				// counted twice).
				if y < top.Y || y >= bottom.Y {
					continue
				}
				t := (y - top.Y) / (bottom.Y - top.Y)
				crossings = append(crossings, crossing{top.X + (bottom.X-top.X)*t, dir})
			}
			sort.Slice(crossings, func(i, j int) bool { return crossings[i].x < crossings[j].x })
			winding := 0
			var from float32
			for _, c := range crossings {
				was := winding
				winding += c.dir
				if was == 0 && winding != 0 {
					from = c.x
				} else if was != 0 && winding == 0 {
					// Share [from, x) out over the pixels.
					start := min(max(from, 0), float32(width))
					end := min(max(c.x, 0), float32(width))
					if end <= start {
						continue
					}
					first := int(math.Floor(float64(start)))
					last := min(int(math.Ceil(float64(end))), width)
					for col := first; col < last; col++ {
						left := max(start, float32(col))
						right := min(end, float32(col)+1)
						if right > left {
							cover[row*width+col] += (right - left) / sub
						}
					}
				}
			}
		}
	}
	out := make([]uint8, len(cover))
	for i, c := range cover {
		out[i] = uint8(max(0, min(1, c))*255 + 0.5)
	}
	return out
}
