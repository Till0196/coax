// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86h47

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Core 1 runs jobs, sequences of I2C accesses with waits between them that the
// host would otherwise pace itself. A job is described in core 1's data RAM:
//
//	+0x00  sequence number, written last; a new one starts the job
//	+0x04  kind
//	+0x08  parameters, up to jobParams words
//	+0x40  the sequence number of the last job done, written last
//	+0x44  error: 0, or what i2cError takes
//	+0x48  results, up to jobResults words
//	+0x60  a counter the wait loop advances on every pass
//
// Core 1 serves nothing else while a job runs, so the host drains the ring
// first and asks after the job by memory request.
const (
	jobArea     = 0xa3a00
	jobSequence = 0x00
	jobKind     = 0x04
	jobParam    = 0x08
	jobDone     = 0x40
	jobError    = 0x44
	jobResult   = 0x48
	jobSpin     = 0x60
	jobParams   = 14
	jobResults  = 3

	// jobMailbox is the mailbox layout a job builds its transfers in.
	jobMailbox = 0xa3b00
)

// Core 1 keeps, per frequency, what tuning there found, for the next tune to
// start from. It lasts while the chip is powered, coaxd restarts included.
// tableEntries entries of tableEntry bytes:
//
//	+0x00  the key: the channel's frequency in kHz; 0 for an entry not in use
//	+0x04  bits 7-0: the capacitor trim the search ended at; bit 8 set when
//	       +0x08 and +0x0C hold transmission parameters, bit 9 when bits
//	       7-0 hold a trim
//	+0x08  the transmission parameters the demodulator locked with (TMCC)
//
// then a header: tableMagic and the entry a new frequency takes when none is
// free. Another build's header means its table, which is cleared.
const (
	tableBase    = 0xa2800
	tableEntry   = 16
	tableEntries = 64
	tableHeader  = tableBase + tableEntries*tableEntry
	tableNext    = tableHeader + 4
	tableMagic   = 0x7ab1e002

	// Bits of an entry's second word.
	tableTMCC    = 1 << 8
	tableCaptrim = 1 << 9

	// programLimit is where core 1's program has to end: the table follows.
	programLimit = tableBase
)

// tableCheckLocked keeps the table if this build laid it out and clears it
// otherwise.
func (bridge *Bridge) tableCheckLocked() {
	if bridge.read32(core1Base+tableHeader) == tableMagic {
		return
	}
	empty := make([]byte, readLimit)
	for offset := uint32(0); offset < tableEntries*tableEntry; offset += readLimit {
		bridge.writeBlock(core1Base+tableBase+offset, empty)
	}
	bridge.write32(core1Base+tableNext, 0)
	bridge.write32(core1Base+tableHeader, tableMagic)
}

// Kinds of job.
const (
	// jobSpinKind runs the wait loop for the number of passes in parameter 0.
	jobSpinKind = 1
	// jobCaptrimKind is the DiB0090's capacitor trim search; see Captrim.
	jobCaptrimKind = 2
	// jobTMCCRecallKind gives the transmission parameters remembered for a
	// frequency; see RecallTMCC.
	jobTMCCRecallKind = 3
	// jobTMCCRememberKind reads the parameters the demodulator locked with and
	// remembers them; see RememberTMCC.
	jobTMCCRememberKind = 4
)

// jobTimeout is how long the host waits for a job beyond the waits it was given.
const jobTimeout = 500 * time.Millisecond

// errJob is a job core 1 did not finish in time.
var errJob = errors.New("MB86H47A codec core 1 did not finish its job")

// runJobLocked starts a job and waits for it; expected is how long it should
// take, and address names the device in an error.
func (bridge *Bridge) runJobLocked(address byte, kind uint32, params []uint32, expected time.Duration) ([]uint32, error) {
	if len(params) > jobParams {
		return nil, fmt.Errorf("MB86H47A job of %d parameters", len(params))
	}
	if err := bridge.ringDrainLocked(); err != nil {
		return nil, err
	}
	sequence := bridge.startJobLocked(kind, params)
	deadline := time.Now().Add(expected + jobTimeout)
	if expected > 2*time.Millisecond {
		time.Sleep(expected - 2*time.Millisecond)
	}
	answer := make([]byte, 8+jobResults*4)
	for {
		bridge.readBlock(core1Base+jobArea+jobDone, answer)
		if bridge.err != nil {
			return nil, bridge.err
		}
		if binary.BigEndian.Uint32(answer) == sequence {
			break
		}
		if time.Now().After(deadline) {
			return nil, errJob
		}
		time.Sleep(time.Millisecond)
	}
	results := make([]uint32, jobResults)
	for i := range results {
		results[i] = binary.BigEndian.Uint32(answer[8+i*4:])
	}
	if code := binary.BigEndian.Uint32(answer[4:]); code != i2cOK {
		return results, fmt.Errorf("MB86H47A job: %w", i2cError(address, byte(code)))
	}
	return results, nil
}

