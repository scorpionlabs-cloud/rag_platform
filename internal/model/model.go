package model

import "time"

type JobStatus string

const (
	JobQueued     JobStatus = "queued"
	JobProcessing JobStatus = "processing"
	JobSucceeded  JobStatus = "succeeded"
	JobFailed     JobStatus = "failed"
	JobRetrying   JobStatus = "retrying"
)

type Job struct {
	ID                  string    `json:"id"`
	Filename            string    `json:"filename"`
	Path                string    `json:"path"`
	Status              JobStatus `json:"status"`
	Stage               string    `json:"stage,omitempty"`
	Error               string    `json:"error,omitempty"`
	Progress            int       `json:"progress"`
	Attempts            int       `json:"attempts"`
	MaxAttempts         int       `json:"max_attempts"`
	UploadBytes         int64     `json:"upload_bytes,omitempty"`
	UploadDurationMs    int64     `json:"upload_duration_ms,omitempty"`
	StartedAt           time.Time `json:"started_at,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	CompletedAt         time.Time `json:"completed_at,omitempty"`
	IngestionDurationMs int64     `json:"ingestion_duration_ms,omitempty"`
	TotalDurationMs     int64     `json:"total_duration_ms,omitempty"`
	SourceKind          string    `json:"source_kind,omitempty"`
	PagesDiscovered     int       `json:"pages_discovered,omitempty"`
	PagesProcessed      int       `json:"pages_processed,omitempty"`
	ExtractedChars      int       `json:"extracted_chars,omitempty"`
	ChunkCount          int       `json:"chunk_count,omitempty"`
	EmbeddingCount      int       `json:"embedding_count,omitempty"`
	VectorUpserted      int       `json:"vector_upserted,omitempty"`
	ExtractDurationMs   int64     `json:"extract_duration_ms,omitempty"`
	ChunkDurationMs     int64     `json:"chunk_duration_ms,omitempty"`
	EmbedDurationMs     int64     `json:"embed_duration_ms,omitempty"`
	UpsertDurationMs    int64     `json:"upsert_duration_ms,omitempty"`
	PipelineNote        string    `json:"pipeline_note,omitempty"`
	DocumentID          string    `json:"document_id,omitempty"`
	SourceChecksum      string    `json:"source_checksum,omitempty"`
	IngestScope         string    `json:"ingest_scope,omitempty"`
	ObjectKey           string    `json:"object_key,omitempty"`
	ObjectURL           string    `json:"object_url,omitempty"`
	ObjectStatus        string    `json:"object_status,omitempty"`
	ObjectError         string    `json:"object_error,omitempty"`
}

type Chunk struct {
	ID             string    `json:"id"`
	JobID          string    `json:"job_id"`
	Text           string    `json:"text"`
	Source         string    `json:"source"`
	Filename       string    `json:"filename,omitempty"`
	Path           string    `json:"path,omitempty"`
	Page           int       `json:"page,omitempty"`
	ChunkIndex     int       `json:"chunk_index,omitempty"`
	SourceKind     string    `json:"source_kind,omitempty"`
	OriginalPath   string    `json:"original_path,omitempty"`
	ManifestPath   string    `json:"manifest_path,omitempty"`
	ImagePath      string    `json:"image_path,omitempty"`
	DocumentID     string    `json:"document_id,omitempty"`
	SourceChecksum string    `json:"source_checksum,omitempty"`
	IngestScope    string    `json:"ingest_scope,omitempty"`
	ObjectKey      string    `json:"object_key,omitempty"`
	ObjectURL      string    `json:"object_url,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type Result struct {
	Text           string  `json:"text"`
	Score          float64 `json:"score"`
	RawScore       float64 `json:"raw_score,omitempty"`
	SemanticScore  float64 `json:"semantic_score,omitempty"`
	KeywordScore   float64 `json:"keyword_score,omitempty"`
	RerankScore    float64 `json:"rerank_score,omitempty"`
	CombinedScore  float64 `json:"combined_score,omitempty"`
	Confidence     float64 `json:"confidence"`
	Explanation    string  `json:"explanation"`
	Source         string  `json:"source"`
	Filename       string  `json:"filename,omitempty"`
	Path           string  `json:"path,omitempty"`
	Page           int     `json:"page,omitempty"`
	ChunkIndex     int     `json:"chunk_index,omitempty"`
	SourceKind     string  `json:"source_kind,omitempty"`
	OriginalPath   string  `json:"original_path,omitempty"`
	ManifestPath   string  `json:"manifest_path,omitempty"`
	ImagePath      string  `json:"image_path,omitempty"`
	DocumentID     string  `json:"document_id,omitempty"`
	SourceChecksum string  `json:"source_checksum,omitempty"`
	IngestScope    string  `json:"ingest_scope,omitempty"`
	ObjectKey      string  `json:"object_key,omitempty"`
	ObjectURL      string  `json:"object_url,omitempty"`
}

type Feedback struct {
	Query  string `json:"query"`
	Result string `json:"result"`
	Useful bool   `json:"useful"`
}

type WSMessage struct {
	Type                string `json:"type"`
	JobID               string `json:"job_id"`
	Filename            string `json:"filename,omitempty"`
	Path                string `json:"path,omitempty"`
	Status              string `json:"status"`
	Stage               string `json:"stage,omitempty"`
	Progress            int    `json:"progress"`
	Error               string `json:"error,omitempty"`
	UploadBytes         int64  `json:"upload_bytes,omitempty"`
	UploadDurationMs    int64  `json:"upload_duration_ms,omitempty"`
	IngestionDurationMs int64  `json:"ingestion_duration_ms,omitempty"`
	TotalDurationMs     int64  `json:"total_duration_ms,omitempty"`
	SourceKind          string `json:"source_kind,omitempty"`
	PagesDiscovered     int    `json:"pages_discovered,omitempty"`
	PagesProcessed      int    `json:"pages_processed,omitempty"`
	ExtractedChars      int    `json:"extracted_chars,omitempty"`
	ChunkCount          int    `json:"chunk_count,omitempty"`
	EmbeddingCount      int    `json:"embedding_count,omitempty"`
	VectorUpserted      int    `json:"vector_upserted,omitempty"`
	ExtractDurationMs   int64  `json:"extract_duration_ms,omitempty"`
	ChunkDurationMs     int64  `json:"chunk_duration_ms,omitempty"`
	EmbedDurationMs     int64  `json:"embed_duration_ms,omitempty"`
	UpsertDurationMs    int64  `json:"upsert_duration_ms,omitempty"`
	PipelineNote        string `json:"pipeline_note,omitempty"`
	DocumentID          string `json:"document_id,omitempty"`
	SourceChecksum      string `json:"source_checksum,omitempty"`
	IngestScope         string `json:"ingest_scope,omitempty"`
	ObjectKey           string `json:"object_key,omitempty"`
	ObjectURL           string `json:"object_url,omitempty"`
	ObjectStatus        string `json:"object_status,omitempty"`
	ObjectError         string `json:"object_error,omitempty"`
}
