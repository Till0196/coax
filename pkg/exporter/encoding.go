// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
)

// The character coding the Windows formats are read in.
//
// Neither of them is UTF-8 and neither can be made to be. A channel definition
// file is read a line at a time and the name put through ShiftJisToUtf16, which
// is code page 932 and has no other case. A TVTest channel file is code page
// 932 as well, unless it opens with a byte-order mark, in which case it is
// UTF-16.
//
// What UTF-8 would do is worse than failing: the bytes are valid code page 932
// and decode to something, so the file loads and every Japanese name in it is
// mojibake.

// cp932 renders text as code page 932, and says whether it fitted.
func cp932(text string) ([]byte, bool) {
	encoded, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(text))
	return encoded, err == nil
}

// fitsCP932 reports whether text can be written in code page 932 at all.
//
// Service names carry ARIB symbols that it has no character for, so this is
// asked before one is put anywhere that cannot fall back to UTF-16.
func fitsCP932(text string) bool {
	_, ok := cp932(text)
	return ok
}

// utf8WithBOM renders text as UTF-8 behind the mark that says so. EDCB reads a
// file as UTF-8 when it opens with the mark and as the machine's own code page
// otherwise, so the mark is what keeps a Japanese service name from coming out
// as mojibake.
func utf8WithBOM(text string) []byte {
	return append([]byte{0xef, 0xbb, 0xbf}, text...)
}

// utf16 renders text as UTF-16 with the byte-order mark that says so.
func utf16(text string) ([]byte, error) {
	return unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(text))
}