// startJobLocked writes a job and starts it, returning its sequence number.
func (bridge *Bridge) startJobLocked(kind uint32, params []uint32) uint32 {
	bridge.jobSequence++
	if bridge.jobSequence == 0 {
		bridge.jobSequence = 1
	}
	data := binary.BigEndian.AppendUint32(nil, kind)
	for _, word := range params {
		data = binary.BigEndian.AppendUint32(data, word)
	}
	bridge.writeBlock(core1Base+jobArea+jobKind, data)
	bridge.write32(core1Base+jobArea+jobSequence, bridge.jobSequence)
	return bridge.jobSequence
}

// The wait loop's rate, in passes a second, is measured once per start by
// reading its counter twice; waits are counted in passes.
const (
	spinCalibration = 100 * time.Millisecond
	// spinPasses is more than core 1 makes in the calibration; the job is ended
	// once read.
	spinPasses = 100_000_000
)

// spinRateLocked is the wait loop's passes a second, measured on first use.
func (bridge *Bridge) spinRateLocked() (float64, error) {
	if bridge.spinRate != 0 {
		return bridge.spinRate, nil
	}
	if err := bridge.ringDrainLocked(); err != nil {
		return 0, err
	}
	sequence := bridge.startJobLocked(jobSpinKind, []uint32{spinPasses})
	time.Sleep(10 * time.Millisecond)
	counter := uint32(core1Base + jobArea + jobSpin)
	before, from := bridge.read32(counter), time.Now()
	time.Sleep(spinCalibration)
	after, to := bridge.read32(counter), time.Now()
	if bridge.err != nil {
		return 0, bridge.err
	}
	if after == before {
		return 0, errJob
	}
	rate := float64(after-before) / to.Sub(from).Seconds()
	// A new sequence number ends the loop; core 1 then takes it as done.
	bridge.jobSequence++
	bridge.write32(core1Base+jobArea+jobSequence, bridge.jobSequence)
	sequence = bridge.jobSequence
	deadline := time.Now().Add(jobTimeout)
	for bridge.read32(core1Base+jobArea+jobDone) != sequence {
		if bridge.err != nil {
			return 0, bridge.err
		}
		if time.Now().After(deadline) {
			return 0, errJob
		}
		time.Sleep(10 * time.Millisecond)
	}
	bridge.spinRate = rate
	return rate, nil
}

// passes is a wait as the wait loop counts it.
func passes(wait time.Duration, rate float64) uint32 {
	return uint32(wait.Seconds()*rate + 0.5)
}

// rememberedStep is the step from a remembered trim: Linux's minimal search
// for a tuner retuned to the frequency it is on.
const rememberedStep = 4

// Captrim runs the DiB0090's capacitor trim search on core 1 as
// dib0090_captrim_search does before P1G, with the DiB8000's gate open to the
// tuner at address. A trim remembered for key, the channel's frequency in kHz,
// is started from with rememberedStep; the trim found is remembered. It gives
// the trim set and its distance from target.
func (bridge *Bridge) Captrim(address byte, lo4 uint16, key uint32, start, step, target int, settleWrite, settleRead time.Duration) (int, int, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.err = nil
	rate, err := bridge.spinRateLocked()
	if err != nil {
		return 0, 0, err
	}
	// The shortest the job can take: a remembered trim's search.
	expected := settleWrite + 3*(settleWrite+settleRead)
	results, err := bridge.runJobLocked(address, jobCaptrimKind, []uint32{
		uint32(address), uint32(lo4), uint32(int32(start)), uint32(int32(step)), uint32(int32(target)),
		passes(settleWrite, rate), passes(settleWrite, rate), passes(settleRead, rate), key,
	}, expected)
	if err != nil {
		return 0, 0, err
	}
	return int(int32(results[0])), int(int32(results[1])), nil
}

// TMCC is a channel's transmission parameters as core 1 keeps them: word 0
// has register 570 & 0x73 in bits 7-0, 505's partial bit at bit 8 and layer A
// from bit 12; word 1 has layer B from bit 0 and layer C from bit 16. A layer
// is segment count (493+i, 4 bits), interleaving (499+i, 2), code rate (481+i,
// 3) and modulation (487+i, 2).
type TMCC = [2]uint32

// RecallTMCC gives the parameters remembered for the frequency key, in kHz.
func (bridge *Bridge) RecallTMCC(key uint32) (TMCC, bool, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.err = nil
	results, err := bridge.runJobLocked(0, jobTMCCRecallKind, []uint32{key}, 0)
	if err != nil {
		return TMCC{}, false, err
	}
	return TMCC{results[1], results[2]}, results[0] != 0, nil
}

// RememberTMCC reads the parameters the demodulator at address locked with and
// remembers them for key. It gives them, and whether they changed.
func (bridge *Bridge) RememberTMCC(address byte, key uint32) (TMCC, bool, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.err = nil
	results, err := bridge.runJobLocked(address, jobTMCCRememberKind, []uint32{uint32(address), key}, 0)
	if err != nil {
		return TMCC{}, false, err
	}
	return TMCC{results[1], results[2]}, results[0] != 0, nil
}
