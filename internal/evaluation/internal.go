package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/qingbo1011/memxplore/internal/adapters/sqlite"
	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
	"github.com/qingbo1011/memxplore/internal/policy"
	strategydef "github.com/qingbo1011/memxplore/internal/strategy"
	"github.com/qingbo1011/memxplore/internal/strategy/evolution"
	"github.com/qingbo1011/memxplore/internal/strategy/formation"
)

// InternalConfig controls the built-in deterministic lifecycle evaluation.
type InternalConfig struct {
	RunID   string
	Seed    int64
	WorkDir string
	Clock   func() time.Time
}

type internalHarness struct {
	store       *sqlite.Store
	service     *application.LifecycleService
	retriever   *application.Retriever
	base        time.Time
	predictions []Prediction
	traces      []TraceReference
	checks      map[string]bool
	strategies  map[string]string
	indexed     int
}

type internalFixture struct {
	ID       string `json:"id"`
	Function string `json:"function"`
	Query    string `json:"query"`
	Purpose  string `json:"purpose"`
}

var internalFixtures = []internalFixture{
	{ID: "factual-update-conflict", Function: "factual", Query: "service deployment region", Purpose: "supersession, historical time, and conflict visibility"},
	{ID: "experiential-success-failure", Function: "experiential", Query: "deployment migration verification lesson", Purpose: "independent success and failure outcomes"},
	{ID: "working-goal-compression", Function: "working", Query: "release phase two state", Purpose: "goal-preserving task-state compaction"},
}

// RunInternal executes actual SQLite lifecycle and lexical retrieval for three paired scenarios.
func RunInternal(ctx context.Context, config InternalConfig) (Run, error) {
	clock := config.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	started := clock().UTC()
	fixtureJSON, _ := json.Marshal(internalFixtures)
	datasetDigest := sha256.Sum256(fixtureJSON)
	if config.RunID == "" {
		config.RunID = fmt.Sprintf("internal-%s-%s", started.Format("20060102T150405.000000000Z"), hex.EncodeToString(datasetDigest[:4]))
	}
	temporary, err := os.MkdirTemp(config.WorkDir, "memxplore-internal-eval-")
	if err != nil {
		return Run{}, fmt.Errorf("create evaluation work directory: %w", err)
	}
	defer os.RemoveAll(temporary)
	store, err := sqlite.Open(ctx, temporary+"/eval.sqlite", sqlite.DefaultOptions())
	if err != nil {
		return Run{}, err
	}
	defer store.Close()
	service, _ := application.NewLifecycleService(policy.OwnerPolicy{}, store)
	retriever, err := application.NewRetriever(application.RetrieverConfig{
		Repository: store, TraceSink: store, Now: func() time.Time { return clock().UTC() },
	})
	if err != nil {
		return Run{}, err
	}
	harness := &internalHarness{
		store: store, service: service, retriever: retriever,
		base: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), checks: make(map[string]bool), strategies: make(map[string]string),
	}
	if err := harness.factual(ctx); err != nil {
		return Run{}, fmt.Errorf("factual scenario: %w", err)
	}
	if err := harness.experiential(ctx); err != nil {
		return Run{}, fmt.Errorf("experiential scenario: %w", err)
	}
	if err := harness.working(ctx); err != nil {
		return Run{}, fmt.Errorf("working scenario: %w", err)
	}
	metrics := Score(harness.predictions, 5)
	metrics.LifecycleChecks = harness.checks
	metrics.IndexedUnits = harness.indexed
	strategyIDs := make([]string, 0, len(harness.strategies))
	for id := range harness.strategies {
		strategyIDs = append(strategyIDs, id)
	}
	sortStrings(strategyIDs)
	strategyHashes := make([]string, len(strategyIDs))
	for index, id := range strategyIDs {
		strategyHashes[index] = harness.strategies[id]
	}
	manifest := NewManifest(config.RunID, "internal-lifecycle-v1", "builtin/internal-v1", config.Seed, DatasetIdentity{
		Name: "memxplore-internal-lifecycle", Revision: "v1", SHA256: hex.EncodeToString(datasetDigest[:]),
		Path: "builtin:internalFixtures", License: "Apache-2.0",
	}, []Variant{
		{ID: "no-memory", Description: "Paired ablation with no stored or retrieved memory."},
		{ID: "lexical", Description: "Generator-free lifecycle with SQLite FTS5/BM25 recall.", StrategyIDs: strategyIDs, StrategyHashes: strategyHashes},
	}, started)
	manifest.TopK = 5
	manifest.Limit = len(internalFixtures)
	manifest.CompletedAt = clock().UTC()
	manifest.Limitations = []string{
		"Small deterministic lifecycle scenarios measure reference invariants, not downstream answer quality.",
		"System latencies are local wall-clock measurements and are not cross-machine comparable.",
		"No provider or model is called by this benchmark.",
	}
	return Run{Manifest: manifest, Predictions: harness.predictions, Metrics: metrics, Traces: harness.traces}, nil
}

