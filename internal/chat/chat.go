// Package chat owns validation for plain-text multiplayer messages. Delivery
// and history are intentionally isolated from economic transactions.
package chat

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxMessageRunes = 1000

var ErrInvalidMessage = errors.New("chat message must be non-empty plain text of at most 1000 characters")

func NormalizeMessage(input string) (string, error) {
	message := strings.TrimSpace(input)
	if message == "" || utf8.RuneCountInString(message) > MaxMessageRunes || !utf8.ValidString(message) {
		return "", ErrInvalidMessage
	}
	for _, character := range message {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return "", ErrInvalidMessage
		}
	}
	return message, nil
}
