// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"bytes"
	"testing"
	"time"
)

func TestACDTCarriesALogo(t *testing.T) {
	body := []byte{0x7f, 0xe1, 0x01, 0xf0, 0x00}
	// logo_type 5, logo_id 0x123, version 7, four bytes.
	body = append(body, 0x05, 0xff, 0x23, 0xf0, 0x07, 0x00, 0x04, 0x89, 'P', 'N', 'G')
	table := ParseCommonData(long(TableIDCDT, 0x0001, body))
	if table == nil {
		t.Fatal("not read")
	}
	if table.OriginalNetworkID != 0x7fe1 || table.LogoType != 5 || table.LogoID != 0x123 || table.LogoVersion != 7 {
		t.Errorf("read %+v", table)
	}
	if !bytes.Equal(table.Data, []byte{0x89, 'P', 'N', 'G'}) {
		t.Errorf("data %x", table.Data)
	}
}

func TestASITNamesTheRecordedService(t *testing.T) {
	network := []byte{0xc2, 0x07, 'J', 'P', 'N', 0x00, 0x01, 0x7f, 0xe1}
	service := []byte{0x48, 0x07, 0x01, 0x00, 0x04, 0x0e, 'S', 'M', 'P'}
	body := []byte{0xf0, byte(len(network))}
	body = append(body, network...)
	body = append(body, 0x04, 0x00, 0x40, byte(len(service)))
	body = append(body, service...)
	table := ParseSelectionInformation(long(TableIDSIT, 0xffff, body))
	if table == nil || len(table.Descriptors) != 1 || len(table.Services) != 1 {
		t.Fatalf("read %+v", table)
	}
	got := table.Services[0]
	if got.ServiceID != 0x0400 || got.RunningStatus != 4 || got.Descriptors[0].Tag != DescriptorTagService {
		t.Errorf("service %+v", got)
	}
	named, ok := ParseServiceDescriptor(got.Descriptors[0].Body)
	if !ok || named.Name != "ＳＭＰ" {
		t.Errorf("named %+v", named)
	}
	identified, ok := ParseNetworkIdentification(table.Descriptors[0].Body)
	if !ok || identified.NetworkID != 0x7fe1 {
		t.Errorf("network %+v", identified)
	}
}

func TestAnSDTTAnnouncesSoftware(t *testing.T) {
	body := []byte{0x7f, 0xe1, 0x7f, 0xe1, 0x04, 0x00, 0x01}
	// Group 1, target 0x100, new 0x101, forced, all versions; 8 bytes
	// described, all of them the schedule.
	body = append(body, 0x11, 0x00, 0x10, 0x14, 0x00, 0x80, 0x00, 0x80)
	// From 2026-09-19 12:00:00 for an hour (MJD 61302 = 0xef66).
	body = append(body, 0xef, 0x66, 0x12, 0x00, 0x00, 0x01, 0x00, 0x00)
	table := ParseSoftwareDownload(long(TableIDSDTT, 0x1234, body))
	if table == nil || table.MakerID != 0x12 || table.ModelID != 0x34 || len(table.Contents) != 1 {
		t.Fatalf("read %+v", table)
	}
	content := table.Contents[0]
	if content.Group != 1 || content.TargetVersion != 0x100 || content.NewVersion != 0x101 || content.DownloadLevel != 1 {
		t.Errorf("content %+v", content)
	}
	if len(content.Schedules) != 1 || content.Schedules[0].Duration != time.Hour || content.Schedules[0].Start.Hour() != 12 {
		t.Errorf("schedules %+v", content.Schedules)
	}
}

func TestAnMHSDTTReadsItsDescriptorsAsMMTOnes(t *testing.T) {
	body := []byte{0x00, 0x01, 0x7f, 0xe1, 0x04, 0x00, 0x01}
	// 13 bytes described: 8 of schedule and a 5-byte descriptor (tag 0x8000,
	// length 2).
	body = append(body, 0x11, 0x00, 0x10, 0x14, 0x00, 0xd0, 0x00, 0x80)
	body = append(body, 0xef, 0x66, 0x12, 0x00, 0x00, 0x01, 0x00, 0x00)
	body = append(body, 0x80, 0x00, 0x02, 0xab, 0xcd)
	table := parseSoftwareDownloadTable(long(0x9e, 0x1234, body), 0x9e, true)
	if table == nil || len(table.Contents) != 1 {
		t.Fatalf("read %+v", table)
	}
	content := table.Contents[0]
	if len(content.Descriptors) != 0 || len(content.MHDescriptors) != 1 || content.MHDescriptors[0].Tag != 0x8000 ||
		!bytes.Equal(content.MHDescriptors[0].Body, []byte{0xab, 0xcd}) || len(content.Schedules) != 1 {
		t.Errorf("content %+v", content)
	}
}

func TestADITSaysWhetherTheStreamChanges(t *testing.T) {
	if changing, ok := ParseDiscontinuityInformation([]byte{TableIDDIT, 0x70, 0x01, 0x80}); !ok || !changing {
		t.Errorf("transition %v %v", changing, ok)
	}
	if _, ok := ParseDiscontinuityInformation([]byte{TableIDSIT, 0x70, 0x01, 0x80}); ok {
		t.Error("another table read as a DIT")
	}
}
