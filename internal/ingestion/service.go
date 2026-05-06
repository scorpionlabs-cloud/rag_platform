package ingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rag-unified-realtime/internal/ai"
	"rag-unified-realtime/internal/logging"
	"rag-unified-realtime/internal/model"
	"rag-unified-realtime/internal/repository"
	"rag-unified-realtime/internal/vector"
)

type Broadcaster interface {
	Broadcast(model.WSMessage)
}

type Service struct {
	repo    *repository.MemoryRepository
	ai      ai.Provider
	vector  *vector.QdrantClient
	dataDir string
	jobs    chan model.Job
	ocr     OCR
	chunker Chunker
	log     *logging.Logger
	wsHub   Broadcaster
}

func NewService(repo *repository.MemoryRepository, aiProvider ai.Provider, vectorClient *vector.QdrantClient, dataDir string, log *logging.Logger) *Service {
	return &Service{
		repo:    repo,
		ai:      aiProvider,
		vector:  vectorClient,
		dataDir: dataDir,
		jobs:    make(chan model.Job, 256),
		ocr:     OCR{},
		chunker: NewChunker(),
		log:     log,
	}
}

func (s *Service) SetBroadcaster(ws Broadcaster) { s.wsHub = ws }

func (s *Service) ConfigureChunking(maxChars, overlapChars int) {
	s.chunker = NewChunkerWithOptions(maxChars, overlapChars)
}

func (s *Service) Start(ctx context.Context, workers int) {
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		go s.worker(ctx)
	}
}

func (s *Service) Enqueue(job model.Job) {
	job.Status = model.JobQueued
	job.Stage = "queued"
	job.Progress = clampProgress(job.Progress)
	job.Error = ""
	s.repo.SaveJob(job)
	s.broadcast(job, "queued")

	select {
	case s.jobs <- job:
		return
	default:
		job.Status = model.JobFailed
		job.Stage = "queue full"
		job.Progress = 100
		job.Error = "ingestion queue is full"
		job.CompletedAt = time.Now()
		job.IngestionDurationMs = durationSince(job.StartedAt)
		job.TotalDurationMs = durationSince(job.CreatedAt)
		s.repo.SaveJob(job)
		s.broadcast(job, "failed")
		if s.log != nil {
			s.log.Error("ingestion queue full", "job_id", job.ID)
		}
	}
}

func (s *Service) Retry(jobID string) bool {
	job, ok := s.repo.GetJob(jobID)
	if !ok {
		return false
	}
	if job.Attempts >= job.MaxAttempts {
		return false
	}
	job.Status = model.JobRetrying
	job.Stage = "retry queued"
	job.Progress = 0
	job.Error = ""
	s.repo.SaveJob(job)
	s.broadcast(job, "retrying")
	select {
	case s.jobs <- job:
		return true
	default:
		job.Status = model.JobFailed
		job.Stage = "queue full"
		job.Progress = 100
		job.Error = "ingestion queue is full"
		job.CompletedAt = time.Now()
		job.IngestionDurationMs = durationSince(job.StartedAt)
		job.TotalDurationMs = durationSince(job.CreatedAt)
		s.repo.SaveJob(job)
		s.broadcast(job, "failed")
		return false
	}
}

func (s *Service) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.jobs:
			s.process(ctx, job)
		}
	}
}

