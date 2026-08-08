package evaluation

import (
	"bufio"
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
)

const (
	longMemEvalV2QuestionCount     = 451
	longMemEvalV2SmallHaystackSize = 100
)

// LongMemEvalV2Config controls the metadata-only Small-tier adapter smoke test.
type LongMemEvalV2Config struct {
	DataRoot             string
	Revision             string
	RunID                string
	Seed                 int64
	Limit                int
	ExpectedHaystackSize int
	Clock                func() time.Time
}

type longMemEvalV2Question struct {
	ID           string          `json:"id"`
	Domain       string          `json:"domain"`
	Environment  string          `json:"environment"`
	QuestionType string          `json:"question_type"`
	Question     string          `json:"question"`
	Image        json.RawMessage `json:"image"`
	Answer       json.RawMessage `json:"answer"`
	EvalFunction string          `json:"eval_function"`
}

type longMemEvalV2Trajectory struct {
	ID          string            `json:"id"`
	Domain      string            `json:"domain"`
	Environment string            `json:"environment"`
	Goal        string            `json:"goal"`
	Outcome     string            `json:"outcome"`
	StartURL    string            `json:"start_url"`
	States      []json.RawMessage `json:"states"`
}

type longMemEvalV2State struct {
	StateIndex        int             `json:"state_index"`
	Step              json.RawMessage `json:"step"`
	URL               string          `json:"url"`
	Action            json.RawMessage `json:"action"`
	Thought           json.RawMessage `json:"thought"`
	AccessibilityTree json.RawMessage `json:"accessibility_tree"`
	Screenshot        json.RawMessage `json:"screenshot"`
}

