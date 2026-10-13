// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package xmltree is a small XML tree, enough to read the documents a
// broadcast sends (ARIB-TTML captions, SVG fonts and pictures): elements,
// attributes and text, entity references, CDATA, and skipping comments and
// processing instructions. Namespace prefixes are dropped from names (a
// broadcast's documents have no clashing local names). A broken document is
// read as far as it goes.
package xmltree

import (
	"strconv"
	"strings"
)

// Element is one element. Name and the attribute keys are local names.
type Element struct {
	Name     string
	Attrs    []Attr
	Children []Node
}

// Attr is one attribute, by local name.
type Attr struct {
	Name, Value string
}

// Node is a child: an element or a run of text, exactly one of which is set.
type Node struct {
	Element *Element
	Text    string
	IsText  bool
}

// Attr is the value of an attribute by local name.
func (e *Element) Attr(name string) (string, bool) {
	for _, a := range e.Attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// Elements are the child elements, in order.
func (e *Element) Elements() []*Element {
	var out []*Element
	for _, n := range e.Children {
		if n.Element != nil {
			out = append(out, n.Element)
		}
	}
	return out
}

// Descendants are the descendant elements in document order.
func (e *Element) Descendants() []*Element {
	var out []*Element
	var walk func(*Element)
	walk = func(e *Element) {
		for _, c := range e.Elements() {
			out = append(out, c)
			walk(c)
		}
	}
	walk(e)
	return out
}

// FindAll are the descendants of a name, in document order.
func (e *Element) FindAll(name string) []*Element {
	var out []*Element
	for _, d := range e.Descendants() {
		if d.Name == name {
			out = append(out, d)
		}
	}
	return out
}

func local(name string) string {
	if i := strings.LastIndexByte(name, ':'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// Decode puts entity references back.
func Decode(text string) string {
	if !strings.Contains(text, "&") {
		return text
	}
	var out strings.Builder
	rest := text
	for {
		at := strings.IndexByte(rest, '&')
		if at < 0 {
			break
		}
		out.WriteString(rest[:at])
		rest = rest[at:]
		end := strings.IndexByte(rest, ';')
		if end < 0 {
			out.WriteString(rest)
			return out.String()
		}
		body := rest[1:end]
		replaced, ok := "", false
		if number, isNumber := strings.CutPrefix(body, "#"); isNumber {
			var code uint64
			var err error
			if hex, isHex := strings.CutPrefix(number, "x"); isHex {
				code, err = strconv.ParseUint(hex, 16, 32)
			} else if hex, isHex := strings.CutPrefix(number, "X"); isHex {
				code, err = strconv.ParseUint(hex, 16, 32)
			} else {
				code, err = strconv.ParseUint(number, 10, 32)
			}
			if err == nil && code <= 0x10ffff && !(code >= 0xd800 && code <= 0xdfff) {
				replaced, ok = string(rune(code)), true
			}
		} else {
			switch body {
			case "lt":
				replaced, ok = "<", true
			case "gt":
				replaced, ok = ">", true
			case "amp":
				replaced, ok = "&", true
			case "quot":
				replaced, ok = "\"", true
			case "apos":
				replaced, ok = "'", true
			}
		}
		if ok {
			out.WriteString(replaced)
			rest = rest[end+1:]
		} else {
			out.WriteByte('&')
			rest = rest[1:]
		}
	}
	out.WriteString(rest)
	return out.String()
}

// tagEnd finds the end of a tag, passing over a '>' inside quotes.
func tagEnd(text []rune, from int) int {
	var quote rune
	for i := from + 1; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i
		}
	}
	return -1
}

func isSpace(c rune) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' || c == 0x85 || c == 0xa0 || c == 0x3000
}

