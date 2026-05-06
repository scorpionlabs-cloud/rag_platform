package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	Enabled     bool
	Provider    string
	BaseURL     string
	Model       string
	APIKey      string
	Timeout     time.Duration
	TopN        int
	Normalize   bool
	FailClosed  bool
}

type Client interface {
	Enabled() bool
	Provider() string
	Model() string
	FailClosed() bool
	Score(ctx context.Context, query string, docs []string) ([]float64, error)
}

type disabledClient struct{}

func Disabled() Client { return disabledClient{} }
func (disabledClient) Enabled() bool { return false }
func (disabledClient) Provider() string { return "local" }
func (disabledClient) Model() string { return "local-lexical" }
func (disabledClient) FailClosed() bool { return false }
func (disabledClient) Score(ctx context.Context, query string, docs []string) ([]float64, error) {
	return nil, errors.New("external reranker disabled")
}

type HTTPClient struct {
	cfg Config
	hc *http.Client
}

func New(cfg Config) Client {
	if !cfg.Enabled {
		return Disabled()
	}
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	if cfg.Provider == "" {
		cfg.Provider = "bge"
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.TopN <= 0 {
		cfg.TopN = 64
	}
	return &HTTPClient{cfg: cfg, hc: &http.Client{Timeout: cfg.Timeout}}
}

func (c *HTTPClient) Enabled() bool { return c.cfg.Enabled }
func (c *HTTPClient) Provider() string { return c.cfg.Provider }
func (c *HTTPClient) Model() string { return c.cfg.Model }
func (c *HTTPClient) FailClosed() bool { return c.cfg.FailClosed }

func (c *HTTPClient) Score(ctx context.Context, query string, docs []string) ([]float64, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty rerank query")
	}
	if len(docs) == 0 {
		return []float64{}, nil
	}
	if c.cfg.BaseURL == "" {
		return nil, errors.New("RERANKER_BASE_URL is required")
	}
	if c.cfg.TopN > 0 && len(docs) > c.cfg.TopN {
		docs = docs[:c.cfg.TopN]
	}

	var scores []float64
	var err error
	switch c.cfg.Provider {
	case "bge", "bge-http", "tei", "text-embeddings-inference":
		scores, err = c.scoreTEI(ctx, query, docs)
	case "cohere", "cohere-compatible", "openai-compatible", "vllm", "xinference":
		scores, err = c.scoreCohereCompatible(ctx, query, docs)
	default:
		err = fmt.Errorf("unsupported reranker provider %q", c.cfg.Provider)
	}
	if err != nil {
		return nil, err
	}
	if len(scores) != len(docs) {
		fixed := make([]float64, len(docs))
		copy(fixed, scores)
		scores = fixed
	}
	if c.cfg.Normalize {
		scores = normalize(scores)
	}
	return scores, nil
}

func (c *HTTPClient) scoreTEI(ctx context.Context, query string, docs []string) ([]float64, error) {
	body := map[string]interface{}{
		"query": query,
		"texts": docs,
		"raw_scores": false,
		"return_text": false,
	}
	if c.cfg.Model != "" {
		body["model"] = c.cfg.Model
	}
	return c.postAndParse(ctx, endpoint(c.cfg.BaseURL, "/rerank"), body, len(docs))
}

func (c *HTTPClient) scoreCohereCompatible(ctx context.Context, query string, docs []string) ([]float64, error) {
	body := map[string]interface{}{
		"model": c.cfg.Model,
		"query": query,
		"documents": docs,
		"top_n": len(docs),
		"return_documents": false,
	}
	return c.postAndParse(ctx, endpoint(c.cfg.BaseURL, "/v1/rerank"), body, len(docs))
}

func (c *HTTPClient) postAndParse(ctx context.Context, url string, body map[string]interface{}, docs int) ([]float64, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("reranker status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	return parseScores(payload, docs)
}

func endpoint(base, suffix string) string {
	if strings.HasSuffix(base, suffix) || strings.HasSuffix(base, "/rerank") || strings.HasSuffix(base, "/v1/rerank") {
		return base
	}
	return base + suffix
}

type rankedItem struct {
	Index int
	Score float64
}

func parseScores(payload []byte, docs int) ([]float64, error) {
	var raw interface{}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	items := collectRankedItems(raw)
	if len(items) == 0 {
		return nil, errors.New("reranker response contained no scores")
	}
	scores := make([]float64, docs)
	for i, item := range items {
		idx := item.Index
		if idx < 0 {
			idx = i
		}
		if idx >= 0 && idx < docs {
			scores[idx] = item.Score
		}
	}
	return scores, nil
}

func collectRankedItems(v interface{}) []rankedItem {
	switch x := v.(type) {
	case []interface{}:
		return collectArray(x)
	case map[string]interface{}:
		for _, key := range []string{"results", "data", "rerank_results", "scores"} {
			if child, ok := x[key]; ok {
				items := collectRankedItems(child)
				if len(items) > 0 {
					return items
				}
			}
		}
		if idx, okIdx := numberField(x, "index", -1); okIdx {
			if score, ok := scoreField(x); ok {
				return []rankedItem{{Index: int(idx), Score: score}}
			}
		}
	}
	return nil
}

func collectArray(arr []interface{}) []rankedItem {
	items := make([]rankedItem, 0, len(arr))
	for i, entry := range arr {
		switch x := entry.(type) {
		case float64:
			items = append(items, rankedItem{Index: i, Score: x})
		case map[string]interface{}:
			idx := float64(i)
			if got, ok := numberField(x, "index", idx); ok {
				idx = got
			}
			if score, ok := scoreField(x); ok {
				items = append(items, rankedItem{Index: int(idx), Score: score})
			}
		}
	}
	return items
}

func scoreField(m map[string]interface{}) (float64, bool) {
	for _, key := range []string{"score", "relevance_score", "rerank_score"} {
		if v, ok := numberField(m, key, 0); ok {
			return v, true
		}
	}
	return 0, false
}

func numberField(m map[string]interface{}, key string, def float64) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return def, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return def, false
	}
}

func normalize(in []float64) []float64 {
	out := append([]float64(nil), in...)
	if len(out) == 0 {
		return out
	}
	min, max := out[0], out[0]
	for _, s := range out {
		if s < min { min = s }
		if s > max { max = s }
	}
	if min >= 0 && max <= 1 {
		for i := range out {
			out[i] = clamp01(out[i])
		}
		return out
	}
	if math.Abs(max-min) < 1e-9 {
		for i := range out { out[i] = clamp01(sigmoid(out[i])) }
		return out
	}
	for i := range out {
		out[i] = clamp01((out[i] - min) / (max - min))
	}
	return out
}

func sigmoid(v float64) float64 { return 1 / (1 + math.Exp(-v)) }
func clamp01(v float64) float64 {
	if v < 0 { return 0 }
	if v > 1 { return 1 }
	return v
}
