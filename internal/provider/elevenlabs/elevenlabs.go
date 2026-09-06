// Package elevenlabs provides the allowlisted ElevenLabs text-to-speech adapter.
package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.elevenlabs.io"
	maximumMP3     = 25 << 20
)

var ErrUnavailable = errors.New("ElevenLabs is unavailable")

// Synthesizer generates one complete bounded MP3 without automatic retries.
type Synthesizer interface {
	Generate(context.Context, string, string) ([]byte, error)
	Available() bool
}

// Client is safe for concurrent use.
type Client struct {
	apiKey, baseURL string
	httpClient      *http.Client
}

// Options configures the adapter. BaseURL and HTTPClient support local tests;
// production wiring uses the fixed ElevenLabs origin.
type Options struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

func New(options Options) Synthesizer {
	if options.APIKey == "" {
		return Unavailable{}
	}
	baseURL := strings.TrimSuffix(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			ResponseHeaderTimeout: 10 * time.Second,
		}, Timeout: 2 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("ElevenLabs redirects are not allowed")
		}}
	}
	return &Client{apiKey: options.APIKey, baseURL: baseURL, httpClient: httpClient}
}

func (client *Client) Available() bool { return true }

// Generate sends only completed response text and the selected voice ID. It
// accepts only a complete signature-valid MP3 no larger than 25 MiB.
func (client *Client) Generate(ctx context.Context, text, voiceID string) (returnData []byte, returnErr error) {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	payload, err := json.Marshal(struct {
		Text    string `json:"text"`
		ModelID string `json:"model_id"`
	}{Text: text, ModelID: "eleven_multilingual_v2"})
	if err != nil {
		return nil, fmt.Errorf("marshal ElevenLabs request: %w", err)
	}
	endpoint := client.baseURL + "/v1/text-to-speech/" + url.PathEscape(voiceID) +
		"?output_format=mp3_44100_128"
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create ElevenLabs request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("xi-api-key", client.apiKey)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send ElevenLabs request: %w", ErrUnavailable)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close ElevenLabs response: %w", ErrUnavailable))
		}
	}()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ElevenLabs HTTP status %d: %w", response.StatusCode, ErrUnavailable)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumMP3+1))
	if err != nil {
		return nil, fmt.Errorf("read ElevenLabs response: %w", ErrUnavailable)
	}
	if len(data) > maximumMP3 || !isMP3(data) {
		return nil, fmt.Errorf("invalid ElevenLabs MP3 response: %w", ErrUnavailable)
	}
	return data, nil
}

func isMP3(data []byte) bool {
	if len(data) >= 3 && string(data[:3]) == "ID3" {
		return true
	}
	return len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0 && data[1]&0x06 != 0
}

// Unavailable performs no network work when ElevenLabs is not configured.
type Unavailable struct{}

func (Unavailable) Available() bool { return false }
func (Unavailable) Generate(context.Context, string, string) ([]byte, error) {
	return nil, ErrUnavailable
}
