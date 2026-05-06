package search

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"rag-unified-realtime/internal/ai"
	"rag-unified-realtime/internal/llm"
	"rag-unified-realtime/internal/logging"
	"rag-unified-realtime/internal/model"
	"rag-unified-realtime/internal/repository"
	"rag-unified-realtime/internal/rerank"
	"rag-unified-realtime/internal/vector"
)

type Options struct {
	RequireContext      bool
	HybridSearch        bool
	VectorLimit         int
	KeywordLimit        int
	RerankLimit         int
	FinalLimit          int
	MinScore            float64
	SemanticWeight      float64
	KeywordWeight       float64
	RerankWeight        float64
	MMRLambda           float64
	UseMultiQuery       bool
	QueryVariants       int
	ContextMaxChars     int
	LLMRequireContext   bool
	LLMMaxContextChunks int
	LLMMaxContextChars  int
	RerankerEnabled     bool
	RerankerProvider    string
	RerankerModel       string
	RerankerFailClosed  bool
	DataDir             string
}

type Service struct {
	repo          *repository.MemoryRepository
	ai            ai.Provider
	llm           llm.Client
	reranker      rerank.Client
	vector        *vector.QdrantClient
	log           *logging.Logger
	opts          Options
	abbreviations map[string]string
}

func NewService(repo *repository.MemoryRepository, aiProvider ai.Provider, vectorClient *vector.QdrantClient, log *logging.Logger, requireContext bool, dataDir string) *Service {
	return NewServiceWithOptions(repo, aiProvider, vectorClient, log, Options{RequireContext: requireContext, HybridSearch: true, VectorLimit: 40, KeywordLimit: 40, RerankLimit: 25, FinalLimit: 8, MinScore: 0.05, SemanticWeight: 0.55, KeywordWeight: 0.20, RerankWeight: 0.25, MMRLambda: 0.82, UseMultiQuery: true, QueryVariants: 3, ContextMaxChars: 16000, LLMRequireContext: true, LLMMaxContextChunks: 6, LLMMaxContextChars: 12000, DataDir: dataDir})
}

func NewServiceWithOptions(repo *repository.MemoryRepository, aiProvider ai.Provider, vectorClient *vector.QdrantClient, log *logging.Logger, opts Options) *Service {
	return NewServiceWithLLM(repo, aiProvider, llm.Disabled(), vectorClient, log, opts)
}

func NewServiceWithLLM(repo *repository.MemoryRepository, aiProvider ai.Provider, llmClient llm.Client, vectorClient *vector.QdrantClient, log *logging.Logger, opts Options) *Service {
	return NewServiceWithLLMAndReranker(repo, aiProvider, llmClient, rerank.Disabled(), vectorClient, log, opts)
}

func NewServiceWithLLMAndReranker(repo *repository.MemoryRepository, aiProvider ai.Provider, llmClient llm.Client, rerankerClient rerank.Client, vectorClient *vector.QdrantClient, log *logging.Logger, opts Options) *Service {
	if opts.VectorLimit <= 0 {
		opts.VectorLimit = 40
	}
	if opts.KeywordLimit <= 0 {
		opts.KeywordLimit = 40
	}
	if opts.RerankLimit <= 0 {
		opts.RerankLimit = 25
	}
	if opts.FinalLimit <= 0 {
		opts.FinalLimit = 8
	}
	if opts.MMRLambda <= 0 || opts.MMRLambda > 1 {
		opts.MMRLambda = 0.82
	}
	if opts.SemanticWeight == 0 && opts.KeywordWeight == 0 && opts.RerankWeight == 0 {
		opts.SemanticWeight, opts.KeywordWeight, opts.RerankWeight = 0.55, 0.20, 0.25
	}
	if opts.QueryVariants <= 0 {
		opts.QueryVariants = 3
	}
	if opts.ContextMaxChars <= 0 {
		opts.ContextMaxChars = 16000
	}
	if opts.LLMMaxContextChunks <= 0 {
		opts.LLMMaxContextChunks = 6
	}
	if opts.LLMMaxContextChars <= 0 {
		opts.LLMMaxContextChars = 12000
	}
	if llmClient == nil {
		llmClient = llm.Disabled()
	}
	if rerankerClient == nil {
		rerankerClient = rerank.Disabled()
	}
	if opts.RerankerProvider == "" {
		opts.RerankerProvider = rerankerClient.Provider()
	}
	if opts.RerankerModel == "" {
		opts.RerankerModel = rerankerClient.Model()
	}
	opts.RerankerEnabled = rerankerClient.Enabled()
	opts.RerankerFailClosed = rerankerClient.FailClosed()
	return &Service{repo: repo, ai: aiProvider, llm: llmClient, reranker: rerankerClient, vector: vectorClient, log: log, opts: opts, abbreviations: LoadAbbreviations(opts.DataDir)}
}

