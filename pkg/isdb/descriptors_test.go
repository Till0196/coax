// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"bytes"
	"reflect"
	"testing"
	"time"
)

func TestAnAudioComponentReadsTheSameFromBothTransmissions(t *testing.T) {
	// stream_content/type, tag, stream_type, simulcast, flags (main, 48 kHz),
	// language "jpn".
	ts := []byte{0x02, 0x03, 0x11, 0x0f, 0xff, 0x4e, 'j', 'p', 'n'}
	read, ok := ParseAudioComponent(ts)
	if !ok {
		t.Fatal("not read")
	}
	if read.Tag != 0x11 || read.Language != "jpn" || !read.Main || read.Simulcast != nil {
		t.Errorf("read %+v", read)
	}
	if hz, ok := read.SamplingHz(); !ok || hz != 48000 {
		t.Errorf("sampling %d %v", hz, ok)
	}
	if read.Channels() != 2 {
		t.Errorf("%d channels", read.Channels())
	}
	mh := []byte{0x02, 0x03, 0x00, 0x11, 0x0f, 0xff, 0x4e, 'j', 'p', 'n'}
	readMH, ok := ParseMHAudioComponent(mh)
	if !ok || readMH.Tag != 0x11 || readMH.Channels() != read.Channels() {
		t.Errorf("MMT read %+v", readMH)
	}
}

func TestADualMonoCommentaryIsStillDualMono(t *testing.T) {
	ts := []byte{0x02, 0x22, 0x11, 0x0f, 0xff, 0xc0, 'j', 'p', 'n', 'e', 'n', 'g'}
	read, ok := ParseAudioComponent(ts)
	if !ok || !read.DualMono() || read.Assist() != AssistVisual || read.Second != "eng" {
		t.Errorf("read %+v", read)
	}
}

func TestTheTwentyTwoPointTwoSoundCountsItsChannels(t *testing.T) {
	if got := (AudioComponent{Kind: 0x11}).Channels(); got != 24 {
		t.Errorf("22.2 has %d channels", got)
	}
	if got := (AudioComponent{Kind: 0x10}).Channels(); got != 12 {
		t.Errorf("0x10 has %d channels", got)
	}
}

func TestAMarkIsNotAName(t *testing.T) {
	marked := AudioComponent{Name: "［Ｓ］"}
	if marked.Called() != "" {
		t.Errorf("a mark was taken for a name: %q", marked.Called())
	}
	marked.Enrich(AudioComponent{Kind: 0x23, Name: "日本語（解説）"})
	if marked.Called() != "日本語（解説）" {
		t.Errorf("not enriched: %+v", marked)
	}
}

func TestTheHierarchyLevelsAgreeBetweenTransmissions(t *testing.T) {
	read, ok := ParseHierarchicalTransmission([]byte{0x01, 0x01, 0x11})
	if !ok || read.Level != 0 || read.Reference == nil || *read.Reference != 0x0111 || read.Group != nil {
		t.Errorf("read %+v", read)
	}
	read, ok = ParseHierarchicalTransmission([]byte{0x00, 0x1f, 0xff})
	if !ok || read.Level != 1 || read.Reference != nil {
		t.Errorf("stand-in read %+v", read)
	}
	group, ok := ParseAssetGroup([]byte{0x01, 0x00})
	if !ok || group.Level != 0 || group.Group == nil || *group.Group != 1 {
		t.Errorf("asset group %+v", group)
	}
}

