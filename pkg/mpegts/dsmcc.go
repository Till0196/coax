// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

// The DSM-CC data carousel: modules of data sent round and round in sections,
// so that a receiver joining at any moment gets all of them within one turn
// (ISO/IEC 13818-6 clauses 7 and 9).
//
// Three tables ride on one identifier. The download info indication (DII,
// table 0x3B) lists the modules of the carousel -- their identifiers, sizes and
// versions, and a few bytes describing each. The download data blocks (DDB,
// table 0x3C) carry the modules cut into blocks of the size the DII said. The
// stream descriptors (table 0x3D) are not part of the download at all: they
// carry events timed against the stream, and are handed on as they come.
//
// Carousel does the gathering: it takes sections of one identifier and gives
// back whole modules, once, when their last block arrives. What the bytes of a
// module mean is for the broadcasting standard on top to read.

// The table identifiers of the three sections.
const (
	TableDII               = 0x3b
	TableDDB               = 0x3c
	TableStreamDescriptors = 0x3d
)

// What a dsmccMessageHeader says the message is.
const (
	dsmccProtocolDiscriminator = 0x11
	dsmccTypeDownload          = 0x03
	dsmccMessageDII            = 0x1002
	dsmccMessageDDB            = 0x1003
)

// DII is the download info indication: the list of modules in the carousel.
type DII struct {
	// TransactionID changes whenever the list does; a DII carrying the one
	// already read says nothing new.
	TransactionID uint32
	// DownloadID ties the blocks to this list: a DDB with a different one is
	// of a different download.
	DownloadID uint32
	// BlockSize is how long every block of every module is, except the last
	// of each.
	BlockSize uint16
	Modules   []ModuleInfo
	// PrivateData is the bytes after the module list, which a standard on top
	// fills with descriptors of its own.
	PrivateData []byte
}

// ModuleInfo is one module as the DII describes it.
type ModuleInfo struct {
	ID      uint16
	Size    uint32
	Version byte
	// Info is the moduleInfoBytes: descriptors, in the shape the standard on
	// top gives them.
	Info []byte
}

// DDB is one block of a module. Data aliases the section it was read from.
type DDB struct {
	DownloadID    uint32
	ModuleID      uint16
	ModuleVersion byte
	BlockNumber   uint16
	Data          []byte
}

// StreamDescriptors is a stream descriptors section: the descriptor loop as
// sent, with the header fields a standard on top gives meaning to.
type StreamDescriptors struct {
	Extension   uint16
	Version     byte
	Number      byte
	LastNumber  byte
	Descriptors []byte
}

type dsmccHeader struct {
	tableID    byte
	extension  uint16
	version    byte
	number     byte
	lastNumber byte
	body       []byte
}

// dsmccSection reads the long section header every DSM-CC section starts with,
// and the bytes between it and the check.
func dsmccSection(section []byte) (dsmccHeader, bool) {
	if len(section) < 12 || section[1]&0x80 == 0 {
		return dsmccHeader{}, false
	}
	length := int(section[1]&0x0f)<<8 | int(section[2])
	end := 3 + length
	if end > len(section) || length < 9 {
		return dsmccHeader{}, false
	}
	return dsmccHeader{
		tableID:    section[0],
		extension:  uint16(section[3])<<8 | uint16(section[4]),
		version:    section[5] >> 1 & 0x1f,
		number:     section[6],
		lastNumber: section[7],
		body:       section[8 : end-4],
	}, true
}

// dsmccMessage reads the message header the DII and DDB share, and the message
// after it. The fourth word is the transactionId of a DII and the downloadId of
// a DDB.
func dsmccMessage(body []byte, wanted uint16) (uint32, []byte, bool) {
	if len(body) < 12 || body[0] != dsmccProtocolDiscriminator || body[1] != dsmccTypeDownload {
		return 0, nil, false
	}
	if uint16(body[2])<<8|uint16(body[3]) != wanted {
		return 0, nil, false
	}
	id := be32(body[4:])
	adaptation := int(body[9])
	length := int(body[10])<<8 | int(body[11])
	if length < adaptation || 12+length > len(body) {
		return 0, nil, false
	}
	return id, body[12+adaptation : 12+length], true
}

