// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/tuner"
)

func TestScanUsesReceiverInterface(t *testing.T) {
	data := make([]byte, 188*8)
	for offset := 0; offset < len(data); offset += 188 {
		data[offset] = 0x47
	}
	device := &fakeDevice{stream: &fakeStream{data: data}}
	report, err := Scan(context.Background(), []channelconfig.Entry{{
		Name: "test", Route: "TER",
		Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "473142857"},
	}}, Options{
		Device: device, LockTimeout: time.Second, DataTimeout: time.Second,
		AnalysisTime: time.Nanosecond, PollInterval: time.Millisecond,
		MinimumBytes: len(data), MaximumBytes: len(data), RequireData: true,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].Status != "found" || report.Results[0].StreamFormat != "ts" {
		t.Fatalf("unexpected report: %#v", report)
	}
	// The tuner is named although it was not known when the report was made:
	// the daemon settles which one to use at the first tune.
	if report.Backend != "test" || report.Device != "fake0" || !device.closed {
		t.Fatalf("receiver metadata or cleanup missing: %#v, closed=%t", report, device.closed)
	}
}

// A scan that tunes nothing has no tuner to name, and says so rather than
// naming one it did not use.
func TestAScanThatTunesNothingNamesNoTuner(t *testing.T) {
	device := &fakeDevice{stream: &fakeStream{}}
	report, err := Scan(context.Background(), nil, Options{
		Device: device, LockTimeout: time.Second, DataTimeout: time.Second,
		AnalysisTime: time.Nanosecond, PollInterval: time.Millisecond,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if report.Backend != "coaxd" || report.Device != "coaxd" {
		t.Errorf("named %s/%s, want the daemon", report.Backend, report.Device)
	}
}

type fakeDevice struct {
	stream *fakeStream
	closed bool
	tuned  bool
	// bounds is what the last stream was asked for, so a test can check that
	// the scanner told the device how much of it it wanted.
	bounds tuner.Bounds
}

// RelativeStreams is zero: this tuner takes a stream identifier, as nearly
// every real one does.
func (device *fakeDevice) RelativeStreams() int { return 0 }

// Backend and Name answer the way the real client does: until something has
// been tuned there is no tuner, only the daemon.
func (device *fakeDevice) Backend() string {
	if !device.tuned {
		return "coaxd"
	}
	return "test"
}

func (device *fakeDevice) Name() string {
	if !device.tuned {
		return "coaxd"
	}
	return "fake0"
}

func (device *fakeDevice) Supports(string) (bool, error) { return true, nil }

func (device *fakeDevice) Tune(map[string]string) error {
	device.tuned = true
	return nil
}
func (device *fakeDevice) OpenStream(bounds tuner.Bounds) (tuner.Stream, error) {
	device.bounds = bounds
	return device.stream, nil
}
func (device *fakeDevice) Status() (tuner.Status, error) {
	return tuner.Status{Locked: true, Names: []string{"lock"}}, nil
}
func (device *fakeDevice) Close() error { device.closed = true; return nil }

type fakeStream struct {
	data []byte
}

func (stream *fakeStream) Read(_ context.Context, buffer []byte) (int, error) {
	if len(stream.data) == 0 {
		return 0, tuner.ErrWouldBlock
	}
	read := copy(buffer, stream.data)
	stream.data = stream.data[read:]
	return read, nil
}

func (stream *fakeStream) Close() error { return nil }

// chunkedStream hands out a fixed number of chunks, one per Read, and records
// how long the caller took to collect them all.
type chunkedStream struct {
	chunk     []byte
	remaining int
}

func (stream *chunkedStream) Read(_ context.Context, buffer []byte) (int, error) {
	if stream.remaining == 0 {
		return 0, tuner.ErrWouldBlock
	}
	stream.remaining--
	return copy(buffer, stream.chunk), nil
}

func (stream *chunkedStream) Close() error { return nil }

// The poll interval exists to pace a device with nothing to say. Applying it
// after a read that returned data would cap the capture at one buffer per
// interval, and the tables naming the services repeat too slowly to survive
// that. The loss would be silent: the scan still reports a lock, just with
// nothing identified in it.
func TestReceiveSampleDoesNotWaitBetweenSuccessfulReads(t *testing.T) {
	const chunks = 20
	const pollInterval = 50 * time.Millisecond
	stream := &chunkedStream{chunk: make([]byte, 32*1024), remaining: chunks}
	options := Options{
		Device:       &fakeDevice{stream: nil},
		DataTimeout:  time.Second,
		AnalysisTime: 50 * time.Millisecond,
		PollInterval: pollInterval,
		MinimumBytes: 188 * 16,
		MaximumBytes: chunks * 32 * 1024,
	}
	started := time.Now()
	sample, _, err := receiveSampleFrom(context.Background(), options, analysisSettled, stream, nil)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("receiveSample: %v", err)
	}
	if len(sample) != chunks*32*1024 {
		t.Fatalf("collected %d bytes, want %d", len(sample), chunks*32*1024)
	}
	// Waiting once per chunk would take at least chunks * pollInterval.
	if budget := chunks * pollInterval / 2; elapsed > budget {
		t.Fatalf("collecting %d chunks took %v, which is the throttled path; a poll interval of %v should not apply between reads that returned data",
			chunks, elapsed, pollInterval)
	}
}

// The other half: an idle device must not be polled in a busy loop.
func TestReceiveSampleWaitsWhenThereIsNothingToRead(t *testing.T) {
	stream := &chunkedStream{chunk: make([]byte, 188*32), remaining: 1}
	options := Options{
		Device:       &fakeDevice{stream: nil},
		DataTimeout:  200 * time.Millisecond,
		AnalysisTime: 300 * time.Millisecond,
		PollInterval: 40 * time.Millisecond,
		MinimumBytes: 188 * 16,
		MaximumBytes: 1 << 20,
	}
	started := time.Now()
	if _, _, err := receiveSampleFrom(context.Background(), options, analysisSettled, stream, nil); err != nil {
		t.Fatalf("receiveSample: %v", err)
	}
	// One chunk arrives at once; the rest of the analysis window is idle and
	// has to be spent waiting rather than spinning.
	if elapsed := time.Since(started); elapsed < 250*time.Millisecond {
		t.Fatalf("the analysis window ended after %v, want it to run its course", elapsed)
	}
}

// The progress line's rule: a backend that cannot measure contributes nothing,
// and a measured zero is still a measurement. Printing "0.0dB" for a frontend
// that never answered would be worse than printing nothing.
func TestStatusDetailShowsOnlyWhatWasMeasured(t *testing.T) {
	for _, test := range []struct {
		name   string
		status tuner.Status
		want   string
	}{
		{
			name:   "a backend with no measurements looks as it always did",
			status: tuner.Status{Names: []string{"SIGNAL", "CARRIER"}},
			want:   "SIGNAL,CARRIER",
		},
		{
			name: "both measurements follow the status names",
			status: tuner.Status{
				Names:    []string{"LOCK"},
				CNR:      tuner.Measured(28.5),
				Strength: tuner.Measured(-42),
			},
			want: "LOCK CNR 28.5dB -42dBm",
		},
		{
			name: "a locked frontend says so once, leaving the room to the readings",
			status: tuner.Status{
				Locked:   true,
				Names:    []string{"SIGNAL", "CARRIER", "VITERBI", "SYNC", "LOCK"},
				CNR:      tuner.Measured(30.8),
				Strength: tuner.Measured(-32),
			},
			want: "LOCK CNR 30.8dB -32dBm",
		},
		{
			name:   "the rungs are kept while the top is out of reach",
			status: tuner.Status{Names: []string{"SIGNAL", "CARRIER", "VITERBI"}},
			want:   "SIGNAL,CARRIER,VITERBI",
		},
		{
			name:   "a backend that spells it its own way is quoted, not corrected",
			status: tuner.Status{Locked: true, Names: []string{"signal", "lock"}},
			want:   "lock",
		},
		{
			name:   "a measured zero is reported, not hidden",
			status: tuner.Status{CNR: tuner.Measured(0)},
			want:   "CNR 0.0dB",
		},
		{
			name:   "an unmeasured zero contributes nothing",
			status: tuner.Status{CNR: tuner.Measurement{Value: 0}},
			want:   "",
		},
		{
			name:   "the BDA quality figure is shown as a percentage",
			status: tuner.Status{Quality: tuner.Measured(82)},
			want:   "quality 82%",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := statusDetail(test.status); got != test.want {
				t.Errorf("statusDetail() = %q, want %q", got, test.want)
			}
		})
	}
}