type SourceEvidence struct {
	Filename       string  `json:"filename,omitempty"`
	Path           string  `json:"path,omitempty"`
	Page           int     `json:"page,omitempty"`
	ChunkIndex     int     `json:"chunk_index,omitempty"`
	Score          float64 `json:"score"`
	SemanticScore  float64 `json:"semantic_score,omitempty"`
	KeywordScore   float64 `json:"keyword_score,omitempty"`
	RerankScore    float64 `json:"rerank_score,omitempty"`
	CombinedScore  float64 `json:"combined_score,omitempty"`
	Source         string  `json:"source"`
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

type Response struct {
	Answer           string           `json:"answer,omitempty"`
	AnswerGenerated  bool             `json:"answer_generated"`
	LLMProvider      string           `json:"llm_provider,omitempty"`
	LLMModel         string           `json:"llm_model,omitempty"`
	LLMError         string           `json:"llm_error,omitempty"`
	ContextChars     int              `json:"context_chars,omitempty"`
	QueryVariants    []string         `json:"query_variants,omitempty"`
	Results          []model.Result   `json:"results"`
	Sources          []SourceEvidence `json:"sources"`
	LatencyMs        int64            `json:"latency_ms"`
	Rewritten        string           `json:"rewritten_query"`
	RequireContext   bool             `json:"require_context"`
	RetrievalMode    string           `json:"retrieval_mode"`
	Collection       string           `json:"collection"`
	ChunksUsed       int              `json:"chunks_used"`
	TopScore         float64          `json:"top_score"`
	QdrantHits       int              `json:"qdrant_hits"`
	KeywordHits      int              `json:"keyword_hits"`
	CandidateCount   int              `json:"candidate_count"`
	RerankedCount    int              `json:"reranked_count"`
	FinalLimit       int              `json:"final_limit"`
	MinScore         float64          `json:"min_score"`
	SemanticWeight   float64          `json:"semantic_weight"`
	KeywordWeight    float64          `json:"keyword_weight"`
	RerankWeight     float64          `json:"rerank_weight"`
	RerankerEnabled  bool             `json:"reranker_enabled"`
	RerankerProvider string           `json:"reranker_provider,omitempty"`
	RerankerModel    string           `json:"reranker_model,omitempty"`
	RerankerError    string           `json:"reranker_error,omitempty"`
	MMRLambda        float64          `json:"mmr_lambda"`
	Error            string           `json:"error,omitempty"`
	QueryExpansions  []string         `json:"query_expansions,omitempty"`
}

func (s *Service) Query(ctx context.Context, query string) (resp Response) {
	start := time.Now()
	query = strings.TrimSpace(query)
	resp = Response{Results: []model.Result{}, Sources: []SourceEvidence{}, RequireContext: s.opts.RequireContext, RetrievalMode: retrievalMode(s.opts), Collection: s.vector.Collection(), FinalLimit: s.opts.FinalLimit, MinScore: s.opts.MinScore, SemanticWeight: s.opts.SemanticWeight, KeywordWeight: s.opts.KeywordWeight, RerankWeight: s.opts.RerankWeight, RerankerEnabled: s.reranker.Enabled(), RerankerProvider: s.reranker.Provider(), RerankerModel: s.reranker.Model(), MMRLambda: s.opts.MMRLambda}
	defer func() { resp.LatencyMs = time.Since(start).Milliseconds() }()

	if query == "" {
		resp.Error = "query is required"
		return resp
	}

	rewritten, err := s.ai.Rewrite(ctx, query)
	if err != nil || strings.TrimSpace(rewritten) == "" {
		rewritten = query
	}
	expanded, expansions := ExpandAbbreviations(rewritten, s.abbreviations)
	if strings.TrimSpace(expanded) != "" {
		rewritten = expanded
	}
	resp.QueryExpansions = expansions
	resp.Rewritten = rewritten

	queryVariants := s.buildQueryVariants(query, rewritten, expansions)
	resp.QueryVariants = queryVariants
	embeddings, err := s.ai.EmbedBatch(ctx, queryVariants)
	if err != nil || len(embeddings) == 0 {
		resp.Error = "failed to embed query for Qdrant retrieval"
		if s.log != nil {
			s.log.Error("rag query embedding failed", "query", query, "error", errorString(err))
		}
		return resp
	}

	allVectorResults := []model.Result{}
	for i, emb := range embeddings {
		vectorResults, err := s.vector.SearchWithOptions(ctx, emb, vector.SearchOptions{Limit: s.opts.VectorLimit, ScoreThreshold: s.opts.MinScore})
		if err != nil {
			resp.Error = "Qdrant retrieval failed: " + err.Error()
			if s.log != nil {
				s.log.Error("rag qdrant retrieval failed", "query", query, "variant", queryVariants[i], "error", err.Error())
			}
			return resp
		}
		allVectorResults = append(allVectorResults, vectorResults...)
	}
	vectorResults := dedupeResults(allVectorResults)
	resp.QdrantHits = len(vectorResults)
	if s.log != nil {
		s.log.Info("rag qdrant retrieval", "query", query, "rewritten", rewritten, "variants", len(queryVariants), "hits", len(vectorResults), "require_context", s.opts.RequireContext, "hybrid", s.opts.HybridSearch, "llm_enabled", s.llm.Enabled())
	}

	if s.opts.RequireContext && len(vectorResults) == 0 {
		resp.Error = "No Qdrant context was retrieved. Refusing to answer because RAG_REQUIRE_CONTEXT=true."
		return resp
	}

	keywordResults := []model.Result(nil)
	if s.opts.HybridSearch {
		keywordResults = s.repo.KeywordSearch(rewritten, s.opts.KeywordLimit)
		resp.KeywordHits = len(keywordResults)
	}

	merged := mergeByIdentity(vectorResults, keywordResults)
	resp.CandidateCount = len(merged)
	if len(merged) == 0 {
		resp.Error = "No relevant context was retrieved."
		return resp
	}

	applyKeywordScores(rewritten, merged)
	normalizeComponentScores(merged)
	sort.SliceStable(merged, func(i, j int) bool { return preRerankScore(merged[i], s.opts) > preRerankScore(merged[j], s.opts) })
	if len(merged) > s.opts.RerankLimit {
		merged = merged[:s.opts.RerankLimit]
	}

	docs := make([]string, len(merged))
	for i := range merged {
		docs[i] = merged[i].Text
	}
	rerankScores, err := s.scoreRerank(ctx, rewritten, docs)
	if err == nil {
		for i := range merged {
			if i < len(rerankScores) {
				merged[i].RerankScore = clamp01(rerankScores[i])
			}
		}
	} else {
		resp.RerankerError = err.Error()
		if s.reranker.Enabled() && s.reranker.FailClosed() {
			resp.Error = "External reranker failed and RERANKER_FAIL_CLOSED=true: " + err.Error()
			return resp
		}
	}
	resp.RerankedCount = len(merged)

	for i := range merged {
		merged[i].CombinedScore = combinedScore(merged[i], s.opts)
		merged[i].Score = merged[i].CombinedScore
		merged[i].Confidence = merged[i].CombinedScore
		if merged[i].RawScore == 0 {
			merged[i].RawScore = merged[i].SemanticScore
		}
	}

	filtered := postProcess(merged, s.opts)
	diverse := mmrSelect(filtered, s.opts.FinalLimit, s.opts.MMRLambda)
	resp.Results = diverse
	resp.Sources = buildSources(resp.Results)
	resp.ChunksUsed = len(resp.Sources)
	if len(resp.Sources) > 0 {
		resp.TopScore = resp.Sources[0].Score
	}
	if s.opts.RequireContext && len(resp.Results) == 0 {
		resp.Error = "Qdrant returned context, but no usable text survived reranking and threshold filtering."
		return resp
	}
	if s.llm.Enabled() {
		resp.LLMProvider = s.llm.Provider()
		resp.LLMModel = s.llm.Model()
		answer, chars, err := s.generateAnswer(ctx, query, rewritten, resp.Results)
		resp.ContextChars = chars
		if err != nil {
			resp.LLMError = err.Error()
			if s.opts.LLMRequireContext && len(resp.Results) == 0 {
				resp.Error = err.Error()
			}
		} else if isNonResponsiveLLMAnswer(query, answer) {
			resp.LLMError = "LLM returned a non-responsive answer; showing extractive retrieved-context fallback"
			resp.Answer = buildExtractiveFallbackAnswer(query, resp.Results)
			resp.AnswerGenerated = false
		} else {
			resp.Answer = answer
			resp.AnswerGenerated = true
		}
	}
	if strings.TrimSpace(resp.Answer) == "" && len(resp.Results) > 0 {
		resp.Answer = buildExtractiveFallbackAnswer(query, resp.Results)
		resp.AnswerGenerated = false
	}
	return resp
}

func (s *Service) scoreRerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	if s.reranker != nil && s.reranker.Enabled() {
		scores, err := s.reranker.Score(ctx, query, docs)
		if err == nil {
			return scores, nil
		}
		if s.log != nil {
			s.log.Error("external reranker failed", "provider", s.reranker.Provider(), "model", s.reranker.Model(), "error", err.Error())
		}
		if s.reranker.FailClosed() {
			return nil, err
		}
	}
	return s.ai.Rerank(ctx, query, docs)
}

