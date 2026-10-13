// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

import (
	"bytes"
	"testing"
)

func dsmccSectionOf(tableID byte, extension uint16, body []byte) []byte {
	length := 9 + len(body)
	out := []byte{tableID, 0xb0 | byte(length>>8), byte(length), byte(extension >> 8), byte(extension), 0xc1, 0, 0}
	out = append(out, body...)
	crc := CRC32(out)
	return append(out, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
}

func dsmccBody(messageID uint16, id uint32, message []byte) []byte {
	out := []byte{dsmccProtocolDiscriminator, dsmccTypeDownload, byte(messageID >> 8), byte(messageID),
		byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id), 0xff, 0, byte(len(message) >> 8), byte(len(message))}
	return append(out, message...)
}

type moduleSpec struct {
	id      uint16
	size    uint32
	version byte
}

func aDII(transaction, download uint32, blockSize uint16, modules []moduleSpec) []byte {
	message := []byte{byte(download >> 24), byte(download >> 16), byte(download >> 8), byte(download),
		byte(blockSize >> 8), byte(blockSize), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	message = append(message, byte(len(modules)>>8), byte(len(modules)))
	for _, module := range modules {
		message = append(message, byte(module.id>>8), byte(module.id),
			byte(module.size>>24), byte(module.size>>16), byte(module.size>>8), byte(module.size),
			module.version, 3, 0x01, 1, 'x')
	}
	message = append(message, 0, 1, 0xf0)
	return dsmccSectionOf(TableDII, uint16(transaction), dsmccBody(dsmccMessageDII, transaction, message))
}

func aDDB(download uint32, module uint16, version byte, block uint16, data []byte) []byte {
	message := []byte{byte(module >> 8), byte(module), version, 0xff, byte(block >> 8), byte(block)}
	message = append(message, data...)
	return dsmccSectionOf(TableDDB, module, dsmccBody(dsmccMessageDDB, download, message))
}

func TestADIIListsItsModules(t *testing.T) {
	dii, ok := ParseDII(aDII(7, 0x1000_0001, 4, []moduleSpec{{1, 10, 2}, {2, 3, 0}}))
	if !ok || dii.TransactionID != 7 || dii.DownloadID != 0x1000_0001 || dii.BlockSize != 4 || len(dii.Modules) != 2 {
		t.Fatalf("DII %+v %v", dii, ok)
	}
	if !bytes.Equal(dii.Modules[0].Info, []byte{0x01, 1, 'x'}) || !bytes.Equal(dii.PrivateData, []byte{0xf0}) {
		t.Errorf("module info % x, private % x", dii.Modules[0].Info, dii.PrivateData)
	}
}

func TestAModuleComesBackOnceItsLastBlockDoes(t *testing.T) {
	var carousel Carousel
	if got := carousel.Push(aDII(1, 1, 4, []moduleSpec{{1, 10, 0}})); got.Kind != BroughtListed {
		t.Fatalf("the list brought %v", got.Kind)
	}
	for _, section := range [][]byte{
		aDDB(1, 1, 0, 2, []byte("ij")),
		aDDB(1, 1, 0, 0, []byte("abcd")),
		aDDB(1, 1, 3, 1, []byte("efgh")), // a version not listed
	} {
		if got := carousel.Push(section); got.Kind != BroughtNothing {
			t.Fatalf("a block brought %v early", got.Kind)
		}
	}
	got := carousel.Push(aDDB(1, 1, 0, 1, []byte("efgh")))
	if got.Kind != BroughtModule || string(got.Module.Data) != "abcdefghij" || got.Module.ID != 1 || got.Module.Version != 0 {
		t.Fatalf("the last block brought %+v", got)
	}
	// Round again: the same blocks are not news, and neither is the list.
	if carousel.Push(aDDB(1, 1, 0, 0, []byte("abcd"))).Kind != BroughtNothing ||
		carousel.Push(aDII(1, 1, 4, []moduleSpec{{1, 10, 0}})).Kind != BroughtNothing {
		t.Error("a second turn of the carousel was news")
	}
}

// A list naming a module of four gigabytes, or one cut into more blocks than
// can be numbered, is damaged: nothing is made room for, and its blocks are
// nothing.
func TestAModuleTooLargeToBeRealIsPassedOver(t *testing.T) {
	var carousel Carousel
	if got := carousel.Push(aDII(1, 1, 4, []moduleSpec{{1, 0xffff_ffff, 0}, {2, 1 << 20, 0}})); got.Kind != BroughtListed {
		t.Fatalf("the list brought %v", got.Kind)
	}
	if carousel.Push(aDDB(1, 1, 0, 0, []byte("abcd"))).Kind != BroughtNothing ||
		carousel.Push(aDDB(1, 2, 0, 0, []byte("abcd"))).Kind != BroughtNothing {
		t.Error("a block of a damaged module was taken")
	}
	for _, module := range carousel.modules {
		if module.blocks != nil {
			t.Error("room was made for a damaged module")
		}
	}
}

func TestANewListKeepsWhatWasWholeAndGathersWhatChanged(t *testing.T) {
	var carousel Carousel
	carousel.Push(aDII(1, 1, 4, []moduleSpec{{1, 2, 0}, {2, 2, 0}}))
	if carousel.Push(aDDB(1, 1, 0, 0, []byte("ab"))).Kind != BroughtModule {
		t.Fatal("module 1 did not come back")
	}
	if carousel.Push(aDII(2, 1, 4, []moduleSpec{{1, 2, 0}, {2, 2, 1}})).Kind != BroughtListed {
		t.Fatal("the changed list was not news")
	}
	if carousel.Push(aDDB(1, 1, 0, 0, []byte("ab"))).Kind != BroughtNothing ||
		carousel.Push(aDDB(1, 2, 0, 0, []byte("xx"))).Kind != BroughtNothing {
		t.Error("a module that stands, or a version gone, was taken")
	}
	got := carousel.Push(aDDB(1, 2, 1, 0, []byte("cd")))
	if got.Kind != BroughtModule || got.Module.ID != 2 || got.Module.Version != 1 || string(got.Module.Data) != "cd" {
		t.Errorf("the changed module brought %+v", got)
	}
}

func TestADamagedDSMCCSectionIsRefused(t *testing.T) {
	bad := aDII(1, 1, 4, []moduleSpec{{1, 10, 0}})[:20]
	if _, ok := ParseDII(bad); ok {
		t.Error("a cut DII was read")
	}
	if _, ok := ParseDDB(aDII(1, 1, 4, nil)); ok {
		t.Error("a DII was read as a DDB")
	}
	var carousel Carousel
	if carousel.Push(aDDB(1, 1, 0, 0, []byte("ab"))).Kind != BroughtNothing {
		t.Error("a block was taken before any list")
	}
}