// A stream with no end can only be stopped by hanging up on it, and hanging up
// costs the tuner: the daemon gives one back when the connection that took it
// goes. Saying how much is wanted lets the stream end by itself.
func TestASampleTellsTheDeviceHowMuchOfTheStreamItWants(t *testing.T) {
	device := &fakeDevice{stream: &fakeStream{data: make([]byte, 64*1024)}}
	options := Options{
		Device:       device,
		DataTimeout:  time.Second,
		AnalysisTime: 250 * time.Millisecond,
		PollInterval: 10 * time.Millisecond,
		MinimumBytes: 188 * 16,
		MaximumBytes: 64 * 1024,
	}
	if _, _, err := receiveSample(context.Background(), options, nil); err != nil {
		t.Fatalf("receiveSample: %v", err)
	}
	if device.bounds.Bytes != options.MaximumBytes {
		t.Errorf("asked for %d bytes, want the %d it keeps", device.bounds.Bytes, options.MaximumBytes)
	}
	if device.bounds.For != options.AnalysisTime {
		t.Errorf("asked for %v of it, want the %v it gathers for", device.bounds.For, options.AnalysisTime)
	}
	if device.bounds.Wait != options.DataTimeout {
		t.Errorf("startup wait = %v, want %v", device.bounds.Wait, options.DataTimeout)
	}
}

