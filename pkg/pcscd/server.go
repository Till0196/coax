// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pcscd

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/Till0196/coax/pkg/winscard"
)

// Server answers pcsc-lite's client library on a Unix socket. It is not a
// general resource manager: it serves one winscard.Service and nothing else.
// An application with an external reader keeps using the real daemon for it by
// being pointed at the other socket; the two never have to agree, because a
// client library talks to exactly one.
type Server struct {
	service *winscard.Service
	table   *Table
	// report receives a line about anything a client did that this could not
	// do. It is optional.
	report func(string)

	mutex sync.Mutex
	// contexts maps the identifier a client knows a context by to the
	// connection that owns it, so that a cancel arriving on another
	// connection can find the one that is waiting.
	contexts   map[uint32]*connection
	waiters    map[*connection]bool
	nextHandle uint32
	// connections counts the clients attached, which is what tells a daemon
	// whether anything still needs it.
	connections int
}

// New builds a server over a service and the reader table it publishes.
func New(service *winscard.Service, table *Table, report func(string)) *Server {
	if report == nil {
		report = func(string) {}
	}
	return &Server{
		service:  service,
		table:    table,
		report:   report,
		contexts: make(map[uint32]*connection),
		waiters:  make(map[*connection]bool),
	}
}

// Serve accepts clients until the listener is closed.
func (s *Server) Serve(listener net.Listener) error {
	for {
		socket, err := listener.Accept()
		if err != nil {
			return err
		}
		client := &connection{
			server:   s,
			socket:   socket,
			contexts: make(map[uint32]winscard.ContextHandle),
			cards:    make(map[int32]winscard.CardHandle),
		}
		s.mutex.Lock()
		s.connections++
		s.mutex.Unlock()
		go client.serve()
	}
}

// Connections is how many clients are attached. A host that shuts itself down
// when idle must still see a client sitting in SCardGetStatusChange that has
// not asked for anything in minutes, which counting requests would miss.
func (s *Server) Connections() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.connections
}

// Notify tells every waiting client that something moved; the table calls it.
// A waiter is unregistered as it is signalled, as the daemon does: the client
// asks again once it has read the new state, and a waiter left registered
// would be signalled twice for one event.
func (s *Server) Notify(result uint32) {
	s.mutex.Lock()
	waiting := make([]*connection, 0, len(s.waiters))
	for client := range s.waiters {
		waiting = append(waiting, client)
	}
	s.waiters = make(map[*connection]bool)
	s.mutex.Unlock()
	for _, client := range waiting {
		client.signal(result)
	}
}

// nextIdentifier hands out the numbers clients know contexts and cards by.
// They are the server's own and mean nothing outside it.
func (s *Server) nextIdentifier() uint32 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.nextHandle++
	return s.nextHandle
}

type connection struct {
	server *Server
	socket net.Conn

	// writes serialises replies, because a waiting client is written to by
	// whichever goroutine noticed the event as well as by this one.
	writes sync.Mutex

	// oldAPI is set for a client of a protocol minor before the reader array
	// became variable-sized.
	oldAPI bool

	mutex    sync.Mutex
	contexts map[uint32]winscard.ContextHandle
	cards    map[int32]winscard.CardHandle
	waiting  bool
}

func (c *connection) serve() {
	defer c.close()
	for {
		request, err := readHeader(c.socket)
		if err != nil {
			return
		}
		if err := c.dispatch(request); err != nil {
			if !errors.Is(err, io.EOF) {
				c.server.report(fmt.Sprintf("client dropped: %v", err))
			}
			return
		}
	}
}

