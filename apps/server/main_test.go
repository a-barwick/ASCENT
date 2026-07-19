package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestSeedWritesDeterministicFixture(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := filepath.Join(t.TempDir(), "first.json")
	second := filepath.Join(t.TempDir(), "second.json")

	if err := run([]string{"seed", "--output", first}, logger); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"seed", "--output", second}, logger); err != nil {
		t.Fatal(err)
	}

	firstContent, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondContent, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstContent) != string(secondContent) {
		t.Fatal("seed output was not deterministic")
	}
}

func TestUnknownCommandFailsSafely(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := run([]string{"unknown"}, logger); err == nil {
		t.Fatal("expected unknown command to fail")
	}
}