// endingStream is a stream that ends, the way a bounded one does: a fixed
// number of chunks and then the end of the response.
type endingStream struct {
	chunk     []byte
	remaining int
}

func (stream *endingStream) Read(_ context.Context, buffer []byte) (int, error) {
	if stream.remaining == 0 {
		return 0, io.EOF
	}
	stream.remaining--
	return copy(buffer, stream.chunk), nil
}

func (stream *endingStream) Close() error { return nil }

// The other side of a bounded stream: the end of the response is the end of the
// sample, not a failure.
func TestASampleEndsWhenTheStreamDoes(t *testing.T) {
	options := Options{
		DataTimeout:  time.Second,
		AnalysisTime: time.Second,
		PollInterval: 10 * time.Millisecond,
		MinimumBytes: 188 * 16,
		MaximumBytes: 1024 * 1024,
	}
	stream := &endingStream{chunk: make([]byte, 32*1024), remaining: 2}
	sample, _, err := receiveSampleFrom(context.Background(), options, analysisSettled, stream, nil)
	if err != nil {
		t.Fatalf("receiveSample: %v", err)
	}
	if len(sample) != 64*1024 {
		t.Errorf("kept %d bytes, want the 65536 the stream carried", len(sample))
	}
}

// countingSignal answers a fixed measurement and counts how often it is asked,
// so that the throttle can be tested without waiting a second for it.
type countingSignal struct {
	fakeDevice
	reads  int
	cnr    float64
	broken bool
}

func (device *countingSignal) Status() (tuner.Status, error) {
	device.reads++
	if device.broken {
		return tuner.Status{}, errors.New("the tuner would not answer")
	}
	return tuner.Status{Locked: true, CNR: tuner.Measured(device.cnr)}, nil
}

