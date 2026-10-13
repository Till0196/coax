// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
)

// Cards is the card slots the daemon serves, an interface so the HTTP layer can
// be tested without a card.
type Cards interface {
	// Readers lists the slots without opening a board.
	Readers() ([]CardInfo, error)
	// Present reports whether a card is seated, which means opening the board.
	Present(reader string) (bool, error)
	// Session resets the card and sends a run of commands, holding the slot for
	// the whole exchange.
	Session(reader string, apdus [][]byte) (atr []byte, responses [][]byte, err error)
	// Connect opens a card, held until the returned connection is closed. This is
	// what a client speaking PC/SC needs and Session deliberately is not: an
	// exchange whose next command depends on the last answer cannot be sent as one
	// batch.
	Connect(reader string) (CardConn, error)
	// Watch waits for the slot's event count to leave since, giving up after wait. A
	// zero wait answers at once.
	Watch(ctx context.Context, reader string, since uint32, wait time.Duration) (CardState, error)
}

// CardConn is one connection to one card. Several may share a card and the
// provider serialises them, T=1 being a single conversation whose sequence
// numbers both ends keep.
type CardConn interface {
	// ATR is the card's answer to reset.
	ATR() []byte
	Protocol() string
	// MaxInput is the longest command APDU this reader carries, zero when it does
	// not say.
	MaxInput() int
	// Transmit sends one command APDU and returns the response with its status
	// bytes.
	Transmit(send []byte) ([]byte, error)
	// BeginTransaction claims the card until EndTransaction.
	BeginTransaction() error
	// EndTransaction releases the claim, applying disposition to the card.
	EndTransaction(disposition string) error
	// Close ends the connection, applying disposition to the card.
	Close(disposition string) error
}

// CardState is what a slot looks like from outside.
type CardState struct {
	// Events counts the insertions and removals the slot has seen.
	Events uint32
	// Changed says the count moved while the caller waited, as against the wait
	// running out.
	Changed bool
	Present bool
	ATR     []byte
	// Sharing is how many connections hold the card, or -1 for an exclusive one.
	Sharing int
}

// CardInfo is one slot.
type CardInfo struct {
	Reader  string
	Backend string
	Board   string
}

// handleCards lists the card slots. Nothing is opened, so nothing here says whether
// a card is in one.
//
//	@Summary		The card slots
//	@Description	Every card slot the attached boards carry.
//	@Tags			cards
//	@Produce		json
//	@Success		200	{array}	hostapi.Card
//	@Router			/cards [get]
func (s *Server) handleCards(writer http.ResponseWriter, _ *http.Request) {
	readers, err := s.options.Cards.Readers()
	if err != nil {
		fail(writer, http.StatusInternalServerError, hostapi.CodeInternal, err.Error())
		return
	}
	listing := make([]hostapi.Card, 0, len(readers))
	for _, reader := range readers {
		listing = append(listing, hostapi.Card{
			ID:      reader.Reader,
			Reader:  reader.Reader,
			Backend: reader.Backend,
			Board:   reader.Board,
		})
	}
	writeJSON(writer, http.StatusOK, listing)
}

// handleCard reports one slot, reading it only when asked: reading means opening the
// board, and drawing a list of readers should not power up every tuner in the house.
//
//	@Summary		One card slot
//	@Description	With `probe=1` the slot is really read, which says whether a card is in it rather than whether the board has one.
//	@Tags			cards
//	@Produce		json
//	@Param			id		path		string	true	"the slot, as a listing names it"
//	@Param			probe	query		bool	false	"read the slot to find out whether a card is in it"
//	@Success		200		{object}	hostapi.Card
//	@Failure		404		{object}	hostapi.Failure	"no slot of that name"
//	@Router			/cards/{id} [get]
func (s *Server) handleCard(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	readers, err := s.options.Cards.Readers()
	if err != nil {
		fail(writer, http.StatusInternalServerError, hostapi.CodeInternal, err.Error())
		return
	}
	for _, reader := range readers {
		if reader.Reader != id {
			continue
		}
		described := hostapi.Card{
			ID:      reader.Reader,
			Reader:  reader.Reader,
			Backend: reader.Backend,
			Board:   reader.Board,
		}
		if request.URL.Query().Get("probe") == "1" {
			present, err := s.options.Cards.Present(id)
			if err != nil {
				fail(writer, http.StatusBadGateway, hostapi.CodeCardError, err.Error())
				return
			}
			described.Present = &present
		}
		writeJSON(writer, http.StatusOK, described)
		return
	}
	fail(writer, http.StatusNotFound, hostapi.CodeUnknownTuner, "no card reader is called "+id)
}

