# Unified Enterprise RAG Platform

This build focuses on a reliable upload -> ingestion -> jobs-status loop before the viewer/editor/search work.

Includes:
- Multipart file upload through `/api/ingest`
- Browser-side byte-level upload progress
- Backend ingestion progress tracking through OCR, chunking, embedding, and Qdrant upsert stages
- Jobs table with status, stage, progress, error, and retry action
- Server-sent job updates on `/api/ws`, plus frontend polling fallback on `/api/job`
- OCR ingestion for text files, text PDFs, scanned PDFs, and image files
- Qdrant vector storage
- Hybrid keyword fallback search

## Run

```bash
docker compose up --build
```

Open the web UI at:

```text
http://localhost:3000
```

The API is exposed at:

```text
http://localhost:8090/api/health
```

The API container also serves the same UI directly at:

```text
http://localhost:8090/
```

## Important upload/proxy detail

The Nginx proxy in `web/nginx.conf` intentionally uses:

```nginx
proxy_pass http://api:8080;
```

Do not add a trailing slash. A trailing slash rewrites `/api/ingest` to `/ingest`, which breaks uploads because the Go API routes are registered under `/api/...`.

## Smoke test upload

After `docker compose up --build`, test upload directly against the API:

```bash
printf 'hello ingestion test\n' > /tmp/rag-test.txt
curl -i -F "file=@/tmp/rag-test.txt" http://localhost:8090/api/ingest
curl http://localhost:8090/api/jobs
```

A successful upload returns `202 Accepted` with a JSON job object. The uploaded file should also appear under `./data` on the host.

## PDF behavior

PDF upload now returns as soon as the file is saved and the ingestion job is queued. PDF preview rendering is lazy and happens only when the browser requests `/api/image`, so preview rendering cannot block the upload response.

For scanned PDFs, ingestion converts PDF pages to PNG using `pdftoppm`, then OCRs those pages with `tesseract`.

## UI layout

The UI is split into separate tabs:

- **Workspace**: upload progress, ingestion progress, timing, and quick navigation.
- **Viewer / Editor**: opens ingested documents, renders PDF pages as editable images, supports pagination, rotation, zoom, text placement, saving edited page images, and re-ingesting edited images.
- **Search / RAG**: asks questions against Qdrant-backed retrieved context and shows Qdrant evidence/source metadata.
- **Jobs**: shows upload/ingestion status, stage metrics, and open/retry actions.
- **Metrics**: raw service metrics.


## Object storage

The API can now persist uploaded source files, edited page images, rendered PDF page images, and image-list manifests to object storage while keeping `/app/data` as the local working cache used by OCR, viewer rendering, and editing.

The built-in provider is **OCI Object Storage through a Pre-Authenticated Request (PAR)**. This avoids adding SDK dependencies and works well for controlled POC deployments. Configure it with environment variables:

```yaml
OBJECT_STORAGE_ENABLED: "true"
OBJECT_STORAGE_REQUIRED: "true"       # fail upload/save if the object write fails
OBJECT_STORAGE_PROVIDER: oci-par
OBJECT_STORAGE_PAR_URL: "https://objectstorage.../p/<par>/n/<namespace>/b/<bucket>/o"
OBJECT_STORAGE_PREFIX: "jarvis/dev"
OBJECT_STORAGE_PUBLIC_BASE_URL: ""     # optional non-secret read base URL, if available
OBJECT_STORAGE_TIMEOUT_SECONDS: "120"
```

When enabled, job responses include:

- `object_status`: `stored`, `failed`, or `disabled`
- `object_key`: the object key written under `OBJECT_STORAGE_PREFIX`
- `object_url`: a safe object URI or the configured public base URL
- `object_error`: upload failure detail when `OBJECT_STORAGE_REQUIRED=false`

Qdrant payloads and the Search / RAG evidence panel also include object keys for retrieved chunks. For full-image PDF ingestion, each rendered page image is uploaded and its object key is preserved in the image-list manifest before OCR/chunking/embedding.

## Jarvis 2.0 hardening notes

This build adds several production-readiness controls for enterprise RAG workflows:

- deterministic lineage metadata in every chunk (`document_id`, `source_checksum`, `ingest_scope`)
- pre-upsert cleanup by document/scope so re-ingestion updates Qdrant instead of silently duplicating stale vectors
- rendered-image ingestion metadata (`image_path`, page number, manifest path, original source path)
- abbreviation expansion for domain acronyms before query embedding; defaults are built in and can be overridden by mounting `data/abbreviations.json`
- Qdrant evidence in the UI now includes document/scope/checksum/object-storage details for auditability

To customize abbreviation handling, copy `data/abbreviations.example.json` to `data/abbreviations.json` and edit the values.

## Retrieval quality controls

This build includes a stronger retrieval pipeline for enterprise RAG evaluation:

- **Improved local embeddings**: token, bigram, and character n-gram hashed embeddings with L2 normalization. The default vector size is now `384` for fewer hash collisions than the earlier small demo vector.
- **Hybrid retrieval**: Qdrant semantic retrieval can be blended with the in-memory keyword index for lexical precision while still enforcing `RAG_REQUIRE_CONTEXT=true` as the Qdrant gate.
- **Configurable reranking**: candidates are reranked using local lexical coverage, bigram overlap, exact phrase evidence, and proximity scoring.
- **MMR diversity**: final results use maximal marginal relevance to reduce duplicate chunks from the same page or repeated annex content.
- **Thresholds and limits**: vector candidates, keyword candidates, rerank pool, final result count, and minimum score are configurable by environment variables.
- **Batched Qdrant upserts**: ingestion writes points in batches and validates embedding vector dimensions before upsert.
- **Chunk overlap**: chunking now uses paragraph/sentence-aware windows with configurable overlap to preserve context across chunk boundaries.
- **Score diagnostics**: the Search / RAG UI now shows semantic, keyword, rerank, and combined scores for retrieved chunks.

Useful environment variables:

```yaml
VECTOR_SIZE: "384"
RAG_HYBRID_SEARCH: "true"
RAG_VECTOR_LIMIT: "40"
RAG_KEYWORD_LIMIT: "40"
RAG_RERANK_LIMIT: "25"
RAG_FINAL_LIMIT: "8"
RAG_MIN_SCORE: "0.05"
RAG_SEMANTIC_WEIGHT: "0.55"
RAG_KEYWORD_WEIGHT: "0.20"
RAG_RERANK_WEIGHT: "0.25"
RAG_MMR_LAMBDA: "0.82"
RAG_CHUNK_CHARS: "1200"
RAG_CHUNK_OVERLAP_CHARS: "180"
QDRANT_TIMEOUT_SECONDS: "15"
QDRANT_UPSERT_BATCH_SIZE: "64"
```

Important: changing `VECTOR_SIZE` requires recreating the Qdrant collection or using a new collection name, because Qdrant collections have a fixed vector dimension.

## `config.yaml` and LLM answer generation

This build now loads `config.yaml` at startup and then overlays environment variables. Docker Compose mounts `./config.yaml` into `/app/config.yaml` and sets `CONFIG_PATH=/app/config.yaml`.

Relevant sections:

```yaml
rag:
  require_context: true        # Qdrant must return chunks before the app answers
  multi_query: true            # create multiple retrieval variants
  query_variants: 3
  vector_limit: 40
  keyword_limit: 40
  rerank_limit: 25
  final_limit: 8
  min_score: 0.05

llm:
  enabled: false               # set true to synthesize answers from retrieved chunks
  provider: ollama             # ollama or openai-compatible
  base_url: http://ollama:11434
  model: llama3.1:8b
  require_context: true
  temperature: 0.1
  max_context_chunks: 6
  max_context_chars: 12000
  max_answer_tokens: 900
```

### Enabling local Ollama generation

1. Set `LLM_ENABLED=true` in `docker-compose.yml` or `config.yaml`.
2. Start the optional Ollama service:

```bash
docker compose --profile llm up --build
```

3. Pull a model once:

```bash
docker compose exec ollama ollama pull llama3.1:8b
```

When enabled, `/api/query` performs:

```text
question -> abbreviation expansion -> multi-query embeddings -> Qdrant retrieval -> keyword blend -> rerank -> MMR -> LLM prompt -> grounded answer
```

The UI shows:

- generated answer
- LLM provider/model
- query variants used for retrieval
- Qdrant evidence panel
- chunk source metadata and scores

If `RAG_REQUIRE_CONTEXT=true`, the app refuses to answer when Qdrant returns no usable chunks. The LLM prompt also instructs the model to answer only from retrieved context and cite source labels such as `[S1]`.


## BGE reranker support

This build can use an external BGE-style cross-encoder reranker after Qdrant candidate retrieval and before MMR/final context selection. Keep it disabled for a zero-dependency local setup, or enable it when you have a reranker HTTP endpoint available.

```yaml
reranker:
  enabled: true
  provider: bge                  # bge/tei or cohere-compatible
  base_url: http://bge-reranker:8080
  model: BAAI/bge-reranker-large
  timeout_seconds: 60
  top_n: 25
  normalize: true                # normalize raw cross-encoder logits to 0..1
  fail_closed: false             # false falls back to local rerank on outage
```

Environment overrides are also supported:

```bash
RERANKER_ENABLED=true
RERANKER_PROVIDER=bge
RERANKER_BASE_URL=http://bge-reranker:8080
RERANKER_MODEL=BAAI/bge-reranker-large
RERANKER_NORMALIZE=true
RERANKER_FAIL_CLOSED=false
```

Supported response shapes include common TEI-style `/rerank` responses and Cohere/OpenAI-compatible `/v1/rerank` responses. The Search / RAG UI shows the active reranker provider/model and any reranker error. If `fail_closed=false`, the system logs the external reranker error and falls back to the built-in local reranker so search remains available.