func (s *Service) process(ctx context.Context, job model.Job) {
	job.Attempts++
	job.Status = model.JobProcessing
	job.Stage = "starting"
	job.Progress = 5
	job.Error = ""
	job.StartedAt = time.Now()
	job.IngestionDurationMs = 0
	if !job.CreatedAt.IsZero() {
		job.TotalDurationMs = time.Since(job.CreatedAt).Milliseconds()
	}
	s.repo.SaveJob(job)
	s.broadcast(job, "processing")
	if s.log != nil {
		s.log.Info("ingestion started", "job_id", job.ID, "file", job.Filename, "path", job.Path)
	}

	err := s.runPipeline(ctx, &job)
	if err != nil {
		job.Error = err.Error()
		if job.Attempts < job.MaxAttempts {
			job.Status = model.JobRetrying
			job.Stage = "retrying"
			job.IngestionDurationMs = durationSince(job.StartedAt)
			job.TotalDurationMs = durationSince(job.CreatedAt)
			s.repo.SaveJob(job)
			s.broadcast(job, "retrying")
			if s.log != nil {
				s.log.Error("ingestion failed, retrying", "job_id", job.ID, "error", err.Error(), "attempt", job.Attempts)
			}
			time.Sleep(time.Duration(job.Attempts*2) * time.Second)
			select {
			case s.jobs <- job:
			default:
				job.Status = model.JobFailed
				job.Stage = "queue full"
				job.Progress = 100
				job.CompletedAt = time.Now()
				job.IngestionDurationMs = durationSince(job.StartedAt)
				job.TotalDurationMs = durationSince(job.CreatedAt)
				s.repo.SaveJob(job)
				s.broadcast(job, "failed")
			}
			return
		}
		job.Status = model.JobFailed
		job.Stage = "failed"
		job.Progress = 100
		job.CompletedAt = time.Now()
		job.IngestionDurationMs = durationSince(job.StartedAt)
		job.TotalDurationMs = durationSince(job.CreatedAt)
		s.repo.SaveJob(job)
		s.broadcast(job, "failed")
		if s.log != nil {
			s.log.Error("ingestion failed", "job_id", job.ID, "error", err.Error())
		}
		return
	}

	job.Status = model.JobSucceeded
	job.Stage = "complete"
	job.Progress = 100
	job.CompletedAt = time.Now()
	job.Error = ""
	job.IngestionDurationMs = durationSince(job.StartedAt)
	job.TotalDurationMs = durationSince(job.CreatedAt)
	s.repo.SaveJob(job)
	s.broadcast(job, "succeeded")
	if s.log != nil {
		s.log.Info("ingestion succeeded", "job_id", job.ID, "file", job.Filename)
	}
}

