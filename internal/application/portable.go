package application

import (
	"fmt"
	"slices"
	"time"

	"github.com/qingbo1011/memxplore/internal/buildinfo"
	"github.com/qingbo1011/memxplore/internal/domain"
)

const (
	// PortableExportFormat identifies the stable, provider-independent subject bundle.
	PortableExportFormat = "memxplore.subject-export"
	// PortableExportSchemaVersion is incremented only for incompatible export changes.
	PortableExportSchemaVersion = buildinfo.ExportSchemaVersion
)

// SubjectExport is a self-contained, versioned export of one authorized data subject.
// Provider embeddings, generated artifacts, jobs, credentials, and operational traces are excluded.
type SubjectExport struct {
	Format        string               `json:"format"`
	SchemaVersion int                  `json:"schema_version"`
	ExportedAt    time.Time            `json:"exported_at"`
	Namespace     domain.ID            `json:"namespace"`
	Subject       domain.ID            `json:"subject"`
	PrivateOwners []domain.ID          `json:"private_owners"`
	IncludeShared bool                 `json:"include_shared"`
	IncludePublic bool                 `json:"include_public"`
	Observations  []domain.Observation `json:"observations"`
	Episodes      []PortableEpisode    `json:"episodes"`
	WorkingSets   []PortableWorkingSet `json:"working_sets"`
	Memories      []PortableMemory     `json:"memories"`
}

// PortableEpisode preserves the episode fields retained by the SQLite schema.
type PortableEpisode struct {
	ID             domain.ID        `json:"id"`
	Namespace      domain.ID        `json:"namespace"`
	Subject        domain.ID        `json:"subject"`
	Task           domain.Content   `json:"task"`
	ObservationIDs []domain.ID      `json:"observation_ids"`
	StartedAt      time.Time        `json:"started_at"`
	EndedAt        time.Time        `json:"ended_at"`
	Outcomes       []domain.Outcome `json:"outcomes"`
}

// PortableWorkingSet adds the persisted lifecycle state to the domain value.
type PortableWorkingSet struct {
	WorkingSet domain.WorkingSet `json:"working_set"`
	State      string            `json:"state"`
}

// PortableMemory groups a stable identity with every immutable version.
type PortableMemory struct {
	Memory   domain.Memory          `json:"memory"`
	Versions []domain.MemoryVersion `json:"versions"`
}

