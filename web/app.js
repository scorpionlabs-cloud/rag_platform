const API = (window.RAG_API_BASE || "/api").replace(/\/$/, "")
let currentPath = ""
let currentJobId = ""
let tool = "text"
let originalImage = null
let eventSource = null
let lastResults = []
let activeUploadRequest = null
let activeUploadStartedAt = 0
let uploadElapsedTimer = null
let jobPollTimer = null
let pageDirty = false
let editorObjects = []
let selectedObjectId = ""
let dragState = null
let documentState = {
  path: "",
  filename: "",
  kind: "",
  pages: 1,
  page: 1,
  rotation: 0,
  zoom: 1,
}

const canvas = document.getElementById("canvas")
const ctx = canvas.getContext("2d")

function cssEscapeValue(value) {
  if (window.CSS && typeof window.CSS.escape === "function") return CSS.escape(value)
  return String(value || "").replace(/[^a-zA-Z0-9_-]/g, "\\$&")
}

function showTab(id) {
  document.querySelectorAll(".tab").forEach(t => t.classList.add("hidden"))
  const tab = document.getElementById(id)
  if (tab) tab.classList.remove("hidden")
  if (id === "jobs") refreshJobs()
}

function setTool(t) {
  tool = t
  document.querySelectorAll(".tool-button").forEach(b => b.classList.remove("selected"))
  const active = document.querySelector(`[data-tool="${cssEscapeValue(t)}"]`)
  if (active) active.classList.add("selected")
}

function makeJobId() {
  if (window.crypto && typeof window.crypto.randomUUID === "function") {
    return window.crypto.randomUUID().replace(/-/g, "")
  }
  return `${Date.now()}_${Math.random().toString(16).slice(2)}`
}

function canvasPoint(e) {
  const rect = canvas.getBoundingClientRect()
  const scaleX = canvas.width / Math.max(1, rect.width)
  const scaleY = canvas.height / Math.max(1, rect.height)
  return {
    x: (e.clientX - rect.left) * scaleX,
    y: (e.clientY - rect.top) * scaleY,
  }
}

canvas.addEventListener("mousedown", (e) => {
  if (!originalImage) return
  const pt = imagePointFromEvent(e)

  if (tool === "text") {
    placeTextAt(pt.x, pt.y)
    return
  }

  if (tool === "select") {
    const hit = hitTestEditorObject(pt.x, pt.y)
    selectEditorObject(hit?.id || "")
    if (hit) {
      dragState = {
        mode: "move",
        id: hit.id,
        startX: pt.x,
        startY: pt.y,
        origX: hit.x,
        origY: hit.y,
      }
    }
    return
  }

  if (tool === "highlight" || tool === "whiteout") {
    const id = makeObjectId()
    const obj = {
      id,
      type: tool,
      x: pt.x,
      y: pt.y,
      w: 1,
      h: 1,
      color: tool === "highlight" ? "#fde047" : "#ffffff",
      alpha: tool === "highlight" ? 0.45 : 1,
    }
    editorObjects.push(obj)
    selectEditorObject(id)
    dragState = { mode: "resize-new", id, startX: pt.x, startY: pt.y }
    pageDirty = true
    redrawPage()
  }
})

canvas.addEventListener("mousemove", (e) => {
  if (!dragState || !originalImage) return
  const pt = imagePointFromEvent(e)
  const obj = editorObjects.find(o => o.id === dragState.id)
  if (!obj) return

  if (dragState.mode === "move") {
    obj.x = clamp(pt.x - dragState.startX + dragState.origX, 0, getImageWidth())
    obj.y = clamp(pt.y - dragState.startY + dragState.origY, 0, getImageHeight())
  } else if (dragState.mode === "resize-new") {
    obj.x = Math.min(dragState.startX, pt.x)
    obj.y = Math.min(dragState.startY, pt.y)
    obj.w = Math.abs(pt.x - dragState.startX)
    obj.h = Math.abs(pt.y - dragState.startY)
  }
  pageDirty = true
  redrawPage()
})

window.addEventListener("mouseup", () => {
  if (!dragState) return
  const obj = editorObjects.find(o => o.id === dragState.id)
  if (obj && (obj.type === "highlight" || obj.type === "whiteout") && (obj.w < 4 || obj.h < 4)) {
    editorObjects = editorObjects.filter(o => o.id !== obj.id)
    selectedObjectId = ""
  }
  dragState = null
  syncSelectedObjectControls()
  redrawPage()
})

window.addEventListener("keydown", (e) => {
  if ((e.key === "Delete" || e.key === "Backspace") && selectedObjectId && !isTypingTarget(e.target)) {
    e.preventDefault()
    deleteSelectedObject()
  }
})

function isTypingTarget(target) {
  const tag = String(target?.tagName || "").toLowerCase()
  return tag === "input" || tag === "textarea" || target?.isContentEditable
}

function makeObjectId() {
  return `obj_${Date.now()}_${Math.random().toString(16).slice(2)}`
}

function imagePointFromEvent(e) {
  const pt = canvasPoint(e)
  return canvasToImagePoint(pt.x, pt.y)
}

function canvasToImagePoint(cx, cy) {
  const rotation = normalizedRotation()
  const zoom = currentZoom()
  const iw = getImageWidth()
  const ih = getImageHeight()
  const targetW = (rotation === 90 || rotation === 270) ? ih * zoom : iw * zoom
  const targetH = (rotation === 90 || rotation === 270) ? iw * zoom : ih * zoom
  let x = 0
  let y = 0
  if (rotation === 90) {
    x = cy / zoom
    y = (targetW - cx) / zoom
  } else if (rotation === 180) {
    x = (targetW - cx) / zoom
    y = (targetH - cy) / zoom
  } else if (rotation === 270) {
    x = (targetH - cy) / zoom
    y = cx / zoom
  } else {
    x = cx / zoom
    y = cy / zoom
  }
  return { x: clamp(x, 0, iw), y: clamp(y, 0, ih) }
}

function getImageWidth() {
  return originalImage ? (originalImage.naturalWidth || originalImage.width || canvas.width) : canvas.width
}

function getImageHeight() {
  return originalImage ? (originalImage.naturalHeight || originalImage.height || canvas.height) : canvas.height
}

function normalizedRotation() {
  return ((documentState.rotation % 360) + 360) % 360
}

function currentZoom() {
  return Math.max(0.25, Math.min(3, documentState.zoom || 1))
}

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, Number(value) || 0))
}

