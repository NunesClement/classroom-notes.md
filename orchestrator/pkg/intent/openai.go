package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const DefaultMaxResponseBytes int64 = 1 << 20

type OpenAICompatibleConfig struct {
	Endpoint         string
	Model            string
	APIKey           string
	JSONMode         bool
	MaxResponseBytes int64
	HTTPClient       *http.Client
}

type OpenAICompatibleClient struct {
	config OpenAICompatibleConfig
}

func NewOpenAICompatibleClient(config OpenAICompatibleConfig) (*OpenAICompatibleClient, error) {
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	config.Model = strings.TrimSpace(config.Model)
	if config.Endpoint == "" {
		return nil, errors.New("Hermes chat-completions endpoint is required")
	}
	parsed, err := url.ParseRequestURI(config.Endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("Hermes endpoint %q must be an absolute HTTP(S) URL", config.Endpoint)
	}
	if config.Model == "" {
		return nil, errors.New("Hermes model is required")
	}
	if config.MaxResponseBytes == 0 {
		config.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if config.MaxResponseBytes < 1 {
		return nil, errors.New("maximum response bytes must be positive")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	return &OpenAICompatibleClient{config: config}, nil
}

func (c *OpenAICompatibleClient) Complete(
	ctx context.Context,
	systemPrompt string,
	userPrompt string,
) (string, error) {
	if ctx == nil {
		return "", errors.New("context cannot be nil")
	}
	requestBody := chatCompletionRequest{
		Model: c.config.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0,
		MaxTokens:   1200,
	}
	if c.config.JSONMode {
		requestBody.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("encode Hermes request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.Endpoint, bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("create Hermes request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.config.APIKey) != "" {
		request.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	response, err := c.config.HTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("call Hermes endpoint: %w", err)
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, c.config.MaxResponseBytes)
	if err != nil {
		return "", fmt.Errorf("read Hermes response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Hermes endpoint returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var completion chatCompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return "", fmt.Errorf("decode Hermes response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", errors.New("Hermes response contained no choices")
	}
	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		return "", errors.New("Hermes response contained empty content")
	}
	return content, nil
}

type chatCompletionRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("response exceeds %d bytes", maximum)
	}
	return data, nil
}
