package evaluation

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/qingbo1011/memxplore/internal/adapters/sqlite"
	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/domain"
	"github.com/qingbo1011/memxplore/internal/observability"
	"github.com/qingbo1011/memxplore/internal/policy"
	strategydef "github.com/qingbo1011/memxplore/internal/strategy"
)

// LongMemEvalV1Config controls the official v1 cleaned-dataset retrieval adapter.
type LongMemEvalV1Config struct {
	DatasetPath   string
	Revision      string
	RunID         string
	Seed          int64
	Limit         int
	TopK          int
	WorkDir       string
	Clock         func() time.Time
	Observability observability.Recorder
}

type longMemEvalV1Turn struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	HasAnswer bool   `json:"has_answer,omitempty"`
}

type longMemEvalV1Instance struct {
	QuestionID         string                `json:"question_id"`
	QuestionType       string                `json:"question_type"`
	Question           string                `json:"question"`
	Answer             json.RawMessage       `json:"answer"`
	QuestionDate       string                `json:"question_date"`
	HaystackSessionIDs []string              `json:"haystack_session_ids"`
	HaystackDates      []string              `json:"haystack_dates"`
	HaystackSessions   [][]longMemEvalV1Turn `json:"haystack_sessions"`
	AnswerSessionIDs   []string              `json:"answer_session_ids"`
}

