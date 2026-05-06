package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	Enabled         bool
	Provider        string
	BaseURL         string
	Model           string
	APIKey          string
	Timeout         time.Duration
	Temperature     float64
	MaxAnswerTokens int
}

type Client interface {
	Enabled() bool
	Provider() string
	Model() string
	Generate(ctx context.Context, prompt string) (string, error)
}

type disabledClient struct{}

func Disabled() Client                  { return disabledClient{} }
func (disabledClient) Enabled() bool    { return false }
func (disabledClient) Provider() string { return "disabled" }
func (disabledClient) Model() string    { return "" }
func (disabledClient) Generate(ctx context.Context, prompt string) (string, error) {
	return "", errors.New("LLM is disabled")
}

type HTTPClient struct {
	cfg Config
	hc  *http.Client
}

func New(cfg Config) Client {
	if !cfg.Enabled {
		return Disabled()
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120 * time.Second
	}
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	if cfg.Provider == "" {
		cfg.Provider = "ollama"
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	return &HTTPClient{cfg: cfg, hc: &http.Client{Timeout: cfg.Timeout}}
}

func (c *HTTPClient) Enabled() bool    { return c.cfg.Enabled }
func (c *HTTPClient) Provider() string { return c.cfg.Provider }
func (c *HTTPClient) Model() string    { return c.cfg.Model }

func (c *HTTPClient) Generate(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("empty prompt")
	}
	switch c.cfg.Provider {
	case "ollama":
		return c.generateOllama(ctx, prompt)
	case "openai", "openai-compatible", "oci-generative-ai-compatible":
		return c.generateOpenAICompatible(ctx, prompt)
	default:
		return "", fmt.Errorf("unsupported LLM provider %q", c.cfg.Provider)
	}
}

func (c *HTTPClient) generateOllama(ctx context.Context, prompt string) (string, error) {
	if c.cfg.BaseURL == "" {
		return "", errors.New("LLM_BASE_URL is required for ollama")
	}
	body := map[string]interface{}{
		"model":   c.cfg.Model,
		"prompt":  prompt,
		"stream":  false,
		"options": map[string]interface{}{"temperature": c.cfg.Temperature, "num_predict": c.cfg.MaxAnswerTokens},
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/api/generate", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("ollama status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", errors.New(out.Error)
	}
	return strings.TrimSpace(out.Response), nil
}

func (c *HTTPClient) generateOpenAICompatible(ctx context.Context, prompt string) (string, error) {
	if c.cfg.BaseURL == "" {
		return "", errors.New("LLM_BASE_URL is required for OpenAI-compatible provider")
	}
	body := map[string]interface{}{
		"model": c.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": "You answer only from the provided RAG context. If the context is insufficient, say so."},
			{"role": "user", "content": prompt},
		},
		"temperature": c.cfg.Temperature,
		"max_tokens":  c.cfg.MaxAnswerTokens,
	}
	data, _ := json.Marshal(body)
	url := c.cfg.BaseURL
	if !strings.HasSuffix(url, "/chat/completions") {
		url += "/v1/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("openai-compatible status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", errors.New("no LLM choices returned")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
