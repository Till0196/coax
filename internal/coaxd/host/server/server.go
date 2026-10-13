// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package server answers HTTP for the tuner host.
//
// A client's only identity is the connection it arrived on: a tuner is held
// while that connection is open and freed the moment it is not, so there is no
// session to create or expire and nothing to clean up after a client that
// vanished.
//
// Leases therefore belong to connections rather than to requests. A scan tunes
// hundreds of channels down one connection and must not lose its tuner between
// two of them; a player holds one connection for hours and needs the tuner for
// exactly that long.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/internal/coaxd/trace"
)

// Options settle what the server cannot work out for itself.
type Options struct {
	// Version and Commit describe the build, for GET /version.
	Version string
	Commit  string
	// LockTimeout is how long a request waits for a lock when it does not say.
	LockTimeout time.Duration
	// Settle is the default wait before reading the measurements again: a
	// demodulator reports its lock before it has a carrier-to-noise figure.
	Settle time.Duration
	// WriteThreshold and WriteInterval decide how much stream is gathered before
	// it is written on. Writing every packet would wake the reader thousands of
	// times a second for no gain.
	WriteThreshold int
	WriteInterval  time.Duration
	// Cards is the card slots, or nil when this server does not serve them.
	Cards Cards
	// Report receives a line about anything worth saying. Optional.
	Report func(string)
	// Port marks a server on a TCP port, which a web page in a browser on this
	// machine can reach and a Unix socket's cannot. See guard.
	Port bool
	// AnyHost accepts a request whatever host it names, for a port opened to
	// the network on purpose. Without it a port answers only to the loopback.
	AnyHost bool
}

// Defaults for the options a caller does not set.
const (
	DefaultLockTimeout    = 5 * time.Second
	DefaultSettle         = 2 * time.Second
	DefaultWriteThreshold = 188 * 312
	DefaultWriteInterval  = 100 * time.Millisecond
)

// Server is the HTTP face of the daemon.
type Server struct {
	pool    *pool.Supervisor
	options Options
	started time.Time

	// owners maps a connection to what it holds; a connection is a client's only
	// identity here. cards is the same for the card slots.
	mutex  sync.Mutex
	owners map[net.Conn]pool.Owner
	cards  map[net.Conn]*held
	// connections is how the daemon knows whether anything still needs it.
	connections int
}

// New builds a server over a pool.
func New(supervisor *pool.Supervisor, options Options) *Server {
	if options.LockTimeout <= 0 {
		options.LockTimeout = DefaultLockTimeout
	}
	if options.Settle < 0 {
		options.Settle = 0
	}
	if options.Settle == 0 {
		options.Settle = DefaultSettle
	}
	if options.WriteThreshold <= 0 {
		options.WriteThreshold = DefaultWriteThreshold
	}
	if options.WriteInterval <= 0 {
		options.WriteInterval = DefaultWriteInterval
	}
	if options.Report == nil {
		options.Report = func(string) {}
	}
	return &Server{
		pool:    supervisor,
		options: options,
		started: time.Now(),
		owners:  make(map[net.Conn]pool.Owner),
		cards:   make(map[net.Conn]*held),
	}
}

// connKey carries the owner of the connection a request arrived on, and cardKey
// what that connection is holding of the cards. Two keys rather than one value
// because the tuner pool and the card slots are separate bookkeeping.
type connKey struct{}

type cardKey struct{}

// Handler is every endpoint this server has.
//
// One set of routes, served the same way wherever the daemon listens. The card
// was once kept off the TCP port and on the Unix socket alone, on the grounds
// that the socket's directory permissions are a boundary the OS enforces. That
// bought little, cost a fork in the routing, and made a daemon reached over the
// network useless for the one thing a remote client needs it for. The port is
// not opened unless --http says so and does not leave the loopback unless
// --allow-remote says so, which is where that decision belongs.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	if s.options.Port {
		return guard(mux, s.options.AnyHost)
	}
	return mux
}

