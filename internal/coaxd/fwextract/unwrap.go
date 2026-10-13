// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"path"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/usb/bridge/asv5211"
)

// unwrapDepthLimit stops a malformed or hostile container from recursing
// without end.  The deepest real path is zip, gzip, tar, module.
const unwrapDepthLimit = 8

// memberSizeLimit bounds what a single container member may expand to, so that
// a crafted archive cannot exhaust memory.
const memberSizeLimit = 512 << 20

// totalExpansionLimit bounds all bytes retained while walking one input: a
// per-member limit alone still lets an archive with many large members exhaust
// memory.
const totalExpansionLimit = 512 << 20

type expansionBudget struct{ remaining int64 }

func newExpansionBudget() *expansionBudget {
	return &expansionBudget{remaining: totalExpansionLimit}
}

func (b *expansionBudget) read(reader io.Reader, prefix []byte) ([]byte, error) {
	limit := int64(memberSizeLimit)
	if b.remaining < limit {
		limit = b.remaining
	}
	if int64(len(prefix)) > limit {
		return nil, fmt.Errorf("expanded data exceeds the %d-byte total limit", totalExpansionLimit)
	}
	restLimit := limit - int64(len(prefix))
	rest, err := io.ReadAll(io.LimitReader(reader, restLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(rest)) > restLimit {
		return nil, fmt.Errorf("expanded data exceeds its %d-byte member limit or the %d-byte total limit",
			memberSizeLimit, totalExpansionLimit)
	}
	content := append(prefix, rest...)
	b.remaining -= int64(len(content))
	return content, nil
}

// take charges n bytes about to be kept, before the work of making them.
func (b *expansionBudget) take(n int) error {
	if n < 0 || int64(n) > b.remaining {
		return fmt.Errorf("expanded data exceeds the %d-byte total limit", totalExpansionLimit)
	}
	b.remaining -= int64(n)
	return nil
}

// Step is one container the search descended through.
type Step struct {
	Kind string // zip, gz, tar, is, cfb, cab, file
	Name string
	Size int
}

// Trace reports the path taken to reach a firmware image.
type Trace []Step

// String renders the trace one container per line.
func (t Trace) String() string {
	var out strings.Builder
	for _, step := range t {
		fmt.Fprintf(&out, "%-8s %s", step.Kind, step.Name)
		if step.Size > 0 {
			fmt.Fprintf(&out, "   (%d)", step.Size)
		}
		out.WriteString("\n")
	}
	return out.String()
}

// Firmware is an image recovered from a driver package.
type Firmware struct {
	// Kind names the chip the image belongs to.
	Kind string
	// FileName is what the image is called where the tools look for it.
	FileName string
	Image    []byte
	// Note carries anything the extractor recognised about the image, or
	// empty when there was nothing to add.
	Note string
}

// ErrNotFound is returned when nothing in the input carried a firmware image.
var ErrNotFound = errors.New("no firmware found in this input")

// extractors are tried in turn against every file the search reaches.  IT9175
// comes first because it validates against an exact image length, so it cannot
// claim a driver belonging to one of the others.
var extractors = []func(driver []byte) (Firmware, error){
	func(driver []byte) (Firmware, error) {
		image, err := ExtractIT9175Firmware(driver)
		return Firmware{Kind: "it9175", FileName: "it9175-firmware.bin", Image: image}, err
	},
	func(driver []byte) (Firmware, error) {
		image, err := ExtractIT930xFirmware(driver)
		if err != nil {
			return Firmware{}, err
		}
		// px4_drv's fwtool identifies this image by CRC32, so reporting it
		// lets an operator cross-check against that project's table.
		return Firmware{
			Kind:     "it930x",
			FileName: "it930x-firmware.bin",
			Image:    image,
			Note:     fmt.Sprintf("crc32 %08x", crc32.ChecksumIEEE(image)),
		}, nil
	},
	func(driver []byte) (Firmware, error) {
		firmware, err := ExtractASV52xxFirmware(driver)
		if err != nil {
			return Firmware{}, err
		}
		note := "unrecognised image; --firmware-index has to be supplied when uploading it"
		if firmware.Known {
			note = fmt.Sprintf("starts at %#04x", firmware.Index)
			if firmware.Description != "" {
				note = firmware.Description + ", " + note
			}
		}
		return Firmware{Kind: "asv52xx", FileName: asv5211.FirmwareFile, Image: firmware.Image, Note: note}, nil
	},
}

// FindFirmware descends through the containers in data until it reaches a
// firmware image: a driver holding one this package can recover, or a file the
// vendor shipped as firmware already.
//
// Containers are recognised by content, so the same call takes a vendor archive,
// anything nested inside it, a bare driver, or a bare image.
func FindFirmware(data []byte, name string) (Firmware, Trace, error) {
	return findFirmware(data, name, 0, newExpansionBudget())
}

func findFirmware(data []byte, name string, depth int, budget *expansionBudget) (Firmware, Trace, error) {
	if depth > unwrapDepthLimit {
		return Firmware{}, nil, ErrNotFound
	}
	// A file that is already firmware is the answer, before anything is
	// opened: TBS ship theirs that way, and dropping such a file on the
	// program has no other way of asking for it.
	if IsDistributedFirmware(name, data) {
		base := path.Base(strings.ReplaceAll(name, `\`, "/"))
		return Firmware{Kind: "file", FileName: base, Image: data},
			Trace{{Kind: "file", Name: name, Size: len(data)}}, nil
	}
	members, kind, err := openContainer(data, name, budget)
	if err != nil {
		return Firmware{}, nil, err
	}
	if kind == "" {
		// Not a container, so this is as deep as the search goes.
		for _, extract := range extractors {
			if firmware, err := extract(data); err == nil {
				return firmware, Trace{{Kind: "file", Name: name, Size: len(data)}}, nil
			}
		}
		return Firmware{}, nil, ErrNotFound
	}
	for _, member := range members {
		firmware, trace, err := findFirmware(member.Data, member.Name, depth+1, budget)
		if err != nil {
			continue
		}
		return firmware, append(Trace{{Kind: kind, Name: name, Size: len(data)}}, trace...), nil
	}
	return Firmware{}, nil, ErrNotFound
}

// openContainer returns the members of data when it is a container this package
// can open, along with a short name for the kind.  An empty kind means data is
// not a container.
func openContainer(data []byte, name string, budget *expansionBudget) ([]Member, string, error) {
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		members, err := extractZip(data, budget)
		return members, "zip", err
	case bytes.HasPrefix(data, []byte{0x1F, 0x8B}):
		members, err := extractGzip(data, name, budget)
		return members, "gz", err
	case bytes.HasPrefix(data, []byte("BZh")):
		members, err := extractBzip2(data, name, budget)
		return members, "bz2", err
	case isTar(data):
		members, err := extractTar(data, budget)
		return members, "tar", err
	case IsCompoundFile(data):
		members, err := ExtractCompoundFile(data, budget)
		return members, "cfb", err
	case IsCabinet(data):
		members, err := ExtractCabinet(data, wantedInCabinet, budget)
		return members, "cab", err
	case IsInstallShieldSetup(data):
		members, err := ExtractInstallShield(data, budget)
		return members, "is", err
	}
	return nil, "", nil
}

// extractZip reads the members of a zip that are worth descending into.
func extractZip(data []byte, budget *expansionBudget) ([]Member, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var members []Member
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() || entry.UncompressedSize64 > memberSizeLimit {
			continue
		}
		file, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("zip member %q: %w", entry.Name, err)
		}
		content, err := readIfWorthwhile(file, entry.Name, budget)
		file.Close()
		if err != nil {
			return nil, fmt.Errorf("zip member %q: %w", entry.Name, err)
		}
		if content != nil {
			members = append(members, Member{Name: entry.Name, Data: content})
		}
	}
	return members, nil
}

// extractBzip2 returns the single stream a bzip2 member holds. Needed because a
// vendor ships its whole collection of chip images as one tar.bz2.
func extractBzip2(data []byte, name string, budget *expansionBudget) ([]Member, error) {
	content, err := budget.read(bzip2.NewReader(bytes.NewReader(data)), nil)
	if err != nil {
		return nil, err
	}
	return []Member{{Name: strings.TrimSuffix(name, ".bz2"), Data: content}}, nil
}

// extractGzip returns the single stream a gzip member holds.
func extractGzip(data []byte, name string, budget *expansionBudget) ([]Member, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	content, err := budget.read(reader, nil)
	if err != nil {
		return nil, err
	}
	inner := reader.Name
	if inner == "" {
		inner = strings.TrimSuffix(name, ".gz")
	}
	return []Member{{Name: inner, Data: content}}, nil
}

// isTar reports whether data begins with a tar header.  The magic field is
// only filled in by ustar and later, and PLEX ships its Linux driver as an old
// v7 tar that leaves it empty, so detection rests on the header checksum the
// reader verifies instead.
func isTar(data []byte) bool {
	if len(data) < 512 {
		return false
	}
	_, err := tar.NewReader(bytes.NewReader(data)).Next()
	return err == nil
}

// extractTar reads the members of a tar that are worth descending into.
func extractTar(data []byte, budget *expansionBudget) ([]Member, error) {
	archive := tar.NewReader(bytes.NewReader(data))
	var members []Member
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return members, nil // a truncated tail must not lose what was read
		}
		if header.Typeflag != tar.TypeReg || header.Size > memberSizeLimit {
			continue
		}
		content, err := readIfWorthwhile(archive, header.Name, budget)
		if err != nil {
			return nil, fmt.Errorf("tar member %q: %w", header.Name, err)
		}
		if content != nil {
			members = append(members, Member{Name: header.Name, Data: content})
		}
	}
	return members, nil
}

// readIfWorthwhile reads a member only when its first bytes say it is a
// container or an executable, so manuals and images are skipped before being
// decompressed in full.  A nil result means the member was not worth reading.
func readIfWorthwhile(reader io.Reader, name string, budget *expansionBudget) ([]byte, error) {
	head := make([]byte, 8)
	read, err := io.ReadFull(reader, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	head = head[:read]
	// A firmware image has no magic of its own, so one is recognised by the
	// name its vendor distributes it under rather than by its first bytes.
	if !worthDescending(head) && !distributedName.MatchString(strings.ToLower(path.Base(name))) {
		return nil, nil
	}
	return budget.read(reader, head)
}

// worthDescending reports whether a member starts like something that can hold
// a firmware image: an archive, a Windows driver, or a Linux module.
func worthDescending(head []byte) bool {
	for _, magic := range [][]byte{
		[]byte("PK\x03\x04"),  // zip
		{0x1F, 0x8B},          // gzip
		[]byte("MSCF"),        // cabinet
		cfbSignature,          // compound file, an .msi
		[]byte("MZ"),          // a Windows executable or driver
		{0x7F, 'E', 'L', 'F'}, // a Linux module
	} {
		if bytes.HasPrefix(head, magic) {
			return true
		}
	}
	return false
}

// wantedInCabinet keeps whole folders of a driver cabinet compressed unless
// they hold a driver.
func wantedInCabinet(name string) bool {
	if index := strings.LastIndexAny(name, "/\\"); index >= 0 {
		name = name[index+1:]
	}
	name = strings.ToLower(name)
	// A driver to take apart, or an image shipped as a file: Siano's Windows
	// driver keeps its .inp images in the same cabinet as its .sys.
	return hasExtension(name, ".sys") || distributedName.MatchString(name)
}
