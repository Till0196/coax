// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package receiver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/chardev"
	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// chardevDevice is one tuner px4_drv publishes as a character device.
//
// The driver holds the hardware here, so this is thin: a channel number worked
// out from a frequency, three ioctls, and a reader. It is not a second way to
// drive the same boards -- the direct USB backends do that, and the two cannot
// both have a board at once -- but the path for a machine where px4_drv is
// already installed.
type chardevDevice struct {
	device *chardev.Device
	driver chardevDriver
	name   string
	// systems are the standards the node accepted when it was asked, which is the
	// only way to find out what a node is.
	systems []chardev.System

	mutex sync.Mutex
	// system is what the device is set to now, which PTX_SET_CHANNEL reads its
	// channel number against.
	system chardev.System
	// tuned and locked describe the last channel asked for. The driver waits for the
	// lock inside the tune and there is no ioctl to ask again.
	tuned  bool
	locked bool
	// number, slot and frequency are kept for the settings listing.
	number    int
	slot      int
	frequency int64
	// lnb is the supply level the last satellite tune set, so that closing can
	// drop it. It starts at lnbUnknown: what the driver was left holding is not
	// knowable from here, and the first tune has to say rather than assume.
	lnb int
	// stream is the open stream, held so that closing the device stops it rather
	// than leaving a reader blocked in the kernel.
	stream *chardevStream
}

// chardevDriver is what the two drivers behind this interface disagree about.
// px4_drv and the PT3 driver share the ioctl numbers and most of their
// meanings, and part company in exactly the four methods here.
type chardevDriver interface {
	// Driver is which of the three this is, for a message that has to say.
	Driver() string
	// RelativeStreams is how many places a satellite transponder's table holds,
	// for a driver that names a stream by its place rather than by its identifier.
	// Zero for a driver that takes an identifier.
	RelativeStreams() int
	// Systems reports what an opened node receives.
	Systems(device *chardev.Device, path string) []chardev.System
	// SelectSystem puts the node into a standard, where the driver has such a thing.
	SelectSystem(device *chardev.Device, system chardev.System) error
	// Channel turns a frequency and the channel's properties into the number and
	// slot PTX_SET_CHANNEL takes.
	Channel(system chardev.System, kilohertz int64, properties map[string]string) (number, slot int, err error)
	// SupplyForUnset says what to do about the aerial supply when a satellite
	// channel did not say: the level to set, and whether to set one at all.
	SupplyForUnset() (level int, set bool)
	// TuneOutcome reads what PTX_SET_CHANNEL answered: whether the demodulator
	// locked, and an error only when something went wrong beyond the signal not
	// being there.
	TuneOutcome(err error) (locked bool, failure error)
}

// driverFor picks the driver that published a node, reading the node's path
// rather than being told: each driver registers its own class with its own
// prefix.
func driverFor(path string) (chardevDriver, error) {
	name, ok := chardev.DriverOfNode(path)
	if !ok {
		return nil, fmt.Errorf("%q is not a character device any of these drivers published", path)
	}
	switch name {
	case chardev.DriverPx4:
		return px4Driver{}, nil
	case chardev.DriverPT3:
		return pt3Driver{}, nil
	case chardev.DriverPT1:
		return pt1Driver{}, nil
	default:
		return nil, fmt.Errorf("%q was published by %q, which this does not drive", path, name)
	}
}

// openChardev claims a character device by its node path.
func openChardev(options Options) (Device, error) {
	path := options.Device
	if path == "" {
		return nil, errors.New("the chardev backend needs the device node to open")
	}
	driver, err := driverFor(path)
	if err != nil {
		return nil, err
	}
	opened, err := chardev.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	device := &chardevDevice{device: opened, driver: driver, name: path, lnb: lnbUnknown}
	device.systems = driver.Systems(opened, path)
	if len(device.systems) == 0 {
		opened.Close()
		return nil, fmt.Errorf("%s receives neither ISDB-T nor ISDB-S", path)
	}
	return device, nil
}

// Systems reports the standards this device accepted when it was opened.
func (device *chardevDevice) Systems() []string {
	names := make([]string, 0, len(device.systems))
	for _, system := range device.systems {
		names = append(names, system.Name())
	}
	return names
}

// ChardevSystems reports the standards a node will accept, for a listing that has
// not opened it for anything else.
func ChardevSystems(path string) ([]string, error) {
	driver, err := driverFor(path)
	if err != nil {
		return nil, err
	}
	opened, err := chardev.Open(path)
	if err != nil {
		return nil, err
	}
	defer opened.Close()
	var names []string
	for _, system := range driver.Systems(opened, path) {
		names = append(names, system.Name())
	}
	return names, nil
}