func TestEventsReadFromBothTransmissions(t *testing.T) {
	// The eight-unit code: two ASCII bytes are one kanji.
	ts := []byte{'j', 'p', 'n', 2, 0x46, 0x7c, 2, 0x4b, 0x5c}
	read, ok := ParseShortEvent(ts)
	if !ok || read.Title != "日" || read.Text != "本" || read.Language != "jpn" {
		t.Errorf("read %+v", read)
	}
	ts[3] = 9
	if _, ok := ParseShortEvent(ts); ok {
		t.Error("a title running past the descriptor was read")
	}
	mh, ok := ParseMHShortEvent([]byte{'j', 'p', 'n', 2, 'A', 'B', 0, 1, 'C'})
	if !ok || mh.Title != "AB" || mh.Text != "C" {
		t.Errorf("MMT read %+v", mh)
	}
	genres := ParseContentDescriptor([]byte{0x12, 0x00, 0x34, 0xff, 0xe1, 0x10})
	want := []ContentGenre{{1, 2, 0x00}, {3, 4, 0xff}, {0xe, 1, 0x10}}
	if !reflect.DeepEqual(genres, want) {
		t.Errorf("genres %+v", genres)
	}
	group, ok := ParseEventGroup([]byte{0x12, 0x04, 0x00, 0x00, 0x10, 0x04, 0x01, 0x00, 0x11})
	wantEvents := []EventReference{{0x0400, 0x0010}, {0x0401, 0x0011}}
	if !ok || group.Type != EventGroupCommon || !reflect.DeepEqual(group.Events, wantEvents) {
		t.Errorf("event group %+v", group)
	}
}

func TestAnExtendedEventItemIsJoinedBeforeItIsDecoded(t *testing.T) {
	// "日本" cut between its two characters: the second descriptor's item has
	// no heading and continues the first.
	first := []byte{0x00, 'j', 'p', 'n', 6, 1, 0x30, 3, 0x46, 0x7c, 0x4b}
	second := []byte{0x10, 'j', 'p', 'n', 3, 0, 1, 0x5c}
	items := JoinExtendedEvents([][]byte{first, second})
	if len(items) != 1 || items[0].Value != "日本" {
		t.Errorf("items %+v", items)
	}
	mh := ParseMHExtendedEvent([]byte{0, 'j', 'p', 'n', 0, 6, 1, 'H', 0, 2, 'V', 'W'})
	if len(mh) != 1 || mh[0].Heading != "H" || mh[0].Value != "VW" {
		t.Errorf("MMT items %+v", mh)
	}
}

func TestAnApplicationServiceReadsItsLocations(t *testing.T) {
	body := []byte{0x00, 0x10, 0x42, 0x00, 0x1f, 0x00, 0x00, 0x1f, 0x01, 0x07, 0x00, 0x1f, 0x02}
	read, ok := ParseApplicationService(body)
	if !ok || read.Resolution != 1 || read.AIT != 0x1f00 || read.Data == nil || *read.Data != 0x1f01 {
		t.Fatalf("read %+v", read)
	}
	if tag, ok := read.EMTTag(0x1f02); !ok || tag != 7 {
		t.Errorf("EMT %+v", read.EMTs)
	}
}

func TestAParentalRatingIsAnAge(t *testing.T) {
	read := ParseParentalRating([]byte("JPN\x04"))
	if len(read) != 1 || read[0].Country != "JPN" {
		t.Fatalf("read %+v", read)
	}
	if age, ok := RatingAge(read[0].Rating); !ok || age != 7 {
		t.Errorf("age %d", age)
	}
	if _, ok := RatingAge(0); ok {
		t.Error("nought is an age")
	}
	if _, ok := RatingAge(0x10); ok {
		t.Error("the broadcaster's own value is an age")
	}
}

func TestCopyControlIsReadForTheWholeAndEachComponent(t *testing.T) {
	read, ok := ParseDigitalCopyControl([]byte{0xa0 | 0x10, 0x1c, 0x05, 0x10, 0xc0, 0x11, 0x20, 0x08})
	if !ok || read.Control != 2 || read.MaximumBitrate == nil || *read.MaximumBitrate != 0x1c || len(read.Components) != 2 {
		t.Fatalf("read %+v", read)
	}
	if read.Components[0].Tag != 0x10 || read.Components[0].Control != 3 {
		t.Errorf("first component %+v", read.Components[0])
	}
	if read.Components[1].MaximumBitrate == nil || *read.Components[1].MaximumBitrate != 8 {
		t.Errorf("second component %+v", read.Components[1])
	}
	mh, ok := ParseMHContentCopyControl([]byte{0x80 | 0x10, 0x00, 0x04, 0x00, 0x10, 0xc0, 0x00})
	if !ok || mh.Control != 2 || len(mh.Components) != 1 || mh.Components[0].Tag != 0x0010 || mh.Components[0].Control != 3 {
		t.Errorf("MMT read %+v", mh)
	}
	if CopyControlName(2) != "一世代のみコピー可" {
		t.Error(CopyControlName(2))
	}
}