function placeTextAt(x, y) {
  const input = document.getElementById("editText")
  const text = String(input?.value || "").trim()
  if (!text) {
    alert("Enter text to place on the page first")
    return
  }
  const displaySize = Math.max(10, Number(document.getElementById("textSize")?.value || 24))
  const size = Math.max(6, displaySize / currentZoom())
  const color = document.getElementById("textColor")?.value || "#111827"
  const maxWidth = Math.max(120, getImageWidth() - x - 24)

  ctx.save()
  ctx.font = `${size}px Arial`
  const lines = wrapTextLines(ctx, text, maxWidth)
  const lineHeight = Math.round(size * 1.25)
  const width = Math.max(...lines.map(line => ctx.measureText(line).width), 1)
  const height = Math.max(lineHeight, lines.length * lineHeight)
  ctx.restore()

  const obj = {
    id: makeObjectId(),
    type: "text",
    text,
    lines,
    x,
    y,
    w: width,
    h: height,
    size,
    color,
    lineHeight,
  }
  editorObjects.push(obj)
  selectEditorObject(obj.id)
  pageDirty = true
  redrawPage()
  updateViewerStatus(`Text added on page ${documentState.page}; save to persist it`)
}

function selectEditorObject(id) {
  selectedObjectId = id || ""
  syncSelectedObjectControls()
  redrawPage()
}

function selectedObject() {
  return editorObjects.find(o => o.id === selectedObjectId) || null
}

function hitTestEditorObject(x, y) {
  for (let i = editorObjects.length - 1; i >= 0; i--) {
    const o = editorObjects[i]
    if (x >= o.x && x <= o.x + Math.max(1, o.w) && y >= o.y && y <= o.y + Math.max(1, o.h)) return o
  }
  return null
}

function applySelectedTextEdit() {
  const obj = selectedObject()
  if (!obj || obj.type !== "text") {
    alert("Select a text overlay first")
    return
  }
  const text = String(document.getElementById("editText")?.value || "").trim()
  if (!text) {
    alert("Enter replacement text first")
    return
  }
  const displaySize = Math.max(10, Number(document.getElementById("textSize")?.value || Math.round(obj.size * currentZoom())))
  obj.size = Math.max(6, displaySize / currentZoom())
  obj.color = document.getElementById("textColor")?.value || obj.color || "#111827"
  obj.text = text
  ctx.save()
  ctx.font = `${obj.size}px Arial`
  obj.lines = wrapTextLines(ctx, text, Math.max(120, getImageWidth() - obj.x - 24))
  obj.lineHeight = Math.round(obj.size * 1.25)
  obj.w = Math.max(...obj.lines.map(line => ctx.measureText(line).width), 1)
  obj.h = Math.max(obj.lineHeight, obj.lines.length * obj.lineHeight)
  ctx.restore()
  pageDirty = true
  redrawPage()
  updateViewerStatus("Updated selected text overlay; save to persist it")
}

function deleteSelectedObject() {
  if (!selectedObjectId) return
  editorObjects = editorObjects.filter(o => o.id !== selectedObjectId)
  selectedObjectId = ""
  pageDirty = true
  syncSelectedObjectControls()
  redrawPage()
  updateViewerStatus("Deleted selected edit; save to persist the page image")
}

function syncSelectedObjectControls() {
  const obj = selectedObject()
  const selectedLabel = document.getElementById("selectedEditStatus")
  const editBtn = document.getElementById("applyTextEditBtn")
  const deleteBtn = document.getElementById("deleteEditBtn")
  if (selectedLabel) {
    selectedLabel.innerText = obj ? `Selected ${obj.type} overlay` : "No edit selected"
  }
  if (deleteBtn) deleteBtn.disabled = !obj
  if (editBtn) editBtn.disabled = !(obj && obj.type === "text")
  if (obj && obj.type === "text") {
    const input = document.getElementById("editText")
    const size = document.getElementById("textSize")
    const color = document.getElementById("textColor")
    if (input) input.value = obj.text || ""
    if (size) size.value = String(Math.round((obj.size || 24) * currentZoom()))
    if (color && obj.color) color.value = obj.color
  }
}

function wrapTextLines(context, text, maxWidth) {
  const paragraphs = text.split(/\n+/)
  const lines = []
  for (const paragraph of paragraphs) {
    const words = paragraph.trim().split(/\s+/).filter(Boolean)
    let line = ""
    for (const word of words) {
      const next = line ? `${line} ${word}` : word
      if (line && context.measureText(next).width > maxWidth) {
        lines.push(line)
        line = word
      } else {
        line = next
      }
    }
    if (line) lines.push(line)
  }
  return lines.length ? lines : [text]
}

function connectWS() {
  try {
    eventSource = new EventSource(`${API}/ws`)
    eventSource.onmessage = (msg) => {
      try {
        const ev = JSON.parse(msg.data)
        if (ev.type === "job_update") {
          updateWorkspaceStatus(ev)
          refreshJobsQuietly()
        }
      } catch (err) {
        console.warn("Bad job event", err)
      }
    }
    eventSource.onerror = () => {
      if (eventSource) eventSource.close()
      setTimeout(connectWS, 2000)
    }
  } catch (err) {
    console.warn("Job stream unavailable", err)
    setTimeout(connectWS, 2000)
  }
}
connectWS()

function setUploadProgress(percent, label) {
  const pct = Math.max(0, Math.min(100, Number(percent) || 0))
  const bar = document.getElementById("uploadProgress")
  const shell = bar?.parentElement
  const status = document.getElementById("uploadStatus")
  if (bar) bar.style.width = `${pct}%`
  if (shell) shell.setAttribute("aria-valuenow", String(Math.round(pct)))
  if (status && label !== undefined) status.innerText = label
}

function setUploadTiming(text) {
  const el = document.getElementById("uploadTiming")
  if (el) el.innerText = text || ""
}

function setIngestionProgress(percent, label) {
  const pct = Math.max(0, Math.min(100, Number(percent) || 0))
  const bar = document.getElementById("ingestionProgress")
  const shell = bar?.parentElement
  const status = document.getElementById("ingestionStatus")
  if (bar) bar.style.width = `${pct}%`
  if (shell) shell.setAttribute("aria-valuenow", String(Math.round(pct)))
  if (status && label !== undefined) status.innerText = label
}

function setIngestionTiming(text) {
  const el = document.getElementById("ingestionTiming")
  if (el) el.innerText = text || ""
}

