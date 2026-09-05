// Package openai provides the allowlisted OpenAI structured-summary adapter.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tiktoken-go/tokenizer"
)

const defaultEndpoint = "https://api.openai.com/v1/responses"

type Client struct {
	apiKey, model, endpoint string
	httpClient              *http.Client
}

// ChatMessage is an allowlisted Responses API conversation item.
type ChatMessage struct{ Role, Content string }

// Tool defines one strict internal tutor retrieval function.
type Tool struct {
	Name, Description string
	Parameters        map[string]any
}

// ChatRequest contains only fields accepted by MIA's tutor adapter.
type ChatRequest struct {
	Instructions      string
	Messages          []ChatMessage
	Tools             []Tool
	OnRequestAccepted func(context.Context) error
}

// ToolCall is a complete provider function call after streamed arguments are assembled.
type ToolCall struct{ CallID, Name, Arguments string }

// StreamResult carries allowlisted cumulative provider usage.
type StreamResult struct{ InputTokens, OutputTokens int64 }

// ToolExecutor authorizes and executes one model-requested internal function.
type ToolExecutor func(context.Context, ToolCall) (string, error)

type Options struct {
	APIKey, Model, Endpoint string
	HTTPClient              *http.Client
	ResponseHeaderTimeout   time.Duration
}

type Result struct {
	JSON                      []byte
	InputTokens, OutputTokens int64
}

type Error struct {
	Code       string
	Retryable  bool
	RetryAfter time.Duration
	Cause      error
}

func (err *Error) Error() string { return err.Code }
func (err *Error) Unwrap() error { return err.Cause }

func New(options Options) *Client {
	endpoint := options.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	client := options.HTTPClient
	if client == nil {
		timeout := options.ResponseHeaderTimeout
		if timeout == 0 {
			timeout = 10 * time.Second
		}
		client = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: timeout}}
	}
	return &Client{apiKey: options.APIKey, model: options.Model, endpoint: endpoint, httpClient: client}
}

// ProviderModel reports the non-secret provider identity used for durable usage diagnostics.
func (client *Client) ProviderModel() (string, string) { return "openai", client.model }

// Stream runs one tutor response independently of any browser connection. It
// permits at most three tool-result rounds and never retries a provider call.
func (client *Client) Stream(ctx context.Context, request ChatRequest, delta func(string) error,
	execute ToolExecutor) (StreamResult, error) {
	totalCtx, totalCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer totalCancel()
	input := make([]any, 0, len(request.Messages)+8)
	for _, message := range request.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			return StreamResult{}, &Error{Code: "openai_invalid_request"}
		}
		input = append(input, map[string]any{"role": message.Role, "content": message.Content})
	}
	tools := make([]map[string]any, 0, len(request.Tools))
	for _, tool := range request.Tools {
		tools = append(tools, map[string]any{"type": "function", "name": tool.Name,
			"description": tool.Description, "parameters": tool.Parameters, "strict": true})
	}
	codec, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		return StreamResult{}, fmt.Errorf("load OpenAI tokenizer: %w", err)
	}
	var generated strings.Builder
	boundedDelta := func(value string) error {
		candidate := generated.String() + value
		if len(candidate) > 64<<10 {
			return &Error{Code: "openai_output_too_large"}
		}
		count, countErr := codec.Count(candidate)
		if countErr != nil {
			return fmt.Errorf("count OpenAI stream output: %w", countErr)
		}
		if count > 2048 {
			return &Error{Code: "openai_output_too_large"}
		}
		generated.WriteString(value)
		if delta == nil {
			return nil
		}
		return delta(value)
	}
	var usage StreamResult
	for round := 0; ; round++ {
		remainingOutput := int64(2048) - usage.OutputTokens
		if remainingOutput <= 0 {
			return usage, &Error{Code: "openai_output_too_large"}
		}
		items, calls, currentUsage, err := client.streamRound(totalCtx, request.Instructions, input, tools, boundedDelta,
			request.OnRequestAccepted, remainingOutput)
		usage.InputTokens += currentUsage.InputTokens
		usage.OutputTokens += currentUsage.OutputTokens
		if usage.OutputTokens > 2048 {
			return usage, &Error{Code: "openai_output_too_large"}
		}
		if err != nil {
			return usage, err
		}
		if len(calls) == 0 {
			return usage, nil
		}
		if round >= 3 || execute == nil {
			return usage, &Error{Code: "openai_tool_limit"}
		}
		input = append(input, items...)
		for _, call := range calls {
			output, err := execute(totalCtx, call)
			if err != nil {
				return usage, err
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": call.CallID,
				"output": output})
		}
	}
}

