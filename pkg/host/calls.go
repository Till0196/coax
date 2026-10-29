// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/tuner"
)

// Boards lists what the daemon can see. Probing opens every board, powering
// hardware up, so it is asked for rather than assumed.
func (c *Client) Boards(ctx context.Context, probe bool) ([]Board, error) {
	query := url.Values{}
	if probe {
		query.Set("open", "1")
	}
	var boards []Board
	if err := c.get(ctx, "/tuners", query, &boards); err != nil {
		return nil, err
	}
	return boards, nil
}

func (c *Client) Tuner(ctx context.Context, id string) (Tuner, error) {
	var found Tuner
	if err := c.get(ctx, "/tuners/"+url.PathEscape(id), nil, &found); err != nil {
		return Tuner{}, err
	}
	return found, nil
}

func (c *Client) Cards(ctx context.Context) ([]Card, error) {
	var cards []Card
	if err := c.get(ctx, "/cards", nil, &cards); err != nil {
		return nil, err
	}
	return cards, nil
}

func (c *Client) Card(ctx context.Context, id string, probe bool) (Card, error) {
	query := url.Values{}
	if probe {
		query.Set("probe", "1")
	}
	var card Card
	if err := c.get(ctx, "/cards/"+url.PathEscape(id), query, &card); err != nil {
		return Card{}, err
	}
	return card, nil
}

// CardSession resets the card and sends the commands that follow in one
// exchange: the card counts its messages from the reset, so they cannot be
// spread over several requests.
func (c *Client) CardSession(ctx context.Context, id string, apdus []string) (CardSession, error) {
	var session CardSession
	err := c.post(ctx, "/cards/"+url.PathEscape(id)+"/session", CardRequest{APDUs: apdus}, &session)
	return session, err
}

// Bounds are how much of a stream is wanted. A zero field is no bound.
type Bounds struct {
	Bytes int
	For   time.Duration
}

// into leaves out what was not asked for, so an endless stream is requested
// exactly as before bounds existed.
func (b Bounds) into(query url.Values) {
	if b.Bytes > 0 {
		query.Set("bytes", strconv.Itoa(b.Bytes))
	}
	if b.For > 0 {
		query.Set("for", b.For.String())
	}
}

type TuneOptions struct {
	// Tuner names one exactly. Empty lets the daemon choose.
	Tuner       string
	LockTimeout time.Duration
	// Settle is how long to hold the channel before re-reading the
	// measurements: a demodulator reports its lock before it has a
	// carrier-to-noise figure to go with it.
	Settle time.Duration
	// NoLockWait asks the daemon to answer as soon as the channel is applied,
	// leaving the wait for the lock to the caller. A scan waits itself, so that
	// the line it is showing goes on counting; without this the wait happens
	// twice.
	NoLockWait bool
	// NoSync asks the daemon for the bytes even when they are not a transport
	// stream. The daemon otherwise refuses a stream that does not line up on
	// packet boundaries, since that is what a demodulator locked to noise
	// produces.
	NoSync bool
	// Priority is how much this work matters. The daemon takes a tuner from work
	// that asked at a lower number when nothing is free, and never from work that
	// asked at the same. Zero is what every request meant before priorities
	// existed.
	Priority int
}

// Tune sets a tuner to a channel and reports what came back. Not locking is an
// answer rather than a failure: a scan asks about hundreds of frequencies
// expecting most of them to be empty.
func (c *Client) Tune(ctx context.Context, properties map[string]string, options TuneOptions) (TuneResult, error) {
	var result TuneResult
	err := c.leaseGet(ctx, "/tune", tuningQuery(properties, options), options.Priority, &result)
	return result, err
}

// Stream tunes and returns the transport stream.
//
// The stream is held by the connection it arrived on, so closing it early gives
// the tuner back -- and loses the connection, since a response not read to the
// end cannot be reused. Bounds avoid that for a caller that wants a sample.
func (c *Client) Stream(ctx context.Context, properties map[string]string, options TuneOptions, bounds Bounds) (io.ReadCloser, string, error) {
	query := tuningQuery(properties, options)
	bounds.into(query)
	target := c.base + "/stream?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	for name, values := range header(options.Priority) {
		request.Header[name] = values
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, "", err
	}
	if response.StatusCode >= 400 {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxAnswer))
		return nil, "", problemFrom(response, body)
	}
	return response.Body, response.Header.Get("X-Coax-Tuner"), nil
}

