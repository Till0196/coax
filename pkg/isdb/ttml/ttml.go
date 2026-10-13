// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package ttml reads the documents of MMT captions (ARIB-TTML: STD-B62
// volume 1 part 3 chapter 3 and TR-B39 volume 2 8.5) and lays them out. This
// is separate from the captions of a transport stream (package caption).
//
// A broadcast's documents have a fixed shape: head/styling/style (per
// xml:id, tts:fontSize="width height", tts:lineHeight,
// arib-tt:letter-spacing, tts:color, tts:backgroundColor, outlines, shadows,
// borders and animations), head/layout/region (tts:origin and tts:extent in
// pixels), body/div/p (region, begin/end), span (style as a list of ids,
// region to restate the origin) and br. Coordinates are pixels at the display
// resolution (3840x2160 usually).
//
// What is decided here is what goes where (Document). Drawing is the
// receiver's, and when to show comes from Paragraph.Begin/End and the
// document's time (the MPU's presentation time).
package ttml

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Till0196/coax/pkg/isdb/xmltree"
)

// Rgba is a colour (#rrggbb or #rrggbbaa).
type Rgba struct {
	R, G, B, A uint8
}

var (
	Transparent = Rgba{}
	White       = Rgba{255, 255, 255, 255}
	Black       = Rgba{0, 0, 0, 255}
)

// ParseRgba reads #rrggbb or #rrggbbaa (TR-B39 8.5.6.4), or a name (STD-B62
// 3.5.5 allows names too).
func ParseRgba(value string) (Rgba, bool) {
	v := strings.TrimSpace(value)
	if hex, ok := strings.CutPrefix(v, "#"); ok {
		n, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			return Rgba{}, false
		}
		switch len(hex) {
		case 6:
			return Rgba{uint8(n >> 16), uint8(n >> 8), uint8(n), 255}, true
		case 8:
			return Rgba{uint8(n >> 24), uint8(n >> 16), uint8(n >> 8), uint8(n)}, true
		}
		return Rgba{}, false
	}
	rgb := func(r, g, b uint8) (Rgba, bool) { return Rgba{r, g, b, 255}, true }
	switch strings.ToLower(v) {
	case "white":
		return White, true
	case "black":
		return Black, true
	case "transparent":
		return Transparent, true
	case "red":
		return rgb(255, 0, 0)
	case "green":
		return rgb(0, 128, 0)
	case "lime":
		return rgb(0, 255, 0)
	case "yellow":
		return rgb(255, 255, 0)
	case "blue":
		return rgb(0, 0, 255)
	case "magenta", "fuchsia":
		return rgb(255, 0, 255)
	case "cyan", "aqua":
		return rgb(0, 255, 255)
	case "gray", "grey":
		return rgb(128, 128, 128)
	case "silver":
		return rgb(192, 192, 192)
	}
	return Rgba{}, false
}

func clamp01(t float32) float32 {
	return max(0, min(1, t))
}

func round8(v float32) uint8 {
	r := math.Round(float64(v))
	return uint8(max(0, min(255, r)))
}

// Mix is the colour between two (t from 0 to 1).
func (c Rgba) Mix(other Rgba, t float32) Rgba {
	t = clamp01(t)
	m := func(a, b uint8) uint8 { return round8(float32(a) + (float32(b)-float32(a))*t) }
	return Rgba{m(c.R, other.R), m(c.G, other.G), m(c.B, other.B), m(c.A, other.A)}
}

// Faded is the colour with an opacity applied.
func (c Rgba) Faded(opacity float32) Rgba {
	c.A = round8(float32(c.A) * clamp01(opacity))
	return c
}

// Rect is a rectangle (top left and size, pixels).
type Rect struct {
	X, Y int
	W, H uint32
}

// Outline is tts:textOutline="colour width blur".
type Outline struct {
	Color Rgba
	Width uint32
	Blur  uint32
}

// Shadow is arib-tt:text-shadow="x y blur colour".
type Shadow struct {
	DX, DY int
	Blur   uint32
	Color  Rgba
}

// Edge is one side of a border (arib-tt:border="solid width colour").
type Edge struct {
	Width uint32
	Color Rgba
}

// outlineSpec is a said outline: none, or a colour (nil for the character's)
// with a width and a blur.
type outlineSpec struct {
	none        bool
	color       *Rgba
	width, blur uint32
}

// edgeSpec is a said edge: nil edge is "none".
type edgeSpec struct{ edge *Edge }

type shadowSpec struct{ shadow *Shadow }

type animationSpec struct{ ref *animationRef }

// style is a character style: only what was said, inherited with apply.
type style struct {
	font        *[2]uint32
	lineHeight  *uint32
	spacing     *uint32
	fg, bg      *Rgba
	outline     *outlineSpec
	vertical    *bool
	opacity     *float32
	underline   *bool
	bold        *bool
	italic      *bool
	shadow      *shadowSpec
	animation   *animationSpec
	border      [4]*edgeSpec
	hasAnything bool
}

// animationRef is arib-tt:animation's value (the name looks up keyframes).
type animationRef struct {
	name       string
	durationMS uint32
	stepEnd    bool
	delayMS    uint32
	iterations *uint32
}

func (s *style) apply(o *style) {
	if o.font != nil {
		s.font = o.font
	}
	if o.lineHeight != nil {
		s.lineHeight = o.lineHeight
	}
	if o.spacing != nil {
		s.spacing = o.spacing
	}
	if o.fg != nil {
		s.fg = o.fg
	}
	if o.bg != nil {
		s.bg = o.bg
	}
	if o.outline != nil {
		s.outline = o.outline
	}
	if o.vertical != nil {
		s.vertical = o.vertical
	}
	if o.opacity != nil {
		s.opacity = o.opacity
	}
	if o.underline != nil {
		s.underline = o.underline
	}
	if o.bold != nil {
		s.bold = o.bold
	}
	if o.italic != nil {
		s.italic = o.italic
	}
	if o.shadow != nil {
		s.shadow = o.shadow
	}
	if o.animation != nil {
		s.animation = o.animation
	}
	for i := range s.border {
		if o.border[i] != nil {
			s.border[i] = o.border[i]
		}
	}
}

func (s *style) clone() style { return *s }

func (s *style) animationOf() *animationRef {
	if s.animation == nil {
		return nil
	}
	return s.animation.ref
}

func (s *style) borders() [4]*Edge {
	var out [4]*Edge
	for i, e := range s.border {
		if e != nil {
			out[i] = e.edge
		}
	}
	return out
}