func (s *Service) runPipeline(ctx context.Context, job *model.Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(job.SourceKind) == "" {
		job.SourceKind = inferSourceKind(job.Path)
	}
	manifestSourcePath := ""
	manifestPath := ""
	if isImageListPath(job.Path) {
		manifestPath = job.Path
		manifestSourcePath = readImageListSourcePath(job.Path)
	}
	resolvedSourcePath := job.Path
	if manifestSourcePath != "" {
		resolvedSourcePath = manifestSourcePath
	}
	job.DocumentID = stableDocumentID(resolvedSourcePath)
	job.SourceChecksum = checksumForIngestTarget(job.Path)
	job.IngestScope = normalizeIngestScope(job.SourceKind)
	if job.IngestScope == "" {
		job.IngestScope = normalizeIngestScope(inferSourceKind(job.Path))
	}
	job.PipelineNote = fmt.Sprintf("source=%s document_id=%s scope=%s checksum=%s", job.SourceKind, shortID(job.DocumentID), job.IngestScope, shortID(job.SourceChecksum))
	removedMem := s.repo.RemoveChunksByDocumentScope(job.DocumentID, job.IngestScope)
	if err := s.vector.DeleteByDocumentScope(ctx, job.DocumentID, job.IngestScope); err != nil && s.log != nil {
		s.log.Error("qdrant cleanup before reingest failed", "job_id", job.ID, "document_id", job.DocumentID, "scope", job.IngestScope, "error", err.Error())
	}
	if s.log != nil && removedMem > 0 {
		s.log.Info("removed stale in-memory chunks before reingest", "job_id", job.ID, "document_id", job.DocumentID, "scope", job.IngestScope, "chunks", removedMem)
	}
	job.PagesDiscovered = discoverPageCount(job.Path)
	if job.PagesDiscovered == 0 {
		job.PagesDiscovered = 1
	}
	s.advance(job, 8, "inspecting source")

	extractStarted := time.Now()
	s.advance(job, 10, "extracting text")
	pages, err := s.ocr.ExtractPages(job.Path)
	job.ExtractDurationMs = time.Since(extractStarted).Milliseconds()
	if err != nil {
		return err
	}
	job.PagesProcessed = len(pages)
	job.ExtractedChars = countPageChars(pages)
	job.PipelineNote = fmt.Sprintf("source=%s document_id=%s scope=%s checksum=%s pages=%d/%d chars=%d", job.SourceKind, shortID(job.DocumentID), job.IngestScope, shortID(job.SourceChecksum), job.PagesProcessed, job.PagesDiscovered, job.ExtractedChars)
	s.advance(job, 25, "text extracted")

	if err := ctx.Err(); err != nil {
		return err
	}
	chunkStarted := time.Now()
	s.advance(job, 30, "chunking")
	chunks := s.chunker.ChunkPages(pages)
	job.ChunkDurationMs = time.Since(chunkStarted).Milliseconds()
	job.ChunkCount = len(chunks)
	if len(chunks) == 0 {
		return &pipelineError{"no chunks produced"}
	}
	s.advance(job, 40, "chunks ready")

	if err := ctx.Err(); err != nil {
		return err
	}
	embedStarted := time.Now()
	s.advance(job, 45, "embedding")
	texts := make([]string, len(chunks))
	for i := range chunks {
		texts[i] = chunks[i].Text
	}
	embeddings, err := s.ai.EmbedBatch(ctx, texts)
	job.EmbedDurationMs = time.Since(embedStarted).Milliseconds()
	job.EmbeddingCount = len(embeddings)
	if err != nil {
		return err
	}
	if len(embeddings) == 0 {
		return &pipelineError{"no embeddings produced"}
	}
	if len(embeddings) != len(chunks) {
		return &pipelineError{fmt.Sprintf("embedding count mismatch: chunks=%d embeddings=%d", len(chunks), len(embeddings))}
	}
	s.advance(job, 60, "embeddings ready")

	upsertStarted := time.Now()
	s.advance(job, 65, "saving vectors")
	points := make([]vector.Point, 0, len(chunks))
	for i, ch := range chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunkPath := job.Path
		originalPath := manifestSourcePath
		if originalPath != "" {
			chunkPath = originalPath
		}
		objectKey := job.ObjectKey
		objectURL := job.ObjectURL
		if ch.ObjectKey != "" {
			objectKey = ch.ObjectKey
			objectURL = ch.ObjectURL
		}
		chunk := repository.NewChunkWithObjectStorage(job.ID, ch.Text, "qdrant", job.Filename, chunkPath, ch.Page, ch.ChunkIndex, job.SourceKind, originalPath, manifestPath, ch.ImagePath, job.DocumentID, job.SourceChecksum, job.IngestScope, objectKey, objectURL)
		s.repo.SaveChunk(chunk)
		pointID := stablePointID(job.DocumentID, job.IngestScope, ch.Page, ch.ChunkIndex, ch.ImagePath, ch.Text)
		points = append(points, vector.Point{ID: pointID, Vector: embeddings[i], Chunk: chunk})
	}
	const progressBase = 65
	const progressSpan = 30
	batchSize := 64
	for start := 0; start < len(points); start += batchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + batchSize
		if end > len(points) {
			end = len(points)
		}
		if err := s.vector.UpsertBatch(ctx, points[start:end]); err != nil {
			return err
		}
		job.VectorUpserted += end - start
		job.UpsertDurationMs = time.Since(upsertStarted).Milliseconds()
		progress := progressBase + int(float64(job.VectorUpserted)/float64(len(points))*progressSpan)
		s.advance(job, progress, fmt.Sprintf("saving vectors %d/%d", job.VectorUpserted, len(points)))
	}
	job.UpsertDurationMs = time.Since(upsertStarted).Milliseconds()
	if job.VectorUpserted != job.ChunkCount {
		return &pipelineError{fmt.Sprintf("qdrant upsert count mismatch: chunks=%d upserts=%d", job.ChunkCount, job.VectorUpserted)}
	}
	if manifestPath != "" {
		job.PipelineNote = fmt.Sprintf("source=%s document_id=%s scope=%s checksum=%s original=%s manifest=%s image_pages=%d/%d chars=%d chunks=%d embeddings=%d qdrant_upserts=%d", job.SourceKind, shortID(job.DocumentID), job.IngestScope, shortID(job.SourceChecksum), filepath.Base(manifestSourcePath), filepath.Base(manifestPath), job.PagesProcessed, job.PagesDiscovered, job.ExtractedChars, job.ChunkCount, job.EmbeddingCount, job.VectorUpserted)
	} else {
		job.PipelineNote = fmt.Sprintf("source=%s document_id=%s scope=%s checksum=%s pages=%d/%d chars=%d chunks=%d embeddings=%d qdrant_upserts=%d", job.SourceKind, shortID(job.DocumentID), job.IngestScope, shortID(job.SourceChecksum), job.PagesProcessed, job.PagesDiscovered, job.ExtractedChars, job.ChunkCount, job.EmbeddingCount, job.VectorUpserted)
	}
	s.advance(job, 98, "finalizing")
	return nil
}

func (s *Service) advance(job *model.Job, progress int, stage string) {
	job.Progress = clampProgress(progress)
	job.Stage = stage
	job.IngestionDurationMs = durationSince(job.StartedAt)
	job.TotalDurationMs = durationSince(job.CreatedAt)
	s.repo.SaveJob(*job)
	s.broadcast(*job, string(job.Status))
}

func durationSince(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return time.Since(t).Milliseconds()
}

func clampProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func inferSourceKind(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	base := strings.ToLower(filepath.Base(path))
	if isImageListPath(path) {
		return "image page set"
	}
	if strings.Contains(base, ".edited.") {
		return "edited page image"
	}
	switch ext {
	case ".pdf":
		return "original pdf"
	case ".png", ".jpg", ".jpeg", ".tif", ".tiff", ".webp":
		return "image"
	case ".txt", ".md", ".csv", ".json", ".log":
		return "text file"
	default:
		return strings.TrimPrefix(ext, ".")
	}
}

