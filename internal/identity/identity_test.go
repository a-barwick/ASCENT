package identity

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const testSecret = "test-only-development-session-key-0001"

func TestDevelopmentSessionRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Date(2077, 5, 24, 14, 38, 4, 0, time.UTC)
	provider, err := NewDevelopmentProvider(true, testSecret, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	input := Actor{ID: "player-helios", DisplayName: "Ari Chen"}
	token, err := provider.Issue(input, now)
	if err != nil {
		t.Fatal(err)
	}
	output, err := provider.Resolve(token, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if output != input {
		t.Fatalf("actor = %#v, want %#v", output, input)
	}
}

func TestDevelopmentSessionRejectsTamperAndExpiry(t *testing.T) {
	t.Parallel()

	now := time.Date(2077, 5, 24, 14, 38, 4, 0, time.UTC)
	provider, err := NewDevelopmentProvider(true, testSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := provider.Issue(Actor{ID: "player-helios", DisplayName: "Ari Chen"}, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	tampered := parts[0] + "." + "x" + parts[1][1:]
	if _, err := provider.Resolve(tampered, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered error = %v, want ErrInvalid", err)
	}
	if _, err := provider.Resolve(token, now.Add(time.Minute)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired error = %v, want ErrExpired", err)
	}
}

func TestDevelopmentIdentityMustBeExplicitlyEnabled(t *testing.T) {
	t.Parallel()

	provider, err := NewDevelopmentProvider(false, "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Issue(Actor{ID: "player", DisplayName: "Player"}, time.Now()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("issue error = %v, want ErrDisabled", err)
	}
	if _, err := provider.Resolve("anything", time.Now()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("resolve error = %v, want ErrDisabled", err)
	}
}

func TestDevelopmentSessionRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	provider, err := NewDevelopmentProvider(true, testSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "missing-dot", "a.b.c", "e30.bad"} {
		if _, err := provider.Resolve(token, time.Now()); err == nil {
			t.Fatalf("Resolve(%q) succeeded", token)
		}
	}
}