func (s *Service) buildQueryVariants(original, rewritten string, expansions []string) []string {
	variants := []string{strings.TrimSpace(rewritten)}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		for _, existing := range variants {
			if strings.EqualFold(existing, v) {
				return
			}
		}
		variants = append(variants, v)
	}
	if s.opts.UseMultiQuery {
		add(original)
		if len(expansions) > 0 {
			add(rewritten + " " + strings.Join(expansions, " "))
		}
		// A precision-biased variant keeps only high-signal terms and acronyms.
		terms := searchTokens(rewritten)
		if len(terms) > 0 {
			keep := make([]string, 0, len(terms))
			for _, t := range terms {
				if len(t) >= 4 || strings.Contains(t, "/") || strings.Contains(t, "&") || strings.Contains(t, "-") {
					keep = append(keep, t)
				}
			}
			add(strings.Join(keep, " "))
		}
	}
	limit := s.opts.QueryVariants
	if limit <= 0 {
		limit = 1
	}
	if len(variants) > limit {
		variants = variants[:limit]
	}
	return variants
}

func dedupeResults(in []model.Result) []model.Result {
	seen := map[string]model.Result{}
	order := []string{}
	for _, r := range in {
		key := resultKey(r)
		if existing, ok := seen[key]; ok {
			if r.Score > existing.Score {
				existing.Score = r.Score
			}
			if r.RawScore > existing.RawScore {
				existing.RawScore = r.RawScore
			}
			if r.SemanticScore > existing.SemanticScore {
				existing.SemanticScore = r.SemanticScore
			}
			mergeMetadata(&existing, r)
			seen[key] = existing
			continue
		}
		seen[key] = r
		order = append(order, key)
	}
	out := make([]model.Result, 0, len(order))
	for _, key := range order {
		out = append(out, seen[key])
	}
	return out
}

