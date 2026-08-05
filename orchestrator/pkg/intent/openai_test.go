package intent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatibleClientCallsHermesJSONEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Fatalf("method=%s, want POST", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		var body chatCompletionRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "glm-5.2" || len(body.Messages) != 2 {
			t.Fatalf("unexpected request: %+v", body)
		}
		if body.ResponseFormat == nil || body.ResponseFormat.Type != "json_object" {
			t.Fatalf("JSON response mode missing: %+v", body.ResponseFormat)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","content":"{\"goal\":\"smoke\"}"}}]}`)
	}))
	defer server.Close()

	client, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		Endpoint:   server.URL,
		Model:      "glm-5.2",
		APIKey:     "secret",
		JSONMode:   true,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := client.Complete(context.Background(), "system", "user")
	if err != nil {
		t.Fatal(err)
	}
	if content != `{"goal":"smoke"}` {
		t.Fatalf("content=%q", content)
	}
}

func TestOpenAICompatibleClientBoundsAndReportsResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(writer, strings.Repeat("x", 32))
	}))
	defer server.Close()
	client, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		Endpoint:         server.URL,
		Model:            "glm-5.2",
		MaxResponseBytes: 16,
		HTTPClient:       server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), "system", "user")
	if err == nil || !strings.Contains(err.Error(), "exceeds 16 bytes") {
		t.Fatalf("unexpected error: %v", err)
	}
}