func (c *connection) close() {
	c.mutex.Lock()
	contexts := c.contexts
	c.contexts = make(map[uint32]winscard.ContextHandle)
	c.cards = make(map[int32]winscard.CardHandle)
	c.mutex.Unlock()

	c.server.mutex.Lock()
	delete(c.server.waiters, c)
	for identifier, owner := range c.server.contexts {
		if owner == c {
			delete(c.server.contexts, identifier)
		}
	}
	if c.server.connections > 0 {
		c.server.connections--
	}
	c.server.mutex.Unlock()

	for _, handle := range contexts {
		_ = c.server.service.ReleaseContext(handle)
	}
	_ = c.socket.Close()
	c.server.table.Refresh()
}

func (c *connection) reply(raw []byte) error {
	c.writes.Lock()
	defer c.writes.Unlock()
	_, err := c.socket.Write(raw)
	return err
}

// signal wakes a waiting client, which is the same eight bytes the wait
// command is answered with.
func (c *connection) signal(result uint32) {
	c.mutex.Lock()
	c.waiting = false
	c.mutex.Unlock()
	raw := make([]byte, sizeWait)
	put(raw, 0, 0)
	put(raw, 1, result)
	_ = c.reply(raw)
}

func (c *connection) context(identifier uint32) (winscard.ContextHandle, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	handle, ok := c.contexts[identifier]
	return handle, ok
}

func (c *connection) card(identifier int32) (winscard.CardHandle, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	handle, ok := c.cards[identifier]
	return handle, ok
}

func (c *connection) dispatch(request header) error {
	switch request.command {
	case cmdVersion:
		return c.version(request)
	case cmdEstablishContext:
		return c.establishContext(request)
	case cmdReleaseContext:
		return c.releaseContext(request)
	case cmdConnect:
		return c.connect(request)
	case cmdReconnect:
		return c.reconnect(request)
	case cmdDisconnect:
		return c.disconnect(request)
	case cmdBeginTransaction:
		return c.beginTransaction(request)
	case cmdEndTransaction:
		return c.endTransaction(request)
	case cmdStatus:
		return c.status(request)
	case cmdTransmit:
		return c.transmit(request)
	case cmdControl:
		return c.control(request)
	case cmdGetAttrib, cmdSetAttrib:
		return c.attribute(request)
	case cmdCancel:
		return c.cancel(request)
	case cmdGetReadersState, cmdGetStateArray:
		return c.sendReaderStates(request.command == cmdGetReadersState)
	case cmdGetStateSize:
		raw := make([]byte, 4)
		put(raw, 0, maxReaders)
		return c.reply(raw)
	case cmdGetReaderEvents:
		raw := make([]byte, sizeEvents)
		put(raw, 0, c.server.table.Events())
		put(raw, 1, uint32(winscard.Success))
		return c.reply(raw)
	case cmdWaitStateChange:
		return c.waitForChange()
	case cmdStopWaiting:
		return c.stopWaiting()
	}
	return fmt.Errorf("unknown command 0x%02x", request.command)
}

// version agrees a protocol version, which is the first thing a client does.
func (c *connection) version(request header) error {
	raw, err := body(c.socket, request.size, sizeVersion)
	if err != nil {
		return err
	}
	major, minor := int32(field(raw, 0)), int32(field(raw, 1))
	result := uint32(winscard.Success)
	switch {
	case major != versionMajor:
		result = uint32(winscard.ErrServiceStopped)
	case minor > versionMinor || minor < backwardMinor:
		result = uint32(winscard.ErrServiceStopped)
	case minor <= oldReaderState:
		c.oldAPI = true
	}
	// The daemon answers with its own version whether or not it accepted the
	// client's, so that a client one minor behind can decide to try again.
	put(raw, 0, uint32(versionMajor))
	put(raw, 1, uint32(versionMinor))
	put(raw, 2, result)
	return c.reply(raw)
}

func (c *connection) establishContext(request header) error {
	raw, err := body(c.socket, request.size, sizeEstablish)
	if err != nil {
		return err
	}
	scope := winscard.Scope(field(raw, 0))
	handle, establishErr := c.server.service.EstablishContext(scope)
	identifier := uint32(0)
	if establishErr == nil {
		identifier = c.server.nextIdentifier()
		c.mutex.Lock()
		c.contexts[identifier] = handle
		c.mutex.Unlock()
		c.server.mutex.Lock()
		c.server.contexts[identifier] = c
		c.server.mutex.Unlock()
	}
	put(raw, 1, identifier)
	put(raw, 2, uint32(winscard.CodeOf(establishErr)))
	return c.reply(raw)
}