// RunLongMemEvalV1 ingests every selected session as a factual memory and evaluates session retrieval.
// Limit 0 is the full official 500-question protocol; positive limits are explicitly partial runs.
func RunLongMemEvalV1(ctx context.Context, config LongMemEvalV1Config) (_ Run, finalErr error) {
	observer := observability.OrNop(config.Observability)
	ctx, endOperation := observer.Start(ctx, "benchmark.run", observability.String("benchmark", "longmemeval-v1-retrieval"))
	defer func() { endOperation(finalErr) }()
	if config.DatasetPath == "" || config.Revision == "" || config.Limit < 0 {
		return Run{}, fmt.Errorf("LongMemEval v1 dataset path, pinned revision, and non-negative limit are required")
	}
	if config.TopK == 0 {
		config.TopK = 5
	}
	if config.TopK < 1 || config.TopK > 100 {
		return Run{}, fmt.Errorf("LongMemEval v1 top-k must be within [1,100]")
	}
	clock := config.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	started := clock().UTC()
	datasetFile, err := os.Open(config.DatasetPath)
	if err != nil {
		return Run{}, fmt.Errorf("open LongMemEval v1 dataset: %w", err)
	}
	datasetDigest, _, err := SHA256Reader(datasetFile)
	_ = datasetFile.Close()
	if err != nil {
		return Run{}, fmt.Errorf("hash LongMemEval v1 dataset: %w", err)
	}
	if config.RunID == "" {
		config.RunID = fmt.Sprintf("longmemeval-v1-%s-%s", started.Format("20060102T150405.000000000Z"), datasetDigest[:8])
	}
	temporary, err := os.MkdirTemp(config.WorkDir, "memxplore-longmemeval-v1-")
	if err != nil {
		return Run{}, err
	}
	defer os.RemoveAll(temporary)
	store, err := sqlite.Open(ctx, temporary+"/eval.sqlite", sqlite.DefaultOptions())
	if err != nil {
		return Run{}, err
	}
	defer store.Close()
	service, _ := application.NewLifecycleService(policy.OwnerPolicy{}, store)
	retriever, err := application.NewRetriever(application.RetrieverConfig{
		Repository: store, TraceSink: store, Now: func() time.Time { return clock().UTC() }, Observability: observer,
	})
	if err != nil {
		return Run{}, err
	}
	adapter := longMemEvalV1Strategy(config.TopK)
	adapterHash, err := adapter.Hash()
	if err != nil {
		return Run{}, err
	}
	predictions := make([]Prediction, 0)
	traces := make([]TraceReference, 0)
	strategyHashes := map[string]string{adapter.ID + "@" + adapter.Version: adapterHash}
	ingestStarted := time.Now()
	indexed, ingestTokens := 0, 0
	cases, err := streamLongMemEvalV1(ctx, config.DatasetPath, config.Limit, func(index int, instance longMemEvalV1Instance) error {
		if err := validateLongMemEvalV1(instance); err != nil {
			return fmt.Errorf("case %d: %w", index, err)
		}
		scope := domain.Scope{
			Namespace: "eval-longmemeval-v1", Owner: "longmemeval-v1", Subject: stableEvalID("subject", instance.QuestionID),
			Actor: "eval-adapter", Context: stableEvalID("question", instance.QuestionID), Visibility: domain.VisibilityPrivate,
		}
		versionSessions := make(map[domain.ID]string, len(instance.HaystackSessions))
		indexedSessions := make(map[string]struct{}, len(instance.HaystackSessions))
		caseBase := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(index) * 24 * time.Hour)
		for sessionIndex, turns := range instance.HaystackSessions {
			sessionID := instance.HaystackSessionIDs[sessionIndex]
			if _, duplicate := indexedSessions[sessionID]; duplicate {
				continue
			}
			indexedSessions[sessionID] = struct{}{}
			text := longMemEvalSessionText(instance.HaystackDates[sessionIndex], turns)
			if text == "" {
				return fmt.Errorf("case %s session %d is empty", instance.QuestionID, sessionIndex)
			}
			observationID := stableEvalID("obs", instance.QuestionID, sessionID)
			capturedAt := caseBase.Add(time.Duration(sessionIndex) * time.Second)
			create := application.MemoryCreate{
				Scope: scope, Function: domain.FunctionFactual,
				Taxonomy: domain.Taxonomy{
					Forms: []string{"token-flat"}, Functions: []string{"factual"}, Dynamics: []string{"formation", "retrieval"},
					Tags: []string{"adapter", "benchmark"},
				},
				Payload: domain.MemoryPayload{Factual: &domain.FactualMemory{
					ClaimSubject: scope.Subject, Predicate: "longmemeval-session",
					Object: domain.Content{Parts: []domain.ContentPart{{Kind: domain.PartText, Text: text}}}, Epistemic: domain.EpistemicAsserted,
				}},
				Provenance: []domain.EvidenceRef{{ObservationID: observationID, PartIndex: 0}},
				ValidTime:  &domain.TimeRange{From: capturedAt},
			}
			payload, _ := json.Marshal(create)
			proposal := application.Proposal{
				ID: stableEvalID("proposal", instance.QuestionID, sessionID), Namespace: scope.Namespace,
				ObservationIDs: []domain.ID{observationID}, Kind: application.ProposalCreate, Payload: payload,
				StrategyID: adapter.ID + "@" + adapter.Version, StrategyHash: adapterHash,
				CreatedAt: capturedAt, UntrustedContent: true,
			}
			applied, err := service.Apply(ctx, scope, proposal, capturedAt.Add(time.Millisecond))
			if err != nil {
				return fmt.Errorf("ingest case %s session %s: %w", instance.QuestionID, sessionID, err)
			}
			versionSessions[applied.Version.ID] = sessionID
			ingestTokens += estimateTokens(text)
			indexed++
		}
		queryAt := caseBase.Add(time.Duration(len(instance.HaystackSessions)+1) * time.Second)
		expectedReferences := longMemEvalV1ExpectedReferences(instance)
		noMemory := Prediction{
			CaseID: instance.QuestionID, Category: instance.QuestionType, Variant: "no-memory", Query: instance.Question,
			ExpectedReferences: append([]string(nil), expectedReferences...),
		}
		recallStarted := time.Now()
		bundle, recallErr := retriever.Recall(ctx, application.RecallRequest{
			Scope: scope, Access: application.AccessScope{PrincipalID: scope.Actor, Namespace: scope.Namespace, PrivateOwners: []domain.ID{scope.Owner}},
			Query: instance.Question, Functions: []domain.MemoryFunction{domain.FunctionFactual}, Mode: application.RetrievalLexical,
			ValidAt: queryAt, SystemAt: queryAt, TokenBudget: 1_000_000, CandidateLimit: config.TopK,
		})
		prediction := Prediction{
			CaseID: instance.QuestionID, Category: instance.QuestionType, Variant: "lexical", Query: instance.Question,
			ExpectedReferences: append([]string(nil), expectedReferences...),
			LatencyMS:          float64(time.Since(recallStarted).Microseconds()) / 1000, InputTokens: estimateTokens(instance.Question),
		}
		if recallErr != nil {
			prediction.Failure = &Failure{Code: "recall_failed", Message: recallErr.Error()}
		} else {
			prediction.TraceIDs = []string{string(bundle.Trace.ID)}
			prediction.RetrievedTokens = bundle.Trace.TokensUsed
			for rank, item := range bundle.Items {
				sessionID := versionSessions[item.VersionID]
				prediction.Retrieved = append(prediction.Retrieved, RankedReference{
					Reference: sessionID, Rank: rank + 1, Score: item.Score.Total,
					MemoryID: string(item.MemoryID), VersionID: string(item.VersionID),
				})
			}
			reference, traceErr := NewTraceReference(instance.QuestionID, "lexical", "retrieval", bundle.Trace)
			if traceErr != nil {
				return traceErr
			}
			traces = append(traces, reference)
			strategyHashes[bundle.Trace.StrategyID] = bundle.Trace.StrategyHash
		}
		predictions = append(predictions, noMemory, prediction)
		return nil
	})
	if err != nil {
		return Run{}, err
	}
	if config.Limit == 0 && cases != 500 {
		return Run{}, fmt.Errorf("full LongMemEval v1 run requires 500 cases, dataset contained %d", cases)
	}
	metrics := Score(predictions, config.TopK)
	metrics.IndexedUnits = indexed
	metrics.IngestTokens = ingestTokens
	metrics.IngestLatencyMS = float64(time.Since(ingestStarted).Microseconds()) / 1000
	strategyIDs := make([]string, 0, len(strategyHashes))
	for id := range strategyHashes {
		strategyIDs = append(strategyIDs, id)
	}
	sortStrings(strategyIDs)
	hashes := make([]string, len(strategyIDs))
	for index, id := range strategyIDs {
		hashes[index] = strategyHashes[id]
	}
	manifest := NewManifest(config.RunID, "longmemeval-v1-retrieval", "longmemeval-v1-session-adapter@1.0.0", config.Seed, DatasetIdentity{
		Name: "xiaowu0162/longmemeval-cleaned", Revision: config.Revision, SHA256: datasetDigest,
		Path: filepath.Base(config.DatasetPath), License: "MIT",
	}, []Variant{
		{ID: "no-memory", Description: "No-memory paired ablation."},
		{ID: "lexical", Description: "Generator-free session memory with SQLite FTS5/BM25.", StrategyIDs: strategyIDs, StrategyHashes: hashes},
	}, started)
	manifest.TopK = config.TopK
	manifest.Limit = cases
	manifest.CompletedAt = clock().UTC()
	manifest.Limitations = []string{
		"This is session-level ingest/retrieval evaluation, not the official model-judged question-answering score.",
		"The adapter is protocol-compatible with the pinned cleaned dataset and makes no reproduction or leaderboard claim.",
		"Recall@K and MRR exclude the 30 official question_id suffix _abs cases; retrieval abstention accuracy is reported separately.",
		"The lexical baseline uses no model, provider calls, or monetary cost.",
		"Repeated session IDs with identical content are indexed once at their first occurrence because references are session-ID-level.",
	}
	if config.Limit > 0 {
		manifest.Limitations = append(manifest.Limitations, fmt.Sprintf("Partial bounded run: first %d cases only.", cases))
	}
	observer.Observe(ctx, observability.MetricBenchmarkCases, float64(cases), observability.String("benchmark", manifest.Benchmark))
	observer.Observe(ctx, observability.MetricBenchmarkFailures, float64(metrics.Variants["lexical"].Failures), observability.String("benchmark", manifest.Benchmark), observability.String("variant", "lexical"))
	return Run{Manifest: manifest, Predictions: predictions, Metrics: metrics, Traces: traces}, nil
}