func (client *Client) streamRound(ctx context.Context, instructions string, input []any, tools []map[string]any,
	delta func(string) error, accepted func(context.Context) error, maxOutput int64) (resultItems []any,
	resultCalls []ToolCall, resultUsage StreamResult, resultErr error) {
	payload, err := json.Marshal(map[string]any{"model": client.model, "instructions": instructions, "input": input,
		"tools": tools, "parallel_tool_calls": false, "max_output_tokens": maxOutput, "store": false, "stream": true})
	if err != nil {
		return nil, nil, StreamResult{}, fmt.Errorf("marshal OpenAI stream request: %w", err)
	}
	codec, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		return nil, nil, StreamResult{}, fmt.Errorf("load OpenAI tokenizer: %w", err)
	}
	tokens, err := codec.Count(string(payload))
	if err != nil || tokens > 32_000 {
		return nil, nil, StreamResult{}, &Error{Code: "openai_input_too_large", Cause: err}
	}
	roundCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(roundCtx, http.MethodPost, client.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, StreamResult{}, fmt.Errorf("create OpenAI stream request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+client.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return nil, nil, StreamResult{}, &Error{Code: "openai_unavailable", Cause: err}
	}
	if response.StatusCode != http.StatusOK {
		_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			return nil, nil, StreamResult{}, &Error{Code: "openai_unavailable", Cause: errors.Join(readErr, closeErr)}
		}
		return nil, nil, StreamResult{}, &Error{Code: "openai_http_" + fmt.Sprint(response.StatusCode),
			Retryable:  response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500,
			RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"))}
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			resultErr = errors.Join(resultErr, &Error{Code: "openai_unavailable", Cause: err})
		}
	}()
	if accepted != nil {
		if err := accepted(ctx); err != nil {
			return nil, nil, StreamResult{}, err
		}
	}
	type scanResult struct {
		data []byte
		err  error
	}
	events := make(chan scanResult)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			line := scanner.Bytes()
			if bytes.HasPrefix(line, []byte("data: ")) {
				data := append([]byte(nil), line[len("data: "):]...)
				select {
				case events <- scanResult{data: data}:
				case <-roundCtx.Done():
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case events <- scanResult{err: err}:
			case <-roundCtx.Done():
			}
		}
	}()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	var items []any
	var calls []ToolCall
	var usage StreamResult
	completed := false
	for {
		select {
		case <-ctx.Done():
			return nil, nil, usage, &Error{Code: "openai_timeout", Cause: ctx.Err()}
		case <-timer.C:
			cancel()
			return nil, nil, usage, &Error{Code: "openai_timeout", Cause: context.DeadlineExceeded}
		case event, ok := <-events:
			if !ok {
				if !completed {
					return nil, nil, usage, &Error{Code: "openai_malformed_response"}
				}
				return items, calls, usage, nil
			}
			if event.err != nil {
				return nil, nil, usage, &Error{Code: "openai_unavailable", Cause: event.err}
			}
			if bytes.Equal(event.data, []byte("[DONE]")) {
				continue
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(60 * time.Second)
			var envelope struct {
				Type     string          `json:"type"`
				Delta    string          `json:"delta"`
				Item     json.RawMessage `json:"item"`
				Response struct {
					Status string `json:"status"`
					Usage  struct {
						InputTokens  int64 `json:"input_tokens"`
						OutputTokens int64 `json:"output_tokens"`
					} `json:"usage"`
				} `json:"response"`
			}
			if err := json.Unmarshal(event.data, &envelope); err != nil {
				return nil, nil, usage, &Error{Code: "openai_malformed_response", Cause: err}
			}
			switch envelope.Type {
			case "response.output_text.delta":
				if envelope.Delta == "" || delta == nil {
					continue
				}
				if err := delta(envelope.Delta); err != nil {
					return nil, nil, usage, err
				}
			case "response.output_item.done":
				var item map[string]any
				if len(envelope.Item) == 0 || json.Unmarshal(envelope.Item, &item) != nil {
					return nil, nil, usage, &Error{Code: "openai_malformed_response"}
				}
				items = append(items, item)
				if item["type"] == "function_call" {
					callID, callIDOK := item["call_id"].(string)
					name, nameOK := item["name"].(string)
					arguments, argumentsOK := item["arguments"].(string)
					if !callIDOK || !nameOK || !argumentsOK || callID == "" || name == "" || !json.Valid([]byte(arguments)) {
						return nil, nil, usage, &Error{Code: "openai_malformed_response"}
					}
					calls = append(calls, ToolCall{CallID: callID, Name: name, Arguments: arguments})
				}
			case "response.completed":
				if envelope.Response.Status != "completed" {
					return nil, nil, usage, &Error{Code: "openai_malformed_response"}
				}
				usage.InputTokens += envelope.Response.Usage.InputTokens
				usage.OutputTokens += envelope.Response.Usage.OutputTokens
				completed = true
			case "response.failed", "response.incomplete", "error":
				return nil, nil, usage, &Error{Code: "openai_provider_failure"}
			}
		}
	}
}