// RunLongMemEvalV2Small validates and materializes the official Small-tier data contract.
// It deliberately does not claim memory retrieval or question-answering quality.
func RunLongMemEvalV2Small(config LongMemEvalV2Config) (Run, error) {
	if config.DataRoot == "" || config.Revision == "" || config.Limit < 0 {
		return Run{}, fmt.Errorf("LongMemEval-V2 data root, pinned revision, and non-negative limit are required")
	}
	if config.ExpectedHaystackSize == 0 {
		config.ExpectedHaystackSize = longMemEvalV2SmallHaystackSize
	}
	if config.ExpectedHaystackSize < 1 {
		return Run{}, fmt.Errorf("LongMemEval-V2 expected haystack size must be positive")
	}
	clock := config.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	started := clock().UTC()
	questionsPath := filepath.Join(config.DataRoot, "questions.jsonl")
	trajectoriesPath := filepath.Join(config.DataRoot, "trajectories.jsonl")
	haystacksPath := filepath.Join(config.DataRoot, "haystacks", "lme_v2_small.json")
	datasetDigest, err := digestNamedFiles(map[string]string{
		"questions.jsonl": questionsPath, "trajectories.jsonl": trajectoriesPath, "haystacks/lme_v2_small.json": haystacksPath,
	})
	if err != nil {
		return Run{}, fmt.Errorf("hash LongMemEval-V2 Small data: %w", err)
	}
	if config.RunID == "" {
		config.RunID = fmt.Sprintf("longmemeval-v2-small-%s-%s", started.Format("20060102T150405.000000000Z"), datasetDigest[:8])
	}
	haystacks, err := readLongMemEvalV2Haystacks(haystacksPath)
	if err != nil {
		return Run{}, err
	}
	questions, err := readLongMemEvalV2Questions(questionsPath, config.Limit)
	if err != nil {
		return Run{}, err
	}
	if len(questions) == 0 {
		return Run{}, fmt.Errorf("LongMemEval-V2 questions file has no selected cases")
	}
	if config.Limit == 0 && len(questions) != longMemEvalV2QuestionCount {
		return Run{}, fmt.Errorf("full LongMemEval-V2 run requires %d questions, dataset contained %d", longMemEvalV2QuestionCount, len(questions))
	}
	required := make(map[string]struct{})
	domainHaystacks := make(map[string][]string)
	for _, question := range questions {
		ids, exists := haystacks[question.ID]
		if !exists {
			return Run{}, fmt.Errorf("question %q has no Small-tier haystack", question.ID)
		}
		if len(ids) != config.ExpectedHaystackSize {
			return Run{}, fmt.Errorf("question %q has %d trajectories; expected %d", question.ID, len(ids), config.ExpectedHaystackSize)
		}
		seenIDs := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			if id == "" {
				return Run{}, fmt.Errorf("question %q has an empty trajectory id", question.ID)
			}
			if _, duplicate := seenIDs[id]; duplicate {
				return Run{}, fmt.Errorf("question %q repeats trajectory %q", question.ID, id)
			}
			seenIDs[id] = struct{}{}
		}
		if previous, exists := domainHaystacks[question.Domain]; exists && !slices.Equal(previous, ids) {
			return Run{}, fmt.Errorf("domain %q does not share one ordered Small-tier haystack", question.Domain)
		}
		domainHaystacks[question.Domain] = append([]string(nil), ids...)
		for _, id := range ids {
			required[id] = struct{}{}
		}
	}
	materializeStarted := time.Now()
	trajectories, ingestTokens, err := readLongMemEvalV2Trajectories(trajectoriesPath, required)
	if err != nil {
		return Run{}, err
	}
	if len(trajectories) != len(required) {
		missing := make([]string, 0)
		for id := range required {
			if _, exists := trajectories[id]; !exists {
				missing = append(missing, id)
			}
		}
		slices.Sort(missing)
		return Run{}, fmt.Errorf("Small-tier haystacks reference %d missing trajectories: %s", len(missing), strings.Join(missing, ", "))
	}
	predictions := make([]Prediction, 0, len(questions)*2)
	traces := make([]TraceReference, 0, len(questions))
	for _, question := range questions {
		ids := haystacks[question.ID]
		caseStarted := time.Now()
		resolved := make([]string, 0, len(ids))
		for _, id := range ids {
			trajectory := trajectories[id]
			if trajectory.Domain != question.Domain {
				return Run{}, fmt.Errorf("question %q domain %q references trajectory %q in domain %q", question.ID, question.Domain, id, trajectory.Domain)
			}
			resolved = append(resolved, id)
		}
		trace := AdapterTrace{
			ID: string(stableEvalID("trace", "longmemeval-v2-small", question.ID)), Adapter: "longmemeval-v2-small@1.0.0",
			CaseID: question.ID, InputIDs: append([]string(nil), ids...), ResolvedIDs: append([]string(nil), resolved...), DatasetSHA256: datasetDigest,
		}
		reference, err := NewTraceReference(question.ID, "schema-adapter", "adapter", trace)
		if err != nil {
			return Run{}, err
		}
		traces = append(traces, reference)
		predictions = append(predictions,
			Prediction{CaseID: question.ID, Category: question.QuestionType, Variant: "no-memory", Query: question.Question, ExpectedReferences: append([]string(nil), ids...)},
			Prediction{
				CaseID: question.ID, Category: question.QuestionType, Variant: "schema-adapter", Query: question.Question,
				ExpectedReferences: append([]string(nil), ids...), Retrieved: rankedReferences(resolved), TraceIDs: []string{trace.ID},
				LatencyMS: float64(time.Since(caseStarted).Microseconds()) / 1000,
			},
		)
	}
	metrics := Score(predictions, config.ExpectedHaystackSize)
	metrics.IndexedUnits = len(trajectories)
	metrics.IngestTokens = ingestTokens
	metrics.IngestLatencyMS = float64(time.Since(materializeStarted).Microseconds()) / 1000
	manifest := NewManifest(config.RunID, "longmemeval-v2-small-adapter-smoke", "longmemeval-v2-small@1.0.0", config.Seed, DatasetIdentity{
		Name: "xiaowu0162/longmemeval-v2", Revision: config.Revision, SHA256: datasetDigest,
		Path: filepath.Base(filepath.Clean(config.DataRoot)), License: "Apache-2.0",
	}, []Variant{
		{ID: "no-memory", Description: "No-materialization paired control."},
		{ID: "schema-adapter", Description: "Strict schema, haystack, domain, and trajectory-reference materialization check."},
	}, started)
	manifest.TopK = config.ExpectedHaystackSize
	manifest.Limit = len(questions)
	manifest.CompletedAt = clock().UTC()
	manifest.Limitations = []string{
		"This smoke test measures adapter materialization completeness only; it is not a memory retrieval or question-answering evaluation.",
		"Reported Recall@K and MRR are integrity checks over official haystack references, not benchmark quality scores.",
		"No screenshots, memory backend, reader model, evaluator, provider calls, or monetary cost are involved.",
		"The adapter is protocol-compatible with pinned Small-tier metadata and makes no reproduction or leaderboard claim.",
	}
	if config.Limit > 0 {
		manifest.Limitations = append(manifest.Limitations, fmt.Sprintf("Partial bounded smoke run: first %d questions only.", len(questions)))
	}
	return Run{Manifest: manifest, Predictions: predictions, Metrics: metrics, Traces: traces}, nil
}