func (h *internalHarness) factual(ctx context.Context) error {
	scope := evalScope("subject-factual", "context-factual")
	initial := evalObservation("obs-factual-initial", scope, "service deployment region is us-east", h.base, nil)
	memory, current, _, err := h.form(ctx, domain.FunctionFactual, initial, 1)
	if err != nil {
		return err
	}
	oldVersion := current
	update := evalObservation("obs-factual-update", scope, "service deployment region is eu-west", h.base.Add(time.Hour), nil)
	memory, current, _, err = h.evolve(ctx, domain.FunctionFactual, memory, current, update, 2)
	if err != nil {
		return err
	}
	conflict := evalObservation("obs-factual-conflict", scope, "service deployment region is ap-south", h.base.Add(2*time.Hour), map[string]string{"evolution": "conflict"})
	_, sibling, _, err := h.evolve(ctx, domain.FunctionFactual, memory, current, conflict, 3)
	if err != nil {
		return err
	}
	prediction, bundle, err := h.recall(ctx, "factual-update-conflict", "factual", scope, "service deployment region", []domain.MemoryFunction{domain.FunctionFactual}, []string{string(current.ID), string(sibling.ID)}, h.base.Add(3*time.Hour))
	if err != nil {
		return err
	}
	h.addPair(prediction, bundle)
	h.checks["factual_superseded_version_excluded"] = !containsVersion(bundle, oldVersion.ID)
	h.checks["factual_conflict_alternatives_visible"] = len(bundle.Groups) == 1 && bundle.Groups[0].Conflict && len(bundle.Groups[0].Items) == 2
	_, historicalBundle, err := h.recall(ctx, "factual-historical", "factual", scope, "service deployment region us-east", []domain.MemoryFunction{domain.FunctionFactual}, []string{string(oldVersion.ID)}, h.base.Add(30*time.Minute))
	if err != nil {
		return err
	}
	h.checks["factual_historical_version_recallable"] = containsVersion(historicalBundle, oldVersion.ID)
	// The historical check is a lifecycle assertion, not a fourth paired benchmark case.
	if err := h.recordTrace("factual-historical", "lexical", "retrieval", historicalBundle.Trace); err != nil {
		return err
	}
	return nil
}

func (h *internalHarness) experiential(ctx context.Context) error {
	scope := evalScope("subject-experiential", "context-experiential")
	failure := evalObservation("obs-experience-failure", scope, "deployment failed; validate the schema before running the migration", h.base.Add(4*time.Hour), nil)
	_, failedVersion, failedOutcome, err := h.formExperiential(ctx, failure, -1, 4)
	if err != nil {
		return err
	}
	success := evalObservation("obs-experience-success", scope, "deployment succeeded; create a backup before the migration and verify afterward", h.base.Add(5*time.Hour), nil)
	_, successVersion, successOutcome, err := h.formExperiential(ctx, success, 1, 5)
	if err != nil {
		return err
	}
	prediction, bundle, err := h.recall(ctx, "experiential-success-failure", "experiential", scope, "deployment migration verification lesson", []domain.MemoryFunction{domain.FunctionExperiential}, []string{string(failedVersion.ID), string(successVersion.ID)}, h.base.Add(6*time.Hour))
	if err != nil {
		return err
	}
	h.addPair(prediction, bundle)
	h.checks["experiential_failure_outcome_preserved"] = failedOutcome == -1
	h.checks["experiential_success_outcome_preserved"] = successOutcome == 1
	h.checks["experiential_both_lessons_recalled"] = containsVersion(bundle, failedVersion.ID) && containsVersion(bundle, successVersion.ID)
	return nil
}