// TestTheSignalIsReadWhileSamplingButNotOnEveryPass. The sampling loop turns
// over many times a second and a status read is a request of its own.
func TestTheSignalIsReadWhileSamplingButNotOnEveryPass(t *testing.T) {
	device := &countingSignal{cnr: 18.9}
	watch := &signalWatch{device: device}

	if got := watch.measurements(); got != "CNR 18.9dB" {
		t.Fatalf("first reading = %q", got)
	}
	for range 100 {
		watch.measurements()
	}
	if device.reads != 1 {
		t.Errorf("the status was read %d times, want once", device.reads)
	}

	// A second on, it is read again, and the figure that has climbed shows.
	watch.read = time.Now().Add(-signalInterval)
	device.cnr = 24.2
	if got := watch.measurements(); got != "CNR 24.2dB" {
		t.Errorf("second reading = %q, want the newer figure", got)
	}
}

// TestAFailedSignalReadKeepsTheLastFigure: the last figure is better than
// none, and a status read can fail for reasons the channel does not share.
func TestAFailedSignalReadKeepsTheLastFigure(t *testing.T) {
	device := &countingSignal{cnr: 12.5}
	watch := &signalWatch{device: device}
	watch.measurements()

	device.broken = true
	watch.read = time.Now().Add(-signalInterval)
	if got := watch.measurements(); got != "CNR 12.5dB" {
		t.Errorf("reading after a failure = %q, want the one before it", got)
	}
}