// handleCardSession resets the card and sends the commands that follow. One request
// is one conversation on purpose: the card counts its messages from the reset, so
// spreading an exchange across requests would make the daemon remember which client
// was mid-conversation -- exactly the state it is built not to keep.
//
//	@Summary		One reset and the commands after it
//	@Description	A card is stateful: what a command answers depends on what came before it. So a session is one reset and the run of commands that follows, carried out with nothing else in between.
//	@Tags			cards
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string				true	"the slot, as a listing names it"
//	@Param			session	body		hostapi.CardRequest	true	"the commands to send after the reset"
//	@Success		200		{object}	hostapi.CardSession
//	@Failure		400		{object}	hostapi.Failure	"the request could not be understood"
//	@Failure		404		{object}	hostapi.Failure	"no slot of that name"
//	@Failure		502		{object}	hostapi.Failure	"the card did not answer"
//	@Router			/cards/{id}/session [post]
func (s *Server) handleCardSession(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")

	if !jsonBody(writer, request) {
		return
	}
	var body hostapi.CardRequest
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxCardBody)).Decode(&body); err != nil {
		fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter, "the request body is not a card request: "+err.Error())
		return
	}
	apdus := make([][]byte, 0, len(body.APDUs))
	for _, written := range body.APDUs {
		apdu, err := parseAPDU(written)
		if err != nil {
			fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter, err.Error())
			return
		}
		apdus = append(apdus, apdu)
	}

	atr, responses, err := s.options.Cards.Session(id, apdus)
	if err != nil && len(responses) == 0 {
		fail(writer, http.StatusBadGateway, hostapi.CodeCardError, err.Error())
		return
	}

	answer := hostapi.CardSession{ATR: formatHex(atr)}
	for index, response := range responses {
		exchange := hostapi.Exchange{
			Command:  formatHex(apdus[index]),
			Response: formatHex(response),
		}
		if len(response) >= 2 {
			exchange.Status = formatHex(response[len(response)-2:])
		}
		answer.Exchanges = append(answer.Exchanges, exchange)
	}
	if err != nil {
		// Part of the conversation happened, and a caller working out how far it got
		// needs both halves.
		writeJSON(writer, http.StatusBadGateway, hostapi.Failure{
			Error: hostapi.Problem{Code: hostapi.CodeCardError, Message: err.Error()},
		})
		return
	}
	writeJSON(writer, http.StatusOK, answer)
}

// maxCardBody bounds a card session's body.
const maxCardBody = 1 << 20

// parseAPDU reads one hexadecimal command, ignoring separators.
func parseAPDU(written string) ([]byte, error) {
	cleaned := strings.NewReplacer(" ", "", ":", "", "-", "", "_", "").Replace(written)
	if cleaned == "" {
		return nil, fmt.Errorf("an empty command APDU")
	}
	if len(cleaned)%2 != 0 {
		return nil, fmt.Errorf("command APDU %q has an odd number of hexadecimal digits", written)
	}
	apdu, err := hex.DecodeString(cleaned)
	if err != nil {
		return nil, fmt.Errorf("command APDU %q: %w", written, err)
	}
	return apdu, nil
}