func (h *internalHarness) working(ctx context.Context) error {
	scope := evalScope("subject-working", "task-release")
	initial := evalObservation("obs-working-initial", scope, "release phase one complete; phase two is pending", h.base.Add(7*time.Hour), map[string]string{"goal": "ship the release without downtime"})
	formationStrategy, _ := formation.NewGeneratorFree(domain.FunctionWorking)
	proposal, err := formationStrategy.Propose(ctx, initial)
	if err != nil {
		return err
	}
	h.recordStrategy(formationStrategy.Package())
	var create application.MemoryCreate
	if err := json.Unmarshal(proposal.Payload, &create); err != nil {
		return err
	}
	working := create.Payload.Working
	expires := h.base.Add(24 * time.Hour)
	if err := h.store.PutWorkingSet(ctx, domain.WorkingSet{
		ID: working.WorkingSetID, Scope: scope, TaskID: scope.Context, Goal: working.Goal,
		ExpiresAt: &expires, CreatedAt: initial.CapturedAt, UpdatedAt: initial.CapturedAt,
	}); err != nil {
		return err
	}
	applied, err := h.service.Apply(ctx, scope, proposal, initial.CapturedAt.Add(time.Minute))
	if err != nil {
		return err
	}
	h.recordOperation("working-goal-compression", applied.Operation)
	h.indexed++
	update := evalObservation("obs-working-update", scope, "release phase two complete; rollout verification is pending", h.base.Add(8*time.Hour), nil)
	memory, current, _, err := h.evolve(ctx, domain.FunctionWorking, applied.Memory, applied.Version, update, 8)
	if err != nil {
		return err
	}
	_ = memory
	prediction, bundle, err := h.recall(ctx, "working-goal-compression", "working", scope, "release phase two state", []domain.MemoryFunction{domain.FunctionWorking}, []string{string(current.ID)}, h.base.Add(9*time.Hour))
	if err != nil {
		return err
	}
	h.addPair(prediction, bundle)
	h.checks["working_goal_preserved"] = current.Payload.Working.Goal.PlainText() == "ship the release without downtime"
	h.checks["working_compaction_tracks_both_observations"] = len(current.Payload.Working.CompactedFrom) == 2
	h.checks["working_task_local_recall"] = containsVersion(bundle, current.ID)
	return nil
}

func (h *internalHarness) form(ctx context.Context, function domain.MemoryFunction, observation domain.Observation, offset int) (domain.Memory, domain.MemoryVersion, domain.Operation, error) {
	strategy, err := formation.NewGeneratorFree(function)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, err
	}
	h.recordStrategy(strategy.Package())
	proposal, err := strategy.Propose(ctx, observation)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, err
	}
	applied, err := h.service.Apply(ctx, observation.Scope, proposal, observation.CapturedAt.Add(time.Duration(offset)*time.Minute))
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, err
	}
	h.recordOperation(string(function), applied.Operation)
	h.indexed++
	return applied.Memory, applied.Version, applied.Operation, nil
}

func (h *internalHarness) formExperiential(ctx context.Context, observation domain.Observation, value float64, offset int) (domain.Memory, domain.MemoryVersion, float64, error) {
	strategy, _ := formation.NewGeneratorFree(domain.FunctionExperiential)
	h.recordStrategy(strategy.Package())
	proposal, err := strategy.Propose(ctx, observation)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, 0, err
	}
	var create application.MemoryCreate
	if err := json.Unmarshal(proposal.Payload, &create); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, 0, err
	}
	evidence := create.Payload.Experiential.Evidence[0]
	episode := domain.Episode{
		ID: evidence.EpisodeID, Scope: observation.Scope, Task: observation.Content,
		ObservationIDs: []domain.ID{observation.ID}, StartedAt: observation.CapturedAt.Add(-time.Minute), EndedAt: observation.CapturedAt,
	}
	outcome := domain.Outcome{
		ID: evidence.OutcomeIDs[0], EpisodeID: episode.ID, Source: "eval-observer", Kind: "task-result", Value: value,
		Evidence: observation.Content, ObservedAt: observation.CapturedAt,
	}
	if err := h.store.PutEpisode(ctx, episode, []domain.Outcome{outcome}); err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, 0, err
	}
	applied, err := h.service.Apply(ctx, observation.Scope, proposal, observation.CapturedAt.Add(time.Duration(offset)*time.Minute))
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, 0, err
	}
	h.recordOperation("experiential-success-failure", applied.Operation)
	h.indexed++
	return applied.Memory, applied.Version, outcome.Value, nil
}