function normalizeJobEvent(ev) {
  if (!ev) return null
  return {
    id: ev.job_id || ev.id || "",
    status: ev.status || "",
    stage: ev.stage || "",
    progress: typeof ev.progress === "number" ? ev.progress : Number(ev.progress || 0),
    error: ev.error || "",
    path: ev.path || "",
    filename: ev.filename || "",
    uploadBytes: Number(ev.upload_bytes || 0),
    uploadDurationMs: Number(ev.upload_duration_ms || 0),
    clientUploadDurationMs: Number(ev.client_upload_duration_ms || 0),
    ingestionDurationMs: Number(ev.ingestion_duration_ms || 0),
    totalDurationMs: Number(ev.total_duration_ms || 0),
    sourceKind: ev.source_kind || "",
    pagesDiscovered: Number(ev.pages_discovered || 0),
    pagesProcessed: Number(ev.pages_processed || 0),
    extractedChars: Number(ev.extracted_chars || 0),
    chunkCount: Number(ev.chunk_count || 0),
    embeddingCount: Number(ev.embedding_count || 0),
    vectorUpserted: Number(ev.vector_upserted || 0),
    extractDurationMs: Number(ev.extract_duration_ms || 0),
    chunkDurationMs: Number(ev.chunk_duration_ms || 0),
    embedDurationMs: Number(ev.embed_duration_ms || 0),
    upsertDurationMs: Number(ev.upsert_duration_ms || 0),
    pipelineNote: ev.pipeline_note || "",
    documentId: ev.document_id || "",
    sourceChecksum: ev.source_checksum || "",
    ingestScope: ev.ingest_scope || "",
  }
}

function updateWorkspaceStatus(ev) {
  const job = normalizeJobEvent(ev)
  if (!job || !job.id) return
  if (currentJobId && job.id !== currentJobId) return

  const parts = []
  if (job.status) parts.push(job.status)
  if (job.stage) parts.push(job.stage)
  parts.push(`${Math.round(job.progress)}%`)
  if (job.error) parts.push(job.error)
  setIngestionProgress(job.progress, parts.join(" · "))

  const timingParts = []
  if (job.uploadBytes) timingParts.push(`file ${formatBytes(job.uploadBytes)}`)
  if (job.uploadDurationMs) timingParts.push(`upload saved ${formatDuration(job.uploadDurationMs)}`)
  if (job.sourceKind) timingParts.push(`source ${job.sourceKind}`)
  if (job.ingestScope) timingParts.push(`scope ${job.ingestScope}`)
  if (job.documentId) timingParts.push(`doc ${shortHash(job.documentId)}`)
  if (job.sourceChecksum) timingParts.push(`checksum ${shortHash(job.sourceChecksum)}`)
  if (job.pagesDiscovered || job.pagesProcessed) timingParts.push(`pages ${job.pagesProcessed || 0}/${job.pagesDiscovered || job.pagesProcessed || 0}`)
  if (job.extractedChars) timingParts.push(`text ${job.extractedChars.toLocaleString()} chars`)
  if (job.chunkCount) timingParts.push(`chunks ${job.chunkCount}`)
  if (job.embeddingCount) timingParts.push(`embeddings ${job.embeddingCount}`)
  if (job.vectorUpserted) timingParts.push(`Qdrant upserts ${job.vectorUpserted}`)
  if (job.ingestionDurationMs) timingParts.push(`${isTerminalStatus(job.status) ? "ingested" : "ingesting"} ${formatDuration(job.ingestionDurationMs)}`)
  if (job.totalDurationMs) timingParts.push(`total ${formatDuration(job.totalDurationMs)}`)
  const stageParts = []
  if (job.extractDurationMs) stageParts.push(`extract ${formatDuration(job.extractDurationMs)}`)
  if (job.chunkDurationMs) stageParts.push(`chunk ${formatDuration(job.chunkDurationMs)}`)
  if (job.embedDurationMs) stageParts.push(`embed ${formatDuration(job.embedDurationMs)}`)
  if (job.upsertDurationMs) stageParts.push(`upsert ${formatDuration(job.upsertDurationMs)}`)
  if (stageParts.length) timingParts.push(stageParts.join(" / "))
  setIngestionTiming(timingParts.join(" · "))

  if (isTerminalStatus(job.status)) stopJobPolling()
}

function isTerminalStatus(status) {
  return ["succeeded", "failed"].includes(String(status || "").toLowerCase())
}

function setOpenCurrentViewerEnabled(enabled) {
  const btn = document.getElementById("openCurrentViewerBtn")
  if (btn) btn.disabled = !enabled
}

async function openCurrentDocumentInViewer() {
  if (!currentPath) {
    showTab("viewer")
    clearViewer("No uploaded document is ready to open yet.")
    return
  }
  await openDocument(currentPath, currentJobId)
}

function resetWorkspaceForJob(jobId, fileName) {
  currentJobId = jobId
  currentPath = ""
  setOpenCurrentViewerEnabled(false)
  clearViewer("Upload complete. Click Open Viewer / Editor when you want to view or edit the document.")
  setUploadProgress(0, `Ready to upload ${fileName || "file"}`)
  setUploadTiming("")
  setIngestionProgress(0, "Waiting for upload")
  setIngestionTiming("")
}

async function upload() {
  const input = document.getElementById("file")
  const uploadBtn = document.getElementById("uploadBtn")
  const file = input?.files?.[0]
  if (!file) {
    alert("Select a file")
    return
  }

  const jobId = makeJobId()
  resetWorkspaceForJob(jobId, file.name)
  startJobTracking(jobId)

  const fullImageIngestOnUpload = Boolean(document.getElementById("fullImageIngestOnUpload")?.checked)

  const fd = new FormData()
  fd.append("file", file)
  fd.append("job_id", jobId)
  if (fullImageIngestOnUpload) {
    fd.append("ingest_mode", "full_image_set")
    fd.append("full_image_ingest", "true")
  }

  if (activeUploadRequest) {
    try { activeUploadRequest.abort() } catch (_) {}
    activeUploadRequest = null
  }

  if (uploadBtn) uploadBtn.disabled = true

  try {
    const job = await sendUpload(fd, file)
    currentJobId = job.id || jobId
    currentPath = job.path || ""

    const uploadMs = Number(job.client_upload_duration_ms || job.upload_duration_ms || 0)
    setUploadProgress(100, `Upload complete in ${formatDuration(uploadMs)} · queued ${currentJobId}`)
    setUploadTiming(`browser upload ${formatDuration(job.client_upload_duration_ms || 0)} · server save ${formatDuration(job.upload_duration_ms || 0)} · ${formatBytes(job.upload_bytes || file.size)}`)
    updateWorkspaceStatus(job)
    startJobTracking(currentJobId)
    refreshJobs()
    setOpenCurrentViewerEnabled(Boolean(currentPath))
    if (currentPath) {
      const viewerStatus = document.getElementById("viewerStatus")
      if (viewerStatus) viewerStatus.innerText = "Upload complete. Click Open Viewer / Editor to load the document."
    }
  } catch (err) {
    stopJobPolling()
    stopUploadElapsedTimer()
    setUploadProgress(0, `Upload failed: ${err.message}`)
    setUploadTiming(activeUploadStartedAt ? `failed after ${formatDuration(performance.now() - activeUploadStartedAt)}` : "failed")
    setIngestionProgress(0, "Upload failed")
    setIngestionTiming("")
    console.error(err)
    alert(`Upload failed: ${err.message}`)
  } finally {
    if (uploadBtn) uploadBtn.disabled = false
  }
}

