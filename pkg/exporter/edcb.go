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

// The two channel files EDCB keeps, which are tab-separated and have no heading:
//
//	ChSet4.txt  a tuner's channels, one line per service
//	  チャンネル名 サービス名 ネットワーク名 チューナ空間 物理チャンネル
//	  ONID TSID SID サービスタイプ 部分受信か 一覧表示に使用するか リモコンID
//
//	ChSet5.txt  every service EDCB knows, one line each, whichever tuner found it
//	  サービス名 ネットワーク名 ONID TSID SID サービスタイプ 部分受信か
//	  EPGデータ取得対象か デフォルト検索対象か
//
// They are one file per BonDriver and one file for the whole installation respectively,
// which is why two commands write them.
//
// EDCB names the first after the driver and the tuner, and falls back to one with an empty
// pair of brackets when it has no tuner name. A scan knows neither, so what is written is
// the stem the caller gave and the empty brackets.
//
// The 「チューナ空間」 and 「物理チャンネル」 columns are what EDCB passes to
// IBonDriver::SetChannel: the space and the index the driver enumerates, the same two
// numbers a TVTest channel file carries. See placement.go.

// WriteChSet4 writes the services of one delivery system as an EDCB tuner
// channel file. One delivery system because that is one channel definition
// file, which is one BonDriver: the space and channel numbers here are indexes
// into what that driver enumerates.
func WriteChSet4(w io.Writer, system string, results []report.Result) (skipped []string, err error) {
	spaces, skipped := place(system, results)
	if len(spaces) == 0 {
		return skipped, fmt.Errorf("no %s channels to write", SystemDisplayName(system))
	}

	var file strings.Builder
	written := 0
	for id, space := range spaces {
		for index, channel := range space.channels {
			for _, service := range channel.stream.services {
				if !televisionServiceType(service.Type) {
					continue
				}
				fmt.Fprintf(&file, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\r\n",
					chsetName(channel),
					serviceName(channel, service),
					channel.stream.networkName,
					id, index,
					channel.stream.networkID, channel.stream.streamID, service.ServiceID,
					service.Type,
					boolField(partialReception(service.Type)),
					boolField(true), // 一覧表示に使用するか
					remoteControlKeyID(channel.stream, service.ServiceID))
				written++
			}
		}
	}
	if written == 0 {
		return skipped, fmt.Errorf("no %s service was named; a tuner channel file is a list of services, and only a channel definition file can be written from carriers alone",
			SystemDisplayName(system))
	}
	_, err = w.Write(utf8WithBOM(file.String()))
	return skipped, err
}

// WriteChSet5 writes every service the scan found, whichever delivery system it
// arrived on, as EDCB's own channel list. One file for the whole scan rather
// than one per system: nothing in it says which tuner found what, so a service
// reached two ways is written once.
func WriteChSet5(w io.Writer, value report.Report) error {
	var file strings.Builder
	seen := make(map[[3]uint16]bool)
	for result := range foundIn(value.Results) {
		streams, _ := carriedStreams(result)
		for _, stream := range streams {
			for _, service := range stream.services {
				if !televisionServiceType(service.Type) {
					continue
				}
				// EDCB keys a service by these three, so writing it twice
				// would be writing over it with itself.
				key := [3]uint16{stream.networkID, stream.streamID, service.ServiceID}
				if stream.networkID == 0 || seen[key] {
					continue
				}
				seen[key] = true
				fmt.Fprintf(&file, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\r\n",
					serviceNameOf(result, stream, service),
					stream.networkName,
					stream.networkID, stream.streamID, service.ServiceID,
					service.Type,
					boolField(partialReception(service.Type)),
					boolField(true), // EPGデータ取得対象か
					boolField(true)) // デフォルト検索対象か
			}
		}
	}
	if len(seen) == 0 {
		return fmt.Errorf("the scan named no service; there is nothing to put in a channel list")
	}
	_, err := w.Write(utf8WithBOM(file.String()))
	return err
}

// serviceName is what a service is called, with something in place of a name
// the tables did not carry: a line with an empty name is a channel nobody can
// pick out of a list.
func serviceName(channel placedChannel, service isdb.ServiceInfo) string {
	if service.Name != "" {
		return service.Name
	}
	return fmt.Sprintf("%s:%d", channel.name, service.ServiceID)
}

func serviceNameOf(result report.Result, stream carriedStream, service isdb.ServiceInfo) string {
	if service.Name != "" {
		return service.Name
	}
	return fallbackName(result, service.ServiceID)
}

// partialReception says whether a service is the one-segment part of a
// multiplex, which EDCB keeps a column for. Always false in practice:
// televisionServiceType decides which services are exported at all and the
// one-segment type is not among them.
func partialReception(serviceType byte) bool {
	const oneSegment = 0xc0
	return serviceType == oneSegment
}

// remoteControlKeyID is the button a receiver puts a service on, for a field
// that holds one byte and means the identifier ARIB defines.
//
// Only what a broadcast actually said is written -- narrower than the TVTest
// channel file's 「リモコン番号」 column, which is happy to hold a three-digit service
// identifier. EDCB reads this column into one byte, so a number that is not a
// remote control key is worse than no number: 310 truncates to 54.
//
// So the column is empty for anything whose network table the scan did not
// capture, and a network table comes round every ten seconds at the outside. A
// scan meant to produce a channel list wants a longer --analysis-time.
func remoteControlKeyID(stream carriedStream, serviceID uint16) byte {
	if key := stream.remoteKeys[serviceID]; key != 0 {
		return key
	}
	return stream.remoteKeyID
}

// boolField is how EDCB writes a flag.
func boolField(set bool) int {
	if set {
		return 1
	}
	return 0
}