func readLongMemEvalV2Questions(path string, limit int) ([]longMemEvalV2Question, error) {
	questions := make([]longMemEvalV2Question, 0)
	err := decodeJSONSequence(path, func(index int, decoder *json.Decoder) error {
		var question longMemEvalV2Question
		if err := decoder.Decode(&question); err != nil {
			return err
		}
		if limit > 0 && index >= limit {
			return nil
		}
		if question.ID == "" || (question.Domain != "web" && question.Domain != "enterprise") || question.Environment == "" || question.QuestionType == "" || strings.TrimSpace(question.Question) == "" || len(question.Image) == 0 || len(question.Answer) == 0 || question.EvalFunction == "" {
			return fmt.Errorf("question %d is incomplete", index)
		}
		questions = append(questions, question)
		return nil
	})
	return questions, err
}

func readLongMemEvalV2Trajectories(path string, required map[string]struct{}) (map[string]longMemEvalV2Trajectory, int, error) {
	result := make(map[string]longMemEvalV2Trajectory, len(required))
	tokens := 0
	err := decodeJSONSequence(path, func(index int, decoder *json.Decoder) error {
		var trajectory longMemEvalV2Trajectory
		if err := decoder.Decode(&trajectory); err != nil {
			return err
		}
		if _, wanted := required[trajectory.ID]; !wanted {
			return nil
		}
		if _, duplicate := result[trajectory.ID]; duplicate {
			return fmt.Errorf("duplicate required trajectory %q", trajectory.ID)
		}
		if err := validateLongMemEvalV2Trajectory(trajectory); err != nil {
			return fmt.Errorf("trajectory %d: %w", index, err)
		}
		encoded, _ := json.Marshal(trajectory)
		tokens += estimateTokens(string(encoded))
		result[trajectory.ID] = trajectory
		return nil
	})
	return result, tokens, err
}

func validateLongMemEvalV2Trajectory(trajectory longMemEvalV2Trajectory) error {
	if trajectory.ID == "" || (trajectory.Domain != "web" && trajectory.Domain != "enterprise") || trajectory.Environment == "" || strings.TrimSpace(trajectory.Goal) == "" || (trajectory.Outcome != "success" && trajectory.Outcome != "failure") || trajectory.StartURL == "" || len(trajectory.States) == 0 {
		return fmt.Errorf("trajectory %q is incomplete", trajectory.ID)
	}
	for index, raw := range trajectory.States {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		var state longMemEvalV2State
		if err := decoder.Decode(&state); err != nil {
			return fmt.Errorf("trajectory %q state %d: %w", trajectory.ID, index, err)
		}
		if state.StateIndex != index || state.URL == "" || len(state.Step) == 0 || len(state.Action) == 0 || len(state.Thought) == 0 || len(state.AccessibilityTree) == 0 || len(state.Screenshot) == 0 {
			return fmt.Errorf("trajectory %q state %d is incomplete or out of order", trajectory.ID, index)
		}
		if err := requireJSONEOF(decoder); err != nil {
			return fmt.Errorf("trajectory %q state %d: %w", trajectory.ID, index, err)
		}
	}
	return nil
}

func readLongMemEvalV2Haystacks(path string) (map[string][]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open LongMemEval-V2 haystacks: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReaderSize(file, 1<<20))
	var haystacks map[string][]string
	if err := decoder.Decode(&haystacks); err != nil {
		return nil, fmt.Errorf("decode LongMemEval-V2 haystacks: %w", err)
	}
	if len(haystacks) == 0 {
		return nil, fmt.Errorf("LongMemEval-V2 haystacks are empty")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	return haystacks, nil
}

func decodeJSONSequence(path string, visit func(int, *json.Decoder) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReaderSize(file, 1<<20))
	decoder.DisallowUnknownFields()
	for index := 0; ; index++ {
		if err := visit(index, decoder); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("decode %s record %d: %w", filepath.Base(path), index, err)
		}
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("JSON contains trailing content")
	}
	return nil
}

func digestNamedFiles(paths map[string]string) (string, error) {
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	slices.Sort(names)
	hash := sha256.New()
	for _, name := range names {
		file, err := os.Open(paths[name])
		if err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte(name))
		_, _ = hash.Write([]byte{0})
		if _, err := io.Copy(hash, file); err != nil {
			_ = file.Close()
			return "", err
		}
		_ = file.Close()
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func rankedReferences(ids []string) []RankedReference {
	result := make([]RankedReference, len(ids))
	for index, id := range ids {
		result[index] = RankedReference{Reference: id, Rank: index + 1, Score: 1}
	}
	return result
}
