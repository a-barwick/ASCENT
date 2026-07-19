package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()

	eventID := "019f78ce-8f6c-7ec1-a9da-27de13dea6db"
	commandID := "019f78ce-8f6c-7ec1-a9da-27de13dea6dc"
	topicSequence := int64(42)
	input := EventEnvelope{
		ProtocolVersion: Version,
		EventId:         &eventID,
		CommandId:       &commandID,
		Sequence:        1842902,
		TopicSequence:   &topicSequence,
		Topic:           "market:lunar:water",
		Type:            "TRADE_EXECUTED",
		OccurredAt:      time.Date(2077, 5, 24, 14, 38, 4, 182000000, time.UTC),
		Payload:         json.RawMessage(`{"price":308.25,"quantity":140}`),
	}

	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	var output EventEnvelope
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}

	if output.Sequence != input.Sequence || output.Topic != input.Topic || output.Type != input.Type {
		t.Fatalf("round trip mismatch: %#v != %#v", output, input)
	}
	if output.EventId == nil || *output.EventId != eventID {
		t.Fatalf("event ID mismatch: %#v", output.EventId)
	}
	if output.CommandId == nil || *output.CommandId != commandID {
		t.Fatalf("command ID mismatch: %#v", output.CommandId)
	}
	if output.TopicSequence == nil || *output.TopicSequence != topicSequence {
		t.Fatalf("topic sequence mismatch: %#v", output.TopicSequence)
	}
	if string(output.Payload) != string(input.Payload) {
		t.Fatalf("payload mismatch: %s != %s", output.Payload, input.Payload)
	}
}
