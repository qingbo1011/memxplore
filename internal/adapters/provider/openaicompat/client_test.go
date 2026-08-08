package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qingbo1011/memxplore/internal/application"
)

func TestGenerateUsesExplicitEndpointAndSchema(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://ollama.test/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer explicit-key" {
			t.Errorf("unexpected request path=%q authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["response_format"] == nil {
			t.Error("missing JSON schema response format")
		}
		return jsonResponse(`{"choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`), nil
	})}
	client, err := New(Config{BaseURL: "http://ollama.test/v1", APIKey: "explicit-key", Client: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Generate(context.Background(), application.GenerationRequest{
		Model: "local", Messages: []application.Message{{Role: "user", Content: "test"}},
		JSONSchemaName: "result", JSONSchema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil || response.Text != `{"ok":true}` || response.Usage.TotalTokens != 5 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestEmbeddingResponseIsReorderedAndValidated(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}],"usage":{"prompt_tokens":2,"total_tokens":2}}`), nil
	})}
	client, err := New(Config{BaseURL: "http://ollama.test/v1", Client: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Embed(context.Background(), application.EmbeddingRequest{
		Model: "local", Input: []string{"first", "second"}, Dimensions: 2,
	})
	if err != nil || response.Vectors[0][0] != 1 || response.Vectors[1][1] != 1 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestNewRejectsImplicitOrUnsupportedEndpoint(t *testing.T) {
	for _, endpoint := range []string{"", "localhost:11434/v1", "file:///tmp/socket"} {
		if _, err := New(Config{BaseURL: endpoint}); err == nil {
			t.Fatalf("New(%q) succeeded", endpoint)
		}
	}
}