func (s *Service) generateAnswer(ctx context.Context, originalQuery, rewritten string, results []model.Result) (string, int, error) {
	if len(results) == 0 {
		return "", 0, fmt.Errorf("no retrieved context is available for LLM generation")
	}
	contextText, chars := buildLLMContext(results, s.opts.LLMMaxContextChunks, s.opts.LLMMaxContextChars)
	if strings.TrimSpace(contextText) == "" {
		return "", 0, fmt.Errorf("retrieved chunks were empty after context assembly")
	}
	prompt := buildRAGPrompt(originalQuery, rewritten, contextText)
	answer, err := s.llm.Generate(ctx, prompt)
	if err != nil {
		return "", chars, err
	}
	if strings.TrimSpace(answer) == "" {
		return "", chars, fmt.Errorf("LLM returned an empty answer")
	}
	return answer, chars, nil
}

func buildLLMContext(results []model.Result, maxChunks, maxChars int) (string, int) {
	if maxChunks <= 0 {
		maxChunks = 6
	}
	if maxChars <= 0 {
		maxChars = 12000
	}
	var b strings.Builder
	usedChars := 0
	usedChunks := 0
	for i, r := range results {
		if usedChunks >= maxChunks {
			break
		}
		text := strings.TrimSpace(r.Text)
		if text == "" {
			continue
		}
		source := fmt.Sprintf("[S%d filename=%s page=%d chunk=%d score=%.4f source_kind=%s]", i+1, safeInline(r.Filename), r.Page, r.ChunkIndex, r.CombinedScore, safeInline(r.SourceKind))
		entry := source + "\n" + text + "\n\n"
		if usedChars+len(entry) > maxChars {
			remaining := maxChars - usedChars
			if remaining < 400 {
				break
			}
			entry = entry[:remaining]
		}
		b.WriteString(entry)
		usedChars += len(entry)
		usedChunks++
	}
	return b.String(), usedChars
}