func TestContentAvailabilitySaysHowLongItMayBeKept(t *testing.T) {
	read, ok := ParseContentAvailability([]byte{0x40 | 0x20 | 0x0c})
	if !ok || !read.CopyRestriction || !read.ImageUnconstrained || read.NoRetention || read.RetentionState != 6 {
		t.Fatalf("read %+v", read)
	}
	if read.RetentionName() != "1時間30分" {
		t.Error(read.RetentionName())
	}
	mh, ok := ParseMHContentUsageControl([]byte{0x80, 0x0e})
	if !ok || mh.RemoteView == nil || !*mh.RemoteView || mh.RetentionName() != "予備" {
		t.Errorf("MMT read %+v", mh)
	}
}

func TestASeriesCountsItsEpisodes(t *testing.T) {
	body := []byte{0x12, 0x34, 0x02 | 0x01, 0xe5, 0x2c, 0x00, 0x51, 0x0c, 0x46, 0x7c}
	read, ok := ParseSeries(body)
	if !ok || read.ID != 0x1234 || read.Pattern != 1 || read.PatternName() != "帯番組" {
		t.Fatalf("read %+v", read)
	}
	if read.ExpireMJD == nil || *read.ExpireMJD != 0xe52c || read.Episode != 5 || read.LastEpisode != 268 || read.Name != "日" {
		t.Errorf("read %+v", read)
	}
}

func TestComponentGroupsAreReadWithTheirNames(t *testing.T) {
	body := []byte{0x10 | 0x02, 0x01, 0x12, 0x00, 0x10, 0x20, 0x00, 0x11, 0x01, 0x11, 0x00, 0x00}
	read, ok := ParseComponentGroup(body)
	if !ok || read.GroupType != 0 || len(read.Groups) != 2 {
		t.Fatalf("read %+v", read)
	}
	if !reflect.DeepEqual(read.Groups[0].Units[0].Components, []uint16{0x00, 0x10}) || read.Groups[0].TotalBitrate == nil || *read.Groups[0].TotalBitrate != 0x20 {
		t.Errorf("first group %+v", read.Groups[0])
	}
	if read.Groups[1].ID != 1 || !reflect.DeepEqual(read.Groups[1].Units[0].Components, []uint16{0x11}) {
		t.Errorf("second group %+v", read.Groups[1])
	}
}

func TestALogoIsDownloadedReferencedOrSpelled(t *testing.T) {
	read, ok := ParseLogoTransmission([]byte{0x01, 0xfe, 0x21, 0xf0, 0x02, 0x12, 0x34})
	if !ok || read.Type != LogoDownloaded || read.LogoID != 0x21 || read.Version != 2 || read.DownloadDataID != 0x1234 {
		t.Errorf("downloaded %+v", read)
	}
	read, ok = ParseLogoTransmission([]byte{0x02, 0x01, 0x05})
	if !ok || read.Type != LogoReferenced || read.LogoID != 0x105 {
		t.Errorf("referenced %+v", read)
	}
	read, ok = ParseLogoTransmission([]byte{0x03, 0x0e, 'S', 'M', 'P'})
	if !ok || read.Type != LogoText || read.Text != "ＳＭＰ" {
		t.Errorf("spelled %+v", read)
	}
}