func (h *internalHarness) evolve(ctx context.Context, function domain.MemoryFunction, memory domain.Memory, current domain.MemoryVersion, observation domain.Observation, offset int) (domain.Memory, domain.MemoryVersion, domain.Operation, error) {
	strategy, err := evolution.NewGeneratorFree(function)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, err
	}
	h.recordStrategy(strategy.Package())
	proposal, err := strategy.Propose(ctx, memory, current, observation)
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, err
	}
	applied, err := h.service.Apply(ctx, observation.Scope, proposal, observation.CapturedAt.Add(time.Duration(offset)*time.Minute))
	if err != nil {
		return domain.Memory{}, domain.MemoryVersion{}, domain.Operation{}, err
	}
	h.recordOperation(string(function), applied.Operation)
	if applied.Memory.ID != memory.ID {
		h.indexed++
	}
	return applied.Memory, applied.Version, applied.Operation, nil
}

func (h *internalHarness) recall(ctx context.Context, caseID, category string, scope domain.Scope, query string, functions []domain.MemoryFunction, expected []string, at time.Time) (Prediction, application.RecallBundle, error) {
	started := time.Now()
	bundle, err := h.retriever.Recall(ctx, application.RecallRequest{
		Scope: scope, Access: application.AccessScope{PrincipalID: scope.Actor, Namespace: scope.Namespace, PrivateOwners: []domain.ID{scope.Owner}},
		Query: query, Functions: functions, Mode: application.RetrievalLexical,
		ValidAt: at, SystemAt: at, TokenBudget: 4096, CandidateLimit: 20,
	})
	latency := float64(time.Since(started).Microseconds()) / 1000
	prediction := Prediction{
		CaseID: caseID, Category: category, Variant: "lexical", Query: query,
		ExpectedReferences: append([]string(nil), expected...), LatencyMS: latency,
		InputTokens: estimateTokens(query), TraceIDs: []string{string(bundle.Trace.ID)},
		RetrievedTokens: bundle.Trace.TokensUsed,
	}
	if err != nil {
		prediction.Failure = &Failure{Code: "recall_failed", Message: err.Error()}
		return prediction, bundle, err
	}
	for index, item := range bundle.Items {
		prediction.Retrieved = append(prediction.Retrieved, RankedReference{
			Reference: string(item.VersionID), Rank: index + 1, Score: item.Score.Total,
			MemoryID: string(item.MemoryID), VersionID: string(item.VersionID),
		})
	}
	h.strategies[bundle.Trace.StrategyID] = bundle.Trace.StrategyHash
	return prediction, bundle, nil
}

func (h *internalHarness) addPair(prediction Prediction, bundle application.RecallBundle) {
	noMemory := Prediction{
		CaseID: prediction.CaseID, Category: prediction.Category, Variant: "no-memory", Query: prediction.Query,
		ExpectedReferences: append([]string(nil), prediction.ExpectedReferences...), LatencyMS: 0,
	}
	h.predictions = append(h.predictions, noMemory, prediction)
	_ = h.recordTrace(prediction.CaseID, prediction.Variant, "retrieval", bundle.Trace)
}

func (h *internalHarness) recordOperation(caseID string, operation domain.Operation) {
	_ = h.recordTrace(caseID, "lexical", "lifecycle", operation)
}

func (h *internalHarness) recordTrace(caseID, variant, kind string, value any) error {
	reference, err := NewTraceReference(caseID, variant, kind, value)
	if err != nil {
		return err
	}
	h.traces = append(h.traces, reference)
	return nil
}

func (h *internalHarness) recordStrategy(definition strategydef.Package) {
	digest, err := definition.Hash()
	if err == nil {
		h.strategies[definition.ID+"@"+definition.Version] = digest
	}
}

func evalScope(subject, contextID domain.ID) domain.Scope {
	return domain.Scope{Namespace: "eval", Owner: "eval-owner", Subject: subject, Actor: "eval-actor", Context: contextID, Visibility: domain.VisibilityPrivate}
}

func evalObservation(id domain.ID, scope domain.Scope, text string, capturedAt time.Time, metadata map[string]string) domain.Observation {
	return domain.Observation{
		ID: id, Scope: scope, SourceKind: "evaluation", SourceReference: string(id),
		Content:       domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: text}}},
		EvidenceClass: domain.EvidenceUntrusted, CapturedAt: capturedAt, Metadata: metadata,
	}
}

func containsVersion(bundle application.RecallBundle, id domain.ID) bool {
	for _, item := range bundle.Items {
		if item.VersionID == id {
			return true
		}
	}
	return false
}

func estimateTokens(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	return (len(value) + 3) / 4
}