// TextStyle is a run's style, settled.
type TextStyle struct {
	Fg, Bg Rgba
	// Font is the character size (width, height).
	FontW, FontH uint32
	Bold         bool
	Italic       bool
	Underline    bool
	Outline      *Outline
	Shadow       *Shadow
	// Border is the character frames' border (top, right, bottom, left), only
	// what the span's style said (a region's border is the display area's,
	// Region.Border). It sits outside the frame; Run.X is the frame inside.
	Border [4]*Edge
	// UnderlineAt is where the underline is (pixels from the start of the
	// line) and how thick: down from the top of a horizontal line, left from
	// the right of a vertical one.
	UnderlineAt, UnderlineWidth uint32
}

// Run is a run of characters, one frame each (TR-B39 8.3.4's character
// frame): rightwards when horizontal, downwards when vertical.
type Run struct {
	// X and Y are the first frame's top left (on the caption plane).
	X, Y int
	// Cell is the frame's size: horizontally (width + spacing, line height),
	// vertically (line height, height + spacing).
	CellW, CellH uint32
	Vertical     bool
	Text         string
	Style        TextStyle
	// Begin and End are the span's own times (document time, milliseconds);
	// without them the paragraph's.
	Begin, End *uint64
	// Animation is the span's own movement (said in the span's style).
	Animation *Animation
}

// Framed is the rectangle with the border (which sits outside the frames).
func (r *Run) Framed() Rect {
	b := r.Bounds()
	w := func(e *Edge) uint32 {
		if e == nil {
			return 0
		}
		return e.Width
	}
	top, right, bottom, left := r.Style.Border[0], r.Style.Border[1], r.Style.Border[2], r.Style.Border[3]
	return Rect{
		X: b.X - int(w(left)),
		Y: b.Y - int(w(top)),
		W: b.W + w(left) + w(right),
		H: b.H + w(top) + w(bottom),
	}
}

// Bounds is the rectangle the frames make.
func (r *Run) Bounds() Rect {
	n := uint32(len([]rune(r.Text)))
	if r.Vertical {
		return Rect{X: r.X, Y: r.Y, W: r.CellW, H: r.CellH * n}
	}
	return Rect{X: r.X, Y: r.Y, W: r.CellW * n, H: r.CellH}
}

// Cell is one character with its frame's top left.
type Cell struct {
	Rune rune
	X, Y int
}

// Cells are the characters' frames in order.
func (r *Run) Cells() []Cell {
	var out []Cell
	for i, c := range []rune(r.Text) {
		if r.Vertical {
			out = append(out, Cell{c, r.X, r.Y + int(r.CellH*uint32(i))})
		} else {
			out = append(out, Cell{c, r.X + int(r.CellW*uint32(i)), r.Y})
		}
	}
	return out
}

// Region is a display area (region).
type Region struct {
	ID   string
	Rect Rect
	// Bg is the region's ground (the region style's tts:backgroundColor),
	// which paints the whole rectangle.
	Bg Rgba
	// Border is the display area's border (the region style's
	// arib-tt:border), outside the rectangle.
	Border [4]*Edge
	// Animation is the region's movement (the region style's
	// arib-tt:animation).
	Animation *Animation
}

// Paragraph is one p.
type Paragraph struct {
	// Region is the index into Document.Regions, or -1.
	Region int
	// Clip is the rectangle overflow is hidden by (tts:overflow works as
	// hidden, TR-B39 8.5.5.20).
	Clip *Rect
	// Begin and End are when to show and clear (document time,
	// milliseconds). Without End it stays until the next document.
	Begin, End *uint64
	Runs       []Run
	// Animation is the whole paragraph's movement (said in the region's
	// style).
	Animation *Animation
}

// Image is a still picture (a div's smpte:backgroundImage).
type Image struct {
	Rect Rect
	// Src is subt://N (a subsample of the same MPU) or #id (a picture in the
	// document).
	Src        string
	Begin, End *uint64
}

// Audio is a sound (arib-tt:audio).
type Audio struct {
	// Src is romsound://N (built in) or subt://N (AIFF-C).
	Src        string
	Looped     bool
	Begin, End *uint64
}

// FontFace is a font for extra characters (arib-tt:font-face).
type FontFace struct {
	Family string
	// Ranges are unicode-range's (start, end) pairs.
	Ranges [][2]uint32
	// URL is subt://N.
	URL string
}

// Covers reports whether the font has a character.
func (f *FontFace) Covers(r rune) bool {
	c := uint32(r)
	for _, rg := range f.Ranges {
		if c >= rg[0] && c <= rg[1] {
			return true
		}
	}
	return false
}

// Frame is one frame of an animation.
type Frame struct {
	// At is from 0 to 1 (position="50%").
	At      float32
	Origin  *[2]int
	Color   *Rgba
	Bg      *Rgba
	Opacity *float32
}

// Animation is a movement (arib-tt:keyframes looked up by
// arib-tt:animation). A value a frame does not say carries on from the frame
// before, and from the element where no frame said it (TR-B39 8.5.7). Before
// it starts it is the 0% frame and after it ends the 100% frame (fill-mode
// both).
type Animation struct {
	Frames     []Frame
	DurationMS uint32
	// StepEnd is step-end; otherwise linear.
	StepEnd bool
	DelayMS uint32
	// Iterations is how many times; nil for infinite.
	Iterations *uint32
	// Base is the position of the moving element; a frame's origin less it is
	// the offset.
	BaseX, BaseY int
	// MirrorX is set when vertical: the x offset goes the other way.
	MirrorX bool
}

// Motion is an animation's values at one time.
type Motion struct {
	DX, DY  int
	Color   *Rgba
	Bg      *Rgba
	Opacity float32
}

// Progress is how far the animation has gone (0 to 1); elapsedMS is since the
// element was shown.
func (a *Animation) Progress(elapsedMS uint64) float32 {
	delay := uint64(a.DelayMS)
	if elapsedMS < delay || a.DurationMS == 0 {
		if a.DurationMS == 0 && elapsedMS >= delay {
			return 1
		}
		return 0
	}
	t := elapsedMS - delay
	duration := uint64(a.DurationMS)
	iteration := t / duration
	if a.Iterations != nil && iteration >= uint64(max(*a.Iterations, 1)) {
		return 1
	}
	return float32(t%duration) / float32(duration)
}

// Running reports whether it still moves (one that has ended need not be
// redrawn).
func (a *Animation) Running(elapsedMS uint64) bool {
	if a.Iterations == nil {
		return true
	}
	return elapsedMS < uint64(a.DelayMS)+uint64(a.DurationMS)*uint64(max(*a.Iterations, 1))
}