// formatHex renders bytes the way card documentation writes them.
func formatHex(data []byte) string {
	parts := make([]string, len(data))
	for index, value := range data {
		parts[index] = fmt.Sprintf("%02X", value)
	}
	return strings.Join(parts, " ")
}

// handleCardConnect opens a card and hands it to the connection this request
// arrived on.
//
//	@Summary		Open a card
//	@Description	Opens the card in a slot and holds it for the HTTP connection this request arrived on, which is what a client speaking PC/SC needs: a run of commands where each depends on the last, with no reset in between. The card is given up when the connection closes, however it closes, so there is nothing to expire and nothing to clean up after a client that vanished. One connection holds one card; a client wanting two opens two connections.
//	@Tags			cards
//	@Produce		json
//	@Param			id	path		string	true	"the slot, as a listing names it"
//	@Success		200	{object}	hostapi.CardConnection
//	@Failure		404	{object}	hostapi.Failure	"no slot of that name"
//	@Failure		409	{object}	hostapi.Failure	"this connection already holds a card"
//	@Failure		502	{object}	hostapi.Failure	"the card did not answer"
//	@Router			/cards/{id}/connect [post]
func (s *Server) handleCardConnect(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	holder := cardsHeld(request)
	if holder == nil {
		fail(writer, http.StatusInternalServerError, hostapi.CodeInternal,
			"this server was not attached to its listener, so it cannot hold a card")
		return
	}

	card, err := s.options.Cards.Connect(id)
	if err != nil {
		s.failCard(writer, id, err)
		return
	}
	if !holder.take(id, card) {
		_ = card.Close(dispositionLeave)
		fail(writer, http.StatusConflict, hostapi.CodeCardError,
			"this connection already holds a card; open another connection for a second one")
		return
	}
	writeJSON(writer, http.StatusOK, hostapi.CardConnection{
		ATR:      formatHex(card.ATR()),
		Protocol: card.Protocol(),
		MaxInput: card.MaxInput(),
	})
}

// handleCardDisconnect gives the card back before the connection closes, for a
// client that wants to open another without dropping its connection.
//
//	@Summary		Give a card back
//	@Description	Releases the card this connection holds, ending a transaction first if one was open. Closing the connection does the same, so this is for a client that means to carry on using it.
//	@Tags			cards
//	@Produce		json
//	@Param			id			path		string	true	"the slot, as a listing names it"
//	@Param			disposition	query		string	false	"what to do with the card: leave, reset, unpower or eject"	Enums(leave, reset, unpower, eject)
//	@Success		200			{object}	hostapi.CardRelease
//	@Failure		400			{object}	hostapi.Failure	"no such disposition"
//	@Router			/cards/{id}/connect [delete]
func (s *Server) handleCardDisconnect(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	wanted, ok := disposition(request)
	if !ok {
		fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter,
			"disposition must be leave, reset, unpower or eject")
		return
	}
	holder := cardsHeld(request)
	if holder == nil {
		writeJSON(writer, http.StatusOK, hostapi.CardRelease{})
		return
	}
	if _, held := holder.on(id); !held {
		// Asking twice is how a client tidying up behaves, and what it wanted is
		// already true.
		writeJSON(writer, http.StatusOK, hostapi.CardRelease{})
		return
	}
	writeJSON(writer, http.StatusOK, hostapi.CardRelease{Released: holder.release(wanted)})
}

