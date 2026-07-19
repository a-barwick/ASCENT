// Package identity defines the player identity boundary used by HTTP and
// command handlers. Development sessions are signed, short lived, and carry no
// password or authoritative company permissions.
package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrDisabled  = errors.New("development identity is disabled")
	ErrMalformed = errors.New("session is malformed")
	ErrExpired   = errors.New("session has expired")
	ErrInvalid   = errors.New("session signature is invalid")
)

const developmentSessionVersion = 1

// Actor is the authenticated player identity. Company authority is deliberately
// resolved by the companies module for every command.
type Actor struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// Provider is implemented by development identity today and by a production
// external identity adapter later.
type Provider interface {
	Resolve(token string, now time.Time) (Actor, error)
}

type claims struct {
	Version     int    `json:"v"`
	ActorID     string `json:"sub"`
	DisplayName string `json:"name"`
	ExpiresAt   int64  `json:"exp"`
}

// DevelopmentProvider issues HMAC-signed local sessions. It is inert unless
// constructed with enabled=true by the process entrypoint.
type DevelopmentProvider struct {
	enabled  bool
	key      []byte
	lifetime time.Duration
}

func NewDevelopmentProvider(enabled bool, secret string, lifetime time.Duration) (*DevelopmentProvider, error) {
	if lifetime <= 0 {
		return nil, errors.New("session lifetime must be positive")
	}
	if enabled && len(secret) < 32 {
		return nil, errors.New("development session secret must be at least 32 bytes")
	}
	return &DevelopmentProvider{
		enabled:  enabled,
		key:      []byte(secret),
		lifetime: lifetime,
	}, nil
}

func (p *DevelopmentProvider) Enabled() bool {
	return p != nil && p.enabled
}

func (p *DevelopmentProvider) ExpiresAt(now time.Time) time.Time {
	if p == nil {
		return now
	}
	return now.Add(p.lifetime)
}

func (p *DevelopmentProvider) Issue(actor Actor, now time.Time) (string, error) {
	if !p.Enabled() {
		return "", ErrDisabled
	}
	if actor.ID == "" || actor.DisplayName == "" {
		return "", errors.New("actor id and display name are required")
	}
	payload, err := json.Marshal(claims{
		Version:     developmentSessionVersion,
		ActorID:     actor.ID,
		DisplayName: actor.DisplayName,
		ExpiresAt:   p.ExpiresAt(now).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("encode session: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + p.sign(encoded), nil
}

func (p *DevelopmentProvider) Resolve(token string, now time.Time) (Actor, error) {
	if !p.Enabled() {
		return Actor{}, ErrDisabled
	}
	encoded, suppliedSignature, ok := strings.Cut(token, ".")
	if !ok || encoded == "" || suppliedSignature == "" || strings.Contains(suppliedSignature, ".") {
		return Actor{}, ErrMalformed
	}
	expectedSignature := p.sign(encoded)
	if !hmac.Equal([]byte(suppliedSignature), []byte(expectedSignature)) {
		return Actor{}, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Actor{}, ErrMalformed
	}
	var session claims
	if err := json.Unmarshal(payload, &session); err != nil {
		return Actor{}, ErrMalformed
	}
	if session.Version != developmentSessionVersion || session.ActorID == "" || session.DisplayName == "" {
		return Actor{}, ErrMalformed
	}
	if !now.Before(time.Unix(session.ExpiresAt, 0)) {
		return Actor{}, ErrExpired
	}
	return Actor{ID: session.ActorID, DisplayName: session.DisplayName}, nil
}

func (p *DevelopmentProvider) sign(encoded string) string {
	mac := hmac.New(sha256.New, p.key)
	_, _ = mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