function sendUpload(formData, file) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    activeUploadRequest = xhr

    const url = `${API}/ingest`
    xhr.open("POST", url, true)
    xhr.setRequestHeader("X-Requested-With", "XMLHttpRequest")

    xhr.upload.onloadstart = () => {
      activeUploadStartedAt = performance.now()
      startUploadElapsedTimer(file)
      setUploadProgress(1, `Uploading ${file.name}...`)
      setUploadTiming(`elapsed 0s · ${formatBytes(file.size)}`)
    }
    xhr.upload.onprogress = (e) => {
      if (!e.lengthComputable) {
        setUploadProgress(5, `Uploading ${file.name}...`)
        return
      }
      const pct = Math.max(1, Math.min(99, (e.loaded / e.total) * 100))
      const elapsedMs = activeUploadStartedAt ? performance.now() - activeUploadStartedAt : 0
      setUploadProgress(pct, `Uploading ${file.name}... ${Math.round(pct)}%`)
      setUploadTiming(`elapsed ${formatDuration(elapsedMs)} · ${formatBytes(e.loaded)} / ${formatBytes(e.total)}`)
    }
    xhr.upload.onload = () => {
      const elapsedMs = activeUploadStartedAt ? performance.now() - activeUploadStartedAt : 0
      stopUploadElapsedTimer()
      const modeText = document.getElementById("fullImageIngestOnUpload")?.checked ? " · rendering PDF page images before queue" : ""
      setUploadProgress(100, `Upload sent in ${formatDuration(elapsedMs)} · waiting for server queue response${modeText}`)
      setUploadTiming(`browser upload ${formatDuration(elapsedMs)} · ${formatBytes(file.size)}`)
    }
    xhr.onload = () => {
      activeUploadRequest = null
      const text = xhr.responseText || ""
      const parsed = parseJSON(text)
      if (xhr.status < 200 || xhr.status >= 300) {
        stopUploadElapsedTimer()
        reject(new Error(extractUploadError(xhr, parsed, text)))
        return
      }
      if (!parsed || typeof parsed !== "object") {
        stopUploadElapsedTimer()
        reject(new Error(`Server returned ${xhr.status} but not JSON. Check that /api/ingest is proxied to the API container.`))
        return
      }
      stopUploadElapsedTimer()
      parsed.client_upload_duration_ms = activeUploadStartedAt ? Math.round(performance.now() - activeUploadStartedAt) : 0
      resolve(parsed)
    }
    xhr.onerror = () => {
      activeUploadRequest = null
      stopUploadElapsedTimer()
      reject(new Error(`Network error posting to ${url}`))
    }
    xhr.onabort = () => {
      activeUploadRequest = null
      stopUploadElapsedTimer()
      reject(new Error("Upload cancelled"))
    }
    xhr.ontimeout = () => {
      activeUploadRequest = null
      stopUploadElapsedTimer()
      reject(new Error("Upload timed out"))
    }
    xhr.send(formData)
  })
}

function startUploadElapsedTimer(file) {
  stopUploadElapsedTimer()
  uploadElapsedTimer = setInterval(() => {
    if (!activeUploadStartedAt) return
    setUploadTiming(`elapsed ${formatDuration(performance.now() - activeUploadStartedAt)} · ${formatBytes(file?.size || 0)}`)
  }, 500)
}

function stopUploadElapsedTimer() {
  if (uploadElapsedTimer) {
    clearInterval(uploadElapsedTimer)
    uploadElapsedTimer = null
  }
}

function parseJSON(text) {
  if (!text) return null
  try { return JSON.parse(text) } catch (_) { return null }
}

function extractUploadError(xhr, parsed, text) {
  if (parsed?.error) return parsed.error
  const clean = String(text || "").replace(/<[^>]*>/g, " ").replace(/\s+/g, " ").trim()
  if (clean) return `${xhr.status} ${xhr.statusText}: ${clean.slice(0, 300)}`
  return `${xhr.status} ${xhr.statusText || "Upload failed"}`
}

function startJobTracking(jobId) {
  if (!jobId) return
  currentJobId = jobId
  stopJobPolling()
  jobPollTimer = setInterval(async () => {
    try {
      const res = await fetch(`${API}/job?job_id=${encodeURIComponent(jobId)}&_=${Date.now()}`, { cache: "no-store" })
      if (res.status === 404) return
      if (!res.ok) return
      const job = await res.json()
      updateWorkspaceStatus(job)
      refreshJobsQuietly()
      if (isTerminalStatus(job.status)) stopJobPolling()
    } catch (_) {
      // SSE is the primary live channel; polling is a fallback.
    }
  }, 1000)
}

function stopJobPolling() {
  if (jobPollTimer) {
    clearInterval(jobPollTimer)
    jobPollTimer = null
  }
}

function clearViewer(message = "No document loaded.") {
  originalImage = null
  editorObjects = []
  selectedObjectId = ""
  dragState = null
  pageDirty = false
  documentState = { path: "", requestedPath: "", openedFromManifest: false, filename: "", kind: "", pages: 1, page: 1, rotation: 0, zoom: 1 }
  canvas.width = 900
  canvas.height = 620
  ctx.clearRect(0, 0, canvas.width, canvas.height)
  ctx.fillStyle = "#f9fafb"
  ctx.fillRect(0, 0, canvas.width, canvas.height)
  ctx.fillStyle = "#6b7280"
  ctx.font = "16px Arial"
  ctx.fillText(message, 24, 40)
  updateViewerStatus(message)
}

async function openDocument(path, jobId = "") {
  if (!path) return
  currentPath = path
  if (jobId) currentJobId = jobId
  updateViewerStatus("Opening document...")
  try {
    const res = await fetch(`${API}/document?path=${encodeURIComponent(path)}&_=${Date.now()}`, { cache: "no-store" })
    if (!res.ok) throw new Error(await readError(res))
    const doc = await res.json()
    currentPath = doc.path || path
    documentState = {
      path: doc.path,
      requestedPath: doc.requested_path || path,
      openedFromManifest: Boolean(doc.opened_from_manifest),
      filename: doc.filename || "document",
      kind: doc.kind || "file",
      pages: Math.max(1, Number(doc.pages || 1)),
      page: 1,
      rotation: 0,
      zoom: 1,
    }
    if (!doc.editable) {
      clearViewer("This file type is ingested but cannot be rendered as an editable image.")
      documentState.path = doc.path
      documentState.filename = doc.filename || "document"
      documentState.kind = doc.kind || "file"
      updateViewerControls()
      return
    }
    await loadDocumentPage()
    showTab("viewer")
  } catch (err) {
    clearViewer(`Unable to open viewer: ${err.message}`)
  }
}

