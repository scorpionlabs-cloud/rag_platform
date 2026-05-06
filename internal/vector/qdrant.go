package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"rag-unified-realtime/internal/model"
)

type QdrantClient struct {
	url        string
	collection string
	vectorSize int
	httpClient *http.Client
	batchSize  int
}

type Option func(*QdrantClient)

func WithTimeout(timeout time.Duration) Option {
	return func(c *QdrantClient) {
		if timeout > 0 {
			c.httpClient.Timeout = timeout
		}
	}
}

func WithUpsertBatchSize(size int) Option {
	return func(c *QdrantClient) {
		if size > 0 {
			c.batchSize = size
		}
	}
}

func NewQdrantClient(url, collection string, vectorSize int, opts ...Option) *QdrantClient {
	if vectorSize <= 0 {
		vectorSize = 384
	}
	c := &QdrantClient{
		url: strings.TrimRight(url, "/"), collection: collection, vectorSize: vectorSize,
		httpClient: &http.Client{Timeout: 15 * time.Second}, batchSize: 64,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

func (c *QdrantClient) Collection() string { return c.collection }
func (c *QdrantClient) VectorSize() int    { return c.vectorSize }

func (c *QdrantClient) EnsureCollection(ctx context.Context) error {
	body := map[string]interface{}{"vectors": map[string]interface{}{"size": c.vectorSize, "distance": "Cosine"}}
	data, _ := json.Marshal(body)

	var lastErr error
	for i := 0; i < 10; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.url+"/collections/"+c.collection, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.httpClient.Do(req)
		if resp != nil && resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		if err == nil && resp != nil && (resp.StatusCode < 300 || resp.StatusCode == http.StatusConflict) {
			return nil
		}
		if err != nil {
			lastErr = err
		} else if resp != nil {
			lastErr = fmt.Errorf("qdrant status %d", resp.StatusCode)
		}
		time.Sleep(time.Duration(i+1) * 300 * time.Millisecond)
	}
	return lastErr
}

func (c *QdrantClient) Upsert(ctx context.Context, id uint64, vector []float32, chunk model.Chunk) error {
	return c.UpsertBatch(ctx, []Point{{ID: id, Vector: vector, Chunk: chunk}})
}

type Point struct {
	ID     uint64
	Vector []float32
	Chunk  model.Chunk
}

func (c *QdrantClient) UpsertBatch(ctx context.Context, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	batchSize := c.batchSize
	if batchSize <= 0 {
		batchSize = len(points)
	}
	for start := 0; start < len(points); start += batchSize {
		end := start + batchSize
		if end > len(points) {
			end = len(points)
		}
		if err := c.upsertBatchOnce(ctx, points[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (c *QdrantClient) upsertBatchOnce(ctx context.Context, points []Point) error {
	payloadPoints := make([]map[string]interface{}, 0, len(points))
	for _, p := range points {
		if len(p.Vector) != c.vectorSize {
			return fmt.Errorf("vector dimension mismatch: got %d want %d", len(p.Vector), c.vectorSize)
		}
		payloadPoints = append(payloadPoints, map[string]interface{}{
			"id":     p.ID,
			"vector": p.Vector,
			"payload": map[string]interface{}{
				"text":            p.Chunk.Text,
				"job_id":          p.Chunk.JobID,
				"filename":        p.Chunk.Filename,
				"path":            p.Chunk.Path,
				"page":            p.Chunk.Page,
				"chunk_index":     p.Chunk.ChunkIndex,
				"source_kind":     p.Chunk.SourceKind,
				"original_path":   p.Chunk.OriginalPath,
				"manifest_path":   p.Chunk.ManifestPath,
				"image_path":      p.Chunk.ImagePath,
				"document_id":     p.Chunk.DocumentID,
				"source_checksum": p.Chunk.SourceChecksum,
				"ingest_scope":    p.Chunk.IngestScope,
				"object_key":      p.Chunk.ObjectKey,
				"object_url":      p.Chunk.ObjectURL,
			},
		})
	}
	body := map[string]interface{}{"points": payloadPoints}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.url+"/collections/"+c.collection+"/points?wait=true", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("qdrant upsert status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return nil
}

func (c *QdrantClient) DeleteByDocumentScope(ctx context.Context, documentID, ingestScope string) error {
	documentID = strings.TrimSpace(documentID)
	ingestScope = strings.TrimSpace(ingestScope)
	if documentID == "" || ingestScope == "" {
		return nil
	}
	body := map[string]interface{}{
		"filter": map[string]interface{}{
			"must": []map[string]interface{}{
				{"key": "document_id", "match": map[string]interface{}{"value": documentID}},
				{"key": "ingest_scope", "match": map[string]interface{}{"value": ingestScope}},
			},
		},
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/collections/"+c.collection+"/points/delete?wait=true", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant delete status %d", resp.StatusCode)
	}
	return nil
}

type qdrantPoint struct {
	Score   float64 `json:"score"`
	Payload struct {
		Text           string `json:"text"`
		Filename       string `json:"filename"`
		Path           string `json:"path"`
		Page           int    `json:"page"`
		ChunkIndex     int    `json:"chunk_index"`
		SourceKind     string `json:"source_kind"`
		OriginalPath   string `json:"original_path"`
		ManifestPath   string `json:"manifest_path"`
		ImagePath      string `json:"image_path"`
		DocumentID     string `json:"document_id"`
		SourceChecksum string `json:"source_checksum"`
		IngestScope    string `json:"ingest_scope"`
		ObjectKey      string `json:"object_key"`
		ObjectURL      string `json:"object_url"`
	} `json:"payload"`
}

type SearchOptions struct {
	Limit          int
	ScoreThreshold float64
	DocumentID     string
	IngestScope    string
	SourceKind     string
}

func (c *QdrantClient) Search(ctx context.Context, vector []float32, limit int) ([]model.Result, error) {
	return c.SearchWithOptions(ctx, vector, SearchOptions{Limit: limit})
}

func (c *QdrantClient) SearchWithOptions(ctx context.Context, queryVector []float32, opts SearchOptions) ([]model.Result, error) {
	if len(queryVector) != c.vectorSize {
		return nil, fmt.Errorf("query vector dimension mismatch: got %d want %d", len(queryVector), c.vectorSize)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	body := map[string]interface{}{"query": queryVector, "limit": limit, "with_payload": true}
	if opts.ScoreThreshold > 0 {
		body["score_threshold"] = opts.ScoreThreshold
	}
	if filter := buildFilter(opts); filter != nil {
		body["filter"] = filter
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/collections/"+c.collection+"/points/query", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("qdrant search status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return nil, nil
	}

	points, err := decodeQdrantPoints(envelope.Result)
	if err != nil {
		return nil, err
	}

	out := make([]model.Result, 0, len(points))
	for _, r := range points {
		out = append(out, model.Result{Text: r.Payload.Text, Score: r.Score, RawScore: r.Score, SemanticScore: r.Score, Source: "qdrant", Filename: r.Payload.Filename, Path: r.Payload.Path, Page: r.Payload.Page, ChunkIndex: r.Payload.ChunkIndex, SourceKind: r.Payload.SourceKind, OriginalPath: r.Payload.OriginalPath, ManifestPath: r.Payload.ManifestPath, ImagePath: r.Payload.ImagePath, DocumentID: r.Payload.DocumentID, SourceChecksum: r.Payload.SourceChecksum, IngestScope: r.Payload.IngestScope, ObjectKey: r.Payload.ObjectKey, ObjectURL: r.Payload.ObjectURL})
	}
	return out, nil
}

func buildFilter(opts SearchOptions) map[string]interface{} {
	var must []map[string]interface{}
	add := func(key, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			must = append(must, map[string]interface{}{"key": key, "match": map[string]interface{}{"value": value}})
		}
	}
	add("document_id", opts.DocumentID)
	add("ingest_scope", opts.IngestScope)
	add("source_kind", opts.SourceKind)
	if len(must) == 0 {
		return nil
	}
	return map[string]interface{}{"must": must}
}

func decodeQdrantPoints(raw json.RawMessage) ([]qdrantPoint, error) {
	var asArray []qdrantPoint
	if err := json.Unmarshal(raw, &asArray); err == nil {
		return asArray, nil
	}
	var asQueryObject struct {
		Points []qdrantPoint `json:"points"`
	}
	if err := json.Unmarshal(raw, &asQueryObject); err == nil {
		return asQueryObject.Points, nil
	}
	return nil, fmt.Errorf("unsupported qdrant result shape")
}
