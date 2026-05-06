package vector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"rag-unified-realtime/internal/model"
)

func TestUpsertPayloadIncludesRenderedImageChunkMetadata(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collections/rag/points" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("wait") != "true" {
			t.Fatalf("expected wait=true")
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"result":{},"status":"ok"}`))
	}))
	defer server.Close()

	client := NewQdrantClient(server.URL, "rag", 2)
	chunk := model.Chunk{
		Text:         "OCR text from rendered page image",
		JobID:        "job-1",
		Filename:     "doc.pdf",
		Path:         "/data/doc.pdf",
		Page:         3,
		ChunkIndex:   4,
		SourceKind:   "full document image set",
		OriginalPath: "/data/doc.pdf",
		ManifestPath: "/data/doc.imagelist.json",
		ImagePath:    "/data/doc.page_0003.render.png",
	}
	if err := client.Upsert(context.Background(), 123, []float32{0.1, 0.2}, chunk); err != nil {
		t.Fatal(err)
	}

	points := payload["points"].([]any)
	point := points[0].(map[string]any)
	pl := point["payload"].(map[string]any)
	for key, want := range map[string]string{
		"source_kind":   chunk.SourceKind,
		"original_path": chunk.OriginalPath,
		"manifest_path": chunk.ManifestPath,
		"image_path":    chunk.ImagePath,
	} {
		if got := pl[key]; got != want {
			t.Fatalf("payload[%s] = %v, want %q", key, got, want)
		}
	}
}