async function loadDocumentPage() {
  if (!documentState.path) return
  updateViewerStatus(`Loading page ${documentState.page}...`)
  const img = new Image()
  img.onload = () => {
    originalImage = img
    editorObjects = []
    selectedObjectId = ""
    dragState = null
    pageDirty = false
    syncSelectedObjectControls()
    redrawPage()
    const manifestNote = documentState.openedFromManifest ? " · opened source document from re-ingest image-set job" : ""
    updateViewerStatus(`Viewing ${documentState.filename}${manifestNote}`)
  }
  img.onerror = () => {
    originalImage = null
    editorObjects = []
    selectedObjectId = ""
    dragState = null
    pageDirty = false
    canvas.width = 900
    canvas.height = 620
    ctx.clearRect(0, 0, canvas.width, canvas.height)
    ctx.fillStyle = "#f9fafb"
    ctx.fillRect(0, 0, canvas.width, canvas.height)
    ctx.fillStyle = "#991b1b"
    ctx.font = "16px Arial"
    ctx.fillText("Unable to render this page as an image.", 24, 40)
    updateViewerStatus("Unable to render this page as an image.")
  }
  img.src = `${API}/document/page?path=${encodeURIComponent(documentState.path)}&page=${documentState.page}&_=${Date.now()}`
}

function redrawPage(showSelection = true) {
  if (!originalImage) return
  const rotation = normalizedRotation()
  const zoom = currentZoom()
  const iw = getImageWidth()
  const ih = getImageHeight()
  const rotated = rotation === 90 || rotation === 270
  const targetW = Math.max(1, Math.round((rotated ? ih : iw) * zoom))
  const targetH = Math.max(1, Math.round((rotated ? iw : ih) * zoom))

  canvas.width = targetW
  canvas.height = targetH
  ctx.clearRect(0, 0, canvas.width, canvas.height)
  ctx.save()
  applyImageTransform(ctx, rotation, zoom, targetW, targetH)
  ctx.drawImage(originalImage, 0, 0, iw, ih)
  drawEditorObjects(ctx, showSelection)
  ctx.restore()
  updateViewerControls()
}

function applyImageTransform(context, rotation, zoom, targetW, targetH) {
  if (rotation === 90) {
    context.translate(targetW, 0)
    context.rotate(Math.PI / 2)
  } else if (rotation === 180) {
    context.translate(targetW, targetH)
    context.rotate(Math.PI)
  } else if (rotation === 270) {
    context.translate(0, targetH)
    context.rotate(3 * Math.PI / 2)
  }
  context.scale(zoom, zoom)
}

function drawEditorObjects(context, showSelection = true) {
  for (const obj of editorObjects) {
    if (obj.type === "text") drawTextObject(context, obj)
    if (obj.type === "highlight") drawRectObject(context, obj)
    if (obj.type === "whiteout") drawRectObject(context, obj)
    if (showSelection && obj.id === selectedObjectId) drawSelectionBox(context, obj)
  }
}

function drawTextObject(context, obj) {
  context.save()
  context.font = `${obj.size || 24}px Arial`
  context.textBaseline = "top"
  context.lineJoin = "round"
  context.strokeStyle = "rgba(255,255,255,0.85)"
  context.lineWidth = Math.max(2, Math.round((obj.size || 24) / 7))
  context.fillStyle = obj.color || "#111827"
  const lines = obj.lines && obj.lines.length ? obj.lines : String(obj.text || "").split(/\n+/)
  const lineHeight = obj.lineHeight || Math.round((obj.size || 24) * 1.25)
  lines.forEach((line, idx) => {
    const yy = obj.y + idx * lineHeight
    context.strokeText(line, obj.x, yy)
    context.fillText(line, obj.x, yy)
  })
  context.restore()
}

function drawRectObject(context, obj) {
  context.save()
  context.globalAlpha = obj.alpha ?? 1
  context.fillStyle = obj.color || (obj.type === "highlight" ? "#fde047" : "#ffffff")
  context.fillRect(obj.x, obj.y, obj.w, obj.h)
  context.restore()
}

function drawSelectionBox(context, obj) {
  context.save()
  context.strokeStyle = "#2563eb"
  context.lineWidth = Math.max(1, 2 / currentZoom())
  context.setLineDash([6 / currentZoom(), 4 / currentZoom()])
  context.strokeRect(obj.x - 3 / currentZoom(), obj.y - 3 / currentZoom(), obj.w + 6 / currentZoom(), obj.h + 6 / currentZoom())
  context.restore()
}

function updateViewerControls() {
  const pageStatus = document.getElementById("pageStatus")
  const viewerStatus = document.getElementById("viewerStatus")
  const hasDoc = Boolean(documentState.path)
  const hasPages = Math.max(1, Number(documentState.pages || 1))
  const canPage = documentState.kind === "pdf" && hasPages > 1
  document.getElementById("prevPageBtn")?.toggleAttribute("disabled", !canPage || documentState.page <= 1)
  document.getElementById("nextPageBtn")?.toggleAttribute("disabled", !canPage || documentState.page >= hasPages)
  document.getElementById("saveEditBtn")?.toggleAttribute("disabled", !hasDoc || !originalImage)
  const canReingest = Boolean(hasDoc && originalImage)
  document.getElementById("reingestPageBtn")?.toggleAttribute("disabled", !canReingest)
  document.getElementById("reingestEditedBtn")?.toggleAttribute("disabled", !canReingest || documentState.kind !== "pdf")
  document.getElementById("reingestFullBtn")?.toggleAttribute("disabled", !canReingest || documentState.kind !== "pdf")
  if (pageStatus) pageStatus.innerText = hasDoc ? `Page ${documentState.page} / ${hasPages} · ${documentState.rotation}° · ${Math.round(documentState.zoom * 100)}%` : "No document"
  if (viewerStatus && hasDoc && !viewerStatus.innerText.startsWith("Viewing ")) {
    const manifestNote = documentState.openedFromManifest ? " · source of image-set job" : ""
    viewerStatus.innerText = `${documentState.filename} · ${documentState.kind}${manifestNote}`
  }
  syncSelectedObjectControls()
}

function updateViewerStatus(text) {
  const status = document.getElementById("viewerStatus")
  if (status) status.innerText = text
  updateViewerControls()
}

