package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	ollamaprovider "github.com/qingbo1011/memxplore/internal/adapters/provider/ollama"
	"github.com/qingbo1011/memxplore/internal/adapters/provider/openaicompat"
	"github.com/qingbo1011/memxplore/internal/adapters/sqlite"
	"github.com/qingbo1011/memxplore/internal/agentevent"
	"github.com/qingbo1011/memxplore/internal/api"
	"github.com/qingbo1011/memxplore/internal/application"
	"github.com/qingbo1011/memxplore/internal/auth"
	"github.com/qingbo1011/memxplore/internal/buildinfo"
	"github.com/qingbo1011/memxplore/internal/daemon"
	"github.com/qingbo1011/memxplore/internal/domain"
	"github.com/qingbo1011/memxplore/internal/evaluation"
	"github.com/qingbo1011/memxplore/internal/observability"
	"github.com/qingbo1011/memxplore/internal/telemetry"
	"github.com/qingbo1011/memxplore/sdk"
)

const (
	defaultAPIURL         = "http://127.0.0.1:7878"
	defaultListen         = "127.0.0.1:7878"
	defaultDB             = "memxplore.sqlite"
	defaultEmbeddingModel = "qwen3-embedding:0.6b"
	defaultGeneratorModel = "hf.co/HauhauCS/Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive:Q4_K_M"
)

