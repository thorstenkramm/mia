package elevenlabs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateSendsAllowlistedRequestAndAcceptsMP3(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/v1/text-to-speech/voice-1", request.URL.Path)
		assert.Equal(t, "mp3_44100_128", request.URL.Query().Get("output_format"))
		assert.Equal(t, "secret", request.Header.Get("xi-api-key"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
		assert.Equal(t, map[string]any{"text": "hello", "model_id": "eleven_multilingual_v2"}, body)
		_, err := response.Write([]byte("ID3audio"))
		require.NoError(t, err)
	}))
	defer server.Close()

	client := New(Options{APIKey: "secret", BaseURL: server.URL})
	data, err := client.Generate(context.Background(), "hello", "voice-1")
	require.NoError(t, err)
	assert.Equal(t, []byte("ID3audio"), data)
}

func TestGenerateRejectsProviderFailuresAndInvalidMP3(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"status": func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusTooManyRequests) },
		"signature": func(response http.ResponseWriter, _ *http.Request) {
			_, err := response.Write([]byte("not audio"))
			require.NoError(t, err)
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			_, err := New(Options{APIKey: "secret", BaseURL: server.URL}).Generate(context.Background(), "hello", "voice")
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrUnavailable)
		})
	}
}

func TestGenerateRejectsOversizedMP3(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, err := response.Write([]byte("ID3" + strings.Repeat("x", maximumMP3)))
		require.NoError(t, err)
	}))
	defer server.Close()
	_, err := New(Options{APIKey: "secret", BaseURL: server.URL}).Generate(context.Background(), "hello", "voice")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestUnavailableMakesNoRequest(t *testing.T) {
	client := New(Options{})
	assert.False(t, client.Available())
	_, err := client.Generate(context.Background(), "secret text", "voice")
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestGenerateHonorsCancellationAndClassifiesTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	client := New(Options{APIKey: "secret", BaseURL: server.URL,
		HTTPClient: &http.Client{Timeout: 20 * time.Millisecond}})
	_, err := client.Generate(context.Background(), "hello", "voice")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnavailable)
}