async function prevPage() {
  if (documentState.page <= 1) return
  documentState.page--
  documentState.rotation = 0
  await loadDocumentPage()
}

async function nextPage() {
  if (documentState.page >= documentState.pages) return
  documentState.page++
  documentState.rotation = 0
  await loadDocumentPage()
}

function rotatePage(delta) {
  if (!originalImage) return
  documentState.rotation = ((documentState.rotation + delta) % 360 + 360) % 360
  redrawPage()
}

function zoomPage(delta) {
  if (!originalImage) return
  documentState.zoom = Math.max(0.25, Math.min(3, Number((documentState.zoom + delta).toFixed(2))))
  redrawPage()
}

function resetView() {
  if (!originalImage) return
  documentState.rotation = 0
  documentState.zoom = 1
  redrawPage()
}

function clearCanvas() {
  editorObjects = []
  selectedObjectId = ""
  dragState = null
  pageDirty = false
  redrawPage()
  syncSelectedObjectControls()
  updateViewerStatus(`Cleared unsaved edits on page ${documentState.page}`)
}

function renderSavedPageDataURL() {
  if (!originalImage) throw new Error("No editable page loaded")
  const rotation = normalizedRotation()
  const iw = getImageWidth()
  const ih = getImageHeight()
  const rotated = rotation === 90 || rotation === 270
  const out = document.createElement("canvas")
  out.width = Math.max(1, rotated ? ih : iw)
  out.height = Math.max(1, rotated ? iw : ih)
  const outCtx = out.getContext("2d")
  outCtx.clearRect(0, 0, out.width, out.height)
  outCtx.save()
  applyImageTransform(outCtx, rotation, 1, out.width, out.height)
  outCtx.drawImage(originalImage, 0, 0, iw, ih)
  drawEditorObjects(outCtx, false)
  outCtx.restore()
  return out.toDataURL("image/png")
}

async function saveEdit() {
  if (!documentState.path || !originalImage) return alert("No editable page loaded")
  let data
  try {
    data = renderSavedPageDataURL()
  } catch (err) {
    alert(`Save failed: ${err.message}`)
    return null
  }
  const res = await fetch(`${API}/document/page/update?path=${encodeURIComponent(documentState.path)}&page=${documentState.page}`, { method: "POST", body: data })
  if (!res.ok) {
    alert(`Save failed: ${await readError(res)}`)
    return null
  }
  const saved = await res.json().catch(() => null)
  pageDirty = false
  editorObjects = []
  selectedObjectId = ""
  dragState = null
  updateViewerStatus(`Saved edited image for page ${documentState.page}`)
  await loadDocumentPage()
  return saved
}

async function reingest(mode = "page") {
  if (!documentState.path || !originalImage) return alert("No editable page loaded")

  const normalizedMode = String(mode || "page").toLowerCase()
  if (pageDirty || editorObjects.length) {
    const saved = await saveEdit()
    if (!saved) return
  }

  const params = new URLSearchParams({
    path: documentState.path,
    page: String(documentState.page),
    mode: normalizedMode,
  })
  const res = await fetch(`${API}/reingest?${params.toString()}`, { method: "POST" })
  if (!res.ok) {
    alert(`Re-ingestion failed: ${await readError(res)}`)
    return
  }
  const job = await res.json().catch(() => null)
  if (job?.id) {
    currentJobId = job.id
    startJobTracking(job.id)
  }
  const label = reingestModeLabel(normalizedMode)
  setIngestionProgress(0, `Queued ${label}`)
  updateWorkspaceStatus(job)
  refreshJobs()
  alert(`${label} re-ingestion queued`)
}

function reingestModeLabel(mode) {
  if (mode === "full") return "full document image set"
  if (mode === "edited") return "edited pages image set"
  return `page ${documentState.page} image`
}

async function search() {
  const input = document.getElementById("query")
  const root = document.getElementById("results")
  const q = String(input?.value || "").trim()
  if (!q) {
    if (root) root.innerHTML = `<div class="error-box">Enter a question first.</div>`
    return
  }

  if (root) root.innerHTML = `<div class="status">Searching Qdrant, reranking, and generating answer...</div>`

  try {
    const res = await fetch(`${API}/query`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({query: q})
    })
    if (!res.ok) {
      const msg = await readError(res)
      if (root) root.innerHTML = `<div class="error-box">Search failed: ${escapeHtml(msg || String(res.status))}</div>`
      return
    }
    const data = await res.json()
    lastResults = data.results || []
    renderResults(data)
  } catch (err) {
    console.error("Search render failed", err)
    if (root) root.innerHTML = `<div class="error-box">Search failed in the browser: ${escapeHtml(err.message || String(err))}</div>`
  }
}

function renderResults(data) {
  const root = document.getElementById("results")
  const results = data.results || []
  const rewritten = data.rewritten_query || ""
  const meta = `
    <div class="rag-evidence">
      <strong>Retrieval:</strong> ${escapeHtml(data.retrieval_mode || "unknown")}
      <span>Qdrant hits: ${Number(data.qdrant_hits || 0)}</span>
      <span>Keyword hits: ${Number(data.keyword_hits || 0)}</span>
      <span>Candidates: ${Number(data.candidate_count || 0)}</span>
      <span>Reranked: ${Number(data.reranked_count || 0)}</span>
      <span>Reranker: ${data.reranker_enabled ? `${escapeHtml(data.reranker_provider || "external")} / ${escapeHtml(data.reranker_model || "")}` : "local fallback"}</span>
      ${data.reranker_error ? `<span class="error-inline">Reranker error: ${escapeHtml(data.reranker_error)}</span>` : ""}
      <span>Min score: ${Number(data.min_score || 0).toFixed(2)}</span>
      <span>Require context: ${data.require_context ? "true" : "false"}</span>
      <span>LLM: ${data.llm_provider ? `${escapeHtml(data.llm_provider)} / ${escapeHtml(data.llm_model || "")}` : "disabled"}</span>
      <span>Context chars: ${Number(data.context_chars || 0).toLocaleString()}</span>
      <span>Latency: ${Number(data.latency_ms || 0)} ms</span>
    </div>`
  const qdrantPanel = renderQdrantEvidencePanel(data)
  if (data.error) {
    root.innerHTML = `
      ${rewritten ? `<div class="status">Rewritten query: ${escapeHtml(rewritten)}</div>` : ""}
      ${meta}
      ${renderQueryExpansions(data)}
      ${renderQueryVariants(data)}
      ${qdrantPanel}
      ${renderLLMAnswer(data)}
      <div class="error-box">${escapeHtml(data.error)}</div>
    `
    return
  }
  root.innerHTML = `
    ${rewritten ? `<div class="status">Rewritten query: ${escapeHtml(rewritten)}</div>` : ""}
    ${meta}
    ${renderQueryExpansions(data)}
    ${renderQueryVariants(data)}
    ${renderLLMAnswer(data)}
    ${results.length === 0 ? `<div class="error-box">No context results returned.</div>` : ""}
    ${results.map((r, i) => `
      <div class="result-card">
        <div>${escapeHtml(r.text)}</div>
        <div class="confidence">${Math.round((r.confidence || 0) * 100)}% confidence</div>
        <div class="score-breakdown">semantic ${formatScore(r.semantic_score)} · keyword ${formatScore(r.keyword_score)} · rerank ${formatScore(r.rerank_score)} · combined ${formatScore(r.combined_score || r.score)}</div>
        <div>${escapeHtml(r.explanation || "")}</div>
        <div class="source-pill">Source: ${escapeHtml(r.source || "unknown")}</div>
        ${renderSingleResultEvidence(data, r)}
        <button onclick="sendFeedback(${i}, true)">Useful</button>
      </div>
    `).join("")}
  `
}