type versionOutput struct {
	Program       string `json:"program"`
	Version       string `json:"version"`
	Protocol      string `json:"protocol_version"`
	StorageSchema int    `json:"storage_schema_version"`
	ExportSchema  int    `json:"export_schema_version"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runContext(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runContext(context.Background(), args, os.Stdin, stdout, stderr)
}

func runContext(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(stdout)
		return 0
	}
	switch args[0] {
	case "version":
		return printVersion(args[1:], stdout, stderr)
	case "serve":
		return serveCommand(ctx, args[1:], stdout, stderr)
	case "mcp":
		return mcpCommand(ctx, args[1:], stdin, stdout, stderr)
	case "token":
		return tokenCommand(ctx, args[1:], stdout, stderr)
	case "remember":
		return rememberCommand(ctx, args[1:], stdout, stderr)
	case "recall":
		return recallCommand(ctx, args[1:], stdout, stderr)
	case "job":
		return jobCommand(ctx, args[1:], stdout, stderr)
	case "archive", "forget", "purge":
		return lifecycleCommand(ctx, args[0], args[1:], stdout, stderr)
	case "ingest":
		return ingestCommand(ctx, args[1:], stdin, stdout, stderr)
	case "data":
		return dataCommand(ctx, args[1:], stdout, stderr)
	case "benchmark":
		return benchmarkCommand(ctx, args[1:], stdout, stderr)
	case "eval":
		return evalCommand(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "memxplore: unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printVersion(args []string, stdout, stderr io.Writer) int {
	output := versionOutput{
		Program: "memxplore", Version: buildinfo.Version, Protocol: buildinfo.ProtocolVersion,
		StorageSchema: buildinfo.StorageSchemaVersion, ExportSchema: buildinfo.ExportSchemaVersion,
	}
	if len(args) == 0 {
		fmt.Fprintf(stdout, "%s %s (protocol %s, storage schema %d, export schema %d)\n",
			output.Program, output.Version, output.Protocol, output.StorageSchema, output.ExportSchema)
		return 0
	}
	if len(args) == 1 && args[0] == "--json" {
		return writeCLIJSON(stdout, stderr, output)
	}
	fmt.Fprintln(stderr, "usage: memxplore version [--json]")
	return 2
}

type runtimeFlags struct {
	db                  string
	namespace           string
	owner               string
	actor               string
	ollamaURL           string
	embeddingModel      string
	embeddingDimensions int
	generatorModel      string
	enableAssisted      bool
	enableAgentEvents   bool
	otelEndpoint        string
	otelServiceName     string
}

func addRuntimeFlags(flags *flag.FlagSet, config *runtimeFlags) {
	flags.StringVar(&config.db, "db", defaultDB, "SQLite database path")
	flags.StringVar(&config.namespace, "namespace", "local", "local namespace")
	flags.StringVar(&config.owner, "owner", "local", "local private owner")
	flags.StringVar(&config.actor, "actor", "local-cli", "local actor identifier")
	flags.StringVar(&config.ollamaURL, "ollama-url", "", "explicit Ollama OpenAI-compatible URL, e.g. http://127.0.0.1:11434/v1")
	flags.StringVar(&config.embeddingModel, "embedding-model", defaultEmbeddingModel, "configured embedding model")
	flags.IntVar(&config.embeddingDimensions, "embedding-dimensions", 1024, "configured embedding dimensions")
	flags.StringVar(&config.generatorModel, "generator-model", defaultGeneratorModel, "configured generator model")
	flags.BoolVar(&config.enableAssisted, "enable-assisted", false, "enable generator-assisted formation using the explicit Ollama URL")
	flags.BoolVar(&config.enableAgentEvents, "enable-agent-events", false, "enable opt-in AgentEvent HTTP ingestion")
	flags.StringVar(&config.otelEndpoint, "otel-endpoint", "", "explicit OTLP/HTTP collector base URL")
	flags.StringVar(&config.otelServiceName, "otel-service-name", "memxplore", "OpenTelemetry service name")
}

type localRuntime struct {
	store     *sqlite.Store
	worker    *daemon.FormationWorker
	api       *api.Server
	telemetry *telemetry.Runtime
}

func (r *localRuntime) Close() error {
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(r.store.Close(), r.telemetry.Shutdown(shutdownContext))
}

func buildRuntime(ctx context.Context, config runtimeFlags, allowLoopbackWithoutToken bool) (*localRuntime, error) {
	telemetryRuntime, err := telemetry.Setup(ctx, telemetry.Config{
		Endpoint: config.otelEndpoint, ServiceName: config.otelServiceName, ServiceVersion: buildinfo.Version,
	})
	if err != nil {
		return nil, err
	}
	store, err := sqlite.Open(ctx, config.db, sqlite.DefaultOptions())
	if err != nil {
		_ = telemetryRuntime.Shutdown(ctx)
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = store.Close()
			_ = telemetryRuntime.Shutdown(ctx)
		}
	}()
	var provider *openaicompat.Client
	if config.ollamaURL != "" {
		provider, err = openaicompat.New(openaicompat.Config{BaseURL: config.ollamaURL})
		if err != nil {
			return nil, err
		}
	}
	if config.enableAssisted && provider == nil {
		return nil, fmt.Errorf("--enable-assisted requires --ollama-url")
	}
	retrieverConfig := application.RetrieverConfig{Repository: store, TraceSink: store, Observability: telemetryRuntime.Recorder}
	workerConfig := daemon.FormationConfig{Store: store, Observability: telemetryRuntime.Recorder}
	if provider != nil {
		retrieverConfig.Embedder = provider
		retrieverConfig.EmbeddingProvider = "ollama"
		retrieverConfig.EmbeddingModel = config.embeddingModel
		retrieverConfig.EmbeddingDimensions = config.embeddingDimensions
		workerConfig.Embedder = provider
		workerConfig.EmbeddingProvider = "ollama"
		workerConfig.EmbeddingModel = config.embeddingModel
		workerConfig.EmbeddingDimensions = config.embeddingDimensions
	}
	if config.enableAssisted {
		workerConfig.Generator = provider
		workerConfig.GeneratorProvider = "ollama"
		workerConfig.GeneratorModel = config.generatorModel
	}
	retriever, err := application.NewRetriever(retrieverConfig)
	if err != nil {
		return nil, err
	}
	worker, err := daemon.NewFormationWorker(workerConfig)
	if err != nil {
		return nil, err
	}
	principal := auth.Principal{
		PrincipalID: domain.ID(config.actor), Namespace: domain.ID(config.namespace), PrivateOwners: []domain.ID{domain.ID(config.owner)},
		Scopes: []auth.Scope{auth.ScopeMemoryRead, auth.ScopeMemoryWrite, auth.ScopeMemoryPurge, auth.ScopeAdmin},
	}
	server, err := api.NewServer(api.Config{
		Store: store, Retriever: retriever, Worker: worker, LoopbackPrincipal: principal,
		AllowLoopbackWithoutToken: allowLoopbackWithoutToken, EnableAgentEvents: config.enableAgentEvents,
		Observability: telemetryRuntime.Recorder,
	})
	if err != nil {
		return nil, err
	}
	failed = false
	return &localRuntime{store: store, worker: worker, api: server, telemetry: telemetryRuntime}, nil
}

func serveCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config runtimeFlags
	addRuntimeFlags(flags, &config)
	listen := flags.String("listen", defaultListen, "HTTP listen address")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	loopback := listenIsLoopback(*listen)
	runtime, err := buildRuntime(ctx, config, loopback)
	if err != nil {
		return cliError(stderr, err)
	}
	defer runtime.Close()
	if !loopback {
		count, countErr := runtime.store.APITokenCount(ctx, time.Now().UTC())
		if countErr != nil {
			return cliError(stderr, countErr)
		}
		if count == 0 {
			fmt.Fprintln(stderr, "memxplore: refusing non-loopback listen without an active scoped token; create one with `memxplore token create`")
			return 1
		}
	}
	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = runtime.worker.Run(serverCtx) }()
	httpServer := &http.Server{
		Addr: *listen, Handler: runtime.api.Handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 2 * time.Minute,
	}
	errorsChannel := make(chan error, 1)
	go func() { errorsChannel <- httpServer.ListenAndServe() }()
	fmt.Fprintf(stdout, "MemXplore %s listening on http://%s (protocol %s, schema %d)\n",
		buildinfo.Version, *listen, buildinfo.ProtocolVersion, buildinfo.StorageSchemaVersion)
	select {
	case err := <-errorsChannel:
		if !errors.Is(err, http.ErrServerClosed) {
			return cliError(stderr, err)
		}
		return 0
	case <-ctx.Done():
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return cliError(stderr, err)
		}
		return 0
	}
}

func mcpCommand(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config runtimeFlags
	addRuntimeFlags(flags, &config)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	runtime, err := buildRuntime(ctx, config, true)
	if err != nil {
		return cliError(stderr, err)
	}
	defer runtime.Close()
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = runtime.worker.Run(workerCtx) }()
	principal := auth.Principal{
		PrincipalID: domain.ID(config.actor), Namespace: domain.ID(config.namespace), PrivateOwners: []domain.ID{domain.ID(config.owner)},
		Scopes: []auth.Scope{auth.ScopeMemoryRead, auth.ScopeMemoryWrite, auth.ScopeMemoryPurge, auth.ScopeAdmin},
	}
	if err := runtime.api.ServeMCPStdio(ctx, stdin, stdout, principal); err != nil && !errors.Is(err, context.Canceled) {
		return cliError(stderr, err)
	}
	return 0
}

func tokenCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprintln(stderr, "usage: memxplore token create [options]")
		return 2
	}
	flags := flag.NewFlagSet("token create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	db := flags.String("db", defaultDB, "SQLite database path")
	id := flags.String("id", "", "token identifier")
	principal := flags.String("principal", "api-client", "principal identifier")
	namespace := flags.String("namespace", "local", "namespace")
	owners := flags.String("owners", "local", "comma-separated private owners")
	scopes := flags.String("scopes", "memory:read,memory:write", "comma-separated scopes")
	expires := flags.String("expires", "", "optional RFC3339 expiry")
	allowShared := flags.Bool("allow-shared", false, "allow shared memory")
	allowPublic := flags.Bool("allow-public", false, "allow public memory")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	now := time.Now().UTC()
	var expiresAt *time.Time
	if *expires != "" {
		parsed, err := time.Parse(time.RFC3339, *expires)
		if err != nil {
			return cliError(stderr, fmt.Errorf("parse --expires: %w", err))
		}
		expiresAt = &parsed
	}
	tokenID := *id
	if tokenID == "" {
		tokenID = "token-" + strconv.FormatInt(now.UnixNano(), 36)
	}
	store, err := sqlite.Open(ctx, *db, sqlite.DefaultOptions())
	if err != nil {
		return cliError(stderr, err)
	}
	defer store.Close()
	spec := auth.TokenSpec{
		ID: domain.ID(tokenID), PrincipalID: domain.ID(*principal), Namespace: domain.ID(*namespace),
		PrivateOwners: domainIDs(splitCSV(*owners)), Scopes: authScopes(splitCSV(*scopes)),
		AllowShared: *allowShared, AllowPublic: *allowPublic, ExpiresAt: expiresAt, CreatedAt: now,
	}
	raw, err := store.CreateAPIToken(ctx, spec)
	if err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, map[string]any{"id": spec.ID, "token": raw, "warning": "save this token now; only its SHA-256 digest is stored"})
}

type remoteFlags struct {
	url   string
	token string
}

func addRemoteFlags(flags *flag.FlagSet, config *remoteFlags) {
	flags.StringVar(&config.url, "url", defaultAPIURL, "daemon base URL")
	flags.StringVar(&config.token, "token", "", "explicit bearer token")
}

func remoteClient(config remoteFlags) (*sdk.Client, error) {
	options := []sdk.Option{}
	if config.token != "" {
		options = append(options, sdk.WithBearerToken(config.token))
	}
	return sdk.NewClient(config.url, options...)
}

func rememberCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("remember", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var remote remoteFlags
	addRemoteFlags(flags, &remote)
	owner := flags.String("owner", "local", "memory owner")
	subject := flags.String("subject", "local", "data subject")
	contextID := flags.String("context", "", "context or task identifier")
	function := flags.String("function", "factual", "factual, experiential, or working")
	strategy := flags.String("strategy", "generator-free", "generator-free or assisted")
	source := flags.String("source", "cli", "source kind")
	idempotency := flags.String("idempotency-key", "", "caller-stable retry key")
	textValue := flags.String("text", "", "evidence text")
	wait := flags.Int("wait", 30000, "milliseconds to wait for terminal job state")
	ttl := flags.Int64("working-ttl", 0, "working-memory TTL in seconds")
	global := flags.Bool("working-global", false, "opt working memory into global recall")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*textValue) == "" {
		return cliError(stderr, fmt.Errorf("--text is required"))
	}
	key := *idempotency
	if key == "" {
		key = "cli-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	client, err := remoteClient(remote)
	if err != nil {
		return cliError(stderr, err)
	}
	result, err := client.Remember(ctx, sdk.RememberRequest{
		IdempotencyKey: key, Owner: sdk.ID(*owner), Subject: sdk.ID(*subject), Context: sdk.ID(*contextID),
		SourceKind: *source, Content: sdk.TextContent(*textValue), Function: *function, Strategy: *strategy,
		WorkingTTLSeconds: *ttl, WorkingGlobalRecall: *global, WaitMilliseconds: *wait,
	})
	if err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, result)
}

func recallCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("recall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var remote remoteFlags
	addRemoteFlags(flags, &remote)
	owner := flags.String("owner", "local", "memory owner")
	subject := flags.String("subject", "local", "data subject")
	contextID := flags.String("context", "", "context or task identifier")
	query := flags.String("query", "", "retrieval query")
	mode := flags.String("mode", "auto", "auto, lexical, semantic, or hybrid")
	functions := flags.String("functions", "", "optional comma-separated memory functions")
	tokenBudget := flags.Int("token-budget", 2048, "maximum estimated evidence tokens")
	candidateLimit := flags.Int("candidate-limit", 20, "maximum candidates before budgeting")
	global := flags.Bool("include-global-working", false, "include explicitly global working memory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*query) == "" {
		return cliError(stderr, fmt.Errorf("--query is required"))
	}
	client, err := remoteClient(remote)
	if err != nil {
		return cliError(stderr, err)
	}
	result, err := client.Recall(ctx, sdk.RecallRequest{
		Owner: sdk.ID(*owner), Subject: sdk.ID(*subject), Context: sdk.ID(*contextID), Query: *query,
		Functions: splitCSV(*functions), Mode: *mode, TokenBudget: *tokenBudget, CandidateLimit: *candidateLimit,
		IncludeGlobalWorking: *global,
	})
	if err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, result)
}

func jobCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("job", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var remote remoteFlags
	addRemoteFlags(flags, &remote)
	id := flags.String("id", "", "job identifier")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *id == "" {
		return cliError(stderr, fmt.Errorf("--id is required"))
	}
	client, err := remoteClient(remote)
	if err != nil {
		return cliError(stderr, err)
	}
	result, err := client.Job(ctx, sdk.ID(*id))
	if err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, result)
}

func lifecycleCommand(ctx context.Context, action string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var remote remoteFlags
	addRemoteFlags(flags, &remote)
	id := flags.String("id", "", "memory identifier")
	confirm := flags.Bool("confirm", false, "confirm irreversible purge")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *id == "" {
		return cliError(stderr, fmt.Errorf("--id is required"))
	}
	if action == "purge" && !*confirm {
		return cliError(stderr, fmt.Errorf("purge is irreversible; repeat with --confirm"))
	}
	client, err := remoteClient(remote)
	if err != nil {
		return cliError(stderr, err)
	}
	var result any
	switch action {
	case "archive":
		result, err = client.Archive(ctx, sdk.ID(*id))
	case "forget":
		result, err = client.Forget(ctx, sdk.ID(*id))
	case "purge":
		result, err = client.Purge(ctx, sdk.ID(*id))
	}
	if err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, result)
}

func ingestCommand(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "codex" {
		fmt.Fprintln(stderr, "usage: memxplore ingest codex [options]")
		return 2
	}
	flags := flag.NewFlagSet("ingest codex", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var remote remoteFlags
	addRemoteFlags(flags, &remote)
	file := flags.String("file", "-", "Codex JSONL file, or - for stdin")
	owner := flags.String("owner", "local", "memory owner")
	subject := flags.String("subject", "local", "data subject")
	function := flags.String("function", "factual", "memory function")
	strategy := flags.String("strategy", "generator-free", "formation strategy")
	wait := flags.Int("wait", 0, "milliseconds to wait per event")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	input := stdin
	var opened *os.File
	if *file != "-" {
		var err error
		opened, err = os.Open(*file)
		if err != nil {
			return cliError(stderr, err)
		}
		defer opened.Close()
		input = opened
	}
	client, err := remoteClient(remote)
	if err != nil {
		return cliError(stderr, err)
	}
	results := make([]sdk.RememberResponse, 0)
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for line := 1; scanner.Scan(); line++ {
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		event, err := agentevent.ParseCodexJSON(scanner.Bytes(), domain.ID(*owner), domain.ID(*subject))
		if err != nil {
			return cliError(stderr, fmt.Errorf("line %d: %w", line, err))
		}
		var publicEvent sdk.AgentEvent
		encoded, _ := json.Marshal(event)
		if err := json.Unmarshal(encoded, &publicEvent); err != nil {
			return cliError(stderr, err)
		}
		result, err := client.IngestAgentEvent(ctx, sdk.AgentEventRequest{
			Event: publicEvent, Function: *function, Strategy: *strategy,
			WaitMilliseconds: *wait,
		})
		if err != nil {
			return cliError(stderr, fmt.Errorf("line %d: %w", line, err))
		}
		results = append(results, result)
	}
	if err := scanner.Err(); err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, map[string]any{"accepted": len(results), "results": results})
}

const maxPortableExportBytes = 256 << 20

func dataCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: memxplore data <export|import|validate|backup|restore> [options]")
		return 2
	}
	switch args[0] {
	case "export":
		return dataExportCommand(ctx, args[1:], stdout, stderr)
	case "import":
		return dataImportCommand(ctx, args[1:], stdout, stderr)
	case "validate":
		return dataValidateCommand(ctx, args[1:], stdout, stderr)
	case "backup":
		return dataBackupCommand(ctx, args[1:], stdout, stderr)
	case "restore":
		return dataRestoreCommand(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "memxplore: unknown data command %q\n", args[0])
		return 2
	}
}

func dataExportCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("data export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	db := flags.String("db", defaultDB, "SQLite database path")
	namespace := flags.String("namespace", "local", "authorized namespace")
	principal := flags.String("principal", "local-cli", "principal identifier")
	subject := flags.String("subject", "", "data subject identifier")
	owners := flags.String("owners", "local", "comma-separated authorized private owners")
	includeShared := flags.Bool("include-shared", false, "include authorized shared data")
	includePublic := flags.Bool("include-public", false, "include authorized public data")
	output := flags.String("output", "", "new export JSON path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *subject == "" || *output == "" {
		return cliError(stderr, fmt.Errorf("--subject and --output are required"))
	}
	store, err := sqlite.Open(ctx, *db, sqlite.DefaultOptions())
	if err != nil {
		return cliError(stderr, err)
	}
	defer store.Close()
	export, err := store.ExportSubject(ctx, application.AccessScope{
		PrincipalID: domain.ID(*principal), Namespace: domain.ID(*namespace),
		PrivateOwners: domainIDs(splitCSV(*owners)), AllowShared: *includeShared, AllowPublic: *includePublic,
	}, domain.ID(*subject), time.Now().UTC())
	if err != nil {
		return cliError(stderr, err)
	}
	digest, err := writeSubjectExportFile(*output, export)
	if err != nil {
		return cliError(stderr, err)
	}
	counts := portableCounts(export)
	return writeCLIJSON(stdout, stderr, map[string]any{
		"path": *output, "sha256": digest, "schema_version": export.SchemaVersion, "counts": counts,
	})
}

func dataImportCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("data import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	db := flags.String("db", defaultDB, "SQLite database path")
	input := flags.String("input", "", "subject export JSON path")
	dryRun := flags.Bool("dry-run", false, "validate all writes and roll back")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *input == "" {
		return cliError(stderr, fmt.Errorf("--input is required"))
	}
	export, err := readSubjectExportFile(*input)
	if err != nil {
		return cliError(stderr, err)
	}
	store, err := sqlite.Open(ctx, *db, sqlite.DefaultOptions())
	if err != nil {
		return cliError(stderr, err)
	}
	defer store.Close()
	result, err := store.ImportSubject(ctx, export, *dryRun)
	if err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, result)
}

func dataValidateCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("data validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	db := flags.String("db", defaultDB, "SQLite database path")
	input := flags.String("input", "", "optional subject export JSON path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *input != "" {
		export, err := readSubjectExportFile(*input)
		if err != nil {
			return cliError(stderr, err)
		}
		return writeCLIJSON(stdout, stderr, map[string]any{
			"valid": true, "kind": "subject_export", "schema_version": export.SchemaVersion, "counts": portableCounts(export),
		})
	}
	store, err := sqlite.Open(ctx, *db, sqlite.DefaultOptions())
	if err != nil {
		return cliError(stderr, err)
	}
	defer store.Close()
	if err := store.Validate(ctx); err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, map[string]any{"valid": true, "kind": "sqlite", "path": *db})
}

func dataBackupCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("data backup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	db := flags.String("db", defaultDB, "SQLite database path")
	output := flags.String("output", "", "new backup path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *output == "" {
		return cliError(stderr, fmt.Errorf("--output is required"))
	}
	store, err := sqlite.Open(ctx, *db, sqlite.DefaultOptions())
	if err != nil {
		return cliError(stderr, err)
	}
	defer store.Close()
	if err := store.Backup(ctx, *output); err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, map[string]any{"path": *output, "valid": true})
}

func dataRestoreCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("data restore", flag.ContinueOnError)
	flags.SetOutput(stderr)
	backup := flags.String("backup", "", "verified SQLite backup path")
	db := flags.String("db", defaultDB, "restore target database path")
	overwrite := flags.Bool("overwrite", false, "replace an existing target while preserving it")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *backup == "" {
		return cliError(stderr, fmt.Errorf("--backup is required"))
	}
	result, err := sqlite.RestoreFile(ctx, *backup, *db, *overwrite)
	if err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, map[string]any{
		"path": *db, "valid": true, "replaced_backup": result.ReplacedBackup,
	})
}

func writeSubjectExportFile(path string, export application.SubjectExport) (string, error) {
	if err := export.Validate(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("create export directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create export file: %w", err)
	}
	hash := sha256.New()
	encoder := json.NewEncoder(io.MultiWriter(file, hash))
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(export); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("encode export file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("sync export file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close export file: %w", err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func readSubjectExportFile(path string) (application.SubjectExport, error) {
	file, err := os.Open(path)
	if err != nil {
		return application.SubjectExport{}, fmt.Errorf("open export file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return application.SubjectExport{}, fmt.Errorf("stat export file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxPortableExportBytes {
		return application.SubjectExport{}, fmt.Errorf("export must be a regular file no larger than %d bytes", maxPortableExportBytes)
	}
	var export application.SubjectExport
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&export); err != nil {
		return application.SubjectExport{}, fmt.Errorf("decode export file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return application.SubjectExport{}, fmt.Errorf("export file contains trailing JSON")
	}
	if err := export.Validate(); err != nil {
		return application.SubjectExport{}, fmt.Errorf("validate export file: %w", err)
	}
	return export, nil
}

func portableCounts(export application.SubjectExport) map[string]int {
	counts := map[string]int{
		"observations": len(export.Observations), "episodes": len(export.Episodes),
		"working_sets": len(export.WorkingSets), "memories": len(export.Memories),
	}
	for _, episode := range export.Episodes {
		counts["outcomes"] += len(episode.Outcomes)
	}
	for _, memory := range export.Memories {
		counts["versions"] += len(memory.Versions)
	}
	return counts
}

func benchmarkCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: memxplore benchmark <internal|longmemeval-v1|longmemeval-v1-local-answer|longmemeval-v2-small> [options]")
		return 2
	}
	var output, otelEndpoint, otelServiceName string
	var runBenchmark func(observability.Recorder) (evaluation.Run, error)
	switch args[0] {
	case "internal":
		flags := flag.NewFlagSet("benchmark internal", flag.ContinueOnError)
		flags.SetOutput(stderr)
		outputFlag := flags.String("output", "runs", "immutable run output root")
		runID := flags.String("run-id", "", "optional unique run identifier")
		seed := flags.Int64("seed", 1, "fixture seed recorded in the manifest")
		workDir := flags.String("work-dir", "", "optional temporary SQLite parent directory")
		addCommandTelemetryFlags(flags, &otelEndpoint, &otelServiceName)
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		output = *outputFlag
		runBenchmark = func(recorder observability.Recorder) (evaluation.Run, error) {
			return evaluation.RunInternal(ctx, evaluation.InternalConfig{RunID: *runID, Seed: *seed, WorkDir: *workDir, Observability: recorder})
		}
	case "longmemeval-v1":
		flags := flag.NewFlagSet("benchmark longmemeval-v1", flag.ContinueOnError)
		flags.SetOutput(stderr)
		outputFlag := flags.String("output", "runs", "immutable run output root")
		dataset := flags.String("dataset", "", "path to longmemeval_s_cleaned.json")
		revision := flags.String("revision", "", "pinned upstream dataset revision")
		runID := flags.String("run-id", "", "optional unique run identifier")
		seed := flags.Int64("seed", 1, "fixture seed recorded in the manifest")
		limit := flags.Int("limit", 0, "first N cases; 0 requires the full 500-case dataset")
		topK := flags.Int("top-k", 5, "retrieval cutoff")
		workDir := flags.String("work-dir", "", "optional temporary SQLite parent directory")
		addCommandTelemetryFlags(flags, &otelEndpoint, &otelServiceName)
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		output = *outputFlag
		runBenchmark = func(recorder observability.Recorder) (evaluation.Run, error) {
			return evaluation.RunLongMemEvalV1(ctx, evaluation.LongMemEvalV1Config{
				DatasetPath: *dataset, Revision: *revision, RunID: *runID, Seed: *seed, Limit: *limit, TopK: *topK,
				WorkDir: *workDir, Observability: recorder,
			})
		}
	case "longmemeval-v1-local-answer":
		flags := flag.NewFlagSet("benchmark longmemeval-v1-local-answer", flag.ContinueOnError)
		flags.SetOutput(stderr)
		outputFlag := flags.String("output", "runs", "immutable run output root")
		dataset := flags.String("dataset", "", "path to longmemeval_s_cleaned.json")
		revision := flags.String("revision", "", "pinned upstream dataset revision")
		runID := flags.String("run-id", "", "optional unique run identifier")
		seed := flags.Int64("seed", 1, "fixture seed recorded in the manifest")
		limit := flags.Int("limit", 2, "first N cases, within [1,10]")
		topK := flags.Int("top-k", 5, "retrieval cutoff")
		tokenBudget := flags.Int("token-budget", 4096, "maximum retrieved evidence tokens")
		maxTokens := flags.Int("max-tokens", 128, "maximum generated answer tokens")
		workDir := flags.String("work-dir", "", "optional temporary SQLite parent directory")
		ollamaURL := flags.String("ollama-url", "http://127.0.0.1:11434", "explicit loopback Ollama native base URL")
		model := flags.String("model", defaultGeneratorModel, "installed local Ollama generator model")
		timeout := flags.Duration("timeout", 10*time.Minute, "per-request local generation timeout")
		addCommandTelemetryFlags(flags, &otelEndpoint, &otelServiceName)
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if !providerURLIsLoopback(*ollamaURL) {
			return cliError(stderr, fmt.Errorf("local answer benchmark requires a loopback --ollama-url"))
		}
		if *timeout <= 0 || *timeout > 30*time.Minute {
			return cliError(stderr, fmt.Errorf("--timeout must be within (0,30m]"))
		}
		output = *outputFlag
		runBenchmark = func(recorder observability.Recorder) (evaluation.Run, error) {
			disableThinking := false
			generator, err := ollamaprovider.New(ollamaprovider.Config{BaseURL: *ollamaURL, Think: &disableThinking, Client: &http.Client{Timeout: *timeout}})
			if err != nil {
				return evaluation.Run{}, err
			}
			return evaluation.RunLongMemEvalV1AnswerSubset(ctx, evaluation.LongMemEvalV1AnswerConfig{
				DatasetPath: *dataset, Revision: *revision, RunID: *runID, Seed: *seed, Limit: *limit, TopK: *topK,
				TokenBudget: *tokenBudget, MaxTokens: *maxTokens, WorkDir: *workDir,
				Provider: "ollama-native", Model: *model, Generator: generator, Observability: recorder,
			})
		}
	case "longmemeval-v2-small":
		flags := flag.NewFlagSet("benchmark longmemeval-v2-small", flag.ContinueOnError)
		flags.SetOutput(stderr)
		outputFlag := flags.String("output", "runs", "immutable run output root")
		dataRoot := flags.String("data-root", "", "directory containing questions.jsonl, trajectories.jsonl, and haystacks")
		revision := flags.String("revision", "", "pinned upstream dataset revision")
		runID := flags.String("run-id", "", "optional unique run identifier")
		seed := flags.Int64("seed", 1, "fixture seed recorded in the manifest")
		limit := flags.Int("limit", 10, "first N questions; 0 validates all questions")
		haystackSize := flags.Int("expected-haystack-size", longMemEvalV2SmallHaystackSizeCLI, "required trajectories per Small-tier haystack")
		addCommandTelemetryFlags(flags, &otelEndpoint, &otelServiceName)
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		output = *outputFlag
		runBenchmark = func(recorder observability.Recorder) (evaluation.Run, error) {
			return evaluation.RunLongMemEvalV2Small(ctx, evaluation.LongMemEvalV2Config{
				DataRoot: *dataRoot, Revision: *revision, RunID: *runID, Seed: *seed, Limit: *limit,
				ExpectedHaystackSize: *haystackSize, Observability: recorder,
			})
		}
	default:
		fmt.Fprintf(stderr, "memxplore: unknown benchmark %q\n", args[0])
		return 2
	}
	telemetryRuntime, err := telemetry.Setup(ctx, telemetry.Config{
		Endpoint: otelEndpoint, ServiceName: otelServiceName, ServiceVersion: buildinfo.Version,
	})
	if err != nil {
		return cliError(stderr, err)
	}
	benchmarkRun, runErr := runBenchmark(telemetryRuntime.Recorder)
	if runErr != nil {
		return cliError(stderr, errors.Join(runErr, shutdownTelemetry(telemetryRuntime)))
	}
	path, writeErr := evaluation.WriteRun(output, benchmarkRun)
	shutdownErr := shutdownTelemetry(telemetryRuntime)
	if err := errors.Join(writeErr, shutdownErr); err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, map[string]any{
		"run_id": benchmarkRun.Manifest.RunID, "benchmark": benchmarkRun.Manifest.Benchmark, "path": path, "metrics": benchmarkRun.Metrics,
	})
}

const longMemEvalV2SmallHaystackSizeCLI = 100

func addCommandTelemetryFlags(flags *flag.FlagSet, endpoint, serviceName *string) {
	flags.StringVar(endpoint, "otel-endpoint", "", "explicit OTLP/HTTP collector base URL")
	flags.StringVar(serviceName, "otel-service-name", "memxplore", "OpenTelemetry service name")
}

func shutdownTelemetry(runtime *telemetry.Runtime) error {
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return runtime.Shutdown(shutdownContext)
}

func evalCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "verify" {
		fmt.Fprintln(stderr, "usage: memxplore eval verify --run <directory>")
		return 2
	}
	flags := flag.NewFlagSet("eval verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runDirectory := flags.String("run", "", "immutable run directory")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *runDirectory == "" {
		return cliError(stderr, fmt.Errorf("--run is required"))
	}
	if err := evaluation.VerifyRun(*runDirectory); err != nil {
		return cliError(stderr, err)
	}
	return writeCLIJSON(stdout, stderr, map[string]any{"run": *runDirectory, "valid": true})
}

func listenIsLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func providerURLIsLoopback(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func domainIDs(values []string) []domain.ID {
	result := make([]domain.ID, len(values))
	for index, value := range values {
		result[index] = domain.ID(value)
	}
	return result
}

func authScopes(values []string) []auth.Scope {
	result := make([]auth.Scope, len(values))
	for index, value := range values {
		result[index] = auth.Scope(value)
	}
	return result
}

func writeCLIJSON(stdout, stderr io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return cliError(stderr, err)
	}
	return 0
}

func cliError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "memxplore: %v\n", err)
	return 1
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "MemXplore - executable agent-memory reference implementation and research workbench")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Usage: memxplore <command> [options]")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Commands:")
	fmt.Fprintln(writer, "  serve       Run the durable HTTP daemon (loopback by default)")
	fmt.Fprintln(writer, "  mcp         Serve MCP JSON-RPC over stdin/stdout")
	fmt.Fprintln(writer, "  token       Create scoped daemon credentials")
	fmt.Fprintln(writer, "  remember    Capture evidence and form memory")
	fmt.Fprintln(writer, "  recall      Retrieve a structured RecallBundle")
	fmt.Fprintln(writer, "  job         Read durable job state")
	fmt.Fprintln(writer, "  archive     Archive memory")
	fmt.Fprintln(writer, "  forget      Logically forget memory")
	fmt.Fprintln(writer, "  purge       Irreversibly purge memory with --confirm")
	fmt.Fprintln(writer, "  ingest      Ingest opt-in agent adapter data")
	fmt.Fprintln(writer, "  data        Export, import, validate, back up, or restore local data")
	fmt.Fprintln(writer, "  benchmark   Run deterministic or LongMemEval evaluations")
	fmt.Fprintln(writer, "  eval        Verify immutable evaluation artifacts")
	fmt.Fprintln(writer, "  version     Print program and schema versions")
	fmt.Fprintln(writer, "  help        Show this help")
}
