// Package mistral provides the allowlisted Mistral OCR adapter.
package mistral

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultEndpoint = "https://api.mistral.ai/v1/ocr"
	model           = "mistral-ocr-4-1"
	maximumResponse = 512 << 20
)

type Client struct {
	apiKey, endpoint string
	httpClient       *http.Client
}

type Options struct {
	APIKey     string
	Endpoint   string
	HTTPClient *http.Client
}

type Page struct {
	Index    int
	Markdown string
}

type Result struct {
	Pages      []Page
	InputUnits int64
}

type Error struct {
	// jscpd:ignore-start
	// Mistral and OpenAI expose provider-specific result and retry contracts.
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
		client = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 30 * time.Second}}
	}
	return &Client{apiKey: options.APIKey, endpoint: endpoint, httpClient: client}
	// jscpd:ignore-end
}

func (client *Client) OCR(ctx context.Context, mediaType string, source []byte) (Result, error) {
	documentType := "document_url"
	if mediaType == "image/jpeg" || mediaType == "image/png" {
		documentType = "image_url"
	}
	dataURL := "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(source)
	document := map[string]string{"type": documentType, documentType: dataURL}
	payload, err := json.Marshal(map[string]any{"model": model, "document": document, "include_image_base64": false})
	if err != nil {
		return Result{}, fmt.Errorf("marshal Mistral OCR request: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, client.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Result{}, fmt.Errorf("create Mistral OCR request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return Result{}, &Error{Code: "mistral_unavailable", Retryable: true, Cause: err}
	}
	responseData, readErr := io.ReadAll(io.LimitReader(response.Body, maximumResponse+1))
	readErr = errors.Join(readErr, response.Body.Close())
	if readErr != nil {
		return Result{}, &Error{Code: "mistral_unavailable", Retryable: true, Cause: readErr}
	}
	if response.StatusCode != http.StatusOK {
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return Result{}, &Error{Code: "mistral_http_" + fmt.Sprint(response.StatusCode), Retryable: retryable,
			RetryAfter: retryAfter(response.Header.Get("Retry-After"))}
	}
	var body struct {
		Pages []struct {
			Index      int             `json:"index"`
			Markdown   string          `json:"markdown"`
			Images     json.RawMessage `json:"images"`
			Dimensions json.RawMessage `json:"dimensions"`
		} `json:"pages"`
		Model     string `json:"model"`
		UsageInfo struct {
			PagesProcessed int64 `json:"pages_processed"`
			DocSizeBytes   int64 `json:"doc_size_bytes"`
		} `json:"usage_info"`
		DocumentAnnotation json.RawMessage `json:"document_annotation"`
	}
	if len(responseData) > maximumResponse {
		return Result{}, &Error{Code: "mistral_malformed_response"}
	}
	decoder := json.NewDecoder(bytes.NewReader(responseData))
	if err := decoder.Decode(&body); err != nil {
		return Result{}, &Error{Code: "mistral_malformed_response", Cause: err}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || len(body.Pages) == 0 {
		return Result{}, &Error{Code: "mistral_malformed_response", Cause: err}
	}
	result := Result{Pages: make([]Page, len(body.Pages)), InputUnits: body.UsageInfo.PagesProcessed}
	for index, page := range body.Pages {
		if page.Index < 0 || page.Markdown == "" {
			return Result{}, &Error{Code: "mistral_malformed_response"}
		}
		result.Pages[index] = Page{Index: page.Index, Markdown: page.Markdown}
	}
	return result, nil
}

func retryAfter(value string) time.Duration {
	// jscpd:ignore-start
	// Retry parsing remains local to the Mistral adapter's provider boundary.
	duration, err := time.ParseDuration(value + "s")
	if err == nil && duration >= 0 {
		return min(duration, time.Hour)
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	return min(max(0, time.Until(when)), time.Hour)
	// jscpd:ignore-end
}
