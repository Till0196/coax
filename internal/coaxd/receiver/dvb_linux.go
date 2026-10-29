// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package receiver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Till0196/coax/internal/coaxd/chardev"
	"github.com/Till0196/coax/internal/coaxd/dvb"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

type linuxDVBDevice struct {
	frontend     *dvb.Frontend
	frontendPath string
	demuxPath    string
	dvrPath      string
	supported    map[uint32]bool
	// frequencyMin and frequencyMax are what the frontend says it can reach.
	// A zero maximum means it did not say, and the range is not checked.
	frequencyMin uint32
	frequencyMax uint32
	// settle is whether the front of the stream is held back until the
	// demodulator settles; see settleFrontendName.
	settle bool
}

func Open(options Options) (Device, error) {
	backend := options.Backend
	if backend == "" || backend == "auto" {
		backend = "dvb"
	}
	if device, handled, err := openUSB(backend, options); handled {
		return device, err
	}
	// The tuners px4_drv and the Earthsoft drivers publish are not DVB
	// adapters and are not reached through /dev/dvb: each has a character
	// device interface of its own, which this backend speaks. Which driver
	// put a node there is read from the node itself; see driverFor.
	if backend == chardev.Backend {
		return openChardev(options)
	}
	if backend != "dvb" {
		return nil, fmt.Errorf("backend %q is not available on Linux", backend)
	}
	root := options.LinuxDVBRoot
	if root == "" {
		root = "/dev/dvb"
	}
	adapterRoot := filepath.Join(root, fmt.Sprintf("adapter%d", options.Adapter))
	frontendPath := filepath.Join(adapterRoot, fmt.Sprintf("frontend%d", options.Frontend))
	if options.Device != "" {
		frontendPath = options.Device
	}
	frontend, err := dvb.OpenFrontend(frontendPath)
	if err != nil {
		return nil, fmt.Errorf("open frontend %s: %w", frontendPath, err)
	}
	device := &linuxDVBDevice{
		frontend: frontend, frontendPath: frontendPath,
		demuxPath: filepath.Join(adapterRoot, fmt.Sprintf("demux%d", options.Demux)),
		dvrPath:   filepath.Join(adapterRoot, fmt.Sprintf("dvr%d", options.DVR)),
	}
	if info, infoErr := frontend.Info(); infoErr == nil {
		device.frequencyMin, device.frequencyMax = info.FrequencyMin, info.FrequencyMax
		device.settle = info.Name == settleFrontendName
	}
	if systems, enumErr := frontend.DeliverySystems(); enumErr == nil {
		device.supported = make(map[uint32]bool, len(systems))
		for _, system := range systems {
			device.supported[system] = true
		}
	}
	return device, nil
}

func (device *linuxDVBDevice) Backend() string { return "dvb" }
func (device *linuxDVBDevice) Name() string    { return device.frontendPath }
func (device *linuxDVBDevice) Close() error    { return device.frontend.Close() }

// Mode is the delivery system the frontend is currently set to, as
// dvbv5-fe-tool prints. A frontend reaching several standards is in one of
// them at a time, and not always the one wanted.
func (device *linuxDVBDevice) Mode() string {
	system, err := device.frontend.DeliverySystem()
	if err != nil {
		return ""
	}
	return system
}

// Settings is everything the frontend will say about how it is tuned, as
// dvbv5-fe-tool prints.
func (device *linuxDVBDevice) Settings() [][2]string {
	parameters, err := device.frontend.Parameters()
	if err != nil {
		return nil
	}
	settings := make([][2]string, 0, len(parameters))
	for _, parameter := range parameters {
		settings = append(settings, [2]string{parameter.Name, parameter.Value})
	}
	return settings
}

func (device *linuxDVBDevice) Supports(deliverySystem string) (bool, error) {
	_, delivery, err := dvb.BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": deliverySystem,
	})
	if err != nil {
		return false, err
	}
	return len(device.supported) == 0 || device.supported[delivery], nil
}

