# Unified Enterprise RAG Platform

A full-stack Retrieval-Augmented Generation platform for document upload, OCR/image ingestion, Qdrant vector search, hybrid retrieval, BGE reranking, LLM answer generation, viewer/editor workflows, job tracking, and optional object storage.

The platform is designed as a practical enterprise RAG foundation: documents are uploaded, parsed, rendered/OCRed when needed, chunked, embedded, written to Qdrant with rich lineage metadata, retrieved through hybrid semantic/keyword search, reranked, and then passed to an LLM for grounded answers with source evidence.

---

## 1. Capabilities

### Document workspace

- Multipart upload through `POST /api/ingest`.
- Browser-side byte-level upload progress.
- Backend ingestion progress with stage timing.
- Upload timing, ingestion timing, total job timing, file size, and processing metrics.
- Optional checkbox to ingest uploaded PDFs as full rendered page-image sets.
- Explicit **Open Viewer / Editor** action; upload does not auto-open the viewer.

### Ingestion

- Text files.
- Native text PDFs.
- Scanned PDFs using rendered page images and OCR.
- Image files.
- Edited page images.
- Full document image-set manifests.
- Single-page edit reingested back into full document image-set ingestion.
- Metrics for pages discovered, pages processed, extracted characters, chunk count, embedding count, Qdrant upserts, and stage timings.

### Viewer / Editor

- Separate Viewer / Editor tab.
- Opens uploaded or ingested documents from Workspace or Jobs.
- PDF pagination.
- Render PDF pages as editable images.
- Zoom, fit width, rotate left/right, reset view.
- Text placement/editing.
- Highlight overlays.
- Delete/white-out regions.
- Select/move/edit/delete overlays.
- Undo/redo.
- Save edited page image as a sidecar PNG.
- Re-ingest current page image, edited pages, or full image set.
- Save one edited page and re-ingest the full image set so the edited page replaces the rendered page in full-document image ingestion.

### Jobs

- Jobs tab with status, stage, progress, timings, file size, source type, processed page/chunk details, error detail, open, and retry actions.
- Server-sent job updates on `/api/ws`.
- Polling fallback through job APIs.

### Search / RAG

- Separate Search / RAG tab.
- Qdrant-backed retrieval with `RAG_REQUIRE_CONTEXT=true` support.
- Hybrid vector + keyword retrieval.
- Query rewrite / multi-query variants.
- Abbreviation expansion.
- Local reranking fallback.
- Optional external BGE reranker.
- MMR diversification.
- LLM answer generation from retrieved chunks only.
- Qdrant evidence panel with source filename, page, chunk, score, source kind, ingest scope, document ID, checksum, image path, and object key where available.

### Object storage

- Optional OCI Object Storage through Pre-Authenticated Request, or local-only cache mode.
- Upload original source files, edited page images, rendered page images, and image-list manifests.
- Keep `/app/data` as local working cache for OCR, viewing, editing, and reingestion.

---

## 2. Repository layout

```text
.
├── Dockerfile
├── README.md
├── config.yaml
├── docker-compose.yml
├── go.mod
├── cmd/
│   └── api/
│       └── main.go
├── internal/
│   ├── ai/              # local embedding provider
│   ├── config/          # config.yaml + env override loading
│   ├── httpapi/         # REST handlers, SSE/websocket-style updates
│   ├── ingestion/       # OCR, chunking, manifests, ingestion service
│   ├── llm/             # Ollama/openai-compatible generation client
│   ├── logging/         # structured logging helpers
│   ├── model/           # shared domain models
│   ├── repository/      # in-memory job/document repository
│   ├── rerank/          # external reranker HTTP client
│   ├── search/          # retrieval, hybrid scoring, reranking, MMR, answer flow
│   ├── storage/         # local + object storage integration
│   └── vector/          # Qdrant client
├── reranker/
│   ├── Dockerfile       # CPU BGE reranker service
│   ├── requirements.txt
│   └── server.py
├── scripts/
│   └── smoke_test.sh
├── web/
│   ├── app.js
│   ├── index.html
│   ├── nginx.conf
│   └── style.css
└── data/
    ├── .gitkeep
    └── abbreviations.example.json
```

---

## 3. Architecture