func (device *chardevDevice) Backend() string { return chardev.Backend }
func (device *chardevDevice) Name() string    { return device.name }

// RelativeStreams says whether this node's driver names a satellite stream by
// its place on the transponder, and how many places its table holds. What a
// caller needs is not which driver this is but what it will take.
func (device *chardevDevice) RelativeStreams() int { return device.driver.RelativeStreams() }

// Mode is the standard the device is set to, a setting beside the channel rather
// than part of it: PTX_SET_CHANNEL reads its argument differently in each.
func (device *chardevDevice) Mode() string {
	device.mutex.Lock()
	defer device.mutex.Unlock()
	return device.system.Name()
}

// Settings is what the device will say about how it is tuned. The channel
// number is included because it is the driver's own name for the channel.
func (device *chardevDevice) Settings() [][2]string {
	device.mutex.Lock()
	defer device.mutex.Unlock()
	if !device.tuned {
		return nil
	}
	settings := [][2]string{
		{"FREQUENCY", strconv.FormatInt(device.frequency*1000, 10)},
		{"PTX_CHANNEL", strconv.Itoa(device.number)},
	}
	if device.system == chardev.SystemISDBS {
		settings = append(settings,
			[2]string{"STREAM_ID", strconv.Itoa(device.slot)},
			[2]string{"LNB_POWER", lnbName(device.lnb)})
	} else if device.slot != 0 {
		settings = append(settings, [2]string{"PTX_OFFSET_KHZ", strconv.Itoa(device.slot)})
	}
	return settings
}

// lnbUnknown is a supply nobody here has set yet, which is not the same as one
// that is off: a node just opened may have been left powered by the process
// before.
const lnbUnknown = -1

func lnbName(level int) string {
	switch level {
	case chardev.LNB11V:
		return "11V"
	case chardev.LNB15V:
		return "15V"
	default:
		return "OFF"
	}
}

func (device *chardevDevice) Supports(deliverySystem string) (bool, error) {
	wanted, err := chardevSystemOf(deliverySystem)
	if err != nil {
		return false, err
	}
	for _, system := range device.systems {
		if system == wanted {
			return true, nil
		}
	}
	return false, nil
}

// chardevSystemOf maps a delivery system name onto the driver's standards,
// which are only the two the interface has. A PX-MLT5 node can receive DVB-T
// and DVB-C as well, but the ptx ioctls cannot ask for them.
func chardevSystemOf(deliverySystem string) (chardev.System, error) {
	switch tuning.Normalize(deliverySystem) {
	case "ISDBT":
		return chardev.SystemISDBT, nil
	case "ISDBS":
		return chardev.SystemISDBS, nil
	default:
		return 0, fmt.Errorf("the chardev backend receives ISDB-T and ISDB-S; %q is neither", deliverySystem)
	}
}