func (device *linuxDVBDevice) Tune(properties map[string]string) error {
	tuneProperties, delivery, err := dvb.BuildTuneProperties(properties)
	if err != nil {
		return err
	}
	device.refreshFrequencyRange(delivery)
	if err := device.checkFrequency(tuneProperties); err != nil {
		return err
	}
	// The switch on the lead is told which port before the demodulator is
	// told anything, because until it has been told, the lead is carrying
	// whatever the last channel left it pointed at.
	command, wanted, err := tuning.DiseqcFor(properties)
	if err != nil {
		return err
	}
	if wanted {
		if err := device.frontend.SendDiseqc(command); err != nil {
			return err
		}
	}
	return device.frontend.Tune(tuneProperties)
}

// refreshFrequencyRange asks the frontend what it can reach in the standard it
// is about to be asked for.
//
// The limits are read when the device is opened, and are then those of whatever
// standard the frontend was last left in, in that standard's units: kilohertz
// for satellite, hertz otherwise. A card that does both, left in satellite mode,
// reports 45000..3224000 to a cable tune, against which every cable channel is a
// thousand times too high -- a whole band reported as beyond the tuner.
//
// Failing to re-read is not an error: the check this feeds is a courtesy and the
// frontend still answers the tune itself.
func (device *linuxDVBDevice) refreshFrequencyRange(delivery uint32) {
	if err := device.frontend.SelectDeliverySystem(delivery); err != nil {
		return
	}
	if low, high, err := device.frontend.FrequencyRange(); err == nil {
		device.frequencyMin, device.frequencyMax = low, high
	}
}

// checkFrequency rejects a channel the tuner cannot reach before asking the
// driver to, so scanning a whole band against a tuner covering part of it
// reports the rest as out of range rather than as a driver error: the frontend
// answers FE_SET_PROPERTY with EINVAL either way.
func (device *linuxDVBDevice) checkFrequency(properties []dvb.TuneProperty) error {
	if device.frequencyMax == 0 {
		return nil
	}
	for _, property := range properties {
		if property.Command != dvb.CommandFrequency {
			continue
		}
		if property.Value < device.frequencyMin || property.Value > device.frequencyMax {
			return fmt.Errorf("%w: %d is outside %d..%d",
				ErrOutOfRange, property.Value, device.frequencyMin, device.frequencyMax)
		}
	}
	return nil
}

func (device *linuxDVBDevice) Status() (Status, error) {
	status, err := device.frontend.Status()
	if err != nil {
		return Status{}, err
	}
	result := Status{Locked: dvb.HasLock(status), Names: dvb.StatusNames(status)}
	// The measurements are optional and many drivers offer none. A frontend
	// that will not answer must not turn a good lock into a status error, so
	// the reading is dropped rather than propagated.
	if statistics, statErr := device.frontend.Statistics(); statErr == nil {
		if statistics.HasCNR {
			result.CNR = Measured(statistics.CNRdB)
		}
		if statistics.HasStrength {
			result.Strength = Measured(statistics.StrengthdBm)
		}
	}
	return result, nil
}

func (device *linuxDVBDevice) OpenStream() (Stream, error) {
	demux, err := dvb.StartDemux(device.demuxPath)
	if err != nil {
		return nil, fmt.Errorf("start demux %s: %w", device.demuxPath, err)
	}
	dvr, err := os.OpenFile(device.dvrPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		dvb.StopDemux(demux)
		return nil, fmt.Errorf("open DVR %s: %w", device.dvrPath, err)
	}
	stream := &linuxDVBStream{demux: demux, dvr: dvr}
	if device.settle {
		return newSettlingStream(stream), nil
	}
	return stream, nil
}

type linuxDVBStream struct {
	demux *os.File
	dvr   *os.File
}

func (stream *linuxDVBStream) Read(ctx context.Context, buffer []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// A device opened non-blocking is handed to the runtime's poller, and a
	// read with nothing to give then waits there rather than answering EAGAIN:
	// for as long as there is no signal, and through a Close waiting for the
	// reader to stop. The deadline is what brings it back. A device the poller
	// would not take answers EAGAIN itself, and refuses the deadline.
	_ = stream.dvr.SetReadDeadline(time.Now().Add(dvrWait))
	read, err := stream.dvr.Read(buffer)
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
		return read, ErrWouldBlock
	}
	return read, err
}

// dvrWait is the longest one read waits for the stream: long beside a packet at
// any rate a carrier runs at, short beside anything waiting for the read to
// come back.
const dvrWait = 200 * time.Millisecond

func (stream *linuxDVBStream) Close() error {
	dvb.StopDemux(stream.demux)
	return stream.dvr.Close()
}