// At is the values at progress p.
func (a *Animation) At(p float32) Motion {
	var origin *[2]int
	{
		type said struct {
			at float32
			v  [2]int
		}
		var s []said
		for _, f := range a.Frames {
			if f.Origin != nil {
				s = append(s, said{f.At, *f.Origin})
			}
		}
		if v, ok := value(a.StepEnd, p, len(s), func(i int) (float32, [2]int) { return s[i].at, s[i].v },
			func(x, y [2]int, t float32) [2]int {
				return [2]int{
					int(math.Round(float64(float32(x[0]) + float32(y[0]-x[0])*t))),
					int(math.Round(float64(float32(x[1]) + float32(y[1]-x[1])*t))),
				}
			}); ok {
			origin = &v
		}
	}
	color := colourValue(a, p, func(f Frame) *Rgba { return f.Color })
	bg := colourValue(a, p, func(f Frame) *Rgba { return f.Bg })
	opacity := float32(1)
	{
		type said struct{ at, v float32 }
		var s []said
		for _, f := range a.Frames {
			if f.Opacity != nil {
				s = append(s, said{f.At, *f.Opacity})
			}
		}
		if v, ok := value(a.StepEnd, p, len(s), func(i int) (float32, float32) { return s[i].at, s[i].v },
			func(x, y, t float32) float32 { return x + (y-x)*t }); ok {
			opacity = v
		}
	}
	m := Motion{Color: color, Bg: bg, Opacity: opacity}
	if origin != nil {
		dx := origin[0] - a.BaseX
		if a.MirrorX {
			dx = -dx
		}
		m.DX = dx
		m.DY = origin[1] - a.BaseY
	}
	return m
}

// MotionAt is the values at a time (elapsedMS).
func (a *Animation) MotionAt(elapsedMS uint64) Motion { return a.At(a.Progress(elapsedMS)) }

func colourValue(a *Animation, p float32, pick func(Frame) *Rgba) *Rgba {
	type said struct {
		at float32
		v  Rgba
	}
	var s []said
	for _, f := range a.Frames {
		if c := pick(f); c != nil {
			s = append(s, said{f.At, *c})
		}
	}
	v, ok := value(a.StepEnd, p, len(s), func(i int) (float32, Rgba) { return s[i].at, s[i].v },
		func(x, y Rgba, t float32) Rgba { return x.Mix(y, t) })
	if !ok {
		return nil
	}
	return &v
}

// value is one value between the frames that say it. Where no frame says it
// the element's value holds (false); before the first frame that says it,
// that frame's.
func value[T any](stepEnd bool, p float32, n int, at func(int) (float32, T), lerp func(T, T, float32) T) (T, bool) {
	var zero T
	if n == 0 {
		return zero, false
	}
	firstAt, firstV := at(0)
	if p <= firstAt {
		return firstV, true
	}
	for i := 0; i+1 < n; i++ {
		aAt, aV := at(i)
		bAt, bV := at(i + 1)
		if p >= aAt && p < bAt {
			if stepEnd || bAt <= aAt {
				return aV, true
			}
			return lerp(aV, bV, (p-aAt)/(bAt-aAt)), true
		}
	}
	_, last := at(n - 1)
	return last, true
}

// Document is one ARIB-TTML document.
type Document struct {
	// Screen is the caption plane's size (the additional information's
	// resolution).
	ScreenW, ScreenH uint32
	Regions          []Region
	Paragraphs       []Paragraph
	Images           []Image
	Audio            []Audio
	Fonts            []FontFace
	// Embedded are the pictures in the document (smpte:image, id to the PNG
	// bytes).
	Embedded map[string][]byte
}

// IsClear reports a clear packet (TR-B39 8.11.5: <tt></tt>, or a document
// with nothing to show).
func (d *Document) IsClear() bool {
	for _, p := range d.Paragraphs {
		if len(p.Runs) > 0 {
			return false
		}
	}
	return len(d.Images) == 0
}

// Begin is when the first thing is shown (document time, milliseconds); 0 for
// a document without times.
func (d *Document) Begin() uint64 {
	var out *uint64
	take := func(v *uint64) {
		if v != nil && (out == nil || *v < *out) {
			x := *v
			out = &x
		}
	}
	for _, p := range d.Paragraphs {
		take(p.Begin)
	}
	for _, i := range d.Images {
		take(i.Begin)
	}
	for _, a := range d.Audio {
		take(a.Begin)
	}
	if out == nil {
		return 0
	}
	return *out
}

// End is when the last thing is cleared; false where something is never
// cleared (no end).
func (d *Document) End() (uint64, bool) {
	var latest uint64
	have := false
	check := func(end *uint64) bool {
		if end == nil {
			return false
		}
		if !have || *end > latest {
			latest = *end
		}
		have = true
		return true
	}
	for _, p := range d.Paragraphs {
		if len(p.Runs) == 0 {
			continue
		}
		if !check(p.End) {
			return 0, false
		}
	}
	for _, i := range d.Images {
		if !check(i.End) {
			return 0, false
		}
	}
	return latest, have
}

// IgnoreTimes drops the times (no timing, TMD=1111: begin and end are ignored
// even where written, TR-B39 8.5.9.2). Shown on arrival, until the next
// document.
func (d *Document) IgnoreTimes() {
	for pi := range d.Paragraphs {
		p := &d.Paragraphs[pi]
		p.Begin, p.End = nil, nil
		for ri := range p.Runs {
			p.Runs[ri].Begin, p.Runs[ri].End = nil, nil
		}
	}
	for i := range d.Images {
		d.Images[i].Begin, d.Images[i].End = nil, nil
	}
	for i := range d.Audio {
		d.Audio[i].Begin, d.Audio[i].End = nil, nil
	}
}

// Animates reports whether anything moves (the drawer then keeps redrawing).
func (d *Document) Animates() bool {
	for _, p := range d.Paragraphs {
		if p.Animation != nil || p.Begin != nil || p.End != nil {
			return true
		}
	}
	for _, i := range d.Images {
		if i.Begin != nil || i.End != nil {
			return true
		}
	}
	return false
}

// ShownAt are the paragraphs shown at a time (document time, milliseconds).
func (d *Document) ShownAt(at uint64) []*Paragraph {
	var out []*Paragraph
	for i := range d.Paragraphs {
		p := &d.Paragraphs[i]
		if len(p.Runs) > 0 && Between(at, p.Begin, p.End) {
			out = append(out, p)
		}
	}
	return out
}

// ImagesAt are the pictures shown at a time.
func (d *Document) ImagesAt(at uint64) []*Image {
	var out []*Image
	for i := range d.Images {
		if Between(at, d.Images[i].Begin, d.Images[i].End) {
			out = append(out, &d.Images[i])
		}
	}
	return out
}