// TestFoundChannelsNameTheirServices is what a scan is run to find out, and
// what a line saying only "found (ts, 10034688 bytes)" leaves out.
func TestFoundChannelsNameTheirServices(t *testing.T) {
	for _, one := range []struct {
		name     string
		analysis *isdb.Analysis
		want     []string
	}{
		{
			name: "the network heads the services it carries",
			analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
				TransportStreamID: 16432, OriginalNetworkID: 4, NetworkID: 4,
				Networks: []isdb.NetworkInfo{{NetworkID: 4, Actual: true, Name: "ＢＳデジタル"}},
				Services: []isdb.ServiceInfo{
					{ServiceID: 1024, Name: "サンプルテレビ", Provider: "サンプル放送", Type: 0x01},
					{ServiceID: 1025, Name: "テストチャンネル１", Type: 0x01, FreeCAMode: true},
				},
			}},
			want: []string{
				streamIndent + "ＢＳデジタル  TSID=16432  ONID=4",
				serviceIndent + "1024  サンプルテレビ  サンプル放送  (TV)",
				serviceIndent + "1025  テストチャンネル１  (TV, scrambled)",
			},
		},
		{
			name: "a stream that named no network is headed by its identifiers alone",
			analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
				TransportStreamID: 31876, OriginalNetworkID: 31876,
				Services: []isdb.ServiceInfo{{ServiceID: 1032}},
			}},
			want: []string{streamIndent + "TSID=31876  ONID=31876", serviceIndent + "1032  (unnamed)"},
		},
		{
			name: "a TLV stream is headed by the stream identifier it is selected with",
			analysis: &isdb.Analysis{TLV: &isdb.TLVInfo{
				NetworkID: 11, TLVStreamID: 45168, OriginalNetworkID: 11,
				Networks: []isdb.TLVNetworkInfo{{NetworkID: 11, Name: "高度BSデジタル放送"}},
				Services: []isdb.ServiceInfo{{ServiceID: 4096, Name: "テスト　衛星４Ｋ", Provider: "サンプル放送", Type: 0x01, FreeCAMode: true}},
			}},
			want: []string{
				streamIndent + "高度BSデジタル放送  TLVSID=45168  ONID=11",
				serviceIndent + "4096  テスト　衛星４Ｋ  サンプル放送  (TV, scrambled)",
			},
		},
		{
			// Each slot heads the stream it holds, and that stream heads its
			// own services: two slots of one carrier belong to different
			// networks and carry their own identifiers.
			name: "a multiplexing frame is listed slot by slot",
			analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
				RelativeStreams: []isdb.RelativeStreamInfo{
					{RelativeStream: 1, Format: "ts", StreamID: 31876, OriginalNetworkID: 31876,
						Transport: &isdb.TransportInfo{
							TransportStreamID: 31876, OriginalNetworkID: 31876,
							Services: []isdb.ServiceInfo{{ServiceID: 1024, Name: "サンプルテレビ", Provider: "サンプル放送"}},
						}},
					{RelativeStream: 2, Format: "ts", StreamID: 31877, OriginalNetworkID: 4,
						Transport: &isdb.TransportInfo{
							TransportStreamID: 31877, OriginalNetworkID: 4,
							Networks: []isdb.NetworkInfo{{NetworkID: 4, Actual: true, Name: "BS Digital"}},
							Services: []isdb.ServiceInfo{{ServiceID: 2048, Name: "サンプル有料"}},
						}},
					{RelativeStream: 3, Format: "ts", StreamID: 31878, OriginalNetworkID: 4},
				},
			}},
			want: []string{
				frameIndent + "TSMF [#1 ts TSID=31876 ONID=31876]",
				streamIndent + "TSID=31876  ONID=31876",
				serviceIndent + "1024  サンプルテレビ  サンプル放送",
				frameIndent + "TSMF [#2 ts TSID=31877 ONID=4]",
				streamIndent + "BS Digital  TSID=31877  ONID=4",
				serviceIndent + "2048  サンプル有料",
				frameIndent + "TSMF [#3 ts TSID=31878 ONID=4]",
			},
		},
		{
			// A slot whose services were not read still says which stream it
			// holds, so it keeps the shape of the slots around it.
			name: "a slot with no services is written out like the others",
			analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
				RelativeStreams: []isdb.RelativeStreamInfo{
					{RelativeStream: 1, Format: "ts", StreamID: 31876, OriginalNetworkID: 4,
						Transport: &isdb.TransportInfo{
							TransportStreamID: 31876, OriginalNetworkID: 4,
							Networks: []isdb.NetworkInfo{{NetworkID: 4, Actual: true, Name: "BS Digital"}},
						}},
				},
			}},
			want: []string{
				frameIndent + "TSMF [#1 ts TSID=31876 ONID=4]",
				streamIndent + "BS Digital  TSID=31876  ONID=4",
			},
		},
		{
			// A carrier with one slot is as much a slot as a carrier with
			// four: the analysis keeps the stream there and nothing at the
			// top, so a reader that looked at the top saw no services at all.
			name: "one slot is a slot like any other",
			analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
				RelativeStreams: []isdb.RelativeStreamInfo{
					{RelativeStream: 1, Format: "ts", StreamID: 31793, OriginalNetworkID: 31793,
						Transport: &isdb.TransportInfo{
							TransportStreamID: 31793, OriginalNetworkID: 31793,
							Services: []isdb.ServiceInfo{{ServiceID: 1024, Name: "サンプルテレビ", Type: 0x01}},
						}},
				},
			}},
			want: []string{
				frameIndent + "TSMF [#1 ts TSID=31793 ONID=31793]",
				streamIndent + "TSID=31793  ONID=31793",
				serviceIndent + "1024  サンプルテレビ  (TV)",
			},
		},
		{
			name: "a slot that carries TLV names what the frame holds",
			analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
				RelativeStreams: []isdb.RelativeStreamInfo{
					{RelativeStream: 1, Format: "tlv", StreamID: 45168, OriginalNetworkID: 11,
						TLV: &isdb.TLVInfo{
							TLVStreamID: 45168, OriginalNetworkID: 11,
							Services: []isdb.ServiceInfo{{ServiceID: 4096, Name: "テスト　衛星４Ｋ", Type: 0x01}},
						}},
				},
			}},
			want: []string{
				frameIndent + "TSMF [#1 tlv TSID=45168 ONID=11]",
				streamIndent + "TLVSID=45168  ONID=11",
				serviceIndent + "4096  テスト　衛星４Ｋ  (TV)",
			},
		},
		{name: "nothing analysed says nothing", analysis: nil, want: nil},
		{
			name:     "a stream with no services says nothing",
			analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{TransportStreamID: 16432}},
			want:     nil,
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			got := channelNotes(one.analysis)
			if len(got) != len(one.want) {
				t.Fatalf("channelNotes() = %#v, want %#v", got, one.want)
			}
			for index := range got {
				if got[index] != one.want[index] {
					t.Errorf("line %d = %q, want %q", index, got[index], one.want[index])
				}
			}
		})
	}
}

