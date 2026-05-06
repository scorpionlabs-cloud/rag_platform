package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rag-unified-realtime/internal/ingestion"
	"rag-unified-realtime/internal/logging"
	"rag-unified-realtime/internal/model"
	"rag-unified-realtime/internal/repository"
	"rag-unified-realtime/internal/search"
	"rag-unified-realtime/internal/storage"
)

const maxUploadBytes int64 = 512 << 20

type Handler struct {
	repo           *repository.MemoryRepository
	ingest         *ingestion.Service
	search         *search.Service
	dataDir        string
	log            *logging.Logger
	hub            *Hub
	objects        storage.Store
	objectRequired bool
}

func NewHandler(repo *repository.MemoryRepository, ingest *ingestion.Service, search *search.Service, dataDir string, log *logging.Logger, hub *Hub, objects storage.Store, objectRequired bool) *Handler {
	ingest.SetBroadcaster(hub)
	if objects == nil {
		objects = storage.DisabledStore{}
	}
	return &Handler{repo: repo, ingest: ingest, search: search, dataDir: dataDir, log: log, hub: hub, objects: objects, objectRequired: objectRequired}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/health", h.health)
	mux.HandleFunc("/api/ws", h.ws)
	mux.HandleFunc("/api/ingest", h.ingestFile)
	mux.HandleFunc("/api/jobs", h.jobs)
	mux.HandleFunc("/api/job", h.job)
	mux.HandleFunc("/api/image", h.image)
	mux.HandleFunc("/api/image/update", h.updateImage)
	mux.HandleFunc("/api/document", h.document)
	mux.HandleFunc("/api/document/page", h.documentPage)
	mux.HandleFunc("/api/document/page/update", h.updateDocumentPage)
	mux.HandleFunc("/api/reingest", h.reingest)
	mux.HandleFunc("/api/retry", h.retry)
	mux.HandleFunc("/api/query", h.query)
	mux.HandleFunc("/api/feedback", h.feedback)
	mux.HandleFunc("/api/metrics", h.metrics)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) ws(w http.ResponseWriter, r *http.Request) { h.hub.HandleWS(w, r) }