// Between is begin <= at < end, where an absent one does not bind.
func Between(at uint64, begin, end *uint64) bool {
	return (begin == nil || at >= *begin) && (end == nil || at < *end)
}

func parseFloat(v string) (float32, bool) {
	f, err := strconv.ParseFloat(v, 32)
	if err != nil {
		return 0, false
	}
	return float32(f), true
}

func trimPx(v string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "px"))
}

func px(value string) (uint32, bool) {
	n, ok := parseFloat(trimPx(value))
	if !ok {
		return 0, false
	}
	return uint32(math.Round(float64(max(n, 0)))), true
}

func pxInt(value string) (int, bool) {
	n, ok := parseFloat(trimPx(value))
	if !ok {
		return 0, false
	}
	return int(math.Round(float64(n))), true
}

func two(value string) (int, int, bool) {
	parts := strings.Fields(value)
	if len(parts) < 2 {
		return 0, 0, false
	}
	a, ok1 := pxInt(parts[0])
	b, ok2 := pxInt(parts[1])
	return a, b, ok1 && ok2
}

// edge reads "kind width colour". Three are needed; none or hidden is no
// edge. said is false where it cannot be read.
func edge(value string) (e *Edge, said bool) {
	parts := strings.Fields(value)
	if len(parts) < 3 {
		return nil, false
	}
	if parts[0] == "none" || parts[0] == "hidden" {
		return nil, true
	}
	width, ok := px(parts[1])
	if !ok {
		return nil, false
	}
	color, ok := ParseRgba(parts[2])
	if !ok {
		return nil, false
	}
	if width == 0 {
		return nil, true
	}
	return &Edge{Width: width, Color: color}, true
}

func ms(value string) (uint32, bool) {
	v := strings.TrimSpace(value)
	if s, ok := strings.CutSuffix(v, "ms"); ok {
		n, ok := parseFloat(s)
		return uint32(max(n, 0)), ok
	}
	if s, ok := strings.CutSuffix(v, "s"); ok {
		n, ok := parseFloat(s)
		return uint32(max(n, 0) * 1000), ok
	}
	n, ok := parseFloat(v)
	return uint32(max(n, 0)), ok
}

func u32p(v uint32) *uint32 { return &v }
func boolp(v bool) *bool    { return &v }

// styleOf reads a style from an element's attributes (by local name; the
// namespace prefixes are dropped).
func styleOf(node *xmltree.Element) style {
	var s style
	for _, a := range node.Attrs {
		v := a.Value
		switch a.Name {
		case "fontSize":
			var sizes []uint32
			for _, p := range strings.Fields(v) {
				if strings.Contains(p, "px") {
					if n, ok := px(p); ok {
						sizes = append(sizes, n)
					}
				}
			}
			if len(sizes) >= 1 {
				w := sizes[0]
				h := w
				if len(sizes) >= 2 {
					h = sizes[1]
				}
				s.font = &[2]uint32{w, h}
			}
		case "lineHeight":
			// Only in px and not negative (normal stays the default).
			if strings.Contains(v, "px") && !strings.Contains(v, "-") {
				if n, ok := px(v); ok {
					s.lineHeight = u32p(n)
				}
			}
		case "letter-spacing":
			if strings.Contains(v, "px") && !strings.Contains(v, "-") {
				if n, ok := px(v); ok {
					s.spacing = u32p(n)
				}
			}
		case "color":
			if c, ok := ParseRgba(v); ok {
				s.fg = &c
			} else {
				s.fg = nil
			}
		case "backgroundColor":
			if c, ok := ParseRgba(v); ok {
				s.bg = &c
			} else {
				s.bg = nil
			}
		case "fontWeight":
			s.bold = boolp(strings.TrimSpace(v) == "bold")
		case "fontStyle":
			s.italic = boolp(strings.TrimSpace(v) == "italic")
		case "textOutline":
			// "[colour] width [blur]" or "none"; a colour alone is none, and
			// the colour can only come first. A width of 0px is no outline: a
			// broadcast writes #000000ff 0px 0px for none.
			parts := strings.Fields(v)
			if len(parts) > 3 {
				parts = parts[:3]
			}
			if len(parts) > 0 && parts[0] == "none" {
				s.outline = &outlineSpec{none: true}
				continue
			}
			if len(parts) == 1 && strings.HasPrefix(parts[0], "#") {
				continue
			}
			colourLater := false
			for _, p := range parts[min(1, len(parts)):] {
				if strings.HasPrefix(p, "#") {
					colourLater = true
				}
			}
			if colourLater {
				continue
			}
			var named *Rgba
			if len(parts) > 0 && strings.HasPrefix(parts[0], "#") {
				if c, ok := ParseRgba(parts[0]); ok {
					named = &c
				}
			}
			var lengths []uint32
			for _, p := range parts {
				if n, ok := px(p); ok {
					lengths = append(lengths, n)
				}
			}
			var width, blur uint32
			if len(lengths) > 0 {
				width = lengths[0]
			}
			if len(lengths) > 1 {
				blur = lengths[1]
			}
			if width == 0 {
				s.outline = &outlineSpec{none: true}
			} else {
				s.outline = &outlineSpec{color: named, width: width, blur: blur}
			}
		case "writingMode":
			switch strings.TrimSpace(v) {
			case "lrtb":
				s.vertical = boolp(false)
			case "tbrl":
				s.vertical = boolp(true)
			}
		case "opacity":
			if n, ok := parseFloat(strings.TrimSpace(v)); ok {
				s.opacity = &n
			} else {
				s.opacity = nil
			}
		case "textDecoration":
			s.underline = boolp(strings.TrimSpace(v) == "underline")
		case "text-shadow":
			// "x y blur colour"; all four or none.
			parts := strings.Fields(v)
			if len(parts) >= 4 {
				dx, ok1 := pxInt(parts[0])
				dy, ok2 := pxInt(parts[1])
				blur, ok3 := px(parts[2])
				c, ok4 := ParseRgba(parts[3])
				if ok1 && ok2 && ok3 && ok4 {
					s.shadow = &shadowSpec{&Shadow{DX: dx, DY: dy, Blur: blur, Color: c}}
				}
			}
		case "animation":
			// name duration timing delay count direction: all six, the
			// duration and the delay in ms, the timing linear or step-end and
			// the direction normal, or it was never said.
			parts := strings.Fields(v)
			if len(parts) >= 6 && strings.HasSuffix(parts[1], "ms") && strings.HasSuffix(parts[3], "ms") &&
				(parts[2] == "linear" || parts[2] == "step-end") && parts[5] == "normal" {
				d, ok1 := ms(parts[1])
				delay, ok2 := ms(parts[3])
				if ok1 && ok2 {
					ref := &animationRef{name: parts[0], durationMS: d, stepEnd: parts[2] == "step-end", delayMS: delay}
					if parts[4] != "infinite" {
						n, err := strconv.ParseUint(parts[4], 10, 32)
						if err != nil {
							n = 1
						}
						ref.iterations = u32p(uint32(n))
					}
					s.animation = &animationSpec{ref}
				}
			}
		case "border":
			if e, said := edge(v); said {
				for i := range s.border {
					s.border[i] = &edgeSpec{e}
				}
			}
		case "border-top", "border-right", "border-bottom", "border-left":
			i := map[string]int{"border-top": 0, "border-right": 1, "border-bottom": 2, "border-left": 3}[a.Name]
			if e, said := edge(v); said {
				s.border[i] = &edgeSpec{e}
			}
		}
	}
	return s
}

