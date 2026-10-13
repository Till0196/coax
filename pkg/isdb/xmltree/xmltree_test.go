// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package xmltree

import (
	"strings"
	"testing"
)

func TestElementsAttributesAndTextAreReadWithoutPrefixes(t *testing.T) {
	root, ok := Parse(`<?xml version="1.0"?><tt xmlns:tts="x"><head><style xml:id="s" tts:color="#fff"/></head><body><p>a&amp;b<br/>c<![CDATA[<d>]]></p><!-- x --></body></tt>`)
	if !ok || root.Name != "tt" {
		t.Fatalf("root %+v", root)
	}
	style := root.FindAll("style")[0]
	if v, _ := style.Attr("id"); v != "s" {
		t.Errorf("id %q", v)
	}
	if v, _ := style.Attr("color"); v != "#fff" {
		t.Errorf("color %q", v)
	}
	p := root.FindAll("p")[0]
	var texts []string
	for _, n := range p.Children {
		if n.IsText {
			texts = append(texts, n.Text)
		}
	}
	if strings.Join(texts, "|") != "a&b|c|<d>" {
		t.Errorf("texts %q", texts)
	}
	if len(p.Elements()) != 1 {
		t.Errorf("elements %d", len(p.Elements()))
	}
}

func TestNestingWithoutEndStopsAtTheLimit(t *testing.T) {
	root, ok := Parse(strings.Repeat("<a>", 100_000))
	if !ok {
		t.Fatal("not read")
	}
	depth := 0
	at := root
	for {
		children := at.Elements()
		if len(children) == 0 {
			break
		}
		depth++
		at = children[0]
	}
	if depth > MostDepth+1 {
		t.Fatalf("depth %d", depth)
	}
}

func TestEntitiesAreDecoded(t *testing.T) {
	if got := Decode("&lt;&#x41;&#66;&bogus;"); got != "<AB&bogus;" {
		t.Fatalf("got %q", got)
	}
}
