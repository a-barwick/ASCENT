package chat

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeMessage(t *testing.T) {
	t.Parallel()

	got, err := NormalizeMessage("  price check\nwater?  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "price check\nwater?" {
		t.Fatalf("message = %q", got)
	}
}

func TestNormalizeMessageRejectsEmptyLongAndControlText(t *testing.T) {
	t.Parallel()

	for _, input := range []string{" \n ", strings.Repeat("x", MaxMessageRunes+1), "hello\x00world"} {
		if _, err := NormalizeMessage(input); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("NormalizeMessage(%q) error = %v", input, err)
		}
	}
}
