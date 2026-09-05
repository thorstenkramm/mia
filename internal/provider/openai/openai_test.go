package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStructuredReturnsOnlyAllowlistedOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if _, err := response.Write([]byte(`{"status":"completed","output":[{"type":"message","status":"completed",` +
			`"role":"assistant","content":[{"type":"output_text","text":"{\"ok\":true}","annotations":[]}]}],` +
			`"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6},"provider_secret":"ignored"}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()
	result, err := New(Options{APIKey: "secret", Model: "test", Endpoint: server.URL,
		HTTPClient: server.Client()}).Structured(context.Background(), "instructions", []string{"input"},
		map[string]any{"type": "object"})
	if err != nil || string(result.JSON) != `{"ok":true}` || result.InputTokens != 4 || result.OutputTokens != 2 {
		t.Fatalf("Structured = %#v, %v", result, err)
	}
}

func TestStreamExecutesToolRoundAndEmitsAllowlistedDelta(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		var payload map[string]any
		if json.Unmarshal(body, &payload) != nil || payload["stream"] != true || payload["store"] != false {
			t.Errorf("unexpected payload: %s", body)
		}
		response.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			_, err = io.WriteString(response, "data: {\"type\":\"response.output_item.done\",\"item\":"+
				"{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"search_material\","+
				"\"arguments\":\"{\\\"terms\\\":[\\\"alpha\\\"]}\"}}\n\n"+
				"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\","+
				"\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n")
		} else {
			_, err = io.WriteString(response, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Answer\"}\n\n"+
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\"}}\n\n"+
				"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\","+
				"\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\n\ndata: [DONE]\n\n")
		}
		if err != nil {
			t.Errorf("write stream: %v", err)
		}
	}))
	defer server.Close()
	var output strings.Builder
	result, err := New(Options{APIKey: "secret", Model: "test", Endpoint: server.URL,
		HTTPClient: server.Client()}).Stream(context.Background(), ChatRequest{Instructions: "Tutor",
		Messages: []ChatMessage{{Role: "user", Content: "Question"}}, Tools: []Tool{{Name: "search_material",
			Parameters: map[string]any{"type": "object"}}}}, func(value string) error {
		output.WriteString(value)
		return nil
	}, func(_ context.Context, call ToolCall) (string, error) {
		if call.Name != "search_material" {
			t.Fatalf("unexpected tool: %#v", call)
		}
		return `[{"material_id":"mat_1"}]`, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != "Answer" || result.InputTokens != 7 || result.OutputTokens != 3 || calls.Load() != 2 {
		t.Fatalf("stream output=%q result=%#v calls=%d", output.String(), result, calls.Load())
	}
}

func TestStructuredRejectsMalformedAndDoesNotRetryPermanentFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	_, err := New(Options{Endpoint: server.URL, HTTPClient: server.Client()}).
		Structured(context.Background(), "instructions", []string{"input"}, map[string]any{"type": "object"})
	var providerError *Error
	if !errors.As(err, &providerError) || providerError.Retryable {
		t.Fatalf("permanent error = %#v, %v", providerError, err)
	}
}

func TestStreamPermitsThreeToolResultRounds(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		round := requests.Add(1)
		response.Header().Set("Content-Type", "text/event-stream")
		var body string
		if round <= 3 {
			body = "data: {\"type\":\"response.output_item.done\",\"item\":{" +
				"\"type\":\"function_call\",\"call_id\":\"call_" + fmt.Sprint(round) +
				"\",\"name\":\"search_material\",\"arguments\":\"{\\\"terms\\\":[\\\"alpha\\\"]}\"}}\n\n"
		} else {
			body = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\n"
		}
		body += "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"," +
			"\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n"
		if _, err := io.WriteString(response, body); err != nil {
			t.Errorf("write stream: %v", err)
		}
	}))
	defer server.Close()
	var executions atomic.Int32
	_, err := New(Options{Endpoint: server.URL, HTTPClient: server.Client()}).Stream(context.Background(),
		ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "question"}}}, func(string) error { return nil },
		func(context.Context, ToolCall) (string, error) {
			executions.Add(1)
			return `[]`, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 4 || executions.Load() != 3 {
		t.Fatalf("requests=%d executions=%d", requests.Load(), executions.Load())
	}
}

func TestStreamRejectsOutputBeyondLocalTokenBudgetBeforeDelivery(t *testing.T) {
	delta := strings.Repeat("token ", 3000)
	encoded, err := json.Marshal(delta)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, writeErr := io.WriteString(response, "data: {\"type\":\"response.output_text.delta\",\"delta\":"+
			string(encoded)+"}\n\n")
		if writeErr != nil {
			t.Errorf("write stream: %v", writeErr)
		}
	}))
	defer server.Close()
	delivered := false
	_, err = New(Options{Endpoint: server.URL, HTTPClient: server.Client()}).Stream(context.Background(),
		ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "question"}}}, func(string) error {
			delivered = true
			return nil
		}, nil)
	var providerError *Error
	if !errors.As(err, &providerError) || providerError.Code != "openai_output_too_large" || delivered {
		t.Fatalf("error=%v delivered=%t", err, delivered)
	}
}