func TestBroadcastersNameTheirAffiliations(t *testing.T) {
	read, ok := ParseExtendedBroadcaster([]byte{0x10, 0x00, 0x2a, 0x21, 0x05, 0x50, 0x01, 0x03, 0x00})
	if !ok || read.Kind != 1 || read.TerrestrialBroadcasterID != 0x2a || !bytes.Equal(read.Affiliations, []byte{0x05, 0x50}) ||
		!reflect.DeepEqual(read.Related, []BroadcasterReference{{0x0103, 0}}) {
		t.Errorf("extended %+v", read)
	}
	related, ok := ParseRelatedBroadcaster([]byte{0x11, 0x10, 0x00, 0x04, 0x02, 0x05, 0x7f, 0xe1})
	if !ok || !reflect.DeepEqual(related.Broadcasters, []BroadcasterReference{{4, 2}}) ||
		!bytes.Equal(related.Affiliations, []byte{5}) || !reflect.DeepEqual(related.OriginalNetworks, []uint16{0x7fe1}) {
		t.Errorf("related %+v", related)
	}
}

func TestACAContractNamesItsFee(t *testing.T) {
	read, ok := ParseCAContractInfo([]byte{0x00, 0x05, 0x12, 0x10, 0x11, 0x02, 0xaa, 0xbb, 0x02, 0x46, 0x7c})
	if !ok || read.CASystemID != 5 || read.Unit != 1 || !reflect.DeepEqual(read.Components, []uint16{0x10, 0x11}) ||
		!bytes.Equal(read.Verification, []byte{0xaa, 0xbb}) || read.FeeName != "日" {
		t.Errorf("contract %+v", read)
	}
	emm, ok := ParseCAEMMTS([]byte{0x00, 0x05, 0x40, 0x10, 0x00, 0x04, 0x1e})
	if !ok || emm.TransportStreamID != 0x4010 || emm.PowerSupplyPeriod != 30 {
		t.Errorf("CA_EMM_TS %+v", emm)
	}
}

func TestSIParametersAreKeptPerTable(t *testing.T) {
	read, ok := ParseSIParameter([]byte{0x01, 0xe5, 0x2c, 0x4e, 0x02, 0x00, 0x0a, 0x50, 0x01, 0x03})
	want := []SIParameterTable{{0x4e, []byte{0x00, 0x0a}}, {0x50, []byte{0x03}}}
	if !ok || read.Version != 1 || read.UpdateMJD != 0xe52c || !reflect.DeepEqual(read.Tables, want) {
		t.Errorf("read %+v", read)
	}
}

func TestATargetRegionNamesItsPrefectures(t *testing.T) {
	read, ok := ParseTargetRegion([]byte{0x01, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02})
	if !ok || !bytes.Equal(read.Prefectures, []byte{1, 55}) {
		t.Fatalf("read %+v", read)
	}
	if name, ok := PrefectureName(14); !ok || name != "東京（島部を除く）" {
		t.Errorf("14 is %q", name)
	}
	if _, ok := PrefectureName(51); ok {
		t.Error("51 has a name")
	}
}

func TestAHyperlinkPointsAtAnEvent(t *testing.T) {
	body := []byte{0x02, LinkEvent, 8, 0x00, 0x04, 0x40, 0x10, 0x01, 0x01, 0x12, 0x34, 0xab}
	read, ok := ParseHyperlink(body)
	if !ok || read.NetworkID != 4 || read.TransportStreamID != 0x4010 || read.ServiceID != 0x0101 || read.EventID != 0x1234 ||
		!bytes.Equal(read.PrivateData, []byte{0xab}) {
		t.Errorf("read %+v", read)
	}
	if _, ok := ParseHyperlink([]byte{0x02, LinkEvent, 4, 0, 4, 0x40, 0x10}); ok {
		t.Error("a short selector was read")
	}
}