func buildRAGPrompt(originalQuery, rewritten, contextText string) string {
	return "You are a grounded RAG answer engine. The user has already asked a specific question. " +
		"Your task is to answer that exact question, not to summarize the document and not to ask for a task. " +
		"Use ONLY the retrieved Qdrant context below. If the answer is not explicitly supported, say: \"The ingested documents do not provide enough information to answer this.\" " +
		"Give a direct answer first in one or two sentences. Then add brief supporting evidence if useful. " +
		"Cite source labels inline like [S1] or [S2] for every factual claim. " +
		"Never say the user provided pages or that no question was asked. Never use outside knowledge.\n\n" +
		"QUESTION TO ANSWER:\n" + originalQuery + "\n\n" +
		"Expanded retrieval query, for reference only:\n" + rewritten + "\n\n" +
		"Retrieved Qdrant context:\n" + contextText + "\nDIRECT ANSWER:\n"
}

func isNonResponsiveLLMAnswer(query, answer string) bool {
	q := strings.TrimSpace(query)
	a := strings.ToLower(strings.TrimSpace(answer))
	if q == "" || a == "" {
		return false
	}
	badPhrases := []string{
		"without a specific question",
		"without a specific task",
		"you've provided",
		"you have provided",
		"i can provide some general information",
		"if you have a specific question",
		"if you have any specific question",
		"happy to help",
	}
	for _, phrase := range badPhrases {
		if strings.Contains(a, phrase) {
			return true
		}
	}
	qTokens := importantTokens(q)
	if len(qTokens) == 0 {
		return false
	}
	matched := 0
	aTokens := map[string]struct{}{}
	for _, tok := range searchTokens(answer) {
		aTokens[tok] = struct{}{}
	}
	for tok := range qTokens {
		if _, ok := aTokens[tok]; ok {
			matched++
		}
	}
	return matched == 0 && len(answer) > 180
}

