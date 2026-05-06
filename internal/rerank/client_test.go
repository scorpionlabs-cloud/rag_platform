package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseCohereCompatibleScores(t *testing.T) {
	payload := []byte(`{"results":[{"index":1,"relevance_score":0.91},{"index":0,"relevance_score":0.32}]}`)
	scores, err := parseScores(payload, 2)
	if err != nil { t.Fatal(err) }
	if scores[0] != 0.32 || scores[1] != 0.91 { t.Fatalf("unexpected scores: %#v", scores) }
}

func TestParseTEIArrayScores(t *testing.T) {
	payload := []byte(`[{"index":0,"score":0.25},{"index":2,"score":0.75}]`)
	scores, err := parseScores(payload, 3)
	if err != nil { t.Fatal(err) }
	if scores[0] != 0.25 || scores[1] != 0 || scores[2] != 0.75 { t.Fatalf("unexpected scores: %#v", scores) }
}

func TestHTTPClientTEISendsTexts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" { t.Fatalf("unexpected path %s", r.URL.Path) }
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Fatal(err) }
		if _, ok := body["texts"]; !ok { t.Fatalf("expected texts in body: %#v", body) }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"index":0,"score":0.7},{"index":1,"score":0.2}]`))
	}))
	defer server.Close()

	client := New(Config{Enabled: true, Provider: "bge", BaseURL: server.URL, Timeout: time.Second, Normalize: true})
	scores, err := client.Score(context.Background(), "query", []string{"doc a", "doc b"})
	if err != nil { t.Fatal(err) }
	if len(scores) != 2 || scores[0] <= scores[1] { t.Fatalf("unexpected scores: %#v", scores) }
}
