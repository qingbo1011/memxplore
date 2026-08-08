package domain

import "fmt"

// ID is an opaque stable identifier. It deliberately carries no vendor UUID semantics.
type ID string

// Visibility controls who may discover content after namespace authorization.
type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityShared  Visibility = "shared"
	VisibilityPublic  Visibility = "public"
)

// Scope separates storage tenancy, ownership, data subjects, actors, and context.
type Scope struct {
	Namespace  ID         `json:"namespace"`
	Owner      ID         `json:"owner"`
	Subject    ID         `json:"subject"`
	Actor      ID         `json:"actor"`
	Context    ID         `json:"context,omitempty"`
	Visibility Visibility `json:"visibility"`
}

// Validate checks identifiers without conflating their meanings.
func (s Scope) Validate() error {
	for field, value := range map[string]ID{
		"scope.namespace": s.Namespace,
		"scope.owner":     s.Owner,
		"scope.subject":   s.Subject,
		"scope.actor":     s.Actor,
	} {
		if err := validateID(field, value, true); err != nil {
			return err
		}
	}
	if err := validateID("scope.context", s.Context, false); err != nil {
		return err
	}
	switch s.Visibility {
	case VisibilityPrivate, VisibilityShared, VisibilityPublic:
		return nil
	default:
		return fmt.Errorf("scope.visibility %q is invalid", s.Visibility)
	}
}
