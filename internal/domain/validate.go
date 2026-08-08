// Package domain defines MemXplore's transport- and storage-independent model.
package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	idPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	extensionLabel = regexp.MustCompile(`^x-[a-z0-9][a-z0-9-]{0,62}$`)
	tagPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)

func validateID(field string, value ID, required bool) error {
	if value == "" && !required {
		return nil
	}
	if !idPattern.MatchString(string(value)) {
		return fmt.Errorf("%s must be an opaque 1-128 character identifier", field)
	}
	return nil
}

func validateRequiredText(field, value string, max int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(value) > max {
		return fmt.Errorf("%s exceeds %d bytes", field, max)
	}
	return nil
}

func validateLabels(field string, values []string, known map[string]struct{}, required bool) error {
	if required && len(values) == 0 {
		return fmt.Errorf("%s requires at least one label", field)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s contains duplicate label %q", field, value)
		}
		seen[value] = struct{}{}
		if _, ok := known[value]; ok {
			continue
		}
		if !extensionLabel.MatchString(value) {
			return fmt.Errorf("%s label %q is neither registered nor an x- extension", field, value)
		}
	}
	return nil
}

// TimeRange is a half-open interval [From, To). A nil To is unbounded.
type TimeRange struct {
	From time.Time  `json:"from"`
	To   *time.Time `json:"to,omitempty"`
}

// Validate checks chronological ordering.
func (r TimeRange) Validate(field string) error {
	if r.From.IsZero() {
		return fmt.Errorf("%s.from is required", field)
	}
	if r.To != nil && !r.To.After(r.From) {
		return fmt.Errorf("%s.to must be after from", field)
	}
	return nil
}