// Validate verifies schema compatibility, authorization closure, and every cross-reference.
func (export SubjectExport) Validate() error {
	if export.Format != PortableExportFormat {
		return fmt.Errorf("export format %q is unsupported", export.Format)
	}
	if export.SchemaVersion != PortableExportSchemaVersion {
		return fmt.Errorf("export schema %d is unsupported", export.SchemaVersion)
	}
	if export.ExportedAt.IsZero() || export.Namespace == "" || export.Subject == "" {
		return fmt.Errorf("export timestamp, namespace, and subject are required")
	}
	owners, err := validateExportOwners(export.PrivateOwners)
	if err != nil {
		return err
	}

	observationIDs := make(map[domain.ID]struct{}, len(export.Observations))
	for index, observation := range export.Observations {
		if err := observation.Validate(); err != nil {
			return fmt.Errorf("observations[%d]: %w", index, err)
		}
		if err := export.validateScope(observation.Scope, owners); err != nil {
			return fmt.Errorf("observations[%d]: %w", index, err)
		}
		if err := addExportID(observationIDs, observation.ID, "observation"); err != nil {
			return err
		}
	}

	episodes := make(map[domain.ID]PortableEpisode, len(export.Episodes))
	outcomeIDs := make(map[domain.ID]domain.ID)
	for index, episode := range export.Episodes {
		if episode.ID == "" || episode.Namespace != export.Namespace || episode.Subject != export.Subject {
			return fmt.Errorf("episodes[%d] has invalid identity or scope", index)
		}
		if err := episode.Task.Validate(); err != nil {
			return fmt.Errorf("episodes[%d].task: %w", index, err)
		}
		if episode.StartedAt.IsZero() || !episode.EndedAt.After(episode.StartedAt) {
			return fmt.Errorf("episodes[%d] has invalid timestamps", index)
		}
		if _, duplicate := episodes[episode.ID]; duplicate {
			return fmt.Errorf("duplicate episode id %s", episode.ID)
		}
		for _, observationID := range episode.ObservationIDs {
			if _, ok := observationIDs[observationID]; !ok {
				return fmt.Errorf("episode %s references absent observation %s", episode.ID, observationID)
			}
		}
		for outcomeIndex, outcome := range episode.Outcomes {
			if err := outcome.Validate(); err != nil {
				return fmt.Errorf("episodes[%d].outcomes[%d]: %w", index, outcomeIndex, err)
			}
			if outcome.EpisodeID != episode.ID {
				return fmt.Errorf("outcome %s belongs to another episode", outcome.ID)
			}
			if _, duplicate := outcomeIDs[outcome.ID]; duplicate {
				return fmt.Errorf("duplicate outcome id %s", outcome.ID)
			}
			outcomeIDs[outcome.ID] = episode.ID
		}
		episodes[episode.ID] = episode
	}

	workingSets := make(map[domain.ID]domain.WorkingSet, len(export.WorkingSets))
	for index, portable := range export.WorkingSets {
		if err := portable.WorkingSet.Validate(); err != nil {
			return fmt.Errorf("working_sets[%d]: %w", index, err)
		}
		if err := export.validateScope(portable.WorkingSet.Scope, owners); err != nil {
			return fmt.Errorf("working_sets[%d]: %w", index, err)
		}
		switch portable.State {
		case "active", "expired", "archived":
		default:
			return fmt.Errorf("working_sets[%d] has invalid state %q", index, portable.State)
		}
		if _, duplicate := workingSets[portable.WorkingSet.ID]; duplicate {
			return fmt.Errorf("duplicate working set id %s", portable.WorkingSet.ID)
		}
		workingSets[portable.WorkingSet.ID] = portable.WorkingSet
	}

	versionIDs := make(map[domain.ID]struct{})
	versionMemories := make(map[domain.ID]domain.ID)
	versionScopes := make(map[domain.ID]domain.Scope)
	dependencies := make(map[domain.ID][]domain.ID)
	memories := make(map[domain.ID]domain.Memory, len(export.Memories))
	for memoryIndex, portable := range export.Memories {
		memory := portable.Memory
		if err := memory.Validate(); err != nil {
			return fmt.Errorf("memories[%d]: %w", memoryIndex, err)
		}
		if err := export.validateScope(memory.Scope, owners); err != nil {
			return fmt.Errorf("memories[%d]: %w", memoryIndex, err)
		}
		if _, duplicate := memories[memory.ID]; duplicate {
			return fmt.Errorf("duplicate memory id %s", memory.ID)
		}
		memories[memory.ID] = memory
		if len(portable.Versions) == 0 {
			return fmt.Errorf("memory %s has no versions", memory.ID)
		}
		currentFound := false
		versionNumbers := make(map[int]struct{}, len(portable.Versions))
		for versionIndex, version := range portable.Versions {
			if version.MemoryID != memory.ID {
				return fmt.Errorf("memories[%d].versions[%d] belongs to another memory", memoryIndex, versionIndex)
			}
			if err := version.Validate(memory.Function); err != nil {
				return fmt.Errorf("memories[%d].versions[%d]: %w", memoryIndex, versionIndex, err)
			}
			if err := addExportID(versionIDs, version.ID, "memory version"); err != nil {
				return err
			}
			if _, duplicate := versionNumbers[version.Number]; duplicate {
				return fmt.Errorf("memory %s contains duplicate version number %d", memory.ID, version.Number)
			}
			versionNumbers[version.Number] = struct{}{}
			versionMemories[version.ID] = memory.ID
			versionScopes[version.ID] = memory.Scope
			dependencies[version.ID] = append([]domain.ID(nil), version.DerivedFrom...)
			currentFound = currentFound || version.Number == memory.CurrentVersion
		}
		if !currentFound {
			return fmt.Errorf("memory %s current version is absent", memory.ID)
		}
	}

	for _, portable := range export.Memories {
		for _, version := range portable.Versions {
			if factual := version.Payload.Factual; factual != nil && factual.ClaimSubject != export.Subject {
				return fmt.Errorf("memory version %s factual claim crosses subject", version.ID)
			}
			for _, evidence := range version.Provenance {
				if _, ok := observationIDs[evidence.ObservationID]; !ok {
					return fmt.Errorf("memory version %s references absent observation %s", version.ID, evidence.ObservationID)
				}
			}
			for _, reference := range append(append([]domain.ID(nil), version.Supersedes...), version.DerivedFrom...) {
				if _, ok := versionIDs[reference]; !ok {
					return fmt.Errorf("memory version %s references absent version %s", version.ID, reference)
				}
			}
			for _, superseded := range version.Supersedes {
				if versionMemories[superseded] != portable.Memory.ID {
					return fmt.Errorf("memory version %s supersedes a version from another memory", version.ID)
				}
			}
			for _, parent := range version.DerivedFrom {
				if !dependencyVisibilityAllowed(versionScopes[parent], portable.Memory.Scope) {
					return fmt.Errorf("memory version %s dependency %s widens visibility", version.ID, parent)
				}
			}
			if experiential := version.Payload.Experiential; experiential != nil {
				for _, evidence := range experiential.Evidence {
					episode, ok := episodes[evidence.EpisodeID]
					if !ok {
						return fmt.Errorf("memory version %s references absent episode %s", version.ID, evidence.EpisodeID)
					}
					for _, outcomeID := range evidence.OutcomeIDs {
						if episodeID, ok := outcomeIDs[outcomeID]; !ok || episodeID != episode.ID {
							return fmt.Errorf("memory version %s references absent outcome %s", version.ID, outcomeID)
						}
					}
				}
			}
			if working := version.Payload.Working; working != nil {
				set, ok := workingSets[working.WorkingSetID]
				if !ok || set.TaskID != working.TaskID || portable.Memory.Scope.Context != working.TaskID {
					return fmt.Errorf("memory version %s references an incompatible working set", version.ID)
				}
				for _, observationID := range working.CompactedFrom {
					if _, ok := observationIDs[observationID]; !ok {
						return fmt.Errorf("memory version %s references absent compacted observation %s", version.ID, observationID)
					}
				}
			}
		}
	}
	if err := validateDependencyGraph(dependencies); err != nil {
		return err
	}
	return nil
}