// tuningQuery renders a channel and the options for it. The properties go over
// in capitals, exactly as the channel file had them: the daemon reads a name in
// capitals as a channel-file property and passes its value through untouched.
func tuningQuery(properties map[string]string, options TuneOptions) url.Values {
	query := url.Values{}
	for name, value := range properties {
		query.Set(strings.ToUpper(name), value)
	}
	if options.Tuner != "" {
		query.Set("tuner", options.Tuner)
	}
	switch {
	case options.NoLockWait:
		query.Set("lock_timeout", "0s")
	case options.LockTimeout > 0:
		query.Set("lock_timeout", options.LockTimeout.String())
	}
	query.Set("settle", options.Settle.String())
	if options.NoSync {
		query.Set("sync", "off")
	}
	return query
}

// Device presents a tuner on the daemon as something the scanner can drive.
// A tuner cannot be held without holding the connection, so one Device is one
// connection for its whole life.
type Device struct {
	client  *Client
	options TuneOptions

	// id is the tuner the daemon chose, learnt from the first tuning and named
	// in every request after so the same one is retuned.
	id      string
	backend string
	// relativeStreams: see RelativeStreams.
	relativeStreams int
	status          tuner.Status
	stream          *stream
	// tuned is the channel this tuner is on, so asking for the stream does not
	// have to describe it again.
	tuned map[string]string
}

func (c *Client) Open(options TuneOptions) *Device {
	return &Device{client: c, options: options}
}

func (d *Device) Backend() string {
	if d.backend == "" {
		return "coaxd"
	}
	return d.backend
}

// RelativeStreams is how many places a satellite transponder's table holds, for
// a tuner whose driver names a stream by its place rather than its identifier.
// Zero for a tuner that takes an identifier, and zero before anything has been
// tuned.
func (d *Device) RelativeStreams() int { return d.relativeStreams }

func (d *Device) Name() string {
	if d.id == "" {
		return "coaxd"
	}
	return d.id
}

// ID is empty until something has been tuned.
func (d *Device) ID() string { return d.id }

// tunerInUse is the tuner a question about this Device is a question about: the
// one being held, else the one the caller named, else none of them in
// particular.
func (d *Device) tunerInUse() string {
	if d.id != "" {
		return d.id
	}
	return d.options.Tuner
}

// Supports reports whether the tuner this will use can reach a delivery system.
// A system a front end receives under another name counts as well: see carries
// and config.DeliverySystemSubstitutes.
//
// Which tuner that is changes as soon as one is held. Before then the question
// is about the machine; a Device holds its tuner for the rest of its life, so
// once one is held the answer has to be about that one. Asking the machine
// after that is how a scan on a satellite-only PT3 came to walk a cable band a
// TBS card on the same host receives.
func (d *Device) Supports(deliverySystem string) (bool, error) {
	boards, err := d.client.Boards(context.Background(), false)
	if err != nil {
		return false, err
	}
	wanted := d.tunerInUse()
	listed := false
	for _, board := range boards {
		for _, one := range board.Tuners {
			if wanted != "" && one.ID != wanted {
				continue
			}
			listed = true
			if len(one.Deliveries) == 0 {
				// A tuner that lists nothing has not said it cannot: Windows BDA offers no
				// way to ask and several Linux frontend drivers will not enumerate. Reading
				// silence as a refusal made every channel of a TBS 6205SE come back
				// "unsupported".
				return true, nil
			}
			if carries(board, one, deliverySystem) {
				return true, nil
			}
		}
	}
	if wanted != "" && !listed && len(boards) != 0 {
		// A tuner named that the daemon does not list is a mistyped name, not a
		// tuner receiving nothing; answering false made a scan of it end in "no
		// channel candidates matched". An empty listing is left alone: a backend
		// that cannot enumerate still tunes.
		return false, fmt.Errorf("no such tuner %q; `coax list` names the ones there are", wanted)
	}
	return false, nil
}

// carries reports whether a tuner's own account of itself covers a delivery
// system, read generously: a front end advertising ISDB-S is taken to receive
// ISDB-S3 as well, because neither the Linux DVB API nor Windows BDA can tell
// the two apart.
//
// The generosity is not restricted to those two interfaces: reading a backend's
// silence as a refusal turns forgetting to list ISDB-S3 on a board into a scan
// that quietly passes over the ISDB-S3 services. See scanner.DiscoverableSystems.
func carries(_ Board, tuner Tuner, deliverySystem string) bool {
	wanted := map[string]bool{channelconfig.CanonicalDeliverySystem(deliverySystem): true}
	for _, instead := range channelconfig.DeliverySystemSubstitutes(deliverySystem) {
		wanted[channelconfig.CanonicalDeliverySystem(instead)] = true
	}
	for _, delivery := range tuner.Deliveries {
		if wanted[channelconfig.CanonicalDeliverySystem(delivery)] {
			return true
		}
	}
	return false
}