func (h *Handler) ingestFile(w http.ResponseWriter, r *http.Request) {
	uploadStarted := time.Now()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	if err := os.MkdirAll(h.dataDir, 0755); err != nil {
		http.Error(w, "data directory error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file field required: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	jobID := strings.TrimSpace(r.FormValue("job_id"))
	if jobID == "" {
		jobID = time.Now().Format("20060102150405.000000000")
	}

	originalName := sanitizeFilename(header.Filename)
	baseName := time.Now().Format("20060102150405.000000000") + "_" + originalName
	sourcePath := filepath.Join(h.dataDir, baseName)
	tmpPath := sourcePath + ".uploading"

	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		http.Error(w, "create upload file failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	written, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		http.Error(w, "save upload failed: "+copyErr.Error(), http.StatusInternalServerError)
		return
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		http.Error(w, "save upload failed: "+closeErr.Error(), http.StatusInternalServerError)
		return
	}
	if written == 0 {
		_ = os.Remove(tmpPath)
		http.Error(w, "uploaded file is empty", http.StatusBadRequest)
		return
	}
	if err := os.Rename(tmpPath, sourcePath); err != nil {
		_ = os.Remove(tmpPath)
		http.Error(w, "finalize upload failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	objectKey, objectURL, objectStatus, objectError, storeErr := h.storeFile(r.Context(), sourcePath)
	if storeErr != nil {
		if h.log != nil {
			h.log.Error("object storage upload failed", "path", sourcePath, "key", objectKey, "error", storeErr.Error())
		}
		if h.objectRequired {
			_ = os.Remove(sourcePath)
			http.Error(w, "object storage upload failed: "+storeErr.Error(), http.StatusBadGateway)
			return
		}
	}

	uploadDurationMs := time.Since(uploadStarted).Milliseconds()
	ingestTarget := sourcePath
	sourceKind := sourceKindForPath(sourcePath)
	stage := "queued"
	pipelineNote := ""

	if wantsFullImageIngest(r) {
		if documentKind(sourcePath) != "pdf" {
			_ = os.Remove(sourcePath)
			http.Error(w, "full image rendering ingestion during upload is only supported for PDFs", http.StatusUnsupportedMediaType)
			return
		}
		manifestPath, err := h.buildPDFImageListManifest(r.Context(), sourcePath, "full document image set", true)
		if err != nil {
			http.Error(w, "unable to render PDF page images for ingestion: "+err.Error(), http.StatusInternalServerError)
			return
		}
		ingestTarget = manifestPath
		sourceKind = "full document image set"
		stage = "queued full document image set from upload"
		pipelineNote = "upload_mode=full_image_set source=" + sourcePath
	}

	now := time.Now()
	job := model.Job{
		ID:               jobID,
		Filename:         originalName,
		Path:             ingestTarget,
		Status:           model.JobQueued,
		Stage:            stage,
		Progress:         0,
		Attempts:         0,
		MaxAttempts:      3,
		UploadBytes:      written,
		UploadDurationMs: uploadDurationMs,
		SourceKind:       sourceKind,
		CreatedAt:        uploadStarted,
		TotalDurationMs:  now.Sub(uploadStarted).Milliseconds(),
		PipelineNote:     pipelineNote,
		ObjectKey:        objectKey,
		ObjectURL:        objectURL,
		ObjectStatus:     objectStatus,
		ObjectError:      objectError,
	}

	if h.log != nil {
		h.log.Info("upload saved", "job_id", job.ID, "file", job.Filename, "path", sourcePath, "ingest_target", ingestTarget, "source_kind", sourceKind, "bytes", written, "upload_ms", uploadDurationMs)
	}
	h.ingest.Enqueue(job)
	writeJSONStatus(w, http.StatusAccepted, job)
}

func wantsFullImageIngest(r *http.Request) bool {
	mode := strings.ToLower(strings.TrimSpace(r.FormValue("ingest_mode")))
	if mode == "full_image_set" || mode == "full-images" || mode == "full" {
		return true
	}
	flag := strings.ToLower(strings.TrimSpace(r.FormValue("full_image_ingest")))
	return flag == "1" || flag == "true" || flag == "yes" || flag == "on"
}

func (h *Handler) jobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.repo.Jobs())
}

func (h *Handler) job(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("job_id"))
	if id == "" {
		http.Error(w, "job_id required", http.StatusBadRequest)
		return
	}
	job, ok := h.repo.GetJob(id)
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	writeJSON(w, job)
}

func (h *Handler) image(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if !h.safePath(path) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	if strings.EqualFold(filepath.Ext(path), ".pdf") {
		h.servePDFPage(w, r, path, 1)
		return
	}
	if documentKind(path) == "image" {
		h.serveImagePage(w, r, path)
		return
	}

	http.ServeFile(w, r, path)
}

func (h *Handler) updateImage(w http.ResponseWriter, r *http.Request) {
	h.updateDocumentPage(w, r)
}

func (h *Handler) document(w http.ResponseWriter, r *http.Request) {
	requestedPath := r.URL.Query().Get("path")
	path := requestedPath
	if !h.safePath(path) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if !fileExists(path) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// Image-list manifests are ingestion inputs, not user-viewable documents.
	// When a full/edited image-set re-ingestion job is opened from Jobs, resolve
	// the manifest back to the original source document so the viewer/editor stays
	// on the editable PDF/image instead of showing the JSON manifest.
	openedFromManifest := false
	if isImageListManifest(path) {
		sourcePath, err := readImageListSourcePath(path)
		if err != nil {
			http.Error(w, "unable to inspect image-list manifest: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if !h.safePath(sourcePath) {
			http.Error(w, "invalid source path in image-list manifest", http.StatusBadRequest)
			return
		}
		if !fileExists(sourcePath) {
			http.Error(w, "source document from image-list manifest not found", http.StatusNotFound)
			return
		}
		path = sourcePath
		openedFromManifest = true
	}

	kind := documentKind(path)
	pages := 1
	if kind == "pdf" {
		n, err := h.pdfPageCount(path)
		if err != nil {
			http.Error(w, "unable to inspect pdf: "+err.Error(), http.StatusInternalServerError)
			return
		}
		pages = n
	}

	writeJSON(w, map[string]interface{}{
		"path":                 path,
		"requested_path":       requestedPath,
		"opened_from_manifest": openedFromManifest,
		"filename":             filepath.Base(path),
		"kind":                 kind,
		"pages":                pages,
		"editable":             kind == "pdf" || kind == "image",
	})
}

func (h *Handler) documentPage(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if !h.safePath(path) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	switch documentKind(path) {
	case "pdf":
		h.servePDFPage(w, r, path, page)
	case "image":
		h.serveImagePage(w, r, path)
	default:
		http.Error(w, "document preview is only available for PDFs and images", http.StatusUnsupportedMediaType)
	}
}

func (h *Handler) updateDocumentPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Query().Get("path")
	if !h.safePath(path) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUploadBytes))
	if readErr != nil {
		http.Error(w, "read edited image failed: "+readErr.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	data, err := decodeImagePayload(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	target := path
	switch documentKind(path) {
	case "pdf":
		target = pdfEditedPagePath(path, page)
	case "image":
		target = imageEditedPath(path)
	default:
		http.Error(w, "document editing is only supported for PDFs and images", http.StatusUnsupportedMediaType)
		return
	}
	if !h.safePath(target) {
		http.Error(w, "invalid edit path", http.StatusBadRequest)
		return
	}
	if err := os.WriteFile(target, data, 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	objectKey, objectURL, objectStatus, _, storeErr := h.storeFile(r.Context(), target)
	if storeErr != nil {
		if h.log != nil {
			h.log.Error("object storage edited image upload failed", "path", target, "key", objectKey, "error", storeErr.Error())
		}
		if h.objectRequired {
			http.Error(w, "object storage upload failed: "+storeErr.Error(), http.StatusBadGateway)
			return
		}
	}
	writeJSON(w, map[string]string{"status": "updated", "path": target, "object_key": objectKey, "object_url": objectURL, "object_status": objectStatus})
}

func (h *Handler) reingest(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if !h.safePath(path) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = "page"
	}

	target := path
	sourceKind := ""
	stage := "queued re-ingest"
	switch documentKind(path) {
	case "pdf":
		var err error
		switch mode {
		case "page", "current", "selected":
			target, err = h.pdfPageImageForReingest(path, page)
			sourceKind = "selected page image"
			stage = fmt.Sprintf("queued page image %d", page)
		case "edited", "edited-pages", "selected-edits":
			target, err = h.buildPDFImageListManifest(r.Context(), path, "edited pages image set", false)
			sourceKind = "edited pages image set"
			stage = "queued edited pages image set"
		case "full", "full-images", "full-image-set":
			target, err = h.buildPDFImageListManifest(r.Context(), path, "full document image set", true)
			sourceKind = "full document image set"
			stage = "queued full document image set"
		default:
			http.Error(w, "unsupported re-ingest mode: use page, edited, or full", http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, "unable to prepare pdf images for re-ingestion: "+err.Error(), http.StatusInternalServerError)
			return
		}
	case "image":
		target = imageForReingest(path)
		sourceKind = sourceKindForPath(target)
		stage = "queued image re-ingest"
	default:
		http.Error(w, "re-ingestion is only supported for PDF page images and image files", http.StatusUnsupportedMediaType)
		return
	}
	if !h.safePath(target) {
		http.Error(w, "invalid re-ingest target", http.StatusBadRequest)
		return
	}

	size := reingestTargetSize(target)
	objectKey, objectURL, objectStatus, objectError, storeErr := h.storeFile(r.Context(), target)
	if storeErr != nil {
		if h.log != nil {
			h.log.Error("object storage reingest target upload failed", "path", target, "key", objectKey, "error", storeErr.Error())
		}
		if h.objectRequired {
			http.Error(w, "object storage upload failed: "+storeErr.Error(), http.StatusBadGateway)
			return
		}
	}
	jobFilename := filepath.Base(target)
	if documentKind(path) == "pdf" {
		jobFilename = filepath.Base(path)
	}
	now := time.Now()
	job := model.Job{
		ID:           now.Format("20060102150405.000000000"),
		Filename:     jobFilename,
		Path:         target,
		Status:       model.JobQueued,
		Stage:        stage,
		Progress:     0,
		Attempts:     0,
		MaxAttempts:  3,
		UploadBytes:  size,
		SourceKind:   sourceKind,
		CreatedAt:    now,
		ObjectKey:    objectKey,
		ObjectURL:    objectURL,
		ObjectStatus: objectStatus,
		ObjectError:  objectError,
	}
	h.ingest.Enqueue(job)
	writeJSON(w, job)
}

type imageListPage struct {
	Page      int    `json:"page"`
	Path      string `json:"path"`
	Edited    bool   `json:"edited,omitempty"`
	ObjectKey string `json:"object_key,omitempty"`
	ObjectURL string `json:"object_url,omitempty"`
}

type imageListManifest struct {
	SourcePath string          `json:"source_path"`
	Mode       string          `json:"mode"`
	Pages      []imageListPage `json:"pages"`
}

func (h *Handler) pdfPageImageForReingest(pdfPath string, page int) (string, error) {
	if page < 1 {
		page = 1
	}
	edited := pdfEditedPagePath(pdfPath, page)
	if fileExists(edited) {
		return edited, nil
	}
	return h.renderPDFPage(pdfPath, page)
}

func (h *Handler) buildPDFImageListManifest(ctx context.Context, pdfPath string, mode string, includeUnedited bool) (string, error) {
	pageCount, err := h.pdfPageCount(pdfPath)
	if err != nil {
		return "", err
	}
	manifest := imageListManifest{
		SourcePath: pdfPath,
		Mode:       mode,
		Pages:      make([]imageListPage, 0, pageCount),
	}
	for page := 1; page <= pageCount; page++ {
		editedPath := pdfEditedPagePath(pdfPath, page)
		if fileExists(editedPath) {
			item := imageListPage{Page: page, Path: editedPath, Edited: true}
			item.ObjectKey, item.ObjectURL, _, _, _ = h.storeFile(ctx, editedPath)
			manifest.Pages = append(manifest.Pages, item)
			continue
		}
		if !includeUnedited {
			continue
		}
		rendered, err := h.renderPDFPage(pdfPath, page)
		if err != nil {
			return "", fmt.Errorf("render page %d: %w", page, err)
		}
		item := imageListPage{Page: page, Path: rendered}
		item.ObjectKey, item.ObjectURL, _, _, _ = h.storeFile(ctx, rendered)
		manifest.Pages = append(manifest.Pages, item)
	}
	if len(manifest.Pages) == 0 {
		return "", fmt.Errorf("no edited pages exist; save edits first or use full document image re-ingest")
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	base := strings.TrimSuffix(pdfPath, filepath.Ext(pdfPath))
	safeMode := strings.NewReplacer(" ", "_", "/", "_", "\\", "_").Replace(strings.ToLower(mode))
	manifestPath := fmt.Sprintf("%s.%s.%s.imagelist.json", base, safeMode, time.Now().Format("20060102150405.000000000"))
	if !h.safePath(manifestPath) {
		return "", fmt.Errorf("invalid manifest path")
	}
	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		return "", err
	}
	_, _, _, _, storeErr := h.storeFile(ctx, manifestPath)
	if storeErr != nil && h.objectRequired {
		return "", storeErr
	}
	return manifestPath, nil
}

func (h *Handler) retry(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("job_id")
	if id == "" {
		http.Error(w, "job_id required", http.StatusBadRequest)
		return
	}
	if !h.ingest.Retry(id) {
		http.Error(w, "unable to retry job", http.StatusConflict)
		return
	}
	writeJSON(w, map[string]string{"status": "retrying"})
}

func (h *Handler) query(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	writeJSON(w, h.search.Query(r.Context(), req.Query))
}

func (h *Handler) feedback(w http.ResponseWriter, r *http.Request) {
	var f model.Feedback
	_ = json.NewDecoder(r.Body).Decode(&f)
	h.repo.SaveFeedback(f)
	writeJSON(w, map[string]string{"status": "saved"})
}

func (h *Handler) metrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]int{"chunks": h.repo.CountChunks(), "jobs": len(h.repo.Jobs())})
}

