package sdk

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestClientRememberUsesBearerAndDecodesJob(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://127.0.0.1:7878/v1/remember" || request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request=%s auth=%q", request.URL, request.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"job":{"id":"job-a","state":"queued"}}`))}, nil
	})
	client, err := NewClient("http://127.0.0.1:7878/", WithBearerToken("secret"), WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Remember(context.Background(), RememberRequest{Owner: "owner-a", Content: TextContent("hello")})
	if err != nil || response.Job.ID != "job-a" || response.Job.State != "queued" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestClientReturnsTypedAPIError(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"forbidden","message":"scope denied"}}`))}, nil
	})
	client, _ := NewClient("https://memory.example", WithHTTPClient(&http.Client{Transport: transport}))
	_, err := client.Recall(context.Background(), RecallRequest{})
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != http.StatusForbidden || apiErr.Code != "forbidden" {
		t.Fatalf("error=%T %v", err, err)
	}
}

func TestClientRejectsUnsafeBaseURL(t *testing.T) {
	for _, value := range []string{"", "localhost:7878", "ftp://example.com"} {
		if _, err := NewClient(value); err == nil {
			t.Fatalf("NewClient(%q) unexpectedly succeeded", value)
		}
	}
}