func discoverPageCount(path string) int {
	if isImageListPath(path) {
		data, err := os.ReadFile(path)
		if err != nil {
			return 0
		}
		var manifest struct {
			Pages []struct {
				Page int    `json:"page"`
				Path string `json:"path"`
			} `json:"pages"`
		}
		if json.Unmarshal(data, &manifest) == nil {
			return len(manifest.Pages)
		}
		return 0
	}
	if strings.ToLower(filepath.Ext(path)) != ".pdf" {
		return 1
	}
	out, err := exec.Command("pdfinfo", path).CombinedOutput()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.EqualFold(strings.TrimSuffix(fields[0], ":"), "Pages") {
			n, _ := strconv.Atoi(fields[1])
			return n
		}
	}
	return 0
}

func isImageListPath(path string) bool {
	return strings.HasSuffix(strings.ToLower(filepath.Base(path)), ".imagelist.json")
}

func readImageListSourcePath(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var manifest ImageListManifest
	if json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return strings.TrimSpace(manifest.SourcePath)
}

func countPageChars(pages []PageText) int {
	total := 0
	for _, page := range pages {
		total += len([]rune(page.Text))
	}
	return total
}

func stableDocumentID(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return hex.EncodeToString(sum[:])
}

func checksumForIngestTarget(path string) string {
	if isImageListPath(path) {
		return checksumImageList(path)
	}
	return checksumFile(path)
}

func checksumImageList(path string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("imagelist:"))
	data, err := os.ReadFile(path)
	if err == nil {
		_, _ = h.Write(data)
	}
	var manifest ImageListManifest
	if err := json.Unmarshal(data, &manifest); err == nil {
		for _, page := range manifest.Pages {
			_, _ = h.Write([]byte(fmt.Sprintf("|page:%d|path:%s|", page.Page, page.Path)))
			if fileSum := checksumFile(page.Path); fileSum != "" {
				_, _ = h.Write([]byte(fileSum))
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func checksumFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func normalizeIngestScope(sourceKind string) string {
	s := strings.ToLower(strings.TrimSpace(sourceKind))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	return strings.Trim(s, "_")
}

func stablePointID(documentID, ingestScope string, page, chunkIndex int, imagePath, text string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(documentID))
	_, _ = h.Write([]byte("|" + ingestScope + "|"))
	_, _ = h.Write([]byte(strconv.Itoa(page)))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(strconv.Itoa(chunkIndex)))
	_, _ = h.Write([]byte("|" + imagePath + "|"))
	textHash := sha256.Sum256([]byte(text))
	_, _ = h.Write(textHash[:])
	return h.Sum64()
}

func shortID(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

type pipelineError struct{ msg string }

func (e *pipelineError) Error() string { return e.msg }

func (s *Service) broadcast(job model.Job, status string) {
	if s.wsHub == nil {
		return
	}
	s.wsHub.Broadcast(model.WSMessage{
		Type:                "job_update",
		JobID:               job.ID,
		Filename:            job.Filename,
		Path:                job.Path,
		Status:              status,
		Stage:               job.Stage,
		Progress:            job.Progress,
		Error:               job.Error,
		UploadBytes:         job.UploadBytes,
		UploadDurationMs:    job.UploadDurationMs,
		IngestionDurationMs: job.IngestionDurationMs,
		TotalDurationMs:     job.TotalDurationMs,
		SourceKind:          job.SourceKind,
		PagesDiscovered:     job.PagesDiscovered,
		PagesProcessed:      job.PagesProcessed,
		ExtractedChars:      job.ExtractedChars,
		ChunkCount:          job.ChunkCount,
		EmbeddingCount:      job.EmbeddingCount,
		VectorUpserted:      job.VectorUpserted,
		ExtractDurationMs:   job.ExtractDurationMs,
		ChunkDurationMs:     job.ChunkDurationMs,
		EmbedDurationMs:     job.EmbedDurationMs,
		UpsertDurationMs:    job.UpsertDurationMs,
		PipelineNote:        job.PipelineNote,
		DocumentID:          job.DocumentID,
		SourceChecksum:      job.SourceChecksum,
		IngestScope:         job.IngestScope,
		ObjectKey:           job.ObjectKey,
		ObjectURL:           job.ObjectURL,
		ObjectStatus:        job.ObjectStatus,
		ObjectError:         job.ObjectError,
	})
}
