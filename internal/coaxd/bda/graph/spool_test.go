// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package graph

import (
	"bytes"
	"os"
	"testing"
)

// read follows a spool the way a stream does: to the end of each file, then on
// to the next once there is one, deleting what it has finished with.
func readSpool(t *testing.T, spool *Spool) []byte {
	t.Helper()
	var got []byte
	path := spool.First()
	for {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, data...)
		next, ok := spool.Next(path)
		if !ok {
			return got
		}
		if err := spool.Consumed(path); err != nil {
			t.Fatal(err)
		}
		path = next
	}
}

func TestSpoolCarriesEverythingAcrossFiles(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TMP", os.Getenv("TMPDIR"))
	spool, err := NewSpool(0)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Remove()
	spool.segment = 10

	var want []byte
	for index := range 5 {
		chunk := bytes.Repeat([]byte{byte(index)}, 7)
		want = append(want, chunk...)
		if written, err := spool.Write(chunk); err != nil || written != len(chunk) {
			t.Fatalf("write %d: %d, %v", index, written, err)
		}
	}
	if len(spool.paths) < 2 {
		t.Fatalf("the spool took %d bytes in %d file", len(want), len(spool.paths))
	}
	if got := readSpool(t, spool); !bytes.Equal(got, want) {
		t.Errorf("read back %x, want %x", got, want)
	}
	if len(spool.paths) != 1 {
		t.Errorf("%d files are left after reading, want the one being written", len(spool.paths))
	}
}

func TestSpoolDropsWhatAReaderFellTooFarBehindFor(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TMP", os.Getenv("TMPDIR"))
	spool, err := NewSpool(0)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Remove()
	spool.segment = 4
	for range spoolUnread * 4 {
		if _, err := spool.Write([]byte("abcd")); err != nil {
			t.Fatal(err)
		}
	}
	if len(spool.paths) != spoolUnread {
		t.Errorf("an unread spool holds %d files, want %d", len(spool.paths), spoolUnread)
	}
}

func TestSpoolLimitSwallowsTheRest(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TMP", os.Getenv("TMPDIR"))
	spool, err := NewSpool(6)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Remove()
	for range 3 {
		if written, err := spool.Write([]byte("abcd")); err != nil || written != 4 {
			t.Fatalf("write: %d, %v", written, err)
		}
	}
	if got := readSpool(t, spool); string(got) != "abcdab" {
		t.Errorf("read back %q, want the first six bytes", got)
	}
}

func TestSpoolRemoveDeletesEveryFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	spool, err := NewSpool(0)
	if err != nil {
		t.Fatal(err)
	}
	spool.segment = 2
	for range 3 {
		_, _ = spool.Write([]byte("xy"))
	}
	if err := spool.Remove(); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 0 {
		t.Errorf("%d files are left", len(left))
	}
	if written, err := spool.Write([]byte("z")); err != nil || written != 1 {
		t.Errorf("a write after Remove: %d, %v", written, err)
	}
}