// Route is one endpoint: its pattern, and whether it is one of the card's. The
// list is a value rather than a sequence of registrations so the check in
// openapi.go can read it, and an endpoint added here but not described there
// fails a test instead of going unnoticed.
type Route struct {
	// Pattern is the method and path, as the router matches them.
	Pattern string
	// Cards marks an endpoint served only when this daemon has card slots to
	// serve.
	Cards bool
}

// Routes is every endpoint this server has.
var Routes = []Route{
	{Pattern: "GET /version"},
	{Pattern: "GET /openapi.json"},
	{Pattern: "GET /tuners"},
	{Pattern: "GET /tuners/{id}"},
	{Pattern: "GET /tune"},
	{Pattern: "GET /stream"},
	{Pattern: "GET /cards", Cards: true},
	{Pattern: "GET /cards/{id}", Cards: true},
	{Pattern: "GET /cards/{id}/events", Cards: true},
	{Pattern: "POST /cards/{id}/session", Cards: true},
	{Pattern: "POST /cards/{id}/connect", Cards: true},
	{Pattern: "DELETE /cards/{id}/connect", Cards: true},
	{Pattern: "POST /cards/{id}/transmit", Cards: true},
	{Pattern: "POST /cards/{id}/transaction", Cards: true},
	{Pattern: "DELETE /cards/{id}/transaction", Cards: true},
}

func (s *Server) routes(mux *http.ServeMux) {
	handlers := map[string]http.HandlerFunc{
		"GET /version":                   s.handleVersion,
		"GET /openapi.json":              s.handleOpenAPI,
		"GET /tuners":                    s.handleTuners,
		"GET /tuners/{id}":               s.handleTuner,
		"GET /tune":                      s.handleTune,
		"GET /stream":                    s.handleStream,
		"GET /cards":                     s.handleCards,
		"GET /cards/{id}":                s.handleCard,
		"GET /cards/{id}/events":         s.handleCardEvents,
		"POST /cards/{id}/session":       s.handleCardSession,
		"POST /cards/{id}/connect":       s.handleCardConnect,
		"DELETE /cards/{id}/connect":     s.handleCardDisconnect,
		"POST /cards/{id}/transmit":      s.handleCardTransmit,
		"POST /cards/{id}/transaction":   s.handleCardBeginTransaction,
		"DELETE /cards/{id}/transaction": s.handleCardEndTransaction,
	}
	for _, route := range Routes {
		if route.Cards && s.options.Cards == nil {
			continue
		}
		mux.HandleFunc(route.Pattern, handlers[route.Pattern])
	}
}

// Attach wires a server to the connection lifecycle of an http.Server, which is
// what makes a lease belong to a connection: an owner is created when the
// connection is accepted and everything it took is given back when it closes,
// however it closes.
func (s *Server) Attach(server *http.Server) {
	server.ConnContext = func(ctx context.Context, connection net.Conn) context.Context {
		owner := s.pool.NewOwner()
		holder := &held{}
		s.mutex.Lock()
		s.owners[connection] = owner
		s.cards[connection] = holder
		s.mutex.Unlock()
		return context.WithValue(context.WithValue(ctx, connKey{}, owner), cardKey{}, holder)
	}
	server.ConnState = func(connection net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			s.mutex.Lock()
			s.connections++
			s.mutex.Unlock()
		case http.StateClosed, http.StateHijacked:
			s.mutex.Lock()
			owner, known := s.owners[connection]
			holder := s.cards[connection]
			delete(s.owners, connection)
			delete(s.cards, connection)
			if s.connections > 0 {
				s.connections--
			}
			s.mutex.Unlock()
			if known {
				s.pool.ReleaseOwner(owner)
			}
			if holder != nil {
				// Left as it is rather than reset: a client whose connection
				// dropped may be back in a moment, and powering the card down
				// under the others sharing it helps nobody.
				holder.release(dispositionLeave)
			}
		}
	}
}

// Connections is how many clients are attached, which is what a daemon deciding
// whether it is still needed counts.
func (s *Server) Connections() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.connections
}