func (d *Device) Tune(properties map[string]string) error {
	d.closeStream()
	options := d.options
	if d.id != "" {
		// Name the tuner already held, so that this is a retune rather than a
		// request for another one.
		options.Tuner = d.id
	}
	result, err := d.client.Tune(context.Background(), properties, options)
	if err != nil {
		return translate(err)
	}
	d.id = result.Tuner
	d.backend = result.Backend
	d.relativeStreams = result.RelativeStreams
	d.status = statusOf(result.Locked, result.Status, result.CNR, result.Strength, result.Quality)
	d.tuned = properties
	return nil
}

// Status is what the last tuning saw, refreshed from the daemon once a tuner is
// known.
func (d *Device) Status() (tuner.Status, error) {
	if d.id == "" {
		return d.status, nil
	}
	found, err := d.client.Tuner(context.Background(), d.id)
	if err != nil {
		// A status that could not be re-read is not worth an error; the
		// reading already taken stands.
		return d.status, nil
	}
	if found.Locked == nil {
		return d.status, nil
	}
	return statusOf(*found.Locked, found.Status, found.CNR, found.Strength, found.Quality), nil
}

func (d *Device) OpenStream(bounds tuner.Bounds) (tuner.Stream, error) {
	if d.id == "" {
		return nil, errors.New("nothing has been tuned yet")
	}
	options := d.options
	options.Tuner = d.id
	// Asked for as tuned; tuning again would drop the lock and start over.
	ctx, cancel := context.WithCancelCause(context.Background())
	var waiting *time.Timer
	if bounds.Wait > 0 {
		waiting = time.AfterFunc(bounds.Wait, func() { cancel(context.DeadlineExceeded) })
	}
	body, _, err := d.client.Stream(ctx, d.tuned, options,
		Bounds{Bytes: bounds.Bytes, For: bounds.For})
	if err != nil {
		if waiting != nil {
			waiting.Stop()
		}
		if cause := context.Cause(ctx); cause != nil {
			err = cause
		}
		cancel(context.Canceled)
		return nil, translate(err)
	}
	d.stream = &stream{body: body, ctx: ctx, cancel: cancel, waiting: waiting}
	return d.stream, nil
}

// Close gives the tuner back by closing the connection holding it.
func (d *Device) Close() error {
	d.closeStream()
	d.client.http.CloseIdleConnections()
	d.client.control.CloseIdleConnections()
	return nil
}

func (d *Device) closeStream() {
	if d.stream != nil {
		_ = d.stream.Close()
		d.stream = nil
	}
}

type stream struct {
	body    io.ReadCloser
	ctx     context.Context
	cancel  context.CancelCauseFunc
	waiting *time.Timer
}

func (s *stream) Read(ctx context.Context, buffer []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		s.cancel(err)
		return 0, err
	}
	stop := context.AfterFunc(ctx, func() { s.cancel(ctx.Err()) })
	defer stop()
	read, err := s.body.Read(buffer)
	if read > 0 && s.waiting != nil {
		s.waiting.Stop()
	}
	if err != nil {
		if cause := context.Cause(s.ctx); cause != nil {
			return read, cause
		}
	}
	if err == io.EOF {
		return read, io.EOF
	}
	return read, err
}

func (s *stream) Close() error {
	if s.waiting != nil {
		s.waiting.Stop()
	}
	s.cancel(context.Canceled)
	return s.body.Close()
}

// statusOf reads what the daemon said about a tuner as a status. The daemon
// answers in the same shape for a tuning it has just done and for a tuner
// somebody asked about, so one reading serves both.
func statusOf(locked bool, names []string, cnr, strength, quality *float64) tuner.Status {
	return tuner.Status{
		Locked:   locked,
		Names:    names,
		CNR:      reading(cnr),
		Strength: reading(strength),
		Quality:  reading(quality),
	}
}

// reading distinguishes a measurement the daemon could not take from zero.
func reading(value *float64) tuner.Measurement {
	if value == nil {
		return tuner.Measurement{}
	}
	return tuner.Measured(*value)
}

// translate turns a problem from the daemon into the error the scanner expects,
// so a channel outside a tuner's range is reported as such rather than a fault.
func translate(err error) error {
	var failure *FailureError
	if !errors.As(err, &failure) {
		return err
	}
	switch failure.Problem.Code {
	case CodeUnsupported:
		if failure.Problem.Reason == ReasonOutOfRange {
			return fmt.Errorf("%w: %s", tuner.ErrOutOfRange, failure.Problem.Message)
		}
		return fmt.Errorf("%w: %s", tuner.ErrUnsupported, failure.Problem.Message)
	}
	return err
}
