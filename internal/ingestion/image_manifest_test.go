package ingestion

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestChunkPagesPreservesImagePathForRenderedImageIngestion(t *testing.T) {
	chunks := NewChunker().ChunkPages([]PageText{{Page: 7, ImagePath: "/tmp/page-0007.png", Text: "Rendered image OCR text should become an embedded chunk with page image metadata."}})
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Page != 7 {
		t.Fatalf("expected page 7, got %d", chunks[0].Page)
	}
	if chunks[0].ImagePath != "/tmp/page-0007.png" {
		t.Fatalf("image path was not preserved: %q", chunks[0].ImagePath)
	}
}

func TestDiscoverPageCountForImageListManifest(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "doc.full_document_image_set.imagelist.json")
	manifest := ImageListManifest{SourcePath: filepath.Join(dir, "doc.pdf"), Mode: "full document image set"}
	manifest.Pages = append(manifest.Pages,
		struct {
			Page      int    `json:"page"`
			Path      string `json:"path"`
			ObjectKey string `json:"object_key,omitempty"`
			ObjectURL string `json:"object_url,omitempty"`
		}{Page: 1, Path: filepath.Join(dir, "doc.page_0001.render.png")},
		struct {
			Page      int    `json:"page"`
			Path      string `json:"path"`
			ObjectKey string `json:"object_key,omitempty"`
			ObjectURL string `json:"object_url,omitempty"`
		}{Page: 2, Path: filepath.Join(dir, "doc.page_0002.render.png")},
	)
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	if got := discoverPageCount(manifestPath); got != 2 {
		t.Fatalf("expected 2 manifest pages, got %d", got)
	}
	if got := readImageListSourcePath(manifestPath); got != manifest.SourcePath {
		t.Fatalf("expected source path %q, got %q", manifest.SourcePath, got)
	}
}