func TestPartialStreamDescriptorsAreRead(t *testing.T) {
	network, ok := ParseNetworkIdentification([]byte{'J', 'P', 'N', 0x00, 0x01, 0x7f, 0xe1})
	if !ok || network.Country != "JPN" || network.MediaType != 1 || network.NetworkID != 0x7fe1 {
		t.Errorf("network %+v", network)
	}
	// Event version 3, start 2026-09-19 12:00:00 (MJD 0xef66), one hour, ten
	// minutes in, the wall clock given.
	body := []byte{0x03, 0xef, 0x66, 0x12, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x10, 0x00, 0x03, 0xef, 0x66, 0x12, 0x10, 0x00}
	read, ok := ParsePartialTSTime(body)
	if !ok || read.EventVersion != 3 || read.Duration == nil || *read.Duration != time.Hour || read.Offset == nil || *read.Offset != 10*time.Minute {
		t.Fatalf("read %+v", read)
	}
	if read.EventStart == nil || read.EventStart.Hour() != 12 || read.JSTTime == nil || read.JSTTime.Minute() != 10 || !read.OthersValid {
		t.Errorf("read %+v", read)
	}
	rate, ok := ParsePartialTS([]byte{0xc0, 0x10, 0x00, 0xc0, 0x08, 0x00, 0xc0, 0x40})
	if !ok || rate.PeakRate != 0x1000 || rate.MinimumSmoothingRate != 0x0800 || rate.MaximumSmoothingBuffer != 0x40 {
		t.Errorf("rate %+v", rate)
	}
}

func TestAnnouncementTextIsEUCJP(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{0xa4, 0xb3, 0xa4, 0xb3, 0xa4, 0xcf, 0xc9, 0xbd, 0xbc, 0xa8, 0x0d, 0x0a, 0x30, 0x31}, "ここは表示\r\n01"},
		{[]byte("000276"), "000276"},
		{[]byte("SharpOAD.bin"), "SharpOAD.bin"},
		// Seven-bit kanji, as one maker's notices come.
		{[]byte("%=%U%H%&%'%\"!#"), "ソフトウェア。"},
		// A UTF-8 notice.
		{[]byte("ソフトウェア更新\r\n　お知らせ"), "ソフトウェア更新\r\n　お知らせ"},
	}
	for _, c := range cases {
		if got := downloadText(c.in); got != c.want {
			t.Errorf("%q read as %q, want %q", c.in, got, c.want)
		}
	}
}

func TestADownloadContentDescriptorCarriesItsText(t *testing.T) {
	body := []byte{
		0x88,                   // reboot, text
		0x00, 0x00, 0x10, 0x00, // component size
		0x00, 0x00, 0x00, 0x01, // download ID
		0x00, 0x00, 0x00, 0x05, // DII time-out
		0xc0, 0x00, 0x10, // leak rate
		0x40, // component tag
		0x00, // no private data
		'j', 'p', 'n', 2, 0xa4, 0xb3,
	}
	read, ok := ParseDownloadContent(body)
	if !ok || !read.Reboot || read.ComponentSize != 0x1000 || read.LeakRate != 0x10 || read.ComponentTag != 0x40 || read.Language != "jpn" || read.Text != "こ" {
		t.Errorf("read %+v", read)
	}
}

func TestSymbolsAreWrittenAsTheBroadcastsWords(t *testing.T) {
	if got := SubstituteSymbols("\U0001F201です"); got != "[ココ]です" {
		t.Errorf("got %q", got)
	}
	if got := SubstituteSymbols("SAMPLE"); got != "SAMPLE" {
		t.Errorf("got %q", got)
	}
}

func TestComponentTagsAreFoundInEitherDescriptor(t *testing.T) {
	if tag, ok := ComponentTag([]Descriptor{{Tag: DescriptorTagAudioComponent, Body: []byte{0x02, 0x03, 0x11}}}); !ok || tag != 0x11 {
		t.Errorf("tag %#x", tag)
	}
	if tag, ok := MHComponentTag([]MMTDescriptor{{Tag: MHDescriptorTagStreamIdentifier, Body: []byte{0x00, 0x20}}}); !ok || tag != 0x20 {
		t.Errorf("MMT tag %#x", tag)
	}
}