func dependencyVisibilityAllowed(parent, child domain.Scope) bool {
	if parent.Namespace != child.Namespace || parent.Subject != child.Subject {
		return false
	}
	switch parent.Visibility {
	case domain.VisibilityPrivate:
		return child.Visibility == domain.VisibilityPrivate && parent.Owner == child.Owner
	case domain.VisibilityShared:
		return child.Visibility != domain.VisibilityPublic
	case domain.VisibilityPublic:
		return true
	default:
		return false
	}
}

func validateDependencyGraph(dependencies map[domain.ID][]domain.ID) error {
	const (
		unvisited = iota
		visiting
		visited
	)
	states := make(map[domain.ID]int, len(dependencies))
	var visit func(domain.ID) error
	visit = func(id domain.ID) error {
		switch states[id] {
		case visiting:
			return fmt.Errorf("memory dependency graph contains a cycle at %s", id)
		case visited:
			return nil
		}
		states[id] = visiting
		for _, parent := range dependencies[id] {
			if err := visit(parent); err != nil {
				return err
			}
		}
		states[id] = visited
		return nil
	}
	for id := range dependencies {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func validateExportOwners(values []domain.ID) (map[domain.ID]struct{}, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("export requires at least one private owner")
	}
	owners := make(map[domain.ID]struct{}, len(values))
	for _, owner := range values {
		if owner == "" {
			return nil, fmt.Errorf("private owner cannot be empty")
		}
		if _, duplicate := owners[owner]; duplicate {
			return nil, fmt.Errorf("duplicate private owner %s", owner)
		}
		owners[owner] = struct{}{}
	}
	return owners, nil
}

func (export SubjectExport) validateScope(scope domain.Scope, owners map[domain.ID]struct{}) error {
	if scope.Namespace != export.Namespace || scope.Subject != export.Subject {
		return fmt.Errorf("scope crosses export namespace or subject")
	}
	switch scope.Visibility {
	case domain.VisibilityPrivate:
		if _, ok := owners[scope.Owner]; !ok {
			return fmt.Errorf("private scope owner %s is not authorized", scope.Owner)
		}
	case domain.VisibilityShared:
		if !export.IncludeShared {
			return fmt.Errorf("shared scope is not authorized")
		}
	case domain.VisibilityPublic:
		if !export.IncludePublic {
			return fmt.Errorf("public scope is not authorized")
		}
	}
	return nil
}

func addExportID(ids map[domain.ID]struct{}, id domain.ID, kind string) error {
	if _, duplicate := ids[id]; duplicate {
		return fmt.Errorf("duplicate %s id %s", kind, id)
	}
	ids[id] = struct{}{}
	return nil
}

// SortSubjectExport normalizes collection order before encoding and hashing.
func SortSubjectExport(export *SubjectExport) {
	slices.Sort(export.PrivateOwners)
	slices.SortFunc(export.Observations, func(left, right domain.Observation) int { return compareID(left.ID, right.ID) })
	slices.SortFunc(export.Episodes, func(left, right PortableEpisode) int { return compareID(left.ID, right.ID) })
	for index := range export.Episodes {
		slices.SortFunc(export.Episodes[index].Outcomes, func(left, right domain.Outcome) int { return compareID(left.ID, right.ID) })
	}
	slices.SortFunc(export.WorkingSets, func(left, right PortableWorkingSet) int {
		return compareID(left.WorkingSet.ID, right.WorkingSet.ID)
	})
	slices.SortFunc(export.Memories, func(left, right PortableMemory) int { return compareID(left.Memory.ID, right.Memory.ID) })
	for index := range export.Memories {
		slices.SortFunc(export.Memories[index].Versions, func(left, right domain.MemoryVersion) int {
			if left.Number < right.Number {
				return -1
			}
			if left.Number > right.Number {
				return 1
			}
			return compareID(left.ID, right.ID)
		})
	}
}

func compareID(left, right domain.ID) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