// element reads what is inside a tag: the name and the attributes.
func element(inner string) *Element {
	inner = strings.TrimLeftFunc(inner, isSpace)
	nameEnd := strings.IndexFunc(inner, func(c rune) bool { return isSpace(c) || c == '/' })
	if nameEnd < 0 {
		nameEnd = len(inner)
	}
	el := &Element{Name: local(inner[:nameEnd])}
	rest := []rune(inner[nameEnd:])
	i := 0
	for i < len(rest) {
		for i < len(rest) && (isSpace(rest[i]) || rest[i] == '/') {
			i++
		}
		if i >= len(rest) {
			break
		}
		start := i
		for i < len(rest) && !isSpace(rest[i]) && rest[i] != '=' && rest[i] != '/' {
			i++
		}
		key := string(rest[start:i])
		if key == "" {
			i++
			continue
		}
		j := i
		for j < len(rest) && isSpace(rest[j]) {
			j++
		}
		value := ""
		if j < len(rest) && rest[j] == '=' {
			j++
			for j < len(rest) && isSpace(rest[j]) {
				j++
			}
			if j < len(rest) && (rest[j] == '"' || rest[j] == '\'') {
				q := rest[j]
				j++
				vs := j
				for j < len(rest) && rest[j] != q {
					j++
				}
				value = string(rest[vs:j])
				j++
			} else {
				vs := j
				for j < len(rest) && !isSpace(rest[j]) {
					j++
				}
				value = string(rest[vs:j])
			}
			i = j
		}
		el.Attrs = append(el.Attrs, Attr{Name: local(key), Value: Decode(value)})
	}
	return el
}

// MostDepth is how deep elements may nest. A broadcast's documents are fewer
// than ten deep; deeper elements are kept empty and their content goes on in
// the parent, since the readers of the tree walk it recursively.
const MostDepth = 64

// Parse reads a document and gives its root element, or false where there is
// none.
func Parse(text string) (*Element, bool) {
	chars := []rune(text)
	stack := []*Element{{}}
	pushChild := func(child Node) {
		parent := stack[len(stack)-1]
		parent.Children = append(parent.Children, child)
	}
	find := func(from int, pattern string) int {
		p := []rune(pattern)
		for i := from; i+len(p) <= len(chars); i++ {
			if string(chars[i:i+len(p)]) == pattern {
				return i
			}
		}
		return -1
	}
	starts := func(at int, pattern string) bool {
		p := []rune(pattern)
		return at+len(p) <= len(chars) && string(chars[at:at+len(p)]) == pattern
	}
	slice := func(a, b int) string {
		if b > len(chars) {
			b = len(chars)
		}
		return string(chars[a:b])
	}
	at, length := 0, len(chars)
	for at < length {
		lt := find(at, "<")
		if lt < 0 {
			pushChild(Node{Text: Decode(slice(at, length)), IsText: true})
			break
		}
		if lt > at {
			pushChild(Node{Text: Decode(slice(at, lt)), IsText: true})
		}
		switch {
		case starts(lt, "<!--"):
			if e := find(lt, "-->"); e >= 0 {
				at = e + 3
			} else {
				at = length
			}
			continue
		case starts(lt, "<![CDATA["):
			e := find(lt, "]]>")
			if e < 0 {
				pushChild(Node{Text: slice(lt+9, length), IsText: true})
				at = length
			} else {
				pushChild(Node{Text: slice(lt+9, e), IsText: true})
				at = e + 3
			}
			continue
		case starts(lt, "<?") || starts(lt, "<!"):
			if e := find(lt, ">"); e >= 0 {
				at = e + 1
			} else {
				at = length
			}
			continue
		}
		gt := tagEnd(chars, lt)
		if gt < 0 {
			break
		}
		inner := slice(lt+1, gt)
		at = gt + 1
		if closing, ok := strings.CutPrefix(inner, "/"); ok {
			closing = local(strings.TrimRight(closing, " /\t\n\r"))
			depth := -1
			for k := len(stack) - 1; k >= 0; k-- {
				if stack[k].Name == closing {
					depth = k
					break
				}
			}
			if depth > 0 {
				for len(stack) > depth {
					done := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					pushChild(Node{Element: done})
				}
			}
			continue
		}
		empty := strings.HasSuffix(inner, "/")
		if empty {
			inner = inner[:len(inner)-1]
		}
		el := element(inner)
		if empty || len(stack) > MostDepth {
			pushChild(Node{Element: el})
		} else {
			stack = append(stack, el)
		}
	}
	for len(stack) > 1 {
		done := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		pushChild(Node{Element: done})
	}
	for _, n := range stack[0].Children {
		if n.Element != nil {
			return n.Element, true
		}
	}
	return nil, false
}