// TestServicesSayWhatTheyAreAndWhetherTheyAreUnderCA. A multiplex carries data
// and promotional services beside the television ones, and a scan that showed
// only names left no way to tell them apart.
func TestServicesSayWhatTheyAreAndWhetherTheyAreUnderCA(t *testing.T) {
	for _, one := range []struct {
		name    string
		service isdb.ServiceInfo
		want    string
	}{
		{"television", isdb.ServiceInfo{Type: 0x01}, "TV"},
		{"the 4K-only television service", isdb.ServiceInfo{Type: 0xad}, "UHD TV"},
		{"data", isdb.ServiceInfo{Type: 0xc0}, "data"},
		{"promotional video", isdb.ServiceInfo{Type: 0xa5}, "promotional video"},
		{"under conditional access", isdb.ServiceInfo{Type: 0x01, FreeCAMode: true}, "TV, scrambled"},
		{"the bit set and nothing else said", isdb.ServiceInfo{FreeCAMode: true}, "scrambled"},
		// Still a fact about the service, and the number is what the
		// standard's own table is indexed by.
		{"a type this does not know", isdb.ServiceInfo{Type: 0x7f}, "type 0x7f"},
		{"a table that said nothing", isdb.ServiceInfo{}, ""},
	} {
		t.Run(one.name, func(t *testing.T) {
			if got := serviceKind(one.service); got != one.want {
				t.Errorf("serviceKind() = %q, want %q", got, one.want)
			}
		})
	}
}

// TestOnlyAnAnswerAboutTheChannelIsResumed. A scan of a whole band is long
// enough to be interrupted, and what it has already settled is worth keeping;
// what it merely failed at is not.
func TestOnlyAnAnswerAboutTheChannelIsResumed(t *testing.T) {
	for _, one := range []struct {
		status string
		want   bool
	}{
		{"found", true},
		{"no_lock", true},
		{"unsupported", true},
		// The channel was there and the sample was not: the tuner locked to
		// it. Worth another go.
		{"no_data", false},
		// A busy tuner, a run that was interrupted: about the attempt.
		{"error", false},
	} {
		t.Run(one.status, func(t *testing.T) {
			previous := scanResult{Status: one.status}
			if got := resumable(previous, ""); got != one.want {
				t.Errorf("resumable(%q) = %v, want %v", one.status, got, one.want)
			}
		})
	}
}

// TestAnotherTunersResultIsNotResumed: the progress file is shared by every
// scan run in a directory, and tuners sit on different leads. A band walked on
// a tuner with nothing on its lead must not be skipped on the one that can
// receive it.
func TestAnotherTunersResultIsNotResumed(t *testing.T) {
	found := scanResult{Status: "found", Device: "us3pout:p9-16:0"}
	if resumable(found, "pt3:6-17-0-0:0") {
		t.Error("a result from another tuner was taken as this tuner's answer")
	}
	if !resumable(found, "us3pout:p9-16:0") {
		t.Error("the tuner's own result was not resumed")
	}
	// A file written before results carried a tuner, and a run that left the
	// choice to the daemon: nothing to tell apart, so the result stands.
	if !resumable(scanResult{Status: "found"}, "pt3:6-17-0-0:0") {
		t.Error("a result from an older progress file was discarded")
	}
	if !resumable(found, "") {
		t.Error("a result was discarded by a run that named no tuner")
	}
}

// countingTicker stands in for the progress display, which writes nothing
// unless its output is a terminal.
type countingTicker struct{ updates int }

func (ticker *countingTicker) Update(string, int64, int64, string) { ticker.updates++ }