// handleCardTransmit sends one command to the card this connection holds.
//
//	@Summary		Send one command
//	@Description	Sends one command APDU to the card this connection holds and returns what it said, including the two status bytes. The card must have been opened on this connection first.
//	@Tags			cards
//	@Accept			json
//	@Produce		json
//	@Param			id			path		string						true	"the slot, as a listing names it"
//	@Param			transmit	body		hostapi.CardTransmitRequest	true	"the command to send"
//	@Success		200			{object}	hostapi.CardTransmitResult
//	@Failure		400			{object}	hostapi.Failure	"the request could not be understood"
//	@Failure		409			{object}	hostapi.Failure	"this connection holds no card in that slot"
//	@Failure		502			{object}	hostapi.Failure	"the card did not answer"
//	@Router			/cards/{id}/transmit [post]
func (s *Server) handleCardTransmit(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	holder := cardsHeld(request)
	if holder == nil {
		noCardHeld(writer, id)
		return
	}
	card, held := holder.on(id)
	if !held {
		noCardHeld(writer, id)
		return
	}

	if !jsonBody(writer, request) {
		return
	}
	var body hostapi.CardTransmitRequest
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxCardBody)).Decode(&body); err != nil {
		fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter,
			"the request body is not a command: "+err.Error())
		return
	}
	apdu, err := parseAPDU(body.APDU)
	if err != nil {
		fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter, err.Error())
		return
	}

	response, err := card.Transmit(apdu)
	if err != nil {
		s.failCard(writer, id, err)
		return
	}
	answer := hostapi.CardTransmitResult{
		Response: formatHex(response),
		Protocol: card.Protocol(),
	}
	if len(response) >= 2 {
		answer.Status = formatHex(response[len(response)-2:])
	}
	writeJSON(writer, http.StatusOK, answer)
}

// handleCardBeginTransaction claims the card against the other connections
// sharing it.
//
//	@Summary		Claim the card
//	@Description	Holds the card against every other connection sharing it until the transaction is ended. Ending the HTTP connection ends it too, so a client that dies mid-transaction does not leave the card claimed.
//	@Tags			cards
//	@Produce		json
//	@Param			id	path		string	true	"the slot, as a listing names it"
//	@Success		200	{object}	hostapi.CardRelease
//	@Failure		409	{object}	hostapi.Failure	"this connection holds no card, or already has a transaction"
//	@Failure		502	{object}	hostapi.Failure	"the card did not answer"
//	@Router			/cards/{id}/transaction [post]
func (s *Server) handleCardBeginTransaction(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	holder := cardsHeld(request)
	if holder == nil {
		noCardHeld(writer, id)
		return
	}
	if _, held := holder.on(id); !held {
		noCardHeld(writer, id)
		return
	}
	card, beginning := holder.beginning()
	if !beginning {
		fail(writer, http.StatusConflict, hostapi.CodeCardError,
			"this connection already has a transaction on "+id)
		return
	}
	if err := card.BeginTransaction(); err != nil {
		// The claim was not taken, so the bookkeeping must not say it was.
		holder.ending()
		s.failCard(writer, id, err)
		return
	}
	writeJSON(writer, http.StatusOK, hostapi.CardRelease{Released: true})
}

// handleCardEndTransaction gives the claim back.
//
//	@Summary		Release the claim
//	@Description	Ends the transaction this connection has on the card, applying a disposition to it.
//	@Tags			cards
//	@Produce		json
//	@Param			id			path		string	true	"the slot, as a listing names it"
//	@Param			disposition	query		string	false	"what to do with the card: leave, reset, unpower or eject"	Enums(leave, reset, unpower, eject)
//	@Success		200			{object}	hostapi.CardRelease
//	@Failure		400			{object}	hostapi.Failure	"no such disposition"
//	@Failure		502			{object}	hostapi.Failure	"the card did not answer"
//	@Router			/cards/{id}/transaction [delete]
func (s *Server) handleCardEndTransaction(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	wanted, ok := disposition(request)
	if !ok {
		fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter,
			"disposition must be leave, reset, unpower or eject")
		return
	}
	holder := cardsHeld(request)
	if holder == nil {
		writeJSON(writer, http.StatusOK, hostapi.CardRelease{})
		return
	}
	if _, held := holder.on(id); !held {
		writeJSON(writer, http.StatusOK, hostapi.CardRelease{})
		return
	}
	card, ending := holder.ending()
	if !ending {
		writeJSON(writer, http.StatusOK, hostapi.CardRelease{})
		return
	}
	if err := card.EndTransaction(wanted); err != nil {
		// The claim is gone either way -- a failed disposition must not leave every
		// other connection blocked on a card nobody is talking to.
		s.failCard(writer, id, err)
		return
	}
	writeJSON(writer, http.StatusOK, hostapi.CardRelease{Released: true})
}