func (c *connection) releaseContext(request header) error {
	raw, err := body(c.socket, request.size, sizeRelease)
	if err != nil {
		return err
	}
	identifier := field(raw, 0)
	handle, ok := c.context(identifier)
	result := winscard.ErrInvalidHandle
	if ok {
		result = winscard.CodeOf(c.server.service.ReleaseContext(handle))
		c.mutex.Lock()
		delete(c.contexts, identifier)
		c.mutex.Unlock()
		c.server.mutex.Lock()
		delete(c.server.contexts, identifier)
		c.server.mutex.Unlock()
	}
	put(raw, 1, uint32(result))
	c.server.table.Refresh()
	return c.reply(raw)
}

func (c *connection) connect(request header) error {
	raw, err := body(c.socket, request.size, sizeConnect)
	if err != nil {
		return err
	}
	context, ok := c.context(field(raw, 0))
	reader := cString(raw[4 : 4+maxReaderName])
	share := winscard.ShareMode(order.Uint32(raw[132:136]))
	protocols := winscard.Protocol(order.Uint32(raw[136:140]))

	var (
		identifier int32
		active     winscard.Protocol
		result     = winscard.ErrInvalidHandle
	)
	if ok {
		handle, agreed, connectErr := c.server.service.Connect(context, reader, share, protocols)
		result = winscard.CodeOf(connectErr)
		if connectErr == nil {
			identifier = int32(c.server.nextIdentifier())
			active = agreed
			c.mutex.Lock()
			c.cards[identifier] = handle
			c.mutex.Unlock()
		}
	}
	order.PutUint32(raw[140:144], uint32(identifier))
	order.PutUint32(raw[144:148], uint32(active))
	order.PutUint32(raw[148:152], uint32(result))
	c.server.table.Refresh()
	return c.reply(raw)
}

func (c *connection) reconnect(request header) error {
	raw, err := body(c.socket, request.size, sizeReconnect)
	if err != nil {
		return err
	}
	handle, ok := c.card(int32(field(raw, 0)))
	share := winscard.ShareMode(field(raw, 1))
	protocols := winscard.Protocol(field(raw, 2))
	initialization := winscard.Disposition(field(raw, 3))

	result := winscard.ErrInvalidHandle
	var active winscard.Protocol
	if ok {
		agreed, reconnectErr := c.server.service.Reconnect(handle, share, protocols, initialization)
		result = winscard.CodeOf(reconnectErr)
		active = agreed
	}
	put(raw, 4, uint32(active))
	put(raw, 5, uint32(result))
	c.server.table.Refresh()
	return c.reply(raw)
}

func (c *connection) disconnect(request header) error {
	raw, err := body(c.socket, request.size, sizeDisconnect)
	if err != nil {
		return err
	}
	identifier := int32(field(raw, 0))
	handle, ok := c.card(identifier)
	result := winscard.ErrInvalidHandle
	if ok {
		result = winscard.CodeOf(c.server.service.Disconnect(handle, winscard.Disposition(field(raw, 1))))
		c.mutex.Lock()
		delete(c.cards, identifier)
		c.mutex.Unlock()
	}
	put(raw, 2, uint32(result))
	c.server.table.Refresh()
	return c.reply(raw)
}

func (c *connection) beginTransaction(request header) error {
	raw, err := body(c.socket, request.size, sizeBegin)
	if err != nil {
		return err
	}
	result := winscard.ErrInvalidHandle
	if handle, ok := c.card(int32(field(raw, 0))); ok {
		result = winscard.CodeOf(c.server.service.BeginTransaction(handle))
	}
	put(raw, 1, uint32(result))
	return c.reply(raw)
}