// TestTheLineKeepsCountingWhileATuneIsInFlight. A tune is one blocking call and
// can take seconds -- five on a card whose vendor driver waits out its own lock
// timeout -- and a line that neither moves nor counts up reads as a hang.
func TestTheLineKeepsCountingWhileATuneIsInFlight(t *testing.T) {
	ticker := &countingTicker{}
	wanted := errors.New("the card would not tune")

	got := whileTuning(ticker, func() error {
		time.Sleep(3 * tuningTick)
		return wanted
	})

	if !errors.Is(got, wanted) {
		t.Errorf("whileTuning returned %v, want the tune's own error", got)
	}
	if ticker.updates < 2 {
		t.Errorf("the line was written %d times while tuning, want it counting up", ticker.updates)
	}
}

// TestATuneThatReturnsAtOnceIsNotHeldUp: the ticker must not add a beat of its
// own to every channel of a scan.
func TestATuneThatReturnsAtOnceIsNotHeldUp(t *testing.T) {
	ticker := &countingTicker{}
	started := time.Now()
	if err := whileTuning(ticker, func() error { return nil }); err != nil {
		t.Fatalf("whileTuning: %v", err)
	}
	if took := time.Since(started); took > tuningTick {
		t.Errorf("a tune that returned at once took %s", took)
	}
	if ticker.updates != 0 {
		t.Errorf("the line was written %d times for a tune that did not wait", ticker.updates)
	}
}

// TestATLVCarrierListsOnlyWhatItDescribes. A TLV carrier's MH-SDT names more
// services than the carrier holds: one measured here declared seven and
// described one, and the stream carried that one's two MMTP flows and nothing
// else.
func TestATLVCarrierListsOnlyWhatItDescribes(t *testing.T) {
	declared := &isdb.Analysis{TSMF: &isdb.TSMFInfo{
		RelativeStreams: []isdb.RelativeStreamInfo{
			{RelativeStream: 1, TLV: &isdb.TLVInfo{
				TLVStreamID: 45329, OriginalNetworkID: 11,
				Services: []isdb.ServiceInfo{
					{ServiceID: 101, Type: 0x01},
					{ServiceID: 161, Name: "サンプル衛星４Ｋ", Provider: "サンプル放送", Type: 0x01},
					{ServiceID: 181, Type: 0x01},
				},
			}},
		},
	}}
	lines := streamNotes(nil, declared.TSMF.RelativeStreams[0].TLV)
	if len(lines) != 2 {
		t.Fatalf("streamNotes() = %#v, want the heading and the one service described", lines)
	}
	if !strings.Contains(lines[1], "161") {
		t.Errorf("the described service was not the one kept: %q", lines[1])
	}

	// A sample too short for any descriptor keeps the lot: an unnamed service
	// is then this program's own shortfall.
	silent := &isdb.Analysis{TSMF: &isdb.TSMFInfo{
		RelativeStreams: []isdb.RelativeStreamInfo{
			{RelativeStream: 1, TLV: &isdb.TLVInfo{
				TLVStreamID: 45329,
				Services:    []isdb.ServiceInfo{{ServiceID: 101}, {ServiceID: 161}},
			}},
		},
	}}
	if lines := streamNotes(nil, silent.TSMF.RelativeStreams[0].TLV); len(lines) != 3 {
		t.Errorf("streamNotes() = %#v, want the heading and both services", lines)
	}
}

