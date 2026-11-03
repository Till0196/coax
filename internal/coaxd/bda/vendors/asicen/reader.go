// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asicen

import "io"

// Reader keeps packet fragments across reads while restoring TS sync bytes.
type Reader struct {
	Source     io.Reader
	Control    *Control
	raw, ready []byte
	scratch    []byte
	ended      error
}

func (r *Reader) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	if len(r.ready) > 0 {
		n := copy(out, r.ready)
		r.ready = r.ready[n:]
		return n, nil
	}
	if r.ended != nil {
		return 0, r.ended
	}
	if r.scratch == nil {
		r.scratch = make([]byte, readSize)
	}
	buf := r.scratch[:readSize-len(r.raw)]
	n, e := r.Source.Read(buf)
	r.raw = append(r.raw, buf[:n]...)
	complete := len(r.raw) / 188 * 188
	if complete > 0 {
		if err := r.Control.RestoreSync(r.raw[:complete]); err != nil {
			r.ended = err
			return 0, err
		}
		r.ready = append(r.ready[:0], r.raw[:complete]...)
		r.raw = r.raw[:copy(r.raw, r.raw[complete:])]
	}
	if e != nil {
		r.ended = e
		if len(r.raw) > 0 && e == io.EOF {
			r.ended = io.ErrUnexpectedEOF
		}
	}
	n = copy(out, r.ready)
	r.ready = r.ready[n:]
	if n > 0 {
		return n, nil
	}
	return 0, r.ended
}
