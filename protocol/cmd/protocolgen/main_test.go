package main

import (
	"strings"
	"testing"
)

func TestPascal(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"commandId":        "CommandId",
		"protocol_version": "ProtocolVersion",
		"occurred-at":      "OccurredAt",
	}
	for input, want := range cases {
		if got := pascal(input); got != want {
			t.Fatalf("pascal(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGenerateIncludesVersionAndDefinitions(t *testing.T) {
	t.Parallel()

	doc := document{
		Version: "9.8.7",
		Defs: map[string]schema{
			"State": {
				Type: "string",
				Enum: []string{"ready"},
			},
			"Envelope": {
				Type:     "object",
				Required: []string{"state"},
				Properties: map[string]schema{
					"state": {Ref: "#/$defs/State"},
				},
			},
		},
	}

	goSource, err := generateGo(doc)
	if err != nil {
		t.Fatal(err)
	}
	tsSource, err := generateTypeScript(doc)
	if err != nil {
		t.Fatal(err)
	}

	for _, generated := range []string{string(goSource), string(tsSource)} {
		if !strings.Contains(generated, "9.8.7") {
			t.Fatalf("generated source did not include protocol version:\n%s", generated)
		}
		if !strings.Contains(generated, "Envelope") {
			t.Fatalf("generated source did not include Envelope:\n%s", generated)
		}
	}
	if strings.HasSuffix(string(tsSource), "\n\n") {
		t.Fatal("generated TypeScript has an extra blank line at EOF")
	}
}
