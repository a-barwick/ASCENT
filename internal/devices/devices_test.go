package devices

import "testing"

func TestValidateDeviceAndPanelRoute(t *testing.T) {
	t.Parallel()

	if err := Validate("device-laptop", "Flight desk", ClassDesktop); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePanelRoute("/market/market-lunar-water?range=24h"); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsUnknownClassAndExternalPanelRoute(t *testing.T) {
	t.Parallel()

	if err := Validate("device", "Desk", Class("watch")); err == nil {
		t.Fatal("unknown class was accepted")
	}
	for _, route := range []string{"https://example.com", "//example.com", "/admin", ""} {
		if err := ValidatePanelRoute(route); err == nil {
			t.Fatalf("route %q was accepted", route)
		}
	}
}
