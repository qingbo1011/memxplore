// Package auth defines scoped daemon credentials independently of HTTP and storage.
package auth

import (
	"fmt"
	"slices"
	"time"

	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
)

// Scope is one protocol capability granted to a credential.
type Scope string

const (
	ScopeMemoryRead  Scope = "memory:read"
	ScopeMemoryWrite Scope = "memory:write"
	ScopeMemoryPurge Scope = "memory:purge"
	ScopeAdmin       Scope = "admin"
)

// TokenSpec is persisted without raw token material.
type TokenSpec struct {
	ID            domain.ID   `json:"id"`
	PrincipalID   domain.ID   `json:"principal_id"`
	Namespace     domain.ID   `json:"namespace"`
	PrivateOwners []domain.ID `json:"private_owners"`
	Scopes        []Scope     `json:"scopes"`
	AllowShared   bool        `json:"allow_shared"`
	AllowPublic   bool        `json:"allow_public"`
	ExpiresAt     *time.Time  `json:"expires_at,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
}

// Principal is the authenticated authorization result used by application services.
type Principal struct {
	TokenID       domain.ID
	PrincipalID   domain.ID
	Namespace     domain.ID
	PrivateOwners []domain.ID
	Scopes        []Scope
	AllowShared   bool
	AllowPublic   bool
}

// Validate checks least-privilege credential structure.
func (s TokenSpec) Validate() error {
	if s.ID == "" || s.PrincipalID == "" || s.Namespace == "" || len(s.PrivateOwners) == 0 || len(s.Scopes) == 0 || s.CreatedAt.IsZero() {
		return fmt.Errorf("token id, principal, namespace, private owners, scopes, and creation time are required")
	}
	if s.ExpiresAt != nil && !s.ExpiresAt.After(s.CreatedAt) {
		return fmt.Errorf("token expiry must follow creation")
	}
	owners := make(map[domain.ID]struct{}, len(s.PrivateOwners))
	for _, owner := range s.PrivateOwners {
		if owner == "" {
			return fmt.Errorf("token private owner cannot be empty")
		}
		if _, duplicate := owners[owner]; duplicate {
			return fmt.Errorf("token contains duplicate private owner %s", owner)
		}
		owners[owner] = struct{}{}
	}
	scopes := make(map[Scope]struct{}, len(s.Scopes))
	for _, scope := range s.Scopes {
		switch scope {
		case ScopeMemoryRead, ScopeMemoryWrite, ScopeMemoryPurge, ScopeAdmin:
		default:
			return fmt.Errorf("token scope %q is invalid", scope)
		}
		if _, duplicate := scopes[scope]; duplicate {
			return fmt.Errorf("token contains duplicate scope %q", scope)
		}
		scopes[scope] = struct{}{}
	}
	return nil
}

// HasScope checks a capability; admin implies every protocol scope.
func (p Principal) HasScope(scope Scope) bool {
	return slices.Contains(p.Scopes, ScopeAdmin) || slices.Contains(p.Scopes, scope)
}

// AccessScope converts authentication into the retrieval authorization contract.
func (p Principal) AccessScope() application.AccessScope {
	return application.AccessScope{
		PrincipalID: p.PrincipalID, Namespace: p.Namespace,
		PrivateOwners: append([]domain.ID(nil), p.PrivateOwners...),
		AllowShared:   p.AllowShared, AllowPublic: p.AllowPublic,
	}
}

// DomainScope constructs an actor-bearing scope after checking the selected owner and subject.
func (p Principal) DomainScope(owner, subject, context domain.ID, visibility domain.Visibility) (domain.Scope, error) {
	if !slices.Contains(p.PrivateOwners, owner) {
		return domain.Scope{}, fmt.Errorf("principal is not authorized for owner %s", owner)
	}
	scope := domain.Scope{
		Namespace: p.Namespace, Owner: owner, Subject: subject, Actor: p.PrincipalID,
		Context: context, Visibility: visibility,
	}
	if err := scope.Validate(); err != nil {
		return domain.Scope{}, err
	}
	return scope, nil
}