func owner(request *http.Request) pool.Owner {
	if value, ok := request.Context().Value(connKey{}).(pool.Owner); ok {
		return value
	}
	return pool.Owner{}
}

// priority is how much the work behind a request said it matters, from the
// header it carries. A request that says nothing is worth zero, which is what
// every client had before the header existed.
func priority(request *http.Request) (int, error) {
	return hostapi.ParsePriority(request.Header.Get(hostapi.PriorityHeader))
}

// @Summary		What this daemon is
// @Description	The protocol number a client checks against what it was built for, and what this build is. Nothing here touches hardware, so it answers whatever else is going on.
// @Tags			meta
// @Produce		json
// @Success		200	{object}	hostapi.Version
// @Router			/version [get]
func (s *Server) handleVersion(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, hostapi.Version{
		Version: s.options.Version,
		Commit:  s.options.Commit,
		Started: s.started.UTC().Format(time.RFC3339),
		Arch:    runtime.GOARCH,
	})
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(body)
}

// fail answers with a problem a client can branch on.
func fail(writer http.ResponseWriter, status int, code, message string) {
	trace.Printf("answered %d %s: %s", status, code, message)
	writeJSON(writer, status, hostapi.Failure{
		Error: hostapi.Problem{Code: code, Message: message},
	})
}

// failWith answers with a problem and whatever was measured before it.
func failWith(writer http.ResponseWriter, status int, problem hostapi.Problem, measured *hostapi.TuneResult) {
	if measured != nil {
		trace.Printf("answered %d %s on %s: %s", status, problem.Code, measured.Tuner, problem.Message)
	} else {
		trace.Printf("answered %d %s: %s", status, problem.Code, problem.Message)
	}
	writeJSON(writer, status, hostapi.Failure{Error: problem, Status: measured})
}

// problemFor turns a failure from the pool into the answer a client sees.
func problemFor(err error) (int, hostapi.Problem) {
	switch {
	case errors.Is(err, pool.ErrUnknownTuner):
		return http.StatusNotFound, hostapi.Problem{
			Code:    hostapi.CodeUnknownTuner,
			Message: err.Error(),
		}
	case errors.Is(err, device.ErrTunerBusy), errors.Is(err, pool.ErrBusy):
		return http.StatusServiceUnavailable, hostapi.Problem{
			Code:    hostapi.CodeTunerBusy,
			Message: err.Error(),
		}
	case errors.Is(err, pool.ErrNoTuner):
		return http.StatusConflict, hostapi.Problem{
			Code:    hostapi.CodeUnsupported,
			Reason:  hostapi.ReasonDeliverySystem,
			Message: err.Error(),
		}
	case errors.Is(err, pool.ErrNoCardReader):
		return http.StatusConflict, hostapi.Problem{
			Code:    hostapi.CodeNoCardReader,
			Message: err.Error(),
		}
	case errors.Is(err, fs.ErrPermission):
		// The device is there and this program may not open it -- only a person
		// can grant the permission.
		return http.StatusServiceUnavailable, hostapi.Problem{
			Code:    hostapi.CodeNoAccess,
			Message: err.Error(),
		}
	case errors.Is(err, fs.ErrNotExist):
		// A board needs a firmware blob and cannot find it: nothing is broken or
		// busy, a file is missing.
		return http.StatusServiceUnavailable, hostapi.Problem{
			Code:    hostapi.CodeNoFirmware,
			Message: err.Error(),
		}
	case errors.Is(err, pool.ErrNoDevices):
		// Not an internal failure: the program works and the bus is unreadable.
		// A client can say something useful about this, which a 500 forbids.
		return http.StatusServiceUnavailable, hostapi.Problem{
			Code:    hostapi.CodeNoDevices,
			Message: err.Error(),
		}
	case pool.IsGone(err):
		return http.StatusBadGateway, hostapi.Problem{
			Code:    hostapi.CodeDeviceGone,
			Message: err.Error(),
		}
	}
	return http.StatusInternalServerError, hostapi.Problem{
		Code:    hostapi.CodeInternal,
		Message: err.Error(),
	}
}