// normalLineHeight is the line height for normal.
func normalLineHeight(fontSize uint32) uint32 {
	base := uint32(math.Ceil(float64(float32(fontSize) * 1.2)))
	return base + base/50
}

// underlineAt is where the underline is (from the start of the line) and how
// thick: from the top of a horizontal line, from the right of a vertical one.
func underlineAt(vertical bool, fontW, fontH uint32, lineHeight *uint32) (uint32, uint32) {
	// In single precision, as the receiver's arithmetic is.
	ceil := func(v float32) float32 { return float32(math.Ceil(float64(v))) }
	size := float32(fontH)
	if vertical {
		size = float32(fontW)
	}
	var line, offset float32
	if lineHeight != nil {
		line = float32(*lineHeight)
		if vertical {
			offset = -ceil(size * 0.6)
		} else {
			offset = ceil(size * 0.45)
		}
	} else {
		line = float32(normalLineHeight(uint32(ceil(size))))
		if vertical {
			offset = -ceil(size * 0.6)
		} else {
			offset = ceil(size * 0.6)
		}
	}
	width := uint32(ceil(size / 8))
	at := max(0, min(line, ceil(line/2+offset)))
	return uint32(at), width
}

// Clock reads hh:mm:ss.fff (TR-B39 8.5.9) into milliseconds; 12.5s and 300ms
// too.
func Clock(value string) (uint64, bool) {
	v := strings.TrimSpace(value)
	if strings.Contains(v, ":") {
		parts := strings.SplitN(v, ":", 3)
		if len(parts) < 3 {
			return 0, false
		}
		h, err1 := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
		m, err2 := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
		if err1 != nil || err2 != nil {
			return 0, false
		}
		s := strings.TrimSpace(parts[2])
		whole, fracMS := s, uint64(0)
		if dot := strings.IndexAny(s, ".:"); dot >= 0 {
			if s[dot] == ':' {
				frames, err := strconv.ParseUint(s[dot+1:], 10, 64)
				if err != nil {
					return 0, false
				}
				whole, fracMS = s[:dot], frames*1000/30
			} else {
				digits := s[dot+1:]
				for i, c := range digits {
					if i >= 3 {
						break
					}
					if c < '0' || c > '9' {
						return 0, false
					}
					fracMS += uint64(c-'0') * []uint64{100, 10, 1}[i]
				}
				whole = s[:dot]
			}
		}
		seconds, err := strconv.ParseUint(whole, 10, 64)
		if err != nil {
			return 0, false
		}
		return ((h*60+m)*60+seconds)*1000 + fracMS, true
	}
	scaled := func(n string, by float64) (uint64, bool) {
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0, false
		}
		return uint64(math.Max(f, 0) * by), true
	}
	if n, ok := strings.CutSuffix(v, "ms"); ok {
		return scaled(n, 1)
	}
	if n, ok := strings.CutSuffix(v, "s"); ok {
		return scaled(n, 1000)
	}
	if n, ok := strings.CutSuffix(v, "m"); ok {
		return scaled(n, 60_000)
	}
	if n, ok := strings.CutSuffix(v, "h"); ok {
		return scaled(n, 3_600_000)
	}
	return 0, false
}

type span struct{ begin, end *uint64 }

func u64p(v uint64) *uint64 { return &v }

// timed is an element's times from its parent's (TR-B39 8.5.9.1: begin and end
// are relative to the parent's start, and without them the parent's hold).
func timed(node *xmltree.Element, parent span) span {
	var base uint64
	if parent.begin != nil {
		base = *parent.begin
	}
	out := parent
	if v, ok := node.Attr("begin"); ok {
		if b, ok := Clock(v); ok {
			out.begin = u64p(base + b)
		}
	}
	if v, ok := node.Attr("end"); ok {
		if e, ok := Clock(v); ok {
			out.end = u64p(base + e)
		}
	}
	return out
}

func sameSpan(a, b span) bool {
	eq := func(x, y *uint64) bool {
		if x == nil || y == nil {
			return x == y
		}
		return *x == *y
	}
	return eq(a.begin, b.begin) && eq(a.end, b.end)
}

// named looks style names up and lays them on (style="a b" is a, then b).
func named(styles map[string]*style, ids string, into *style) {
	for _, id := range strings.Fields(ids) {
		if found, ok := styles[id]; ok {
			into.apply(found)
		}
	}
}

// applyTextStyles inherits only the character style: not the colours, the
// ground, the border or the movement.
func applyTextStyles(into *style, from *style) {
	only := from.clone()
	only.fg, only.bg, only.animation = nil, nil, nil
	only.border = [4]*edgeSpec{}
	into.apply(&only)
}

// regionDef is a region as read (the position and the style laid on).
type regionDef struct {
	// origin and extent as written: vertically the origin is the top right.
	origin    *[2]int
	extent    *[2]uint32
	style     style
	index     int
	hasExtent bool
}

// rect is the rectangle by its top left. Vertically the origin is the top
// right (TR-B39 8.5.5.9).
func (r *regionDef) rect(vertical bool) (Rect, bool) {
	if r.origin == nil || r.extent == nil {
		return Rect{}, false
	}
	o, e := r.origin, r.extent
	x := o[0]
	if vertical {
		x = o[0] - int(e[0])
	}
	return Rect{X: x, Y: o[1], W: e[0], H: e[1]}, true
}

// flow is what goes in one display area at one time.
type flow struct {
	region   string
	inRegion bool
	time     span
	pieces   []piece
}

type piece struct {
	brk    bool
	text   string
	styles string
	// hasStyles is whether some span said a style list (even empty).
	hasStyles bool
	region    string
	inRegion  bool
	time      span
	timed     bool
	own       style
}

type reader struct {
	styles    map[string]*style
	regions   map[string]*regionDef
	keyframes map[string][]Frame
	flows     []*flow
	out       Document
}