func be32(data []byte) uint32 {
	return uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
}

// ParseDII reads a DII section.
func ParseDII(section []byte) (DII, bool) {
	header, ok := dsmccSection(section)
	if !ok || header.tableID != TableDII {
		return DII{}, false
	}
	transaction, message, ok := dsmccMessage(header.body, dsmccMessageDII)
	if !ok || len(message) < 20 {
		return DII{}, false
	}
	dii := DII{
		TransactionID: transaction,
		DownloadID:    be32(message),
		BlockSize:     uint16(message[4])<<8 | uint16(message[5]),
	}
	// windowSize, ackPeriod, tCDownloadWindow and tCDownloadScenario come
	// between, eight bytes nothing here uses.
	compatibility := int(message[16])<<8 | int(message[17])
	at := 18 + compatibility
	if at+2 > len(message) {
		return DII{}, false
	}
	count := int(message[at])<<8 | int(message[at+1])
	at += 2
	dii.Modules = make([]ModuleInfo, 0, min(count, 64))
	for range count {
		if at+8 > len(message) {
			return DII{}, false
		}
		end := at + 8 + int(message[at+7])
		if end > len(message) {
			return DII{}, false
		}
		dii.Modules = append(dii.Modules, ModuleInfo{
			ID:      uint16(message[at])<<8 | uint16(message[at+1]),
			Size:    be32(message[at+2:]),
			Version: message[at+6],
			Info:    append([]byte(nil), message[at+8:end]...),
		})
		at = end
	}
	if at+2 > len(message) {
		return DII{}, false
	}
	private := int(message[at])<<8 | int(message[at+1])
	at += 2
	if at+private > len(message) {
		return DII{}, false
	}
	dii.PrivateData = append([]byte(nil), message[at:at+private]...)
	return dii, true
}

// ParseDDB reads a DDB section.
func ParseDDB(section []byte) (DDB, bool) {
	header, ok := dsmccSection(section)
	if !ok || header.tableID != TableDDB {
		return DDB{}, false
	}
	download, message, ok := dsmccMessage(header.body, dsmccMessageDDB)
	if !ok || len(message) < 6 {
		return DDB{}, false
	}
	return DDB{
		DownloadID:    download,
		ModuleID:      uint16(message[0])<<8 | uint16(message[1]),
		ModuleVersion: message[2],
		BlockNumber:   uint16(message[4])<<8 | uint16(message[5]),
		Data:          message[6:],
	}, true
}

// ParseStreamDescriptors reads a stream descriptors section.
func ParseStreamDescriptors(section []byte) (StreamDescriptors, bool) {
	header, ok := dsmccSection(section)
	if !ok || header.tableID != TableStreamDescriptors {
		return StreamDescriptors{}, false
	}
	return StreamDescriptors{
		Extension:   header.extension,
		Version:     header.version,
		Number:      header.number,
		LastNumber:  header.lastNumber,
		Descriptors: append([]byte(nil), header.body...),
	}, true
}

// Module is a module, whole.
type Module struct {
	DownloadID uint32
	ID         uint16
	Version    byte
	// Info is the moduleInfoBytes the DII gave for it.
	Info []byte
	Data []byte
}

// BroughtKind says what one section of the carousel brought.
type BroughtKind int

const (
	// BroughtNothing is a section that said nothing new.
	BroughtNothing BroughtKind = iota
	// BroughtListed is a new list of modules. The carousel's contents from
	// here on are the modules of this list; whatever was gathered of a module
	// not in it is let go.
	BroughtListed
	// BroughtModule is a module whose last block arrived.
	BroughtModule
	// BroughtStream is a stream descriptors section, as it came.
	BroughtStream
)

// Brought is what one section of the carousel brought; the field its Kind
// names is the one filled in.
type Brought struct {
	Kind   BroughtKind
	Listed DII
	Module Module
	Stream StreamDescriptors
}

// carouselMostModule is the largest module gathered. A data broadcast's modules
// are tens of kilobytes; this leaves room for any that is real and none for a
// size field that has been damaged into gigabytes.
const carouselMostModule = 16 * 1024 * 1024

// carouselMostBlocks is the most blocks a module can come in: a block's number
// is sixteen bits.
const carouselMostBlocks = 1 << 16