func streamLongMemEvalV1(ctx context.Context, path string, limit int, visit func(int, longMemEvalV1Instance) error) (cases int, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReaderSize(file, 1<<20))
	decoder.DisallowUnknownFields()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return 0, fmt.Errorf("LongMemEval v1 dataset must be a JSON array")
	}
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return cases, err
		}
		var instance longMemEvalV1Instance
		if err := decoder.Decode(&instance); err != nil {
			return cases, fmt.Errorf("decode LongMemEval v1 case %d: %w", cases, err)
		}
		if limit > 0 && cases >= limit {
			continue
		}
		if err := visit(cases, instance); err != nil {
			return cases, err
		}
		cases++
	}
	if _, err := decoder.Token(); err != nil {
		return cases, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return cases, fmt.Errorf("LongMemEval v1 dataset contains trailing JSON")
	}
	return cases, nil
}

func validateLongMemEvalV1(instance longMemEvalV1Instance) error {
	if instance.QuestionID == "" || strings.TrimSpace(instance.Question) == "" || instance.QuestionType == "" || len(instance.Answer) == 0 {
		return fmt.Errorf("question id, type, text, and answer are required")
	}
	count := len(instance.HaystackSessionIDs)
	if count == 0 || len(instance.HaystackDates) != count || len(instance.HaystackSessions) != count {
		return fmt.Errorf("haystack ids, dates, and sessions must be non-empty and aligned")
	}
	sessions := make(map[string]int, count)
	for index, id := range instance.HaystackSessionIDs {
		if id == "" {
			return fmt.Errorf("haystack session %d has no id", index)
		}
		if previous, duplicate := sessions[id]; duplicate {
			if !slices.Equal(instance.HaystackSessions[previous], instance.HaystackSessions[index]) {
				return fmt.Errorf("duplicate haystack session id %q has different content", id)
			}
			continue
		}
		sessions[id] = index
		if len(instance.HaystackSessions[index]) == 0 {
			return fmt.Errorf("haystack session %q is empty", id)
		}
		hasContent := false
		for _, turn := range instance.HaystackSessions[index] {
			if turn.Role != "user" && turn.Role != "assistant" {
				return fmt.Errorf("haystack session %q has invalid turn", id)
			}
			hasContent = hasContent || strings.TrimSpace(turn.Content) != ""
		}
		if !hasContent {
			return fmt.Errorf("haystack session %q has no content", id)
		}
	}
	if len(instance.AnswerSessionIDs) == 0 && !strings.HasSuffix(instance.QuestionID, "_abs") {
		return fmt.Errorf("non-abstention question requires answer_session_ids")
	}
	for _, id := range instance.AnswerSessionIDs {
		if _, exists := sessions[id]; !exists {
			return fmt.Errorf("answer session %q is not in the haystack", id)
		}
	}
	return nil
}