func (r *reader) animation(at *animationRef, baseX, baseY int, mirror bool) *Animation {
	if at == nil {
		return nil
	}
	frames, ok := r.keyframes[at.name]
	if !ok {
		return nil
	}
	return &Animation{
		Frames: append([]Frame(nil), frames...), DurationMS: at.durationMS, StepEnd: at.stepEnd,
		DelayMS: at.delayMS, Iterations: at.iterations, BaseX: baseX, BaseY: baseY, MirrorX: mirror,
	}
}

// resolveStyles settles the style elements in the order they refer to each
// other (an earlier style can refer to a later one and the other way round;
// a loop stops).
func (r *reader) resolveStyles(elements []*xmltree.Element) {
	var visiting []string
	var resolve func(id string)
	resolve = func(id string) {
		if _, done := r.styles[id]; done {
			return
		}
		for _, v := range visiting {
			if v == id {
				return
			}
		}
		var el *xmltree.Element
		for _, e := range elements {
			if v, ok := e.Attr("id"); ok && v == id {
				el = e
				break
			}
		}
		if el == nil {
			return
		}
		visiting = append(visiting, id)
		var s style
		if ids, ok := el.Attr("style"); ok {
			for _, ref := range strings.Fields(ids) {
				resolve(ref)
				if found, ok := r.styles[ref]; ok {
					s.apply(found)
				}
			}
		}
		own := styleOf(el)
		s.apply(&own)
		visiting = visiting[:len(visiting)-1]
		r.styles[id] = &s
	}
	for _, e := range elements {
		if id, ok := e.Attr("id"); ok {
			resolve(id)
		}
	}
}

func (r *reader) readHead(head *xmltree.Element) {
	descendants := head.Descendants()
	var styles []*xmltree.Element
	for _, e := range descendants {
		if e.Name == "style" {
			styles = append(styles, e)
		}
	}
	r.resolveStyles(styles)
	for _, node := range descendants {
		switch node.Name {
		case "region":
			id, ok := node.Attr("id")
			if !ok {
				continue
			}
			var s style
			if v, ok := node.Attr("style"); ok {
				named(r.styles, v, &s)
			}
			own := styleOf(node)
			s.apply(&own)
			def := &regionDef{style: s, index: len(r.out.Regions)}
			if v, ok := node.Attr("origin"); ok {
				if x, y, ok := two(v); ok {
					def.origin = &[2]int{x, y}
				}
			}
			if v, ok := node.Attr("extent"); ok {
				if w, h, ok := two(v); ok {
					def.extent = &[2]uint32{uint32(max(w, 0)), uint32(max(h, 0))}
				}
			}
			vertical := def.style.vertical != nil && *def.style.vertical
			bx, by := 0, 0
			if def.origin != nil {
				bx, by = def.origin[0], def.origin[1]
			}
			rect, ok := def.rect(vertical)
			if !ok {
				rect = Rect{W: r.out.ScreenW, H: r.out.ScreenH}
			}
			bg := Transparent
			if def.style.bg != nil {
				bg = *def.style.bg
			}
			r.out.Regions = append(r.out.Regions, Region{
				ID: id, Rect: rect, Bg: bg, Border: def.style.borders(),
				Animation: r.animation(def.style.animationOf(), bx, by, vertical),
			})
			r.regions[id] = def
		case "keyframes":
			name, ok := node.Attr("animationName")
			if !ok {
				continue
			}
			var frames []Frame
			for _, f := range node.Elements() {
				if f.Name != "keyframe" {
					continue
				}
				var frame Frame
				if v, ok := f.Attr("position"); ok {
					if p, ok := parseFloat(strings.TrimSuffix(strings.TrimSpace(v), "%")); ok {
						frame.At = clamp01(p / 100)
					}
				}
				if v, ok := f.Attr("origin"); ok {
					if x, y, ok := two(v); ok {
						frame.Origin = &[2]int{x, y}
					}
				}
				if v, ok := f.Attr("color"); ok {
					if c, ok := ParseRgba(v); ok {
						frame.Color = &c
					}
				}
				if v, ok := f.Attr("backgroundColor"); ok {
					if c, ok := ParseRgba(v); ok {
						frame.Bg = &c
					}
				}
				if v, ok := f.Attr("opacity"); ok {
					if o, ok := parseFloat(strings.TrimSpace(v)); ok {
						frame.Opacity = &o
					}
				}
				frames = append(frames, frame)
			}
			sort.SliceStable(frames, func(i, j int) bool { return frames[i].At < frames[j].At })
			if len(frames) >= 2 {
				r.keyframes[name] = frames
			}
		case "font-face":
			// A font without unicode-range is not used.
			v, ok := node.Attr("unicode-range")
			if !ok {
				continue
			}
			family, _ := node.Attr("font-family")
			url := ""
			for _, s := range node.Elements() {
				if s.Name == "src" {
					url, _ = s.Attr("url")
					break
				}
			}
			r.out.Fonts = append(r.out.Fonts, FontFace{Family: family, Ranges: unicodeRanges(v), URL: url})
		case "image":
			// smpte:image (a PNG in Base64).
			id, ok := node.Attr("id")
			if !ok {
				continue
			}
			var text strings.Builder
			for _, c := range node.Children {
				if c.IsText {
					text.WriteString(c.Text)
				}
			}
			if b, ok := base64(text.String()); ok {
				r.out.Embedded[id] = b
			}
		}
	}
}

// readBody reads the first div under body (TR-B39 8.5.4: body has one div).
// Its p are text, its div pictures and sounds.
func (r *reader) readBody(body *xmltree.Element) {
	t := timed(body, span{})
	var div *xmltree.Element
	for _, e := range body.Elements() {
		if e.Name == "div" {
			div = e
			break
		}
	}
	if div == nil {
		return
	}
	t = timed(div, t)
	for _, child := range div.Elements() {
		switch child.Name {
		case "p":
			r.paragraph(child, timed(child, t))
		case "div":
			ct := timed(child, t)
			if src, ok := child.Attr("backgroundImage"); ok {
				// A picture's region is taken horizontally (the origin is the
				// top left).
				if id, ok := child.Attr("region"); ok {
					if def, ok := r.regions[id]; ok {
						if rect, ok := def.rect(false); ok {
							r.out.Images = append(r.out.Images, Image{Rect: rect, Src: strings.TrimSpace(src), Begin: ct.begin, End: ct.end})
						}
					}
				}
			}
			for _, a := range child.Elements() {
				if a.Name == "audio" {
					r.audio(a, ct)
				}
			}
		}
	}
	flows := r.flows
	r.flows = nil
	for _, f := range flows {
		r.layOut(f)
	}
}

