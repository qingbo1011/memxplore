package telemetry

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/qingbo1011/memxplore/internal/observability"
)

func TestOTLPURLsRequireExplicitCredentialFreeBase(t *testing.T) {
	trace, metric, err := otlpURLs("https://collector.example/tenant/")
	if err != nil {
		t.Fatal(err)
	}
	if trace != "https://collector.example/tenant/v1/traces" || metric != "https://collector.example/tenant/v1/metrics" {
		t.Fatalf("trace=%q metric=%q", trace, metric)
	}
	for _, endpoint := range []string{"collector:4318", "ftp://collector", "https://user:secret@collector", "https://collector?token=secret"} {
		if _, _, err := otlpURLs(endpoint); err == nil {
			t.Fatalf("endpoint %q passed validation", endpoint)
		}
	}
}

func TestSetupWithoutEndpointIsNoop(t *testing.T) {
	runtime, err := Setup(t.Context(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, end := runtime.Recorder.Start(t.Context(), "test")
	if ctx == nil {
		t.Fatal("nil context")
	}
	end(nil)
	if err := runtime.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSetupExportsTraceAndMetricsOnShutdown(t *testing.T) {
	var mu sync.Mutex
	requests := make(map[string]int)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests[request.URL.Path]++
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request,
		}, nil
	})}
	runtime, err := Setup(t.Context(), Config{
		Endpoint: "http://collector.test", ServiceName: "memxplore-test", ServiceVersion: "test", httpClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, end := runtime.Recorder.Start(t.Context(), "test.operation", observability.String("mode", "fixture"))
	runtime.Recorder.Observe(ctx, observability.MetricBenchmarkCases, 1, observability.String("benchmark", "fixture"))
	end(nil)
	if err := runtime.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests["/v1/traces"] == 0 || requests["/v1/metrics"] == 0 {
		t.Fatalf("collector requests=%v", requests)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