func (c *connection) endTransaction(request header) error {
	raw, err := body(c.socket, request.size, sizeEnd)
	if err != nil {
		return err
	}
	result := winscard.ErrInvalidHandle
	if handle, ok := c.card(int32(field(raw, 0))); ok {
		result = winscard.CodeOf(c.server.service.EndTransaction(handle, winscard.Disposition(field(raw, 1))))
	}
	put(raw, 2, uint32(result))
	return c.reply(raw)
}

// status answers SCardStatus, of which the client uses only the return value:
// it reads the card's state, protocol and ATR from the reader table instead.
func (c *connection) status(request header) error {
	raw, err := body(c.socket, request.size, sizeStatus)
	if err != nil {
		return err
	}
	result := winscard.ErrInvalidHandle
	if handle, ok := c.card(int32(field(raw, 0))); ok {
		reported, statusErr := c.server.service.Status(handle)
		result = winscard.CodeOf(statusErr)
		if statusErr == nil {
			c.server.table.Observed(reported)
		}
	}
	put(raw, 1, uint32(result))
	return c.reply(raw)
}

// transmit carries one APDU. The command follows the structure on the socket,
// and the answer follows the answering structure the same way.
func (c *connection) transmit(request header) error {
	raw, err := body(c.socket, request.size, sizeTransmit)
	if err != nil {
		return err
	}
	sendProtocol := winscard.Protocol(field(raw, 1))
	sendLength := field(raw, 3)
	capacity := field(raw, 6)
	if sendLength > maxTransmit {
		return fmt.Errorf("a command of %d bytes is over the limit", sendLength)
	}
	command := make([]byte, sendLength)
	if _, err := io.ReadFull(c.socket, command); err != nil {
		return err
	}

	var answer []byte
	result := winscard.ErrInvalidHandle
	answerProtocol := sendProtocol
	if handle, ok := c.card(int32(field(raw, 0))); ok {
		received, agreed, transmitErr := c.server.service.Transmit(handle, sendProtocol, command)
		result = winscard.CodeOf(transmitErr)
		if transmitErr == nil {
			answer, answerProtocol = received, agreed
		}
	}
	if result == winscard.Success && uint32(len(answer)) > capacity {
		result = winscard.ErrInsufficientBuffer
	}
	put(raw, 4, uint32(answerProtocol))
	put(raw, 6, uint32(len(answer)))
	put(raw, 7, uint32(result))
	if err := c.reply(raw); err != nil {
		return err
	}
	if result != winscard.Success {
		return nil
	}
	return c.reply(answer)
}

// control carries a reader control code, which these readers do not have.
func (c *connection) control(request header) error {
	raw, err := body(c.socket, request.size, sizeControl)
	if err != nil {
		return err
	}
	sendLength := field(raw, 2)
	capacity := field(raw, 3)
	if sendLength > maxTransmit {
		return fmt.Errorf("a control command of %d bytes is over the limit", sendLength)
	}
	command := make([]byte, sendLength)
	if _, err := io.ReadFull(c.socket, command); err != nil {
		return err
	}

	var answer []byte
	result := winscard.ErrInvalidHandle
	if handle, ok := c.card(int32(field(raw, 0))); ok {
		received, controlErr := c.server.service.Control(handle, field(raw, 1), command)
		result = winscard.CodeOf(controlErr)
		if controlErr == nil {
			answer = received
		}
	}
	if result == winscard.Success && uint32(len(answer)) > capacity {
		result = winscard.ErrInsufficientBuffer
	}
	put(raw, 4, uint32(len(answer)))
	put(raw, 5, uint32(result))
	if err := c.reply(raw); err != nil {
		return err
	}
	if result != winscard.Success {
		return nil
	}
	return c.reply(answer)
}

