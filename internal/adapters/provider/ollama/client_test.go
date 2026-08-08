package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qingbo1011/memxplore/internal/application"
)

func TestGenerateMapsNativeChatAndDisablesThinking(t *testing.T) {
	disabled := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/chat" {
			t.Fatalf("path=%s", request.URL.Path)
		}
		var body struct {
			Think   *bool `json:"think"`
			Options struct {
				Temperature float64 `json:"temperature"`
				NumPredict  int     `json:"num_predict"`
			} `json:"options"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Think == nil || *body.Think || body.Options.Temperature != 0 || body.Options.NumPredict != 64 {
			t.Fatalf("body=%+v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"model":"local","created_at":"2026-08-08T00:00:00Z","message":{"role":"assistant","content":"4"},"done":true,"done_reason":"stop","total_duration":1,"load_duration":1,"prompt_eval_count":12,"prompt_eval_duration":1,"eval_count":2,"eval_duration":1}`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Think: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Generate(context.Background(), application.GenerationRequest{
		Model: "local", Messages: []application.Message{{Role: "user", Content: "What is 2+2?"}}, MaxTokens: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Text != "4" || response.FinishReason != "stop" || response.Usage.InputTokens != 12 || response.Usage.OutputTokens != 2 || response.Usage.TotalTokens != 14 {
		t.Fatalf("response=%+v", response)
	}
}

func TestGenerateRejectsUnknownResponseFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"unexpected":true}`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), application.GenerationRequest{
		Model: "local", Messages: []application.Message{{Role: "user", Content: "test"}},
	})
	if err == nil {
		t.Fatal("unknown response passed strict decoding")
	}
}
