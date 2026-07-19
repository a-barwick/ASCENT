package gamepostgres

import (
	"reflect"
	"testing"
)

func TestFixedToDisplayAndPercentageChange(t *testing.T) {
	t.Parallel()

	if got := fixedToDisplay(30_825, 2); got != 308.25 {
		t.Fatalf("fixedToDisplay = %v, want 308.25", got)
	}
	if got := fixedToDisplay(8_420_000, 3); got != 8_420 {
		t.Fatalf("quantity display = %v, want 8420", got)
	}
	if got := percentageChange(29_740, 30_825); got < 3.64 || got > 3.66 {
		t.Fatalf("percentage change = %v, want about 3.65", got)
	}
}

func TestCompanyMetricsUseNormalLedgerBalances(t *testing.T) {
	t.Parallel()

	cash, assets, liabilities, netWorth, credit, ratio, rating := companyMetrics(financialPosition{
		Cash:        1_248_000_000_000,
		Assets:      4_739_000_000_000,
		Liabilities: 2_136_000_000_000,
	})
	if cash != 12_480_000_000 || assets != 47_390_000_000 || liabilities != 21_360_000_000 {
		t.Fatalf("ledger display totals = %v %v %v", cash, assets, liabilities)
	}
	if netWorth != 26_030_000_000 || credit <= 0 || ratio < 0.82 || ratio > 0.821 || rating != "A-" {
		t.Fatalf("company metrics = net %v credit %v ratio %v rating %q", netWorth, credit, ratio, rating)
	}
}

func TestPermissionsForOwnerWithOperatorGrant(t *testing.T) {
	t.Parallel()

	want := []string{
		"chat.send",
		"device.manage",
		"freight.deliver",
		"game.view",
		"market.trade",
		"operator.compensate",
		"production.run",
	}
	if got := permissionsFor("owner", true); !reflect.DeepEqual(got, want) {
		t.Fatalf("permissions = %#v, want %#v", got, want)
	}
}
