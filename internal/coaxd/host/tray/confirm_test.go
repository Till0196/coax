// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tray

import (
	"strings"
	"testing"
)

// TestConfirmPromptWarnsWhenSomethingIsAttached pins the part of the question
// that matters: whether it reads as a warning, and which answer is waiting
// under the cursor.
//
// A daemon stopped by accident takes a recording with it, and the icon sits in
// the one part of the screen people click at without reading.
func TestConfirmPromptWarnsWhenSomethingIsAttached(t *testing.T) {
	for _, want := range []struct {
		count    int
		warns    bool
		says     string
		defaults string
	}{
		{count: 0, warns: false, says: "Nothing is using it", defaults: "Yes"},
		{count: 1, warns: true, says: "One client", defaults: "No"},
		{count: 2, warns: true, says: "2 clients", defaults: "No"},
		{count: 47, warns: true, says: "47 clients", defaults: "No"},
		{count: -1, warns: false, says: "Stop the tuner daemon?", defaults: "Yes"},
	} {
		text, flags := confirmPrompt(want.count)
		if !strings.Contains(text, want.says) {
			t.Errorf("with %d attached the question is %q, which does not say %q", want.count, text, want.says)
		}
		if warns := flags&promptIconWarn == promptIconWarn; warns != want.warns {
			t.Errorf("with %d attached the question warns=%v, want %v", want.count, warns, want.warns)
		}
		second := flags&promptSecondFirst != 0
		if wantSecond := want.defaults == "No"; second != wantSecond {
			t.Errorf("with %d attached the default answer is %q", want.count,
				map[bool]string{true: "No", false: "Yes"}[second])
		}
		if flags&promptYesNo == 0 {
			t.Errorf("with %d attached the question offers no yes and no", want.count)
		}
	}
}

// TestConfirmPromptSaysNothingItCannotKnow covers a daemon that is not
// counting: saying "0 clients" would be inventing a fact.
func TestConfirmPromptSaysNothingItCannotKnow(t *testing.T) {
	text, _ := confirmPrompt(-1)
	if strings.Contains(text, "0") || strings.Contains(text, "Nothing is using") {
		t.Errorf("a daemon that cannot count said %q", text)
	}
}
