// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Till0196/coax/pkg/host"
)

// runCard talks to a card in one of the boards. A reset and the commands after
// it go over as one request because they are one conversation: the card counts
// its messages from the reset, so another program's command landing in the
// middle would break both. Hence nothing here to open or close.
func runCard(arguments []string, stdout, stderr io.Writer) error {
	command := newHardwareCommand("card", stderr)
	flags := command.flags
	reader := flags.String("reader", "", "which card reader to use, as `coax card --list` names it")
	list := flags.Bool("list", false, "list the card readers without touching a card")
	statusOnly := flags.Bool("status", false, "report whether a card is seated, without resetting it")
	asJSON := flags.Bool("json", false, "report as JSON instead of a table")
	if err := command.parse(arguments); err != nil {
		return err
	}
	apdus := flags.Args()
	if *statusOnly && len(apdus) != 0 {
		return fmt.Errorf("--status reports the slot only; drop it to send a command")
	}
	if *list && len(apdus) != 0 {
		return fmt.Errorf("--list reports the readers only; drop it to send a command")
	}

	ctx := context.Background()
	client, err := command.dial(ctx)
	if err != nil {
		return err
	}
	cards, err := client.Cards(ctx)
	if err != nil {
		return err
	}
	if len(cards) == 0 {
		return fmt.Errorf("no board with a card reader is attached")
	}

	if *list {
		return writeCards(stdout, cards, *asJSON)
	}

	chosen, err := chooseReader(cards, *reader)
	if err != nil {
		return err
	}

	if *statusOnly {
		card, err := client.Card(ctx, chosen, true)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(stdout, card)
		}
		seated := "absent"
		if card.Present != nil && *card.Present {
			seated = "present"
		}
		_, err = fmt.Fprintf(stdout, "Reader:  %s\nCard:    %s\n", card.Reader, seated)
		return err
	}

	session, err := client.CardSession(ctx, chosen, apdus)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, session)
	}
	if _, err := fmt.Fprintf(stdout, "Reader:  %s\nATR:     %s\n", chosen, session.ATR); err != nil {
		return err
	}
	for _, exchange := range session.Exchanges {
		if _, err := fmt.Fprintf(stdout, "\nCommand:  %s\nResponse: %s\n", exchange.Command, exchange.Response); err != nil {
			return err
		}
	}
	return nil
}

// chooseReader settles which slot to use. One attached reader needs no choosing;
// guessing between several would touch a card the caller did not mean.
func chooseReader(cards []host.Card, named string) (string, error) {
	if named != "" {
		for _, card := range cards {
			if card.ID == named || card.Reader == named {
				return card.ID, nil
			}
		}
		return "", fmt.Errorf("no card reader is called %q; run coax card --list to see them", named)
	}
	if len(cards) == 1 {
		return cards[0].ID, nil
	}
	names := make([]string, 0, len(cards))
	for _, card := range cards {
		names = append(names, card.Reader)
	}
	return "", fmt.Errorf("there are %d card readers; name one with --reader:\n  %s",
		len(cards), strings.Join(names, "\n  "))
}

func writeCards(stdout io.Writer, cards []host.Card, asJSON bool) error {
	if asJSON {
		return writeJSON(stdout, cards)
	}
	table := newTable(stdout)
	fmt.Fprintln(table, "READER\tBACKEND\tBOARD")
	for _, card := range cards {
		fmt.Fprintf(table, "%s\t%s\t%s\n", card.Reader, card.Backend, card.Board)
	}
	return table.Flush()
}
