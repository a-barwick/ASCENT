package companies

import (
	"errors"
	"testing"
)

func TestDirectoryAuthorizesOnlyGrantedCompanyPermission(t *testing.T) {
	directory, err := NewDirectory(
		[]Company{
			{ID: "company-a", Name: "A"},
			{ID: "company-b", Name: "B"},
		},
		[]Membership{
			{PlayerID: "player-trader", CompanyID: "company-a", Roles: []Role{RoleTrader}},
			{PlayerID: "player-operator", CompanyID: "company-a", Roles: []Role{RoleOperator}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := directory.Authorize("player-trader", "company-a", PermissionTrade); err != nil {
		t.Fatalf("expected trade permission: %v", err)
	}
	if err := directory.Authorize("player-trader", "company-a", PermissionProduce); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected production to be denied, got %v", err)
	}
	if err := directory.Authorize("player-trader", "company-b", PermissionTrade); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected other company to be denied, got %v", err)
	}
	if err := directory.Authorize("player-operator", "company-a", PermissionCompensate); err != nil {
		t.Fatalf("expected operator compensation permission: %v", err)
	}
}

func TestOwnerCannotConstructOperatorCompensation(t *testing.T) {
	directory, err := NewDirectory(
		[]Company{{ID: "company-a", Name: "A"}},
		[]Membership{{PlayerID: "player-owner", CompanyID: "company-a", Roles: []Role{RoleOwner}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := directory.Authorize("player-owner", "company-a", PermissionCompensate); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected operator-only permission to be denied, got %v", err)
	}
}