func (client *Client) Structured(ctx context.Context, instructions string, input []string, schema map[string]any) (Result, error) {
	messages := []map[string]string{{"role": "system", "content": instructions}}
	for _, chunk := range input {
		messages = append(messages, map[string]string{"role": "user", "content": chunk})
	}
	payload, err := json.Marshal(map[string]any{
		"model": client.model, "input": messages, "max_output_tokens": 2048, "store": false,
		"text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "structured_output",
			"strict": true, "schema": schema}},
	})
	if err != nil {
		return Result{}, fmt.Errorf("marshal OpenAI response request: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, client.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Result{}, fmt.Errorf("create OpenAI response request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return Result{}, &Error{Code: "openai_unavailable", Retryable: true, Cause: err}
	}
	responseData, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	readErr = errors.Join(readErr, response.Body.Close())
	if readErr != nil {
		return Result{}, &Error{Code: "openai_unavailable", Retryable: true, Cause: readErr}
	}
	if response.StatusCode != http.StatusOK {
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return Result{}, &Error{Code: "openai_http_" + fmt.Sprint(response.StatusCode), Retryable: retryable,
			RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"))}
	}
	var body struct {
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
		Output []struct {
			Type, Status, Role string
			Content            []struct {
				Type        string          `json:"type"`
				Text        string          `json:"text"`
				Annotations json.RawMessage `json:"annotations"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens                             int64 `json:"input_tokens"`
			OutputTokens                            int64 `json:"output_tokens"`
			TotalTokens                             int64 `json:"total_tokens"`
			InputTokensDetails, OutputTokensDetails json.RawMessage
		} `json:"usage"`
	}
	if len(responseData) > 1<<20 {
		return Result{}, &Error{Code: "openai_malformed_response"}
	}
	decoder := json.NewDecoder(bytes.NewReader(responseData))
	if err := decoder.Decode(&body); err != nil {
		return Result{}, &Error{Code: "openai_malformed_response", Cause: err}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || body.Status != "completed" {
		return Result{}, &Error{Code: "openai_malformed_response", Cause: err}
	}
	var output string
	for _, item := range body.Output {
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" {
				if output != "" {
					return Result{}, &Error{Code: "openai_malformed_response"}
				}
				output = content.Text
			}
		}
	}
	if output == "" || !json.Valid([]byte(output)) {
		return Result{}, &Error{Code: "openai_malformed_response"}
	}
	return Result{JSON: []byte(output), InputTokens: body.Usage.InputTokens,
		OutputTokens: body.Usage.OutputTokens}, nil
}

func parseRetryAfter(value string) time.Duration {
	duration, err := time.ParseDuration(value + "s")
	if err == nil && duration >= 0 {
		return min(duration, time.Hour)
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	return min(max(0, time.Until(when)), time.Hour)
}