// attribute reads or writes a card attribute. Both directions use one
// structure, with the value carried inside it rather than after it.
func (c *connection) attribute(request header) error {
	raw, err := body(c.socket, request.size, sizeGetSet)
	if err != nil {
		return err
	}
	identifier := int32(field(raw, 0))
	attribute := winscard.Attribute(field(raw, 1))
	length := order.Uint32(raw[8+maxAttribute : 12+maxAttribute])
	if length > maxAttribute {
		return fmt.Errorf("an attribute of %d bytes is over the limit", length)
	}

	handle, ok := c.card(identifier)
	result := winscard.ErrInvalidHandle
	if ok && request.command == cmdGetAttrib {
		value, attributeErr := c.server.service.GetAttrib(handle, attribute)
		result = winscard.CodeOf(attributeErr)
		if attributeErr == nil {
			if uint32(len(value)) > length {
				result = winscard.ErrInsufficientBuffer
			} else {
				copy(raw[8:8+maxAttribute], value)
				length = uint32(len(value))
			}
		}
	} else if ok {
		value := append([]byte(nil), raw[8:8+length]...)
		result = winscard.CodeOf(c.server.service.SetAttrib(handle, attribute, value))
	}
	order.PutUint32(raw[8+maxAttribute:12+maxAttribute], length)
	order.PutUint32(raw[12+maxAttribute:16+maxAttribute], uint32(result))
	return c.reply(raw)
}

// cancel unblocks another connection's wait. It arrives on a connection of its
// own, since the waiting one is blocked reading; that is why the server keeps
// the context table, as the identifier here is the only thing tying the two
// connections together.
func (c *connection) cancel(request header) error {
	raw, err := body(c.socket, request.size, sizeCancel)
	if err != nil {
		return err
	}
	identifier := field(raw, 0)
	result := winscard.ErrInvalidHandle

	c.server.mutex.Lock()
	owner := c.server.contexts[identifier]
	waiting := owner != nil && c.server.waiters[owner]
	if waiting {
		delete(c.server.waiters, owner)
	}
	c.server.mutex.Unlock()

	switch {
	case owner == nil:
		result = winscard.ErrInvalidHandle
	case waiting:
		owner.signal(uint32(winscard.ErrCancelled))
		result = winscard.Success
	default:
		// Nothing was waiting, and a cancel kept for the next wait would
		// cancel one the application never asked to cancel.
		result = winscard.Success
	}
	put(raw, 1, uint32(result))
	return c.reply(raw)
}

func (c *connection) waitForChange() error {
	c.mutex.Lock()
	c.waiting = true
	c.mutex.Unlock()
	c.server.mutex.Lock()
	c.server.waiters[c] = true
	c.server.mutex.Unlock()
	c.server.table.Watch()
	if c.oldAPI {
		// A client of the older protocol expects the reader array to follow
		// its registration.
		return c.sendReaderStates(true)
	}
	return nil
}

// stopWaiting takes the client off the event list. The answer is the same
// message an event would have been, so a client that was signalled at the same
// moment reads exactly one of the two.
func (c *connection) stopWaiting() error {
	c.server.mutex.Lock()
	registered := c.server.waiters[c]
	delete(c.server.waiters, c)
	c.server.mutex.Unlock()
	c.server.table.Unwatch()
	if !registered {
		return nil
	}
	raw := make([]byte, sizeWait)
	put(raw, 0, 0)
	put(raw, 1, uint32(winscard.Success))
	return c.reply(raw)
}

// sendReaderStates writes the reader array. The older protocol always takes
// the fixed number of entries, so both forms are the same length here.
func (c *connection) sendReaderStates(bool) error {
	entries := c.server.table.States()
	raw := make([]byte, 0, maxReaders*readerStateLen)
	for index := 0; index < maxReaders; index++ {
		if index < len(entries) {
			raw = append(raw, entries[index].encode()...)
			continue
		}
		raw = append(raw, make([]byte, readerStateLen)...)
	}
	return c.reply(raw)
}
