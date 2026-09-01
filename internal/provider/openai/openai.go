// Package openai provides the allowlisted OpenAI structured-summary adapter.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultEndpoint = "https://api.openai.com/v1/responses"

type Client struct {
	apiKey, model, endpoint string
	httpClient              *http.Client
}

type Options struct {
	APIKey, Model, Endpoint string
	HTTPClient              *http.Client
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
		client = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 10 * time.Second}}
	}
	return &Client{apiKey: options.APIKey, model: options.Model, endpoint: endpoint, httpClient: client}
}

func (client *Client) Structured(ctx context.Context, instructions string, input []string, schema map[string]any) (Result, error) {
	messages := []map[string]string{{"role": "system", "content": instructions}}
	for _, chunk := range input {
		messages = append(messages, map[string]string{"role": "user", "content": chunk})
	}
	payload, err := json.Marshal(map[string]any{
		"model": client.model, "input": messages, "max_output_tokens": 2048, "store": false,
		"text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "material_brief",
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