```text
Browser UI
  ├── Workspace: upload/progress/timing
  ├── Viewer / Editor: page render/edit/save/reingest
  ├── Search / RAG: question answering + evidence
  └── Jobs: job lifecycle/status
        │
        ▼
Nginx web container
  └── proxies /api/* to api:8080
        │
        ▼
Go API container
  ├── upload handler
  ├── ingestion workers
  ├── OCR/render/image manifest pipeline
  ├── chunking + embeddings
  ├── Qdrant upsert/search
  ├── BGE reranker client
  ├── LLM client
  ├── viewer/editor endpoints
  └── job/status events
        │
        ├── Qdrant vector database
        ├── bge-reranker service, optional but recommended
        ├── Ollama service, optional for local LLM
        └── object storage, optional
```

---

## 4. Quick start

### Start core services

```bash
docker compose up --build --force-recreate
```

Open:

```text
http://localhost:3000
```

API health:

```bash
curl http://localhost:8090/api/health
```

Qdrant:

```bash
curl http://localhost:6333/collections
```

CPU BGE reranker:

```bash
curl http://localhost:8081/health
```

---

## 5. Start with Ollama LLM

If the `ollama` service is behind the `llm` compose profile, start it with:

```bash
docker compose --profile llm up --build --force-recreate
```

Pull the configured model once:

```bash
docker compose exec ollama ollama pull llama3.1:8b
```

Check Ollama:

```bash
curl http://localhost:11434/api/tags
```

Check from inside the API container:

```bash
docker compose exec api sh -lc 'wget -qO- http://ollama:11434/api/tags'
```

If you want Ollama to always start, remove this from the `ollama` service:

```yaml
profiles:
  - llm
```

---

## 6. Docker Compose reference

A typical CPU-only compose includes:

```yaml
services:
  api:
    build: .
    restart: unless-stopped
    ports:
      - "8090:8080"
    environment:
      PORT: "8080"
      CONFIG_PATH: /app/config.yaml
      DATA_DIR: /app/data

      QDRANT_URL: http://qdrant:6333
      QDRANT_COLLECTION: rag
      VECTOR_SIZE: "384"

      WORKERS: "4"
      RAG_REQUIRE_CONTEXT: "true"

      LLM_ENABLED: "true"
      LLM_PROVIDER: ollama
      LLM_BASE_URL: http://ollama:11434
      LLM_MODEL: llama3.1:8b
      LLM_TIMEOUT_SECONDS: "120"

      RERANKER_ENABLED: "true"
      RERANKER_PROVIDER: bge
      RERANKER_BASE_URL: http://bge-reranker:8080
      RERANKER_MODEL: BAAI/bge-reranker-base
      RERANKER_TIMEOUT_SECONDS: "120"
      RERANKER_TOP_N: "25"
      RERANKER_NORMALIZE: "true"
      RERANKER_FAIL_CLOSED: "false"

      OBJECT_STORAGE_ENABLED: "false"
      OBJECT_STORAGE_REQUIRED: "false"
      OBJECT_STORAGE_PROVIDER: oci-par
      OBJECT_STORAGE_PAR_URL: ""
      OBJECT_STORAGE_PREFIX: jarvis/dev
      OBJECT_STORAGE_PUBLIC_BASE_URL: ""
      OBJECT_STORAGE_TIMEOUT_SECONDS: "120"
    volumes:
      - ./data:/app/data
      - ./config.yaml:/app/config.yaml:ro
    depends_on:
      - qdrant
      - bge-reranker

  qdrant:
    image: qdrant/qdrant:v1.13.4
    restart: unless-stopped
    ports:
      - "6333:6333"
    volumes:
      - qdrant_data:/qdrant/storage

  bge-reranker:
    build:
      context: ./reranker
    restart: unless-stopped
    environment:
      RERANKER_MODEL: BAAI/bge-reranker-base
      RERANKER_MAX_LENGTH: "512"
      HF_HOME: /models/huggingface
      TRANSFORMERS_CACHE: /models/huggingface
      TOKENIZERS_PARALLELISM: "false"
    volumes:
      - hf_cache:/models/huggingface
    ports:
      - "8081:8080"

  web:
    image: nginx:1.27-alpine
    restart: unless-stopped
    ports:
      - "3000:80"
    volumes:
      - ./web:/usr/share/nginx/html:ro
      - ./web/nginx.conf:/etc/nginx/conf.d/default.conf:ro
    depends_on:
      - api

  ollama:
    image: ollama/ollama:latest
    restart: unless-stopped
    profiles:
      - llm
    ports:
      - "11434:11434"
    volumes:
      - ollama_data:/root/.ollama

volumes:
  qdrant_data:
  hf_cache:
  ollama_data:
```