// gathering is a module part way through arriving.
type gathering struct {
	info  ModuleInfo
	count int
	// blocks is made room for when the first block arrives: a list may name
	// many modules, and room for all of them at once is room a receiver tuned
	// for a moment never fills.
	blocks [][]byte
	have   int
	// done says this version was already given back whole, so that the next
	// turn of the carousel does not give it back again.
	done bool
}

// Carousel gathers the modules of one carousel out of its sections. One
// identifier is one carousel: a DII lists what is there, the blocks are matched
// to the list by downloadId, module identifier and version, and a module comes
// back when its last block does. A list read before (same transactionId) is not
// news, and a module given back is not given back again while its version
// stands.
type Carousel struct {
	listed        bool
	transactionID uint32
	downloadID    uint32
	blockSize     int
	modules       []gathering
}

// Push takes one section and says what it brought.
func (carousel *Carousel) Push(section []byte) Brought {
	if len(section) == 0 {
		return Brought{}
	}
	switch section[0] {
	case TableDII:
		if dii, ok := ParseDII(section); ok {
			return carousel.list(dii)
		}
	case TableDDB:
		if ddb, ok := ParseDDB(section); ok {
			return carousel.block(ddb)
		}
	case TableStreamDescriptors:
		if stream, ok := ParseStreamDescriptors(section); ok {
			return Brought{Kind: BroughtStream, Stream: stream}
		}
	}
	return Brought{}
}

func (carousel *Carousel) list(dii DII) Brought {
	if carousel.listed && carousel.transactionID == dii.TransactionID {
		return Brought{}
	}
	sameDownload := carousel.listed && carousel.downloadID == dii.DownloadID
	carousel.listed = true
	carousel.transactionID = dii.TransactionID
	carousel.downloadID = dii.DownloadID
	carousel.blockSize = max(int(dii.BlockSize), 1)
	old := carousel.modules
	carousel.modules = make([]gathering, 0, len(dii.Modules))
	for _, info := range dii.Modules {
		// A module that was already whole at this version stays done, so that
		// a list re-sent with one module changed gives back only the one.
		kept := false
		if sameDownload {
			for _, was := range old {
				if was.done && was.info.ID == info.ID && was.info.Version == info.Version {
					kept = true
					break
				}
			}
		}
		size := uint64(info.Size)
		count := int((size + uint64(carousel.blockSize) - 1) / uint64(carousel.blockSize))
		// A module larger than anything broadcast carries, or in more blocks
		// than a block number can count, is a damaged list. It is passed over
		// rather than made room for.
		unreasonable := size > carouselMostModule || count > carouselMostBlocks
		module := gathering{info: info, count: count, done: kept || unreasonable}
		if module.done {
			module.count = 0
		}
		carousel.modules = append(carousel.modules, module)
	}
	return Brought{Kind: BroughtListed, Listed: dii}
}

func (carousel *Carousel) block(ddb DDB) Brought {
	if !carousel.listed || ddb.DownloadID != carousel.downloadID {
		return Brought{}
	}
	var module *gathering
	for index := range carousel.modules {
		candidate := &carousel.modules[index]
		if candidate.info.ID == ddb.ModuleID && candidate.info.Version == ddb.ModuleVersion {
			module = candidate
			break
		}
	}
	if module == nil || module.done {
		return Brought{}
	}
	at := int(ddb.BlockNumber)
	if at >= module.count {
		return Brought{}
	}
	if module.blocks == nil {
		module.blocks = make([][]byte, module.count)
	}
	if module.blocks[at] != nil {
		return Brought{}
	}
	module.blocks[at] = append([]byte{}, ddb.Data...)
	module.have++
	if module.have < module.count {
		return Brought{}
	}
	data := make([]byte, 0, min(int(module.info.Size), carouselMostModule))
	for _, block := range module.blocks {
		data = append(data, block...)
	}
	module.blocks = nil
	module.done = true
	return Brought{Kind: BroughtModule, Module: Module{
		DownloadID: carousel.downloadID,
		ID:         module.info.ID,
		Version:    module.info.Version,
		Info:       module.info.Info,
		Data:       data,
	}}
}