func (r *reader) audio(node *xmltree.Element, t span) {
	src, ok := node.Attr("src")
	if !ok {
		return
	}
	loop, _ := node.Attr("loop")
	r.out.Audio = append(r.out.Audio, Audio{Src: strings.TrimSpace(src), Looped: strings.TrimSpace(loop) == "true", Begin: t.begin, End: t.end})
}

// paragraph puts a p's spans on the flow of each display area. A span that
// refers to its own region goes on that region's flow; what goes in one
// region at one time is one flow.
func (r *reader) paragraph(p *xmltree.Element, t span) {
	pRegion, pHasRegion := p.Attr("region")
	var items []piece
	inlines(p, &inline{time: t}, &items)
	for _, item := range items {
		var region string
		var inRegion bool
		var itemTime span
		if !item.brk {
			region, inRegion = item.region, item.inRegion
			if !inRegion {
				region, inRegion = pRegion, pHasRegion
			}
			itemTime = t
			if item.timed {
				itemTime = item.time
			}
		} else if len(r.flows) > 0 {
			last := r.flows[len(r.flows)-1]
			region, inRegion, itemTime = last.region, last.inRegion, last.time
		} else {
			region, inRegion, itemTime = pRegion, pHasRegion, t
		}
		var found *flow
		for _, f := range r.flows {
			if f.inRegion == inRegion && f.region == region && sameSpan(f.time, itemTime) {
				found = f
				break
			}
		}
		if found != nil {
			found.pieces = append(found.pieces, item)
		} else {
			r.flows = append(r.flows, &flow{region: region, inRegion: inRegion, time: itemTime, pieces: []piece{item}})
		}
	}
}

type said struct {
	text  string
	style style
}

type cut struct {
	text  []rune
	style style
}

// layOut lays one flow out.
func (r *reader) layOut(f *flow) {
	if !f.inRegion {
		return
	}
	region, ok := r.regions[f.region]
	if !ok || region.origin == nil || region.extent == nil {
		// Text without a region cannot be placed.
		return
	}
	origin, extent := *region.origin, *region.extent
	// A span's style inherits the region's character style and lays its own
	// on; a span that says vertical makes the whole region vertical.
	var items []*said
	vertical := region.style.vertical != nil && *region.style.vertical
	for _, pc := range f.pieces {
		if pc.brk {
			items = append(items, nil)
			continue
		}
		var s style
		applyTextStyles(&s, &region.style)
		named(r.styles, pc.styles, &s)
		s.apply(&pc.own)
		if s.vertical != nil {
			vertical = *s.vertical
		}
		items = append(items, &said{text: pc.text, style: s})
	}
	area := Rect{X: origin[0], Y: origin[1], W: extent[0], H: extent[1]}
	if vertical {
		area.X = origin[0] - int(extent[0])
	}
	paragraph := Paragraph{
		Region: region.index, Clip: &area, Begin: f.time.begin, End: f.time.end,
		Animation: r.animation(region.style.animationOf(), origin[0], origin[1], vertical),
	}
	// Cut into lines: a character that does not fit goes to the next line
	// (automatic wrapping). Spaces collapse, and the leading space after a
	// wrap and the trailing space before one are dropped (spaces around
	// <br/> stay).
	areaSize := int(extent[0])
	if vertical {
		areaSize = int(extent[1])
	}
	lines := [][]*cut{{}}
	remaining := areaSize
	afterWrap := false
	lastIsSpace := func(line []*cut) bool {
		if len(line) == 0 {
			return false
		}
		t := line[len(line)-1].text
		return len(t) > 0 && t[len(t)-1] == ' '
	}
	lineEmpty := func(line []*cut) bool {
		for _, c := range line {
			if len(c.text) > 0 {
				return false
			}
		}
		return true
	}
	pushCut := func(s style) {
		lines[len(lines)-1] = append(lines[len(lines)-1], &cut{style: s})
	}
	for _, item := range items {
		if item == nil {
			lines = append(lines, nil)
			remaining = areaSize
			afterWrap = false
			continue
		}
		if item.style.font == nil {
			// Without a size it cannot be shown.
			continue
		}
		font := *item.style.font
		var spacing uint32
		if item.style.spacing != nil {
			spacing = *item.style.spacing
		}
		step := int(font[0]) + int(spacing)
		if vertical {
			step = int(font[1]) + int(spacing)
		}
		chars := []rune(item.text)
		pushCut(item.style)
		for i := 0; i < len(chars); {
			c := chars[i]
			if c == '\t' || c == '\n' || c == '\r' {
				c = ' '
			}
			line := lines[len(lines)-1]
			ignore := c == ' ' && ((lineEmpty(line) && afterWrap) || lastIsSpace(line))
			switch {
			case step <= remaining:
				if !ignore {
					cur := line[len(line)-1]
					cur.text = append(cur.text, c)
					remaining -= step
				}
				i++
			case lineEmpty(line):
				// A character bigger than the area: a line of its own.
				if !ignore {
					cur := line[len(line)-1]
					cur.text = append(cur.text, c)
					lines = append(lines, nil)
					pushCut(item.style)
					remaining = areaSize
					afterWrap = true
				}
				i++
			default:
				// It does not fit: wrap (dropping the trailing space) and try
				// the same character on the next line.
				if lastIsSpace(line) {
					for k := len(line) - 1; k >= 0; k-- {
						t := line[k].text
						if len(t) > 0 && t[len(t)-1] == ' ' {
							line[k].text = t[:len(t)-1]
							break
						}
					}
				}
				lines = append(lines, nil)
				pushCut(item.style)
				remaining = areaSize
				afterWrap = true
			}
		}
	}
	// Put the lines: top to bottom horizontally, right to left vertically. A
	// line is as thick as its largest line height, with the characters in
	// the middle.
	along := area.Y
	if vertical {
		along = origin[0]
	}
	lineSize := func(s *style) uint32 {
		if s.lineHeight != nil {
			return *s.lineHeight
		}
		var font [2]uint32
		if s.font != nil {
			font = *s.font
		}
		if vertical {
			return normalLineHeight(font[0])
		}
		return normalLineHeight(font[1])
	}
	for _, line := range lines {
		var cuts []*cut
		for _, c := range line {
			if len(c.text) > 0 {
				cuts = append(cuts, c)
			}
		}
		if len(cuts) == 0 {
			continue
		}
		var thick uint32
		for _, c := range cuts {
			thick = max(thick, lineSize(&c.style))
		}
		across := area.X
		if vertical {
			across = area.Y
		}
		for _, c := range cuts {
			s := &c.style
			font := *s.font
			var spacing uint32
			if s.spacing != nil {
				spacing = *s.spacing
			}
			opacity := float32(1)
			if s.opacity != nil {
				opacity = *s.opacity
			}
			// The colour: the span's style, else the region's, else white.
			fg := White
			switch {
			case s.fg != nil:
				fg = *s.fg
			case region.style.fg != nil:
				fg = *region.style.fg
			}
			fg = fg.Faded(opacity)
			var outline *Outline
			if s.outline != nil && !s.outline.none {
				color := fg
				if s.outline.color != nil {
					color = s.outline.color.Faded(opacity)
				}
				outline = &Outline{Color: color, Width: s.outline.width, Blur: s.outline.blur}
			}
			border := s.borders()
			w := func(e *Edge) int {
				if e == nil {
					return 0
				}
				return int(e.Width)
			}
			before, after := w(border[3]), w(border[1])
			if vertical {
				before, after = w(border[0]), w(border[2])
			}
			cellW, cellH := font[0]+spacing, thick
			if vertical {
				cellW, cellH = thick, font[1]+spacing
			}
			count := len(c.text)
			x, y := across+before, along
			if vertical {
				x, y = along-int(thick), across+before
			}
			bg := Transparent
			if s.bg != nil {
				bg = *s.bg
			}
			var shadow *Shadow
			if s.shadow != nil {
				shadow = s.shadow.shadow
			}
			ulAt, ulWidth := underlineAt(vertical, font[0], font[1], s.lineHeight)
			paragraph.Runs = append(paragraph.Runs, Run{
				X: x, Y: y, CellW: cellW, CellH: cellH, Vertical: vertical, Text: string(c.text),
				Style: TextStyle{
					Fg: fg, Bg: bg.Faded(opacity), FontW: font[0], FontH: font[1],
					Bold: s.bold != nil && *s.bold, Italic: s.italic != nil && *s.italic,
					Underline: s.underline != nil && *s.underline,
					Outline:   outline, Shadow: shadow, Border: border,
					UnderlineAt: ulAt, UnderlineWidth: ulWidth,
				},
				Begin: f.time.begin, End: f.time.end,
				Animation: r.animation(s.animationOf(), origin[0], origin[1], vertical),
			})
			step := int(cellW)
			if vertical {
				step = int(cellH)
			}
			across += before + count*step + after
		}
		if vertical {
			along -= int(thick)
		} else {
			along += int(thick)
		}
	}
	r.out.Paragraphs = append(r.out.Paragraphs, paragraph)
}