---

## 7. Nginx proxy requirement

The web proxy must preserve `/api/...` paths.

Correct:

```nginx
location /api/ {
  proxy_pass http://api:8080;
  proxy_http_version 1.1;

  proxy_set_header Host $host;
  proxy_set_header X-Real-IP $remote_addr;
  proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  proxy_set_header X-Forwarded-Proto $scheme;

  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection "upgrade";

  client_max_body_size 500M;
  proxy_request_buffering off;
  proxy_read_timeout 3600;
  proxy_send_timeout 3600;
}
```

Do not use a trailing slash here:

```nginx
proxy_pass http://api:8080/;
```

That can rewrite `/api/ingest` into `/ingest`, which breaks the upload route.

---

## 8. Configuration model

The app loads:

```text
config.yaml first
then environment variable overrides
```

Default mount:

```yaml
CONFIG_PATH: /app/config.yaml
volumes:
  - ./config.yaml:/app/config.yaml:ro
```

Important sections:

```yaml
server:
  port: 8080

data:
  dir: /app/data

qdrant:
  url: http://qdrant:6333
  collection: rag
  vector_size: 384
  timeout_seconds: 15
  upsert_batch_size: 64

rag:
  require_context: true
  hybrid_search: true
  multi_query: true
  query_variants: 3
  vector_limit: 40
  keyword_limit: 40
  rerank_limit: 25
  final_limit: 8
  min_score: 0.05
  semantic_weight: 0.55
  keyword_weight: 0.20
  rerank_weight: 0.25
  mmr_lambda: 0.82
  chunk_chars: 1200
  chunk_overlap_chars: 180

llm:
  enabled: true
  provider: ollama
  base_url: http://ollama:11434
  model: llama3.1:8b
  require_context: true
  temperature: 0.1
  max_context_chunks: 6
  max_context_chars: 12000
  max_answer_tokens: 900

reranker:
  enabled: true
  provider: bge
  base_url: http://bge-reranker:8080
  model: BAAI/bge-reranker-base
  timeout_seconds: 120
  top_n: 25
  normalize: true
  fail_closed: false
```

---

## 9. Data directory

Compose maps:

```yaml
DATA_DIR: /app/data
volumes:
  - ./data:/app/data
```

Meaning:

```text
host:      ./data
container: /app/data
```

Uploaded files, rendered pages, edited sidecar images, manifests, and abbreviation overrides are stored or cached there.

Sanity check after upload:

```bash
ls -lah ./data
```

---

## 10. Upload and ingestion flow

```text
User selects file
  ↓
Browser sends multipart upload with XHR progress
  ↓
API saves to /app/data using temp file then atomic rename
  ↓
Job is queued
  ↓
Worker determines source type
  ↓
PDF text extraction or image rendering/OCR
  ↓
Chunks are created with metadata
  ↓
Embeddings are generated
  ↓
Existing Qdrant points for document/scope are cleaned
  ↓
New vectors are upserted to Qdrant
  ↓
Job status updates Workspace and Jobs
```

For PDF full-image ingestion:

```text
PDF upload
  ↓
Render every PDF page to PNG
  ↓
Create image-list manifest
  ↓
OCR every rendered page image
  ↓
Chunk OCR text with original page metadata
  ↓
Embed chunks
  ↓
Upsert to Qdrant
```

---

## 11. Viewer / Editor flow

```text
Open document
  ↓
Fetch document metadata/page count
  ↓
Render current page as image
  ↓
User applies text/highlight/delete overlays
  ↓
Save edited image
  ↓
Write sidecar PNG
  ↓
Optionally re-ingest current page, edited pages, or full image set
```

For **Save This Page + Re-ingest Full Image Set**:

```text
Save current edited page sidecar
  ↓
Build full image-set manifest
  ↓
Use edited PNG for edited page
  ↓
Use rendered PNGs for all other pages
  ↓
OCR/chunk/embed/upsert full image-set into Qdrant
```

The original PDF remains unchanged.

---

## 12. Search / RAG flow

```text
User question
  ↓
Normalize and expand abbreviations
  ↓
Generate query variants
  ↓
Embed query variants
  ↓
Retrieve candidates from Qdrant
  ↓
Blend keyword retrieval
  ↓
External BGE reranker if enabled
  ↓
Local reranker fallback if needed
  ↓
MMR diversification
  ↓
Build grounded context
  ↓
LLM answers using retrieved context only
  ↓
UI shows answer + Qdrant evidence panel
```

If `RAG_REQUIRE_CONTEXT=true`, the API refuses to answer if Qdrant returns no usable context.

---

## 13. BGE reranker on CPU

This project includes a CPU FastAPI reranker service under `reranker/`.

### `reranker/requirements.txt`

Use the pinned versions below because `FlagEmbedding==1.3.3` requires `transformers==4.44.2`.

```text
fastapi==0.115.6
uvicorn[standard]==0.34.0
FlagEmbedding==1.3.3
torch==2.5.1
transformers==4.44.2
sentencepiece==0.2.0
protobuf==5.29.3
accelerate==1.2.1
```

### Test BGE reranker

From host:

```bash
curl http://localhost:8081/health
```

Rerank test:

```bash
curl http://localhost:8081/rerank \
  -H "Content-Type: application/json" \
  -d '{
    "query": "What organization publishes Cartographic Perspectives?",
    "texts": [
      "Cartographic Perspectives is the journal of the North American Cartographic Information Society.",
      "Hillshading renders a surface with a three-dimensional appearance."
    ],
    "top_n": 2
  }'
```

From the API container:

```bash
docker compose exec api sh -lc 'wget -qO- http://bge-reranker:8080/health'
```

Expected API logs during search:

```text
rag qdrant retrieval ... hits=40 require_context=true hybrid=true llm_enabled=true
bge-reranker ... "POST /rerank HTTP/1.1" 200 OK
```

---

## 14. Qdrant vector store

Check collection:

```bash
curl http://localhost:6333/collections/rag
```

Count points:

```bash
curl http://localhost:6333/collections/rag/points/count \
  -H "Content-Type: application/json" \
  -d '{}'
```

Scroll payloads:

```bash
curl http://localhost:6333/collections/rag/points/scroll \
  -H "Content-Type: application/json" \
  -d '{
    "limit": 5,
    "with_payload": true,
    "with_vector": false
  }'
```

Expected payload fields include:

```json
{
  "document_id": "...",
  "source_checksum": "...",
  "ingest_scope": "...",
  "source_kind": "pdf_text|rendered_page_image|image|text_file",
  "filename": "...",
  "path": "...",
  "original_path": "...",
  "manifest_path": "...",
  "image_path": "...",
  "page": 1,
  "chunk_index": 0,
  "text": "..."
}
```

### Vector size warning

Qdrant collections have fixed vector size. If you change `VECTOR_SIZE`, delete and recreate the collection before reingestion:

```bash
curl -X DELETE http://localhost:6333/collections/rag
```

Then restart and re-upload/re-ingest documents.

---

## 15. LLM behavior

The LLM is used only after retrieval and reranking.

The answer prompt is designed to:

- answer the exact user question;
- use only retrieved Qdrant context;
- avoid generic summaries;
- avoid saying “you provided pages”; 
- cite source labels like `[S1]`, `[S2]`;
- say when retrieved context is insufficient.

If Ollama is down, the Search page should still show retrieval diagnostics and may show an LLM error in the answer panel.

Common Ollama error:

```text
lookup ollama on 127.0.0.11:53: server misbehaving
```

Fix:

```bash
docker compose --profile llm up -d ollama
docker compose exec ollama ollama pull llama3.1:8b
```

---

## 16. Object storage

Object storage is optional. Local cache remains required for OCR/viewing/editing.

Environment variables:

```yaml
OBJECT_STORAGE_ENABLED: "true"
OBJECT_STORAGE_REQUIRED: "true"
OBJECT_STORAGE_PROVIDER: oci-par
OBJECT_STORAGE_PAR_URL: "https://objectstorage.../p/<par>/n/<namespace>/b/<bucket>/o"
OBJECT_STORAGE_PREFIX: jarvis/dev
OBJECT_STORAGE_PUBLIC_BASE_URL: ""
OBJECT_STORAGE_TIMEOUT_SECONDS: "120"
```

When enabled, jobs and Qdrant payloads may include:

```text
object_status
object_key
object_url
object_error
```

Recommended behavior:

- `OBJECT_STORAGE_REQUIRED=false` for development.
- `OBJECT_STORAGE_REQUIRED=true` for controlled deployments where object persistence is mandatory.

---

## 17. Abbreviations

Built-in abbreviation expansion improves retrieval for domain acronyms.

To override or extend:

```bash
cp data/abbreviations.example.json data/abbreviations.json
```

Example:

```json
{
  "SMRT": "Singapore Mass Rapid Transit",
  "UAT": "User Acceptance Testing",
  "SIT": "System Integration Testing",
  "S&C": "Signal and Communication",
  "UC1B": "Use Case 1B",
  "WI": "Work Instruction"
}
```

---

## 18. API endpoints

Common endpoints:

```text
GET  /api/health
POST /api/ingest
GET  /api/jobs
GET  /api/job?id=<job_id>
POST /api/job/retry
POST /api/query
GET  /api/document
GET  /api/image
POST /api/edit/save
POST /api/reingest/current-page
POST /api/reingest/edited-pages
POST /api/reingest/full-image-set
GET  /api/ws
```

Names may vary slightly by build. Inspect `internal/httpapi/handlers.go` for exact route registration.

---

## 19. Smoke tests

### Upload test

```bash
printf 'hello ingestion test\n' > /tmp/rag-test.txt
curl -i -F "file=@/tmp/rag-test.txt" http://localhost:8090/api/ingest
curl http://localhost:8090/api/jobs
```

### Query test

After ingesting a document:

```bash
curl -s http://localhost:8090/api/query \
  -H "Content-Type: application/json" \
  -d '{"question":"What organization publishes Cartographic Perspectives?"}'
```

### BGE reranker test

```bash
curl http://localhost:8081/rerank \
  -H "Content-Type: application/json" \
  -d '{
    "query": "What organization publishes Cartographic Perspectives?",
    "texts": [
      "Cartographic Perspectives is the journal of the North American Cartographic Information Society.",
      "Unrelated hillshading text."
    ],
    "top_n": 2
  }'
```

### Ollama test

```bash
curl http://localhost:11434/api/tags
```

### Qdrant count test