// handleCardEvents waits for the slot to change, which is what a client
// standing in for SCardGetStatusChange needs.
//
//	@Summary		Wait for the slot to change
//	@Description	Answers when the slot's event count leaves `since`, or when `wait` runs out. This is what a client watching for a card being seated or removed asks, instead of polling the daemon: the polling that has to happen belongs next to the hardware, and a slot is only watched while somebody is waiting on it. A zero `wait` answers at once with the state as it stands.
//	@Tags			cards
//	@Produce		json
//	@Param			id		path		string	true	"the slot, as a listing names it"
//	@Param			since	query		integer	false	"the event count the caller last saw; the answer waits until it moves"
//	@Param			wait	query		number	false	"how many seconds to wait, up to the daemon's own limit; zero answers at once"
//	@Success		200		{object}	hostapi.CardState
//	@Failure		400		{object}	hostapi.Failure	"since or wait could not be read"
//	@Failure		404		{object}	hostapi.Failure	"no slot of that name"
//	@Failure		502		{object}	hostapi.Failure	"the slot could not be read"
//	@Router			/cards/{id}/events [get]
func (s *Server) handleCardEvents(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	query := request.URL.Query()

	var since uint64
	if written := query.Get("since"); written != "" {
		parsed, err := strconv.ParseUint(written, 10, 32)
		if err != nil {
			fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter,
				"since must be a whole number of events")
			return
		}
		since = parsed
	}

	wait := maxCardWait
	if written := query.Get("wait"); written != "" {
		seconds, err := strconv.ParseFloat(written, 64)
		// Not a number compares false with everything, and so passes "not
		// negative"; it is refused along with the infinities.
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
			fail(writer, http.StatusBadRequest, hostapi.CodeInvalidParameter,
				"wait must be a number of seconds, and not a negative one")
			return
		}
		// Bounded before it becomes a Duration: a number of seconds too large
		// for one would come out negative.
		if seconds < maxCardWait.Seconds() {
			wait = time.Duration(seconds * float64(time.Second))
		}
	}

	state, err := s.options.Cards.Watch(request.Context(), id, uint32(since), wait)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// The client went away mid-wait, the ordinary end of a long poll.
			return
		}
		s.failCard(writer, id, err)
		return
	}
	writeJSON(writer, http.StatusOK, hostapi.CardState{
		Events:  int(state.Events),
		Changed: state.Changed,
		Present: state.Present,
		ATR:     formatHex(state.ATR),
		Sharing: state.Sharing,
	})
}

// maxCardWait bounds a wait for the slot to change. A wait holds the board open,
// since opening it per poll is what powers a card up and down; bounding it puts a
// ceiling on how long an idle watcher keeps the hardware awake.
const maxCardWait = 30 * time.Second

// failCard reports a card failure as the kind of problem it is: a slot that is
// not there is the caller's mistake, anything else is the card's.
func (s *Server) failCard(writer http.ResponseWriter, id string, err error) {
	if readers, listErr := s.options.Cards.Readers(); listErr == nil {
		for _, reader := range readers {
			if reader.Reader == id {
				fail(writer, http.StatusBadGateway, hostapi.CodeCardError, err.Error())
				return
			}
		}
		fail(writer, http.StatusNotFound, hostapi.CodeUnknownTuner, "no card reader is called "+id)
		return
	}
	fail(writer, http.StatusBadGateway, hostapi.CodeCardError, err.Error())
}