func longMemEvalSessionText(date string, turns []longMemEvalV1Turn) string {
	var output strings.Builder
	if date != "" {
		output.WriteString("Session date: ")
		output.WriteString(date)
		output.WriteByte('\n')
	}
	for _, turn := range turns {
		output.WriteString(turn.Role)
		output.WriteString(": ")
		output.WriteString(strings.TrimSpace(turn.Content))
		output.WriteByte('\n')
	}
	return strings.TrimSpace(output.String())
}

func longMemEvalV1ExpectedReferences(instance longMemEvalV1Instance) []string {
	if strings.HasSuffix(instance.QuestionID, "_abs") {
		return nil
	}
	return instance.AnswerSessionIDs
}

func longMemEvalV1Strategy(topK int) strategydef.Package {
	parameters, _ := json.Marshal(map[string]any{"granularity": "session", "retrieval": "lexical", "top_k": topK})
	return strategydef.Package{
		ID: "adapter.longmemeval-v1.session", Version: "1.0.0", Implementation: "internal/evaluation/longmemeval_v1.go",
		Label: strategydef.ImplementationAdapter, Fidelity: strategydef.FidelityProtocolCompatible,
		Parameters: parameters, Capabilities: []string{"formation", "retrieval", "factual", "benchmark-adapter"},
		Repair: strategydef.RepairPolicy{MaxAttempts: 0, Strict: true}, PaperSources: []string{"arXiv:2410.10813"},
	}
}

func stableEvalID(prefix string, values ...string) domain.ID {
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return domain.ID(prefix + "-" + hex.EncodeToString(digest[:12]))
}