// inline is what a p's content inherits: the nearest span's style list, its
// region, its times and its own style.
type inline struct {
	styles    string
	hasStyles bool
	region    string
	inRegion  bool
	time      span
	own       style
	timed     bool
}

// inlines puts a p's content in document order: line breaks, and text with
// the nearest span's style.
func inlines(el *xmltree.Element, inherited *inline, out *[]piece) {
	for _, child := range el.Children {
		if child.IsText {
			// There is no text directly under p (only in spans, TR-B39
			// 8.5.4).
			if !inherited.hasStyles && !inherited.inRegion && !inherited.timed && el.Name == "p" {
				continue
			}
			if child.Text == "" {
				continue
			}
			*out = append(*out, piece{
				text: child.Text, styles: inherited.styles, hasStyles: inherited.hasStyles,
				region: inherited.region, inRegion: inherited.inRegion,
				time: inherited.time, timed: inherited.timed, own: inherited.own,
			})
			continue
		}
		e := child.Element
		if e == nil {
			continue
		}
		switch e.Name {
		case "br":
			*out = append(*out, piece{brk: true})
		case "span":
			own := inherited.own.clone()
			spanOwn := styleOf(e)
			own.apply(&spanOwn)
			_, hasBegin := e.Attr("begin")
			_, hasEnd := e.Attr("end")
			hasTime := hasBegin || hasEnd
			t := inherited.time
			if hasTime {
				t = timed(e, inherited.time)
			}
			next := inline{time: t, own: own, timed: inherited.timed || hasTime, hasStyles: true}
			inner, hasInner := e.Attr("style")
			switch {
			case inherited.hasStyles && hasInner:
				next.styles = inherited.styles + " " + inner
			case hasInner:
				next.styles = inner
			default:
				next.styles = inherited.styles
			}
			if v, ok := e.Attr("region"); ok {
				next.region, next.inRegion = v, true
			} else {
				next.region, next.inRegion = inherited.region, inherited.inRegion
			}
			inlines(e, &next, out)
		case "audio":
		default:
			inlines(e, inherited, out)
		}
	}
}

// unicodeRanges turns U+E000, U+0-7F, U+A5, U+0-7F into a list of ranges.
func unicodeRanges(value string) [][2]uint32 {
	var out [][2]uint32
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		body, ok := strings.CutPrefix(part, "U+")
		if !ok {
			body, ok = strings.CutPrefix(part, "u+")
		}
		if !ok {
			continue
		}
		lo, hi := body, body
		if a, b, found := strings.Cut(body, "-"); found {
			lo, hi = a, b
		}
		l, err1 := strconv.ParseUint(lo, 16, 32)
		h, err2 := strconv.ParseUint(hi, 16, 32)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, [2]uint32{uint32(l), uint32(h)})
	}
	return out
}

// base64 reads smpte:image's content, skipping white space.
func base64(text string) ([]byte, bool) {
	out := make([]byte, 0, len(text)*3/4)
	var acc uint32
	bits := 0
	for _, c := range text {
		var v uint32
		switch {
		case c >= 'A' && c <= 'Z':
			v = uint32(c - 'A')
		case c >= 'a' && c <= 'z':
			v = uint32(c-'a') + 26
		case c >= '0' && c <= '9':
			v = uint32(c-'0') + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			return out, true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			continue
		default:
			return nil, false
		}
		acc = acc<<6 | v
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
			acc &= 1<<bits - 1
		}
	}
	return out, true
}

// Parse reads one TTML document and lays it out. screenW and screenH are the
// display resolution (the additional information's resolution). False where
// it cannot be read.
func Parse(text string, screenW, screenH uint32) (*Document, bool) {
	doc, ok := xmltree.Parse(text)
	if !ok {
		return nil, false
	}
	r := &reader{
		styles:    map[string]*style{},
		regions:   map[string]*regionDef{},
		keyframes: map[string][]Frame{},
		out:       Document{ScreenW: screenW, ScreenH: screenH, Embedded: map[string][]byte{}},
	}
	for _, e := range doc.Elements() {
		if e.Name == "head" {
			r.readHead(e)
			break
		}
	}
	for _, e := range doc.Elements() {
		if e.Name == "body" {
			r.readBody(e)
			break
		}
	}
	return &r.out, true
}