// The package list is the answer the descriptors were standing in for, so it
// decides on its own: a service it names is carried even if nothing described
// it, and one it leaves out is not carried however well described.
func TestTheTLVPackageListDecidesWhatIsCarried(t *testing.T) {
	analysis := &isdb.Analysis{TSMF: &isdb.TSMFInfo{
		RelativeStreams: []isdb.RelativeStreamInfo{
			{RelativeStream: 1, TLV: &isdb.TLVInfo{
				TLVStreamID:       45329,
				OriginalNetworkID: 11,
				CarriedServiceIDs: []uint16{101},
				Services: []isdb.ServiceInfo{
					{ServiceID: 101, Type: 0x01},
					{ServiceID: 161, Name: "サンプル衛星４Ｋ", Provider: "サンプル放送", Type: 0x01},
				},
			}},
		},
	}}
	lines := streamNotes(nil, analysis.TSMF.RelativeStreams[0].TLV)
	if len(lines) != 2 {
		t.Fatalf("streamNotes() = %#v, want the heading and the one service carried", lines)
	}
	if !strings.Contains(lines[1], "101") {
		t.Errorf("the carried service was not the one kept: %q", lines[1])
	}
}

// A one-segment service carries the data service type, so saying "data" would
// hide what it is. The network having put it in the partial reception layer is
// what the line says instead.
func TestAOneSegmentServiceIsNotCalledData(t *testing.T) {
	analysis := &isdb.Analysis{Transport: &isdb.TransportInfo{
		TransportStreamID: 0x0400,
		Services: []isdb.ServiceInfo{
			{ServiceID: 1024, Type: 0x01, Name: "サンプル総合"},
			{ServiceID: 1032, Type: 0xc0, Name: "サンプル総合１", PartialReception: true},
		},
	}}
	lines := describeServices(analysisServices(analysis))
	if len(lines) != 2 {
		t.Fatalf("describeServices() = %#v", lines)
	}
	if !strings.Contains(lines[1], "1seg") || strings.Contains(lines[1], "data") {
		t.Errorf("the one-segment service reads as %q", lines[1])
	}
}

// A line watching a sample fill shows the rate it is arriving at, not how much
// has piled up: the rate is the one worth comparing against what the multiplex
// says it runs at.
func TestTheSamplingLineShowsTheRate(t *testing.T) {
	if got := sampledRate(3_750_000, 2*time.Second); got != "15.00 Mbps" {
		t.Errorf("sampledRate() = %q, want 15.00 Mbps", got)
	}
	// Too little of a capture to divide: a figure made from one read would
	// swing wildly and mean nothing.
	if got := sampledRate(1024, 10*time.Millisecond); got != "" {
		t.Errorf("sampledRate() = %q, want nothing yet", got)
	}
	if got := sampledRate(0, time.Second); got != "" {
		t.Errorf("sampledRate() = %q, want nothing yet", got)
	}
}

// stalledStream delivers its prefix and then waits for the read's deadline.
type stalledStream struct{ prefix []byte }

func (stream *stalledStream) Read(ctx context.Context, buffer []byte) (int, error) {
	if len(stream.prefix) > 0 {
		n := copy(buffer, stream.prefix)
		stream.prefix = stream.prefix[n:]
		return n, nil
	}
	<-ctx.Done()
	return 0, ctx.Err()
}

func (*stalledStream) Close() error { return nil }

func TestSampleDeadlinesInterruptStalledReads(t *testing.T) {
	for _, test := range []struct {
		name      string
		bytes     int
		wantError bool
	}{
		{"no data", 0, true},
		{"insufficient data", 1, true},
		{"analysis", 2, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := Options{
				DataTimeout:  30 * time.Millisecond,
				AnalysisTime: 40 * time.Millisecond,
				MinimumBytes: 2, MaximumBytes: 100,
				PollInterval: time.Millisecond,
			}
			stream := &stalledStream{prefix: make([]byte, test.bytes)}
			started := time.Now()
			sample, _, err := receiveSampleFrom(t.Context(), options, func([]byte) bool { return false }, stream, nil)
			if (err != nil) != test.wantError || len(sample) != test.bytes {
				t.Fatalf("sample bytes=%d err=%v", len(sample), err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("blocked for %v", elapsed)
			}
		})
	}
}

func TestDrainDeadlineInterruptsAStalledRead(t *testing.T) {
	started := time.Now()
	drain(t.Context(), &stalledStream{}, make([]byte, 1), time.Millisecond)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("drain blocked for %v", elapsed)
	}
}