```bash
curl http://localhost:6333/collections/rag/points/count \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

## 20. Troubleshooting

### Upload stays on “Uploading...”

Check browser DevTools → Network → `POST /api/ingest`.

Likely causes:

- Nginx `proxy_pass` has a trailing slash and rewrites the path.
- `client_max_body_size` too small.
- `proxy_request_buffering` not disabled.
- API crashed during save.
- Host `./data` permissions issue.

Fix permissions for debugging:

```bash
mkdir -p data
chmod -R 777 data
```

### PDF upload works but ingestion is too fast

Check whether the job ingested:

- original PDF text;
- one edited page image;
- edited pages only;
- full document image set.

Jobs should show source type, pages processed, characters extracted, chunks, embeddings, and Qdrant upserts.

### Search page shows LLM error but retrieval is working

If Search shows Qdrant hits and reranked count, retrieval is healthy.

Fix Ollama:

```bash
docker compose --profile llm up -d ollama
docker compose exec ollama ollama pull llama3.1:8b
```

Or disable LLM:

```yaml
LLM_ENABLED: "false"
```

### External BGE reranker DNS error

Error:

```text
lookup bge-reranker on 127.0.0.11:53: server misbehaving
```

Meaning the API cannot resolve the reranker service.

Check:

```bash
docker compose ps
docker compose exec api sh -lc 'wget -qO- http://bge-reranker:8080/health'
```

Make sure `bge-reranker` is defined and running.

### BGE dependency conflict during Docker build

Error:

```text
FlagEmbedding==1.3.3 depends on transformers==4.44.2
```

Use:

```text
transformers==4.44.2
```

not `4.48.0`.

### Qdrant response unmarshal error

Newer Qdrant `/points/query` returns:

```json
{
  "result": {
    "points": []
  }
}
```

Older/search-style APIs may return:

```json
{
  "result": []
}
```

The Qdrant client should support both shapes.

### Search answer is generic or wrong

Check:

- Qdrant hits > 0.
- BGE reranker is returning `200 OK`.
- Evidence panel sources actually support the answer.
- `RAG_REQUIRE_CONTEXT=true`.
- `LLM_REQUIRE_CONTEXT=true` or equivalent config.
- Prompt instructs the model to answer only the question, not summarize pages.

---

## 21. Improving retrieval accuracy

For a practical >90% retrieval target, measure against a gold evaluation set.

Recommended metrics:

```text
Hit@5 >= 90%
MRR >= 0.75
Citation accuracy >= 90%
No-context refusal = 100%
```

Recommended stack:

```text
layout-aware parsing
+ full rendered page OCR
+ section/page/figure metadata
+ stronger embeddings
+ hybrid retrieval
+ BGE reranking
+ MMR deduplication
+ strict context grounding
+ eval harness
```

Current state:

| Capability | Status |
|---|---|
| Better document parsing | Partial |
| Better embeddings | Partial, local hashed embeddings |
| Hybrid retrieval | Yes |
| BGE reranking | Yes, optional external service |
| Good metadata | Yes |
| Visual-page ingestion | Yes |
| Real evaluation set | Not yet included |

---

## 22. Development commands

Build API:

```bash
go build -o /tmp/rag_api ./cmd/api
```

Run tests:

```bash
go test ./...
```

Check frontend syntax:

```bash
node --check web/app.js
```

Rebuild containers:

```bash
docker compose down
docker compose up --build --force-recreate
```

Rebuild only reranker:

```bash
docker compose build --no-cache bge-reranker
```

---

## 23. Operational checklist

Before demo or UAT:

- [ ] `docker compose ps` shows `api`, `web`, `qdrant`, and `bge-reranker` running.
- [ ] Ollama running if LLM is enabled.
- [ ] `curl http://localhost:8081/health` returns reranker health.
- [ ] `curl http://localhost:11434/api/tags` returns model list if LLM enabled.
- [ ] Qdrant collection exists.
- [ ] Uploaded file appears in `./data`.
- [ ] Job reaches succeeded state.
- [ ] Jobs show pages/chunks/embeddings/upserts.
- [ ] Search evidence panel shows Qdrant sources.
- [ ] Answer cites source labels.
- [ ] Viewer opens the uploaded source document only after user clicks Open Viewer / Editor.
- [ ] Edited page sidecar saves successfully.
- [ ] Full image-set reingest uses edited page image where applicable.

---

## 24. Security and production notes

This is a POC-oriented build. Before production hardening:

- Add authentication and authorization.
- Add per-user/workspace isolation.
- Add persistent job store instead of only in-memory state.
- Add object storage lifecycle policies.
- Add malware scanning for uploads.
- Add file type validation and size quotas.
- Add audit logs for uploads, edits, reingestion, and queries.
- Add secrets management instead of plain environment variables.
- Add TLS termination.
- Add healthchecks and readiness probes.
- Add eval harness and regression gates for retrieval quality.

---

## 25. Known limitations

- Local hashed embeddings are useful for POC but are not equivalent to BGE/E5/OpenAI embeddings.
- Full layout-aware table/figure extraction is not complete.
- Evaluation harness is not yet included.
- In-memory job repository is not durable across API restarts.
- OCR quality depends on Tesseract and rendered page quality.
- CPU BGE reranking is accurate but can be slow for large candidate pools.
- LLM answer quality depends on the configured model and prompt adherence.

---

## 26. Recommended next improvements

1. Add real embedding provider support for BGE/E5/OpenAI-compatible embeddings.
2. Add evaluation harness for Hit@K, MRR, citation accuracy, and no-context refusal.
3. Add layout-aware parsing for headings, tables, figure captions, and page regions.
4. Add persistent database for jobs/documents.
5. Add authentication and workspace isolation.
6. Add admin screen for collection rebuild, reingestion, and eval runs.
7. Add downloadable retrieval report for each answer.