func buildExtractiveFallbackAnswer(query string, results []model.Result) string {
	if len(results) == 0 {
		return "No retrieved context is available."
	}
	qTokens := importantTokens(query)
	bestIdx := 0
	bestScore := -1.0
	bestSentence := ""
	for i, r := range results {
		for _, sentence := range splitSentences(r.Text) {
			score := sentenceOverlapScore(sentence, qTokens)
			if score > bestScore {
				bestScore = score
				bestIdx = i
				bestSentence = strings.TrimSpace(sentence)
			}
		}
	}
	if bestSentence == "" {
		bestSentence = strings.TrimSpace(results[0].Text)
		if len(bestSentence) > 500 {
			bestSentence = bestSentence[:500] + "..."
		}
	}
	return fmt.Sprintf("I found relevant retrieved context, but the LLM did not return a focused answer. Best supporting excerpt: %s [S%d]", bestSentence, bestIdx+1)
}

func splitSentences(text string) []string {
	text = strings.ReplaceAll(text, "\n", " ")
	parts := strings.FieldsFunc(text, func(r rune) bool { return r == '.' || r == '?' || r == '!' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) >= 20 {
			out = append(out, p)
		}
	}
	if len(out) == 0 && strings.TrimSpace(text) != "" {
		out = append(out, strings.TrimSpace(text))
	}
	return out
}

func importantTokens(text string) map[string]struct{} {
	stop := map[string]struct{}{"what": {}, "which": {}, "who": {}, "whom": {}, "whose": {}, "where": {}, "when": {}, "why": {}, "how": {}, "the": {}, "a": {}, "an": {}, "is": {}, "are": {}, "was": {}, "were": {}, "to": {}, "of": {}, "in": {}, "on": {}, "for": {}, "and": {}, "or": {}, "does": {}, "do": {}, "did": {}, "that": {}, "this": {}, "it": {}, "by": {}, "from": {}, "with": {}, "publishes": {}, "publish": {}}
	out := map[string]struct{}{}
	for _, tok := range searchTokens(text) {
		if len(tok) < 3 {
			continue
		}
		if _, ok := stop[tok]; ok {
			continue
		}
		out[tok] = struct{}{}
	}
	return out
}

func sentenceOverlapScore(sentence string, qTokens map[string]struct{}) float64 {
	if len(qTokens) == 0 {
		return 0
	}
	sTokens := map[string]struct{}{}
	for _, tok := range searchTokens(sentence) {
		sTokens[tok] = struct{}{}
	}
	matches := 0
	for tok := range qTokens {
		if _, ok := sTokens[tok]; ok {
			matches++
		}
	}
	return float64(matches) / float64(len(qTokens))
}

func safeInline(v string) string {
	v = strings.TrimSpace(v)
	v = strings.ReplaceAll(v, "\n", " ")
	v = strings.ReplaceAll(v, "\r", " ")
	if v == "" {
		return "unknown"
	}
	return v
}

func retrievalMode(opts Options) string {
	prefix := "qdrant"
	if opts.UseMultiQuery {
		prefix = "multiquery+" + prefix
	}
	reranker := "local-rerank"
	if opts.RerankerEnabled {
		reranker = "external-rerank"
	}
	if opts.HybridSearch {
		return prefix + "+keyword+" + reranker + "+mmr"
	}
	return prefix + "+" + reranker + "+mmr"
}

func errorString(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

func mergeByIdentity(vectorResults, keywordResults []model.Result) []model.Result {
	seen := map[string]*model.Result{}
	order := []string{}
	add := func(r model.Result) {
		if len(strings.TrimSpace(r.Text)) < 10 {
			return
		}
		key := resultKey(r)
		if existing, ok := seen[key]; ok {
			if r.Source == "qdrant" || existing.Source == "" {
				existing.Source = mergeSource(existing.Source, r.Source)
			}
			if r.SemanticScore > existing.SemanticScore {
				existing.SemanticScore = r.SemanticScore
				existing.RawScore = r.RawScore
			}
			if r.Score > existing.KeywordScore && r.Source == "keyword" {
				existing.KeywordScore = r.Score
			}
			mergeMetadata(existing, r)
			return
		}
		copy := r
		if copy.Source == "qdrant" && copy.SemanticScore == 0 {
			copy.SemanticScore = copy.Score
		}
		if copy.Source == "keyword" {
			copy.KeywordScore = copy.Score
		}
		seen[key] = &copy
		order = append(order, key)
	}
	for _, r := range vectorResults {
		add(r)
	}
	for _, r := range keywordResults {
		add(r)
	}
	out := make([]model.Result, 0, len(order))
	for _, key := range order {
		out = append(out, *seen[key])
	}
	return out
}

func resultKey(r model.Result) string {
	if r.DocumentID != "" && r.IngestScope != "" && r.Page > 0 && r.ChunkIndex > 0 {
		return strings.Join([]string{r.DocumentID, r.IngestScope, intString(r.Page), intString(r.ChunkIndex)}, "|")
	}
	return strings.ToLower(strings.TrimSpace(r.Path)) + "|" + intString(r.Page) + "|" + intString(r.ChunkIndex) + "|" + fingerprintText(r.Text)
}

func mergeSource(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" || a == b {
		return a
	}
	if strings.Contains(a, b) {
		return a
	}
	return a + "+" + b
}

