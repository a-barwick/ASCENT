package ids

import "testing"

func TestNewUUID(t *testing.T) {
	t.Parallel()

	first, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if !IsUUID(first) || !IsUUID(second) {
		t.Fatalf("invalid UUIDs: %q %q", first, second)
	}
	if first == second {
		t.Fatal("generated duplicate UUIDs")
	}
}

func TestIsUUIDRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "not-a-uuid", "00000000-0000-0000-0000-00000000000z"} {
		if IsUUID(value) {
			t.Fatalf("accepted malformed UUID %q", value)
		}
	}
}
