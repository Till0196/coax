// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"fmt"
	"io"
	"strings"

	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/report"
)

// The TVTest channel file, which is a line per service:
//
//	; TVTest チャンネル設定ファイル
//	; 名称,チューニング空間,チャンネル,リモコン番号,サービスタイプ,サービスID,ネットワークID,TSID,状態
//	;#SPACE(0,BS)
//	テスト　衛星,0,14,1,1,101,4,16625,1
//
// It is the other half of a channel definition file rather than a replacement for one:
// that file tells the BonDriver where the carriers are, this tells TVTest which services
// are on them, referring to the carriers by the space and index the driver enumerates them
// at. Both are written from one placement. See placement.go.
//
// EDCB does not read this format itself; it ships ch2chset.vbs, which turns one of these
// into the ChSet4.txt and ChSet5.txt it does read.
//
// Only the first three fields are required of a line. The four that identify the service
// are written when they are known and left empty when they are not, as TVTest does.

// WriteCh2 writes the services of one delivery system as a TVTest channel
// file. The system is the same one WriteChSet is given: one BonDriver, one
// channel definition file, one of these beside it.
func WriteCh2(w io.Writer, system string, results []report.Result) (skipped []string, err error) {
	spaces, skipped := place(system, results)
	if len(spaces) == 0 {
		return skipped, fmt.Errorf("no %s channels to write", SystemDisplayName(system))
	}

	// The two lines TVTest writes at the head of one of these, word for word.
	// They are the format's, not this program's, which is why they are the one
	// thing here that is not in English.
	var file strings.Builder
	file.WriteString("; TVTest チャンネル設定ファイル\r\n")
	file.WriteString("; 名称,チューニング空間,チャンネル,リモコン番号,サービスタイプ,サービスID,ネットワークID,TSID,状態\r\n")
	file.WriteString("; written by coax for " + SystemDisplayName(system) + "\r\n")

	written := 0
	for id, space := range spaces {
		lines := ch2Space(space, id)
		if len(lines) == 0 {
			// A space whose carriers named no service is left undeclared rather than
			// declared empty, as TVTest does. The number is still the channel definition
			// file's.
			continue
		}
		fmt.Fprintf(&file, ";#SPACE(%d,%s)\r\n", id, space.name)
		for _, line := range lines {
			file.WriteString(line)
		}
		written += len(lines)
	}
	if written == 0 {
		// A file of nothing but space declarations would load and leave TVTest
		// with no channels, which reads as the scan having found nothing.
		return skipped, fmt.Errorf("no %s service was named; a channel file needs services, and only a channel definition file can be written from carriers alone",
			SystemDisplayName(system))
	}

	encoded, err := ch2Encoding(file.String())
	if err != nil {
		return skipped, err
	}
	_, err = w.Write(encoded)
	return skipped, err
}

// ch2Space is the lines of one channel space, in the order the carriers are
// numbered.
func ch2Space(space *placedSpace, id int) []string {
	var lines []string
	for index, channel := range space.channels {
		for _, service := range channel.stream.services {
			if !televisionServiceType(service.Type) {
				continue
			}
			lines = append(lines, ch2Line(channel, id, index, service))
		}
	}
	return lines
}

func ch2Line(channel placedChannel, space, index int, service isdb.ServiceInfo) string {
	name := service.Name
	if name == "" {
		// A service the tables did not name still has to be called something,
		// or there is no way to pick it out of a list.
		name = fmt.Sprintf("%s:%d", channel.name, service.ServiceID)
	}
	state := 0
	if enabledServiceType(service.Type) {
		state = 1
	}
	return fmt.Sprintf("%s,%d,%d,%d,%d,%d,%d,%d,%d\r\n",
		ch2Name(name), space, index,
		remoteControlKey(channel.stream, service.ServiceID),
		service.Type, service.ServiceID,
		channel.stream.networkID, channel.stream.streamID, state)
}

// enabledServiceType says whether a service is one TVTest leaves switched on.
// Television, promotional video and the 4K-only television service type are
// what somebody means to watch; the rest is written with its state off, so it
// is in the file to be switched on rather than missing. Same division TVTest's
// own scan makes.
func enabledServiceType(serviceType byte) bool {
	switch serviceType {
	case 0x01, 0xa5, 0xad:
		return true
	default:
		return false
	}
}

// remoteControlKey is the button a receiver puts a service on.
//
// Two of the three broadcasts say so outright and are believed. The advanced services say
// it service by service, in the remote control key descriptor of their TLV-NIT, which ARIB
// TR-B39 36.1.2.3 requires whenever that table is transmitted. A terrestrial network says
// it once for the whole multiplex, in the TS information descriptor of its network table.
//
// The older satellite platforms say nothing and number their services instead, so the
// number is worked out by the same arithmetic TVTest uses:
//
//	BS   101..103    the button is the identifier less 100:      BS 1 to 3
//	BS   141..229    less 100 and then divided by ten:           BS 4 to 12
//	SKY  32769..33767 less 32768
//
// Anything else is left at zero, which TVTest reads as "not said".
//
// Both descriptors live in a network table, the table a scan is most likely to have
// missed. So the arithmetic above is also what a TLV service falls back to when the sample
// held no TLV-NIT, and it gets that one wrong, because a TLV service reuses the numbers of
// the transport stream services under another network. A scan meant to produce a channel list wants a longer
// --analysis-time.
func remoteControlKey(stream carriedStream, serviceID uint16) int {
	if key := stream.remoteKeys[serviceID]; key != 0 {
		return int(key)
	}
	if stream.remoteKeyID != 0 {
		return int(stream.remoteKeyID)
	}
	for _, assignment := range remoteKeyAssignments {
		if stream.networkID != assignment.networkID ||
			serviceID < assignment.first || serviceID > assignment.last {
			continue
		}
		key := int(serviceID) - assignment.subtract
		if assignment.divide != 0 {
			key /= assignment.divide
		}
		return key
	}
	if serviceID < 1000 {
		return int(serviceID)
	}
	return 0
}

// remoteKeyAssignments are the platforms that number their services by button.
// The values are TVTest's own defaults, overridable in its settings; nothing
// here is in any stream.
var remoteKeyAssignments = []struct {
	networkID   uint16
	first, last uint16
	subtract    int
	divide      int
}{
	{4, 101, 103, 100, 0},
	{4, 141, 229, 100, 10},
	{10, 32769, 33767, 32768, 0},
}

// ch2Name quotes a channel name where it would otherwise be read as something
// else: a leading # or ; is a comment, a comma ends the field, and a quote
// inside a quoted name is written twice. Exactly what TVTest quotes.
func ch2Name(name string) string {
	if !strings.HasPrefix(name, "#") && !strings.HasPrefix(name, ";") &&
		!strings.ContainsAny(name, `,"`) {
		return name
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ch2Encoding renders the file the way TVTest reads one: code page 932 where
// the names fit in it, and UTF-16 with the mark where they do not -- an ARIB
// symbol has no code page 932 character, and service names carry them. Same
// choice TVTest makes when it saves.
func ch2Encoding(text string) ([]byte, error) {
	if encoded, ok := cp932(text); ok {
		return encoded, nil
	}
	return utf16(text)
}