// Tune selects a channel. A tune that does not lock is not a failure: the driver
// waits for the lock and answers EAGAIN when it does not come.
func (device *chardevDevice) Tune(properties map[string]string) error {
	system, err := chardevSystemOf(properties["DELIVERY_SYSTEM"])
	if err != nil {
		return err
	}
	supported, _ := device.Supports(properties["DELIVERY_SYSTEM"])
	if !supported {
		return fmt.Errorf("%s does not receive %s", device.name, system.Name())
	}
	if err := tuning.RefuseGain(properties, device.name); err != nil {
		return err
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	kilohertz := chardev.KilohertzOf(frequency)

	number, slot, err := device.driver.Channel(system, kilohertz, properties)
	if err != nil {
		return outOfRange(err)
	}
	lnb := chardev.LNBOff
	setSupply := false
	if system == chardev.SystemISDBS {
		if lnb, setSupply, err = lnbLevel(device.driver, properties); err != nil {
			return err
		}
	}

	device.mutex.Lock()
	defer device.mutex.Unlock()

	if err := device.driver.SelectSystem(device.device, system); err != nil {
		return err
	}
	device.system = system
	// The supply comes up before the tune, because the tune is what waits for the
	// lock and a lock will not come while the low-noise block is unpowered.
	if setSupply {
		raised, err := device.setLNB(lnb)
		if err != nil {
			return err
		}
		if raised {
			time.Sleep(supplySettle)
		}
	}

	device.tuned, device.locked = true, false
	device.number, device.slot, device.frequency = number, slot, kilohertz
	locked, failure := device.driver.TuneOutcome(device.device.SetChannel(number, slot))
	if failure != nil {
		return failure
	}
	device.locked = locked
	return nil
}

// setLNB moves the supply to a level and remembers where it is. It reports
// whether the line was raised, which is what has to be waited on.
func (device *chardevDevice) setLNB(level int) (bool, error) {
	if level == device.lnb {
		return false, nil
	}
	if err := device.device.SetLNBPower(level); err != nil {
		return false, err
	}
	device.lnb = level
	return level != chardev.LNBOff, nil
}

// lnbLevel reads the supply the channel asks for in the levels these drivers
// have: a step rather than a voltage, 11 V or 15 V. A channel that says nothing
// is left to the driver. The polarization is not consulted: ISDB-S carries one.
func lnbLevel(driver chardevDriver, properties map[string]string) (level int, asked bool, err error) {
	lnb, err := tuning.ParseLNB(properties)
	if err != nil {
		return 0, false, err
	}
	switch lnb {
	case tuning.LNBOff:
		return chardev.LNBOff, true, nil
	case tuning.LNBLow:
		return chardev.LNB11V, true, nil
	case tuning.LNBHigh:
		return chardev.LNB15V, true, nil
	}
	level, asked = driver.SupplyForUnset()
	return level, asked, nil
}

// supplySettle is how long the aerial line is given after the supply is raised
// before the tuner is asked to lock. A low-noise block that has just been
// powered is not ready when the ioctl returns, and tuning into that window
// gives a demodulator that never settles -- so a cold aerial looks like a tuner
// that works two times in three. Paid once per lease.
const supplySettle = 500 * time.Millisecond

// outOfRange turns "the driver has no channel number for that" into the answer
// the rest of the program gives for a frequency a tuner cannot reach.
func outOfRange(err error) error {
	if errors.Is(err, chardev.ErrNoChannel) {
		return fmt.Errorf("%w: %s", ErrOutOfRange, err)
	}
	return err
}

// Status reports the lock the last tune reached, and the ratio behind it. No
// ioctl asks whether the demodulator is locked: PTX_SET_CHANNEL waits for the
// lock and reports it, so the answer is what the tune said.
func (device *chardevDevice) Status() (Status, error) {
	device.mutex.Lock()
	defer device.mutex.Unlock()
	if !device.tuned || !device.locked {
		return Status{}, nil
	}
	status := Status{Locked: true, Names: []string{"SIGNAL", "CARRIER", "LOCK"}}
	// The reading is optional: a device that will not answer must not turn a good
	// lock into a status error.
	if raw, err := device.device.CNRRaw(); err == nil {
		convert := tc90522.CNRFromRawT
		if device.system == chardev.SystemISDBS {
			convert = tc90522.CNRFromRawS
		}
		if cnr, measured := convert(raw); measured {
			status.CNR = Measured(cnr)
		}
	}
	// There is no signal strength here: the interface has no ioctl for one.
	return status, nil
}

func (device *chardevDevice) OpenStream() (Stream, error) {
	device.mutex.Lock()
	defer device.mutex.Unlock()
	if device.stream != nil {
		return nil, fmt.Errorf("%s is already streaming", device.name)
	}
	if err := device.device.StartStreaming(); err != nil {
		return nil, err
	}
	stream := newChardevStream(device)
	device.stream = stream
	return stream, nil
}

func (device *chardevDevice) Close() error {
	device.mutex.Lock()
	stream := device.stream
	device.mutex.Unlock()
	if stream != nil {
		// Closing the node while a reader is blocked in the kernel would leave it
		// there: the driver has no timeout and no poll.
		stream.Close()
	}
	device.mutex.Lock()
	// Only for a node this program actually put a supply on: a terrestrial node
	// has no supply to drop.
	if device.lnb != lnbUnknown {
		_, _ = device.setLNB(chardev.LNBOff)
	}
	device.mutex.Unlock()
	return device.device.Close()
}

// chardevStream reads the transport stream out of the driver's ring buffer on a
// goroutine of its own: the driver's read blocks until its buffer is full and
// offers no poll and no timeout, so one issued straight from the caller could
// not be given up on when the request is cancelled.
type chardevStream struct {
	device *chardevDevice
	// chunks carries what the reader has taken from the driver. It is short: the
	// ring buffer in the kernel is the buffer.
	chunks chan []byte
	// spare holds chunk buffers a reader has finished with, so the same few go
	// round instead of one being allocated per read.
	//
	// A live tuner is read about every six milliseconds, and the cost that matters
	// is the garbage: collecting it stops the world, and that means preemption
	// signals, which are the one thing these drivers cannot be interrupted by. See
	// chardev.whileTheDriverWaits.
	//
	// Sends and receives here never block: a buffer that finds no room is dropped,
	// and a reader that finds none waiting allocates one.
	spare chan []byte
	// pending is what a Read could not fit in the buffer it was given, and
	// pendingChunk the whole buffer it is part of, kept so it can go back on the
	// spare list.
	pending      []byte
	pendingChunk []byte
	// stopping is closed the moment a close is asked for, so that a reader with
	// nowhere to put what it has stops waiting for somewhere.
	stopping chan struct{}
	// done is closed once the reader has left the kernel for good.
	done chan struct{}
	// failure is what the reader stopped for, once it has.
	failure error

	closeOnce sync.Once
}

// chardevChunk is how much is asked for in one read: small enough that a
// channel with little on it still returns something in reasonable time, since
// the driver fills the buffer before returning, and large enough not to cross
// into the kernel for every few packets. Thirty-two packets is about six
// milliseconds of a full multiplex.
const chardevChunk = 188 * 32

func newChardevStream(device *chardevDevice) *chardevStream {
	stream := &chardevStream{
		device: device,
		chunks: make(chan []byte, 4),
		// Room for every buffer that can be in play: four in the channel, one held by
		// a Read, and one being filled.
		spare:    make(chan []byte, 6),
		stopping: make(chan struct{}),
		done:     make(chan struct{}),
	}
	go stream.read()
	return stream
}

func (stream *chardevStream) read() {
	defer close(stream.done)
	defer close(stream.chunks)
	defer device.HaltOnPanic()
	for {
		select {
		case <-stream.stopping:
			return
		default:
		}
		buffer := stream.takeBuffer()
		read, err := stream.device.device.Read(buffer)
		if read > 0 {
			select {
			case stream.chunks <- buffer[:read]:
			case <-stream.stopping:
				return
			}
		} else {
			// Nothing came back, so the buffer never left this goroutine.
			stream.recycle(buffer)
		}
		if err != nil {
			stream.failure = err
			return
		}
		if read == 0 {
			// A ring buffer that is no longer running answers with a short read and no
			// error rather than an end of file, and going straight round again would spin
			// a core. Not read as the end: a stop is not the only way to get here.
			select {
			case <-stream.stopping:
				return
			case <-time.After(chardevIdlePoll):
			}
		}
	}
}

// chardevIdlePoll is how long the reader waits before asking again after the
// driver answered with nothing.
const chardevIdlePoll = 2 * time.Millisecond

// takeBuffer produces a chunk buffer, reusing one that has come back if there
// is one.
func (stream *chardevStream) takeBuffer() []byte {
	select {
	case buffer := <-stream.spare:
		return buffer[:chardevChunk]
	default:
		return make([]byte, chardevChunk)
	}
}

// recycle offers a finished buffer back to the reader, and lets it go if the
// list is full.
func (stream *chardevStream) recycle(buffer []byte) {
	if cap(buffer) < chardevChunk {
		return
	}
	select {
	case stream.spare <- buffer[:chardevChunk]:
	default:
	}
}

// Read hands over what the reader has taken, and reports that it would block
// when there is nothing yet. It does not wait: the caller polls, and blocking
// here would stop it noticing that its client had gone.
func (stream *chardevStream) Read(ctx context.Context, buffer []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(stream.pending) == 0 {
		select {
		case chunk, ok := <-stream.chunks:
			if !ok {
				if stream.failure != nil {
					return 0, stream.failure
				}
				return 0, ErrWouldBlock
			}
			stream.pending, stream.pendingChunk = chunk, chunk
		default:
			return 0, ErrWouldBlock
		}
	}
	copied := copy(buffer, stream.pending)
	stream.pending = stream.pending[copied:]
	if len(stream.pending) == 0 {
		stream.recycle(stream.pendingChunk)
		stream.pendingChunk = nil
	}
	return copied, nil
}

func (stream *chardevStream) Close() error {
	var err error
	stream.closeOnce.Do(func() {
		// The reader is let go of first: it may be waiting to hand over a chunk nobody
		// is coming back for, which the stop below would not end.
		close(stream.stopping)
		err = stream.device.device.StopStreaming()
		// Stopping the stream is what wakes a reader blocked in the driver. A
		// stop that failed -- the tuner unplugged under it -- may not have, and
		// nothing else will. The wait is bounded then, and the device stays
		// marked as streaming so that no second reader joins the first.
		select {
		case <-stream.done:
		case <-time.After(chardevStopBudget):
			if err == nil {
				err = fmt.Errorf("%s: the reader did not come back within %v of the stream stopping",
					stream.device.name, chardevStopBudget)
			}
			return
		}
		stream.device.mutex.Lock()
		stream.device.stream = nil
		stream.device.mutex.Unlock()
	})
	return err
}

// chardevStopBudget is how long a close waits for the reader. A ring buffer
// that has been stopped wakes its reader at once; this is for one that does
// not.
const chardevStopBudget = 2 * time.Second