function renderQdrantEvidencePanel(data) {
  const sources = data.sources || []
  const topScore = Number(data.top_score || 0)
  return `
    <div class="qdrant-panel ${sources.length ? "" : "qdrant-panel-empty"}">
      <div class="qdrant-title">${sources.length ? "Retrieved from Qdrant" : "No Qdrant context retrieved"}</div>
      <div class="qdrant-grid">
        <div><span>Collection:</span> <strong>${escapeHtml(data.collection || "rag")}</strong></div>
        <div><span>Chunks used:</span> <strong>${Number(data.chunks_used || sources.length || 0)}</strong></div>
        <div><span>Top score:</span> <strong>${topScore ? topScore.toFixed(4) : "0.0000"}</strong></div>
      </div>
      ${sources.length ? `
        <div class="qdrant-sources-title">Sources:</div>
        <ul class="qdrant-sources">
          ${sources.slice(0, 5).map(formatSourceItem).join("")}
        </ul>` : ""}
    </div>`
}

function renderSingleResultEvidence(data, result) {
  if ((result.source || "") !== "qdrant") return ""
  const source = {
    filename: result.filename,
    path: result.path,
    page: result.page,
    chunk_index: result.chunk_index,
    score: result.raw_score || result.score,
    semantic_score: result.semantic_score,
    keyword_score: result.keyword_score,
    rerank_score: result.rerank_score,
    combined_score: result.combined_score || result.score,
    source: result.source,
    source_kind: result.source_kind,
    original_path: result.original_path,
    manifest_path: result.manifest_path,
    image_path: result.image_path,
    document_id: result.document_id,
    source_checksum: result.source_checksum,
    ingest_scope: result.ingest_scope,
    object_key: result.object_key,
    object_url: result.object_url
  }
  return `
    <div class="qdrant-panel compact">
      <div class="qdrant-title">Retrieved from Qdrant</div>
      <div class="qdrant-grid">
        <div><span>Collection:</span> <strong>${escapeHtml(data.collection || "rag")}</strong></div>
        <div><span>Chunks used:</span> <strong>${Number(data.chunks_used || 0)}</strong></div>
        <div><span>Top score:</span> <strong>${Number(data.top_score || 0).toFixed(4)}</strong></div>
      </div>
      <div class="qdrant-sources-title">Source:</div>
      <ul class="qdrant-sources">${formatSourceItem(source)}</ul>
    </div>`
}

function formatSourceItem(src) {
  const name = src.filename || basename(src.path || "document")
  const page = Number(src.page || 0)
  const chunk = Number(src.chunk_index || 0)
  const score = Number(src.score || 0)
  const parts = [escapeHtml(name)]
  if (page > 0) parts.push(`page ${page}`)
  if (chunk > 0) parts.push(`chunk ${chunk}`)
  if (src.source_kind) parts.push(escapeHtml(src.source_kind))
  if (src.ingest_scope) parts.push(`scope ${escapeHtml(src.ingest_scope)}`)
  if (src.document_id) parts.push(`doc ${shortHash(src.document_id)}`)
  if (src.source_checksum) parts.push(`checksum ${shortHash(src.source_checksum)}`)
  if (src.image_path) parts.push(`image ${escapeHtml(basename(src.image_path))}`)
  if (src.object_key) parts.push(`object ${escapeHtml(src.object_key)}`)
  if (score > 0) parts.push(`raw ${score.toFixed(4)}`)
  if (src.semantic_score) parts.push(`semantic ${formatScore(src.semantic_score)}`)
  if (src.keyword_score) parts.push(`keyword ${formatScore(src.keyword_score)}`)
  if (src.rerank_score) parts.push(`rerank ${formatScore(src.rerank_score)}`)
  if (src.combined_score) parts.push(`combined ${formatScore(src.combined_score)}`)
  return `<li>${parts.join(" — ")}</li>`
}

function formatScore(value) {
  const n = Number(value || 0)
  return n ? n.toFixed(3) : "0.000"
}

function renderLLMAnswer(data) {
  const hasAnswer = data.answer_generated && String(data.answer || "").trim()
  const provider = [data.llm_provider, data.llm_model].filter(Boolean).join(" / ")
  const error = String(data.llm_error || "").trim()

  if (!hasAnswer && !error && !provider) return ""

  return `
    <div class="answer-card">
      <div class="answer-header">
        <strong>Answer</strong>
        ${provider ? `<span>${escapeHtml(provider)}</span>` : `<span>LLM disabled</span>`}
      </div>
      ${hasAnswer ? `<div class="answer-body">${formatAnswerText(data.answer)}</div>` : ""}
      ${error ? `<div class="error-box compact-error">LLM error: ${escapeHtml(error)}</div>` : ""}
    </div>`
}

function formatAnswerText(text) {
  return escapeHtml(text || "")
    .replace(/\n{2,}/g, "</p><p>")
    .replace(/\n/g, "<br>")
    .replace(/^/, "<p>")
    .replace(/$/, "</p>")
}

function renderQueryVariants(data) {
  const variants = data.query_variants || []
  if (!variants.length) return ""
  return `
    <details class="rag-variants">
      <summary>Query variants (${variants.length})</summary>
      <ul>${variants.map(v => `<li>${escapeHtml(v)}</li>`).join("")}</ul>
    </details>`
}

function renderQueryExpansions(data) {
  const expansions = data.query_expansions || []
  if (!expansions.length) return ""
  return `<div class="rag-expansions"><strong>Abbreviation expansion:</strong> ${expansions.map(escapeHtml).join(" · ")}</div>`
}

function shortHash(value) {
  const s = String(value || "")
  return s.length > 12 ? s.slice(0, 12) : s
}

