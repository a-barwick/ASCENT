// Package companies defines company membership and permission rules.
package companies

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrInvalidCompany    = errors.New("invalid company")
	ErrInvalidMembership = errors.New("invalid membership")
	ErrUnauthorized      = errors.New("company permission denied")
)

type Permission string

const (
	PermissionView       Permission = "view"
	PermissionTrade      Permission = "trade"
	PermissionProduce    Permission = "produce"
	PermissionFreight    Permission = "freight"
	PermissionCompensate Permission = "compensate"
)

type Role string

const (
	RoleOwner     Role = "owner"
	RoleTrader    Role = "trader"
	RoleProducer  Role = "producer"
	RoleLogistics Role = "logistics"
	RoleAuditor   Role = "auditor"
	RoleOperator  Role = "operator"
)

type Company struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Membership struct {
	PlayerID  string `json:"playerId"`
	CompanyID string `json:"companyId"`
	Roles     []Role `json:"roles"`
}

// Directory is an immutable value after construction. Snapshot methods return
// copies so callers cannot change authorization state behind a command.
type Directory struct {
	companies   map[string]Company
	memberships map[string]map[string]map[Role]struct{}
}

func NewDirectory(companyList []Company, membershipList []Membership) (Directory, error) {
	directory := Directory{
		companies:   make(map[string]Company, len(companyList)),
		memberships: make(map[string]map[string]map[Role]struct{}),
	}

	for _, company := range companyList {
		if company.ID == "" || company.Name == "" {
			return Directory{}, fmt.Errorf("%w: id and name are required", ErrInvalidCompany)
		}
		if _, exists := directory.companies[company.ID]; exists {
			return Directory{}, fmt.Errorf("%w: duplicate id %q", ErrInvalidCompany, company.ID)
		}
		directory.companies[company.ID] = company
	}

	for _, membership := range membershipList {
		if membership.PlayerID == "" || membership.CompanyID == "" || len(membership.Roles) == 0 {
			return Directory{}, fmt.Errorf("%w: player, company, and role are required", ErrInvalidMembership)
		}
		if _, exists := directory.companies[membership.CompanyID]; !exists {
			return Directory{}, fmt.Errorf("%w: unknown company %q", ErrInvalidMembership, membership.CompanyID)
		}
		companyMemberships := directory.memberships[membership.PlayerID]
		if companyMemberships == nil {
			companyMemberships = make(map[string]map[Role]struct{})
			directory.memberships[membership.PlayerID] = companyMemberships
		}
		roles := companyMemberships[membership.CompanyID]
		if roles == nil {
			roles = make(map[Role]struct{})
			companyMemberships[membership.CompanyID] = roles
		}
		for _, role := range membership.Roles {
			if !validRole(role) {
				return Directory{}, fmt.Errorf("%w: unknown role %q", ErrInvalidMembership, role)
			}
			roles[role] = struct{}{}
		}
	}

	return directory, nil
}

func (d Directory) Authorize(playerID, companyID string, permission Permission) error {
	if !validPermission(permission) {
		return fmt.Errorf("%w: unknown permission %q", ErrUnauthorized, permission)
	}
	companyMemberships := d.memberships[playerID]
	roles := companyMemberships[companyID]
	for role := range roles {
		if roleAllows(role, permission) {
			return nil
		}
	}
	return fmt.Errorf("%w: player %q lacks %q for company %q", ErrUnauthorized, playerID, permission, companyID)
}

func (d Directory) Allows(playerID, companyID string, permission Permission) bool {
	return d.Authorize(playerID, companyID, permission) == nil
}

func (d Directory) Companies() []Company {
	result := make([]Company, 0, len(d.companies))
	for _, company := range d.companies {
		result = append(result, company)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

func (d Directory) Memberships(playerID string) []Membership {
	companyMemberships := d.memberships[playerID]
	result := make([]Membership, 0, len(companyMemberships))
	for companyID, roleSet := range companyMemberships {
		roles := make([]Role, 0, len(roleSet))
		for role := range roleSet {
			roles = append(roles, role)
		}
		sort.Slice(roles, func(i, j int) bool {
			return roles[i] < roles[j]
		})
		result = append(result, Membership{
			PlayerID:  playerID,
			CompanyID: companyID,
			Roles:     roles,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CompanyID < result[j].CompanyID
	})
	return result
}

func validPermission(permission Permission) bool {
	switch permission {
	case PermissionView, PermissionTrade, PermissionProduce, PermissionFreight, PermissionCompensate:
		return true
	default:
		return false
	}
}

func validRole(role Role) bool {
	switch role {
	case RoleOwner, RoleTrader, RoleProducer, RoleLogistics, RoleAuditor, RoleOperator:
		return true
	default:
		return false
	}
}

func roleAllows(role Role, permission Permission) bool {
	switch role {
	case RoleOwner:
		return permission != PermissionCompensate
	case RoleTrader:
		return permission == PermissionView || permission == PermissionTrade
	case RoleProducer:
		return permission == PermissionView || permission == PermissionProduce
	case RoleLogistics:
		return permission == PermissionView || permission == PermissionFreight
	case RoleAuditor:
		return permission == PermissionView
	case RoleOperator:
		return true
	default:
		return false
	}
}