func (h *Handler) storeFile(ctx context.Context, filePath string) (objectKey, objectURL, objectStatus, objectError string, err error) {
	objectStatus = "disabled"
	if h.objects == nil || !h.objects.Enabled() {
		if h.objectRequired {
			err := fmt.Errorf("object storage is required but not configured")
			return "", "", "failed", err.Error(), err
		}
		return "", "", objectStatus, "", nil
	}
	objectKey = storage.ObjectKeyForPath(h.dataDir, filePath)
	obj, err := h.objects.PutFile(ctx, objectKey, filePath)
	if err != nil {
		return objectKey, "", "failed", err.Error(), err
	}
	return obj.Key, obj.URL, "stored", "", nil
}

func (h *Handler) safePath(path string) bool {
	if path == "" {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absDataDir, err := filepath.Abs(h.dataDir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absDataDir, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (h *Handler) servePDFPage(w http.ResponseWriter, r *http.Request, pdfPath string, page int) {
	if page < 1 {
		page = 1
	}
	if edited := pdfEditedPagePath(pdfPath, page); fileExists(edited) {
		http.ServeFile(w, r, edited)
		return
	}
	rendered, err := h.renderPDFPage(pdfPath, page)
	if err != nil {
		http.Error(w, "unable to render pdf page: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.ServeFile(w, r, rendered)
}

func (h *Handler) serveImagePage(w http.ResponseWriter, r *http.Request, imagePath string) {
	if edited := imageEditedPath(imagePath); fileExists(edited) {
		http.ServeFile(w, r, edited)
		return
	}
	http.ServeFile(w, r, imagePath)
}

func imageForReingest(imagePath string) string {
	if edited := imageEditedPath(imagePath); fileExists(edited) {
		return edited
	}
	return imagePath
}

func (h *Handler) renderPDFPage(pdfPath string, page int) (string, error) {
	if page < 1 {
		page = 1
	}
	renderedPath := pdfRenderedPagePath(pdfPath, page)
	if fileExists(renderedPath) {
		return renderedPath, nil
	}

	prefix := strings.TrimSuffix(renderedPath, ".png")
	cmd := exec.Command("pdftoppm", "-r", "140", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", "-png", pdfPath, prefix)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftoppm: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if !fileExists(renderedPath) {
		return "", fmt.Errorf("page image not created")
	}
	_, _, _, _, storeErr := h.storeFile(context.Background(), renderedPath)
	if storeErr != nil && h.objectRequired {
		return "", storeErr
	}
	return renderedPath, nil
}

func (h *Handler) pdfPageCount(pdfPath string) (int, error) {
	out, err := exec.Command("pdfinfo", pdfPath).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("pdfinfo: %w: %s", err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != "Pages" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err == nil && n > 0 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("page count not found")
}

func pdfRenderedPagePath(pdfPath string, page int) string {
	base := strings.TrimSuffix(pdfPath, filepath.Ext(pdfPath))
	return fmt.Sprintf("%s.page_%04d.render.png", base, page)
}

func pdfEditedPagePath(pdfPath string, page int) string {
	base := strings.TrimSuffix(pdfPath, filepath.Ext(pdfPath))
	return fmt.Sprintf("%s.page_%04d.edited.png", base, page)
}

func imageEditedPath(imagePath string) string {
	base := strings.TrimSuffix(imagePath, filepath.Ext(imagePath))
	return base + ".edited.png"
}

func documentKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		return "pdf"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff":
		return "image"
	default:
		return "file"
	}
}

func isImageListManifest(path string) bool {
	return strings.HasSuffix(strings.ToLower(filepath.Base(path)), ".imagelist.json")
}

func readImageListSourcePath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var manifest imageListManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	sourcePath := strings.TrimSpace(manifest.SourcePath)
	if sourcePath == "" {
		return "", fmt.Errorf("source_path missing")
	}
	return sourcePath, nil
}

func reingestTargetSize(path string) int64 {
	if isImageListManifest(path) {
		data, err := os.ReadFile(path)
		if err == nil {
			var manifest imageListManifest
			if json.Unmarshal(data, &manifest) == nil {
				var total int64
				for _, page := range manifest.Pages {
					if stat, err := os.Stat(page.Path); err == nil {
						total += stat.Size()
					}
				}
				if total > 0 {
					return total
				}
			}
		}
	}
	if stat, err := os.Stat(path); err == nil {
		return stat.Size()
	}
	return 0
}

func sourceKindForPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(base, ".imagelist.json") {
		return "image page set"
	}
	if strings.Contains(base, ".page_") && strings.Contains(base, ".edited.") {
		return "edited page image"
	}
	if strings.HasSuffix(base, ".edited.png") {
		return "edited image"
	}
	switch documentKind(path) {
	case "pdf":
		return "original pdf"
	case "image":
		return "image"
	default:
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".txt", ".md", ".csv", ".json", ".log":
			return "text file"
		default:
			return strings.TrimPrefix(ext, ".")
		}
	}
}

func parsePositiveInt(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

func decodeImagePayload(body []byte) ([]byte, error) {
	text := strings.TrimSpace(string(body))
	if strings.HasPrefix(text, "data:image") {
		parts := strings.SplitN(text, ",", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid image payload")
		}
		decoded, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("invalid base64 image payload")
		}
		return decoded, nil
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("empty image payload")
	}
	return body, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "upload"
	}
	return name
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func WithCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Cache-Control, X-Requested-With")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