func mergeMetadata(dst *model.Result, src model.Result) {
	if dst.Filename == "" {
		dst.Filename = src.Filename
	}
	if dst.Path == "" {
		dst.Path = src.Path
	}
	if dst.Page == 0 {
		dst.Page = src.Page
	}
	if dst.ChunkIndex == 0 {
		dst.ChunkIndex = src.ChunkIndex
	}
	if dst.SourceKind == "" {
		dst.SourceKind = src.SourceKind
	}
	if dst.OriginalPath == "" {
		dst.OriginalPath = src.OriginalPath
	}
	if dst.ManifestPath == "" {
		dst.ManifestPath = src.ManifestPath
	}
	if dst.ImagePath == "" {
		dst.ImagePath = src.ImagePath
	}
	if dst.DocumentID == "" {
		dst.DocumentID = src.DocumentID
	}
	if dst.SourceChecksum == "" {
		dst.SourceChecksum = src.SourceChecksum
	}
	if dst.IngestScope == "" {
		dst.IngestScope = src.IngestScope
	}
	if dst.ObjectKey == "" {
		dst.ObjectKey = src.ObjectKey
	}
	if dst.ObjectURL == "" {
		dst.ObjectURL = src.ObjectURL
	}
}

func applyKeywordScores(query string, results []model.Result) {
	queryTokens := searchTokens(query)
	querySet := make(map[string]bool, len(queryTokens))
	for _, t := range queryTokens {
		querySet[t] = true
	}
	for i := range results {
		lex := lexicalScore(querySet, queryTokens, results[i].Text)
		if lex > results[i].KeywordScore {
			results[i].KeywordScore = lex
		}
	}
}

func normalizeComponentScores(results []model.Result) {
	var maxSemantic, maxKeyword float64
	for _, r := range results {
		if r.SemanticScore > maxSemantic {
			maxSemantic = r.SemanticScore
		}
		if r.KeywordScore > maxKeyword {
			maxKeyword = r.KeywordScore
		}
	}
	for i := range results {
		if maxSemantic > 0 {
			results[i].SemanticScore = results[i].SemanticScore / maxSemantic
		}
		if maxKeyword > 0 {
			results[i].KeywordScore = results[i].KeywordScore / maxKeyword
		}
	}
}

func preRerankScore(r model.Result, opts Options) float64 {
	return clamp01((opts.SemanticWeight*r.SemanticScore + opts.KeywordWeight*r.KeywordScore) / math.Max(0.0001, opts.SemanticWeight+opts.KeywordWeight))
}

func combinedScore(r model.Result, opts Options) float64 {
	den := opts.SemanticWeight + opts.KeywordWeight + opts.RerankWeight
	if den <= 0 {
		den = 1
	}
	return clamp01((opts.SemanticWeight*r.SemanticScore + opts.KeywordWeight*r.KeywordScore + opts.RerankWeight*r.RerankScore) / den)
}

