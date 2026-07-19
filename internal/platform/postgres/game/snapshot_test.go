package gamepostgres

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestGameSnapshotJSONTopLevelMatchesWebContract(t *testing.T) {
	t.Parallel()

	snapshot := GameSnapshot{
		Markets:         []SnapshotMarket{},
		OpenOrders:      []SnapshotOpenOrder{},
		Trades:          []SnapshotTrade{},
		Inventory:       []InventoryPosition{},
		Facilities:      []SnapshotFacility{},
		ProductionTrace: []ProductionTrace{},
		Freight:         []FreightShipment{},
		Devices:         []SnapshotDevice{},
		Panels:          []DevicePanel{},
		Chat:            []ChatMessage{},
		Alerts:          []SnapshotAlert{},
		OperatorAudit:   []AuditEntry{},
		Indices:         []MarketIndex{},
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	want := []string{
		"actor", "alerts", "chat", "company", "devices", "facilities", "freight",
		"indices", "inventory", "markets", "membership", "openOrders", "operatorAudit",
		"panels", "productionTrace", "systemTime", "trades",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot keys = %#v, want %#v", got, want)
	}
	for _, arrayKey := range []string{
		"alerts", "chat", "devices", "facilities", "freight", "indices", "inventory",
		"markets", "openOrders", "operatorAudit", "panels", "productionTrace", "trades",
	} {
		if string(object[arrayKey]) != "[]" {
			t.Fatalf("%s encoded as %s, want []", arrayKey, object[arrayKey])
		}
	}
}

func TestSnapshotStatusMappingsStayInsideFrontendUnions(t *testing.T) {
	t.Parallel()

	if snapshotFacilityStatus("maintenance") != "offline" {
		t.Fatal("maintenance facility was not mapped to offline")
	}
	if snapshotDeliveryStatus("scheduled") != "ready" {
		t.Fatal("scheduled delivery was not mapped to ready")
	}
	if snapshotDeviceStatus("registered") != "online" {
		t.Fatal("registered device was not mapped to online")
	}
	if snapshotAuditOutcome("committed", "operator.compensate") != "compensated" {
		t.Fatal("operator compensation outcome was not mapped")
	}
}

func TestMarketIndicesDeriveFromMarketProjection(t *testing.T) {
	t.Parallel()

	indices := marketIndices([]SnapshotMarket{{
		Location: "Lunar orbit", Commodity: "Liquid oxygen", LastPrice: 521.35, Change24Hour: 0.8,
	}})
	if len(indices) != 1 || indices[0].Name != "Lunar orbit Liquid oxygen" || indices[0].Value <= 0 || indices[0].Change != 0.8 {
		t.Fatalf("indices = %#v", indices)
	}
}
