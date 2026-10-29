// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import (
	"encoding/binary"
	"github.com/Till0196/coax/pkg/win32"
	"testing"
)

type selectorWrite struct {
	set             win32.GUID
	id, node, value uint32
}
type selectorRecorder struct{ writes []selectorWrite }

func (r *selectorRecorder) SetNode(set win32.GUID, id, node uint32, data []byte) error {
	r.writes = append(r.writes, selectorWrite{set, id, node, binary.LittleEndian.Uint32(data)})
	return nil
}

func TestStreamSelection(t *testing.T) {
	for _, test := range []struct {
		name   string
		mode   DemodMode
		stream int32
		value  uint32
		count  int
		fails  bool
	}{
		{"ISDB-S first", ModeISDBS, -1, 0, 1, false},
		{"ISDB-S absolute", ModeISDBS, 16400, 0x14010, 1, false},
		{"ISDB-S zero", ModeISDBS, 0, 0, 1, false},
		{"ISDB-S relative slot", ModeISDBS, 4, 4, 1, false},
		{"ISDB-S too wide", ModeISDBS, 65536, 0, 0, true},
		{"S2 all streams", ModeDVBS2, -1, 0xffffffff, 1, false},
		{"S2 explicit stream", ModeDVBS2, 255, 255, 1, false},
		{"S2 too wide", ModeDVBS2, 256, 0, 0, true},
		{"T2 PLP", ModeDVBT2, 0x1234, 0x34, 1, false},
		{"C2 data slice", ModeDVBC2, 123, 123, 1, false},
		{"whole cable multiplex", ModeDVBC, -1, 0, 0, false},
		{"J83-B", ModeJ83B, -1, 0, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := &selectorRecorder{}
			err := SelectStream(r, test.mode, test.stream)
			if (err != nil) != test.fails || len(r.writes) != test.count {
				t.Fatalf("writes=%v err=%v", r.writes, err)
			}
			if test.count == 1 {
				w := r.writes[0]
				if w.node != 1 || w.value != test.value {
					t.Fatalf("write=%+v", w)
				}
				if test.mode == ModeDVBT2 {
					if w.set.Data1 != 0xef30f379 || w.id != 12 {
						t.Fatalf("PLP write=%+v", w)
					}
				} else if w.set != demodulatorSet || w.id != 1 {
					t.Fatalf("stream write=%+v", w)
				}
			}
		})
	}
}