function basename(path) {
  return String(path || "").split(/[\\/]/).pop() || "document"
}

async function sendFeedback(index, useful) {
  const q = document.getElementById("query").value
  const r = lastResults?.[index]
  if (!r) return
  await fetch(`${API}/feedback`, {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({query: q, result: r.text, useful})
  })
  alert("Feedback saved")
}

async function refreshJobs() {
  const root = document.getElementById("jobsList")
  if (!root) return
  try {
    const res = await fetch(`${API}/jobs?_=${Date.now()}`, { cache: "no-store" })
    if (!res.ok) {
      root.innerText = `Unable to load jobs: ${res.status}`
      return
    }
    const jobs = await res.json()
    root.innerHTML = jobs.length ? renderJobsTable(jobs) : `<div class="status">No jobs yet.</div>`
  } catch (err) {
    root.innerText = `Unable to load jobs: ${err.message}`
  }
}

function renderJobsTable(jobs) {
  return `
    <table class="jobs-table">
      <thead>
        <tr>
          <th>Job ID</th>
          <th>File</th>
          <th>Status</th>
          <th>Stage</th>
          <th>Progress</th>
          <th>Size</th>
          <th>Upload Time</th>
          <th>Ingestion Time</th>
          <th>Total Time</th>
          <th>Processed</th>
          <th>Stage Timings</th>
          <th>Error</th>
          <th>Action</th>
        </tr>
      </thead>
      <tbody>
        ${jobs.map(job => {
          const active = currentJobId && job.id === currentJobId
          const canRetry = String(job.status || "").toLowerCase() === "failed"
          const path = escapeAttr(job.path || "")
          const id = escapeAttr(job.id || "")
          return `
            <tr class="${active ? "active-job" : ""}">
              <td class="mono">${escapeHtml(job.id || "")}</td>
              <td>${escapeHtml(job.filename || "")}</td>
              <td>${escapeHtml(job.status || "")}</td>
              <td>${escapeHtml(job.stage || "")}</td>
              <td>${Number(job.progress || 0)}%</td>
              <td>${formatBytes(job.upload_bytes || 0)}</td>
              <td>${formatDuration(job.upload_duration_ms || 0)}</td>
              <td>${formatDuration(job.ingestion_duration_ms || 0)}</td>
              <td>${formatDuration(job.total_duration_ms || 0)}</td>
              <td>${renderJobProcessed(job)}</td>
              <td>${renderJobStageTimings(job)}</td>
              <td>${escapeHtml(job.error || "")}</td>
              <td class="actions">
                ${job.path ? `<button onclick="openJob('${path}', '${id}')">Open</button>` : ""}
                ${canRetry ? `<button onclick="retryJob('${id}')">Retry</button>` : ""}
              </td>
            </tr>
          `
        }).join("")}
      </tbody>
    </table>
  `
}

function renderJobProcessed(job) {
  const parts = []
  if (job.source_kind) parts.push(escapeHtml(job.source_kind))
  if (job.ingest_scope) parts.push(`scope ${escapeHtml(job.ingest_scope)}`)
  if (job.document_id) parts.push(`doc ${shortHash(job.document_id)}`)
  if (job.source_checksum) parts.push(`checksum ${shortHash(job.source_checksum)}`)
  const pagesProcessed = Number(job.pages_processed || 0)
  const pagesDiscovered = Number(job.pages_discovered || pagesProcessed || 0)
  if (pagesProcessed || pagesDiscovered) parts.push(`pages ${pagesProcessed}/${pagesDiscovered}`)
  if (job.extracted_chars) parts.push(`${Number(job.extracted_chars).toLocaleString()} chars`)
  if (job.chunk_count) parts.push(`${Number(job.chunk_count).toLocaleString()} chunks`)
  if (job.embedding_count) parts.push(`${Number(job.embedding_count).toLocaleString()} embeddings`)
  if (job.vector_upserted) parts.push(`${Number(job.vector_upserted).toLocaleString()} Qdrant`)
  if (job.pipeline_note && parts.length === 0) parts.push(escapeHtml(job.pipeline_note))
  return parts.length ? `<div class="job-details">${parts.join("<br>")}</div>` : ""
}

function renderJobStageTimings(job) {
  const parts = []
  if (job.extract_duration_ms) parts.push(`extract ${formatDuration(job.extract_duration_ms)}`)
  if (job.chunk_duration_ms) parts.push(`chunk ${formatDuration(job.chunk_duration_ms)}`)
  if (job.embed_duration_ms) parts.push(`embed ${formatDuration(job.embed_duration_ms)}`)
  if (job.upsert_duration_ms) parts.push(`upsert ${formatDuration(job.upsert_duration_ms)}`)
  return parts.length ? `<div class="job-details">${parts.map(escapeHtml).join("<br>")}</div>` : ""
}

async function openJob(path, jobId) {
  currentJobId = jobId || currentJobId
  await openDocument(path, jobId)
}

async function retryJob(jobId) {
  const res = await fetch(`${API}/retry?job_id=${encodeURIComponent(jobId)}`, { method: "POST" })
  if (!res.ok) {
    alert("Retry failed")
    return
  }
  currentJobId = jobId
  startJobTracking(jobId)
  refreshJobs()
}

function refreshJobsQuietly() {
  const jobs = document.getElementById("jobs")
  if (jobs && !jobs.classList.contains("hidden")) refreshJobs()
}

async function loadMetrics() {
  const res = await fetch(`${API}/metrics?_=${Date.now()}`, { cache: "no-store" })
  document.getElementById("metricsOut").innerText = JSON.stringify(await res.json(), null, 2)
}

async function readError(res) {
  const text = await res.text().catch(() => "")
  return text.trim() || `${res.status} ${res.statusText}`
}

function formatDuration(ms) {
  const n = Number(ms || 0)
  if (!Number.isFinite(n) || n <= 0) return "0s"
  if (n < 1000) return `${Math.round(n)}ms`
  const seconds = n / 1000
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)}s`
  const minutes = Math.floor(seconds / 60)
  const rem = Math.round(seconds % 60)
  return `${minutes}m ${rem}s`
}

function formatBytes(bytes) {
  const n = Number(bytes || 0)
  if (!Number.isFinite(n) || n <= 0) return "-"
  const units = ["B", "KB", "MB", "GB"]
  let value = n
  let idx = 0
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024
    idx++
  }
  return `${value.toFixed(idx === 0 ? 0 : 1)} ${units[idx]}`
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"
  }[c]))
}

function escapeAttr(s) {
  return escapeHtml(s).replace(/`/g, "&#96;")
}

clearViewer("Upload a PDF or image, or open a completed job.")
setTool("select")