func buildSources(results []model.Result) []SourceEvidence {
	sources := make([]SourceEvidence, 0, len(results))
	for _, r := range results {
		if !strings.Contains(r.Source, "qdrant") {
			continue
		}
		score := r.RawScore
		if score == 0 {
			score = r.Score
		}
		sources = append(sources, SourceEvidence{Filename: r.Filename, Path: r.Path, Page: r.Page, ChunkIndex: r.ChunkIndex, Score: score, SemanticScore: r.SemanticScore, KeywordScore: r.KeywordScore, RerankScore: r.RerankScore, CombinedScore: r.CombinedScore, Source: r.Source, SourceKind: r.SourceKind, OriginalPath: r.OriginalPath, ManifestPath: r.ManifestPath, ImagePath: r.ImagePath, DocumentID: r.DocumentID, SourceChecksum: r.SourceChecksum, IngestScope: r.IngestScope, ObjectKey: r.ObjectKey, ObjectURL: r.ObjectURL})
	}
	return sources
}

func postProcess(results []model.Result, opts Options) []model.Result {
	var out []model.Result
	for _, r := range results {
		if len(strings.TrimSpace(r.Text)) < 10 {
			continue
		}
		if opts.RequireContext && !strings.Contains(r.Source, "qdrant") {
			continue
		}
		if r.CombinedScore < opts.MinScore {
			continue
		}
		r.Confidence = r.CombinedScore
		switch {
		case strings.Contains(r.Source, "qdrant") && r.KeywordScore > 0.15:
			r.Explanation = "Retrieved from Qdrant and boosted by lexical/rerank evidence"
		case strings.Contains(r.Source, "qdrant"):
			r.Explanation = "Retrieved from Qdrant semantic context"
		case r.KeywordScore > 0:
			r.Explanation = "Retrieved from keyword fallback context"
		default:
			r.Explanation = "Retrieved context candidate"
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CombinedScore > out[j].CombinedScore })
	return out
}

func mmrSelect(results []model.Result, limit int, lambda float64) []model.Result {
	if limit <= 0 || len(results) <= limit {
		return results
	}
	selected := make([]model.Result, 0, limit)
	remaining := append([]model.Result(nil), results...)
	for len(selected) < limit && len(remaining) > 0 {
		bestIdx := 0
		bestScore := math.Inf(-1)
		for i, r := range remaining {
			sim := maxSimilarity(r, selected)
			score := lambda*r.CombinedScore - (1-lambda)*sim
			if score > bestScore {
				bestScore, bestIdx = score, i
			}
		}
		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}
	return selected
}

func maxSimilarity(r model.Result, selected []model.Result) float64 {
	if len(selected) == 0 {
		return 0
	}
	rTerms := makeSet(searchTokens(r.Text))
	max := 0.0
	for _, s := range selected {
		sim := jaccard(rTerms, makeSet(searchTokens(s.Text)))
		if sim > max {
			max = sim
		}
	}
	return max
}

func lexicalScore(querySet map[string]bool, queryTokens []string, doc string) float64 {
	docTokens := searchTokens(doc)
	if len(querySet) == 0 || len(docTokens) == 0 {
		return 0
	}
	docSet := makeSet(docTokens)
	coverage := 0.0
	for q := range querySet {
		if docSet[q] {
			coverage++
		}
	}
	coverage /= float64(len(querySet))
	phrase := 0.0
	docNorm := strings.Join(docTokens, " ")
	if len(queryTokens) > 1 && strings.Contains(docNorm, strings.Join(queryTokens, " ")) {
		phrase = 1
	}
	return clamp01(0.78*coverage + 0.22*phrase)
}

func searchTokens(text string) []string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '/' || r == '&' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	raw := strings.Fields(b.String())
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.Trim(t, ".-/&")
		if len(t) >= 2 && !smallStopWords[t] {
			out = append(out, t)
		}
	}
	return out
}

var smallStopWords = map[string]bool{"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "from": true, "what": true, "how": true, "why": true, "where": true, "when": true, "are": true, "was": true, "were": true}

func makeSet(tokens []string) map[string]bool {
	m := map[string]bool{}
	for _, t := range tokens {
		if t != "" {
			m[t] = true
		}
	}
	return m
}
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
func intString(v int) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	n := v
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
func fingerprintText(text string) string {
	t := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if len(t) > 160 {
		return t[:160]
	}
	return t
}
