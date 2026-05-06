package repository

import (
	"sort"
	"strings"
	"sync"
	"time"

	"rag-unified-realtime/internal/model"
)

type MemoryRepository struct {
	mu       sync.RWMutex
	jobs     map[string]model.Job
	chunks   []model.Chunk
	feedback []model.Feedback
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{jobs: make(map[string]model.Job)}
}

func (r *MemoryRepository) SaveJob(job model.Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[job.ID] = job
}

func (r *MemoryRepository) GetJob(id string) (model.Job, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.jobs[id]
	return j, ok
}

func (r *MemoryRepository) Jobs() []model.Job {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]model.Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (r *MemoryRepository) SaveChunk(chunk model.Chunk) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chunks = append(r.chunks, chunk)
}

func (r *MemoryRepository) RemoveChunksByDocumentScope(documentID, ingestScope string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(documentID) == "" || strings.TrimSpace(ingestScope) == "" {
		return 0
	}
	kept := r.chunks[:0]
	removed := 0
	for _, c := range r.chunks {
		if c.DocumentID == documentID && c.IngestScope == ingestScope {
			removed++
			continue
		}
		kept = append(kept, c)
	}
	r.chunks = kept
	return removed
}

func (r *MemoryRepository) CountChunks() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.chunks)
}

func (r *MemoryRepository) KeywordSearch(query string, limit int) []model.Result {
	r.mu.RLock()
	defer r.mu.RUnlock()
	terms := strings.Fields(strings.ToLower(query))
	var scored []model.Result
	for _, c := range r.chunks {
		textLower := strings.ToLower(c.Text)
		score := 0.0
		for _, term := range terms {
			if strings.Contains(textLower, term) {
				score += 1.0
			}
		}
		if score > 0 {
			scored = append(scored, model.Result{Text: c.Text, Score: score, RawScore: score, Source: "keyword", Filename: c.Filename, Path: c.Path, Page: c.Page, ChunkIndex: c.ChunkIndex, SourceKind: c.SourceKind, OriginalPath: c.OriginalPath, ManifestPath: c.ManifestPath, ImagePath: c.ImagePath, DocumentID: c.DocumentID, SourceChecksum: c.SourceChecksum, IngestScope: c.IngestScope, ObjectKey: c.ObjectKey, ObjectURL: c.ObjectURL})
		}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > limit {
		return scored[:limit]
	}
	return scored
}

func (r *MemoryRepository) SaveFeedback(f model.Feedback) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.feedback = append(r.feedback, f)
}

func NewChunk(jobID, text, source string) model.Chunk {
	return NewChunkWithMetadata(jobID, text, source, source, "", 1, 0)
}

func NewChunkWithMetadata(jobID, text, source, filename, path string, page, chunkIndex int) model.Chunk {
	return NewChunkWithFullMetadata(jobID, text, source, filename, path, page, chunkIndex, "", "", "", "")
}

func NewChunkWithFullMetadata(jobID, text, source, filename, path string, page, chunkIndex int, sourceKind, originalPath, manifestPath, imagePath string) model.Chunk {
	return NewChunkWithLineage(jobID, text, source, filename, path, page, chunkIndex, sourceKind, originalPath, manifestPath, imagePath, "", "", "")
}

func NewChunkWithObjectStorage(jobID, text, source, filename, path string, page, chunkIndex int, sourceKind, originalPath, manifestPath, imagePath, documentID, sourceChecksum, ingestScope, objectKey, objectURL string) model.Chunk {
	chunk := NewChunkWithLineage(jobID, text, source, filename, path, page, chunkIndex, sourceKind, originalPath, manifestPath, imagePath, documentID, sourceChecksum, ingestScope)
	chunk.ObjectKey = objectKey
	chunk.ObjectURL = objectURL
	return chunk
}

func NewChunkWithLineage(jobID, text, source, filename, path string, page, chunkIndex int, sourceKind, originalPath, manifestPath, imagePath, documentID, sourceChecksum, ingestScope string) model.Chunk {
	return model.Chunk{
		ID:             time.Now().Format("20060102150405.000000000"),
		JobID:          jobID,
		Text:           text,
		Source:         source,
		Filename:       filename,
		Path:           path,
		Page:           page,
		ChunkIndex:     chunkIndex,
		SourceKind:     sourceKind,
		OriginalPath:   originalPath,
		ManifestPath:   manifestPath,
		ImagePath:      imagePath,
		DocumentID:     documentID,
		SourceChecksum: sourceChecksum,
		IngestScope:    ingestScope,
		CreatedAt:      time.Now(),
	}
}
