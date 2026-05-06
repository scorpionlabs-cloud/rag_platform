package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port                       int
	DataDir                    string
	QdrantURL                  string
	QdrantCollection           string
	VectorSize                 int
	Workers                    int
	RAGRequireContext          bool
	RAGHybridSearch            bool
	RAGVectorLimit             int
	RAGKeywordLimit            int
	RAGRerankLimit             int
	RAGFinalLimit              int
	RAGMinScore                float64
	RAGSemanticWeight          float64
	RAGKeywordWeight           float64
	RAGRerankWeight            float64
	RAGMMRLambda               float64
	RAGChunkChars              int
	RAGChunkOverlapChars       int
	RAGQueryVariants           int
	RAGUseMultiQuery           bool
	RAGContextMaxChars         int
	RerankerEnabled            bool
	RerankerProvider           string
	RerankerBaseURL            string
	RerankerModel              string
	RerankerAPIKey             string
	RerankerTimeout            time.Duration
	RerankerTopN               int
	RerankerNormalize          bool
	RerankerFailClosed         bool
	QdrantTimeout              time.Duration
	QdrantUpsertBatchSize      int
	ObjectStorageEnabled       bool
	ObjectStorageRequired      bool
	ObjectStorageProvider      string
	ObjectStoragePARURL        string
	ObjectStoragePrefix        string
	ObjectStoragePublicBaseURL string
	ObjectStorageTimeout       time.Duration
	LLMEnabled                 bool
	LLMProvider                string
	LLMBaseURL                 string
	LLMModel                   string
	LLMAPIKey                  string
	LLMTimeout                 time.Duration
	LLMRequireContext          bool
	LLMTemperature             float64
	LLMMaxContextChunks        int
	LLMMaxContextChars         int
	LLMMaxAnswerTokens         int
}

// Load loads config from config.yaml first, then overlays environment variables.
// The parser intentionally supports a simple YAML subset: nested sections with scalar values.
// Example: rag.require_context can be written as:
// rag:\n  require_context: true
func Load() Config {
	cfg := defaults()
	path := firstNonEmpty(os.Getenv("CONFIG_PATH"), "config.yaml")
	if fileCfg, err := loadYAMLSubset(path); err == nil {
		applyMap(&cfg, fileCfg)
	} else if os.Getenv("CONFIG_PATH") != "" {
		fmt.Fprintf(os.Stderr, "config: unable to read %s: %v\n", path, err)
	}
	applyEnv(&cfg)
	return cfg
}

func defaults() Config {
	return Config{
		Port:                       8080,
		DataDir:                    "/app/data",
		QdrantURL:                  "http://qdrant:6333",
		QdrantCollection:           "rag",
		VectorSize:                 384,
		Workers:                    4,
		RAGRequireContext:          true,
		RAGHybridSearch:            true,
		RAGVectorLimit:             40,
		RAGKeywordLimit:            40,
		RAGRerankLimit:             25,
		RAGFinalLimit:              8,
		RAGMinScore:                0.05,
		RAGSemanticWeight:          0.55,
		RAGKeywordWeight:           0.20,
		RAGRerankWeight:            0.25,
		RAGMMRLambda:               0.82,
		RAGChunkChars:              1200,
		RAGChunkOverlapChars:       180,
		RAGQueryVariants:           3,
		RAGUseMultiQuery:           true,
		RAGContextMaxChars:         16000,
		RerankerEnabled:            false,
		RerankerProvider:           "bge",
		RerankerBaseURL:            "http://bge-reranker:8080",
		RerankerModel:              "BAAI/bge-reranker-large",
		RerankerAPIKey:             "",
		RerankerTimeout:            60 * time.Second,
		RerankerTopN:               25,
		RerankerNormalize:          true,
		RerankerFailClosed:         false,
		QdrantTimeout:              15 * time.Second,
		QdrantUpsertBatchSize:      64,
		ObjectStorageEnabled:       false,
		ObjectStorageRequired:      false,
		ObjectStorageProvider:      "oci-par",
		ObjectStoragePARURL:        "",
		ObjectStoragePrefix:        "rag-unified",
		ObjectStoragePublicBaseURL: "",
		ObjectStorageTimeout:       120 * time.Second,
		LLMEnabled:                 false,
		LLMProvider:                "ollama",
		LLMBaseURL:                 "http://ollama:11434",
		LLMModel:                   "llama3.1:8b",
		LLMAPIKey:                  "",
		LLMTimeout:                 120 * time.Second,
		LLMRequireContext:          true,
		LLMTemperature:             0.1,
		LLMMaxContextChunks:        6,
		LLMMaxContextChars:         12000,
		LLMMaxAnswerTokens:         900,
	}
}

func applyEnv(c *Config) {
	c.Port = getInt("PORT", c.Port)
	c.DataDir = getStr("DATA_DIR", c.DataDir)
	c.QdrantURL = getStr("QDRANT_URL", c.QdrantURL)
	c.QdrantCollection = getStr("QDRANT_COLLECTION", c.QdrantCollection)
	c.VectorSize = getInt("VECTOR_SIZE", c.VectorSize)
	c.Workers = getInt("WORKERS", c.Workers)
	c.RAGRequireContext = getBool("RAG_REQUIRE_CONTEXT", c.RAGRequireContext)
	c.RAGHybridSearch = getBool("RAG_HYBRID_SEARCH", c.RAGHybridSearch)
	c.RAGVectorLimit = getInt("RAG_VECTOR_LIMIT", c.RAGVectorLimit)
	c.RAGKeywordLimit = getInt("RAG_KEYWORD_LIMIT", c.RAGKeywordLimit)
	c.RAGRerankLimit = getInt("RAG_RERANK_LIMIT", c.RAGRerankLimit)
	c.RAGFinalLimit = getInt("RAG_FINAL_LIMIT", c.RAGFinalLimit)
	c.RAGMinScore = getFloat("RAG_MIN_SCORE", c.RAGMinScore)
	c.RAGSemanticWeight = getFloat("RAG_SEMANTIC_WEIGHT", c.RAGSemanticWeight)
	c.RAGKeywordWeight = getFloat("RAG_KEYWORD_WEIGHT", c.RAGKeywordWeight)
	c.RAGRerankWeight = getFloat("RAG_RERANK_WEIGHT", c.RAGRerankWeight)
	c.RAGMMRLambda = getFloat("RAG_MMR_LAMBDA", c.RAGMMRLambda)
	c.RAGChunkChars = getInt("RAG_CHUNK_CHARS", c.RAGChunkChars)
	c.RAGChunkOverlapChars = getInt("RAG_CHUNK_OVERLAP_CHARS", c.RAGChunkOverlapChars)
	c.RAGQueryVariants = getInt("RAG_QUERY_VARIANTS", c.RAGQueryVariants)
	c.RAGUseMultiQuery = getBool("RAG_MULTI_QUERY", c.RAGUseMultiQuery)
	c.RAGContextMaxChars = getInt("RAG_CONTEXT_MAX_CHARS", c.RAGContextMaxChars)
	c.RerankerEnabled = getBool("RERANKER_ENABLED", c.RerankerEnabled)
	c.RerankerProvider = getStr("RERANKER_PROVIDER", c.RerankerProvider)
	c.RerankerBaseURL = getStr("RERANKER_BASE_URL", c.RerankerBaseURL)
	c.RerankerModel = getStr("RERANKER_MODEL", c.RerankerModel)
	c.RerankerAPIKey = getStr("RERANKER_API_KEY", c.RerankerAPIKey)
	c.RerankerTimeout = time.Duration(getInt("RERANKER_TIMEOUT_SECONDS", int(c.RerankerTimeout/time.Second))) * time.Second
	c.RerankerTopN = getInt("RERANKER_TOP_N", c.RerankerTopN)
	c.RerankerNormalize = getBool("RERANKER_NORMALIZE", c.RerankerNormalize)
	c.RerankerFailClosed = getBool("RERANKER_FAIL_CLOSED", c.RerankerFailClosed)
	c.QdrantTimeout = time.Duration(getInt("QDRANT_TIMEOUT_SECONDS", int(c.QdrantTimeout/time.Second))) * time.Second
	c.QdrantUpsertBatchSize = getInt("QDRANT_UPSERT_BATCH_SIZE", c.QdrantUpsertBatchSize)
	c.ObjectStorageEnabled = getBool("OBJECT_STORAGE_ENABLED", c.ObjectStorageEnabled)
	c.ObjectStorageRequired = getBool("OBJECT_STORAGE_REQUIRED", c.ObjectStorageRequired)
	c.ObjectStorageProvider = getStr("OBJECT_STORAGE_PROVIDER", c.ObjectStorageProvider)
	c.ObjectStoragePARURL = getStr("OBJECT_STORAGE_PAR_URL", c.ObjectStoragePARURL)
	c.ObjectStoragePrefix = getStr("OBJECT_STORAGE_PREFIX", c.ObjectStoragePrefix)
	c.ObjectStoragePublicBaseURL = getStr("OBJECT_STORAGE_PUBLIC_BASE_URL", c.ObjectStoragePublicBaseURL)
	c.ObjectStorageTimeout = time.Duration(getInt("OBJECT_STORAGE_TIMEOUT_SECONDS", int(c.ObjectStorageTimeout/time.Second))) * time.Second
	c.LLMEnabled = getBool("LLM_ENABLED", c.LLMEnabled)
	c.LLMProvider = getStr("LLM_PROVIDER", c.LLMProvider)
	c.LLMBaseURL = getStr("LLM_BASE_URL", c.LLMBaseURL)
	c.LLMModel = getStr("LLM_MODEL", c.LLMModel)
	c.LLMAPIKey = getStr("LLM_API_KEY", c.LLMAPIKey)
	c.LLMTimeout = time.Duration(getInt("LLM_TIMEOUT_SECONDS", int(c.LLMTimeout/time.Second))) * time.Second
	c.LLMRequireContext = getBool("LLM_REQUIRE_CONTEXT", c.LLMRequireContext)
	c.LLMTemperature = getFloat("LLM_TEMPERATURE", c.LLMTemperature)
	c.LLMMaxContextChunks = getInt("LLM_MAX_CONTEXT_CHUNKS", c.LLMMaxContextChunks)
	c.LLMMaxContextChars = getInt("LLM_MAX_CONTEXT_CHARS", c.LLMMaxContextChars)
	c.LLMMaxAnswerTokens = getInt("LLM_MAX_ANSWER_TOKENS", c.LLMMaxAnswerTokens)
}

func applyMap(c *Config, m map[string]string) {
	get := func(keys ...string) (string, bool) {
		for _, k := range keys {
			if v, ok := m[strings.ToLower(k)]; ok {
				return v, true
			}
		}
		return "", false
	}
	setStr := func(dst *string, keys ...string) {
		if v, ok := get(keys...); ok {
			*dst = v
		}
	}
	setInt := func(dst *int, keys ...string) {
		if v, ok := get(keys...); ok {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}
	setFloat := func(dst *float64, keys ...string) {
		if v, ok := get(keys...); ok {
			if n, err := strconv.ParseFloat(v, 64); err == nil {
				*dst = n
			}
		}
	}
	setBool := func(dst *bool, keys ...string) {
		if v, ok := get(keys...); ok {
			*dst = parseBool(v, *dst)
		}
	}
	setDuration := func(dst *time.Duration, keys ...string) {
		if v, ok := get(keys...); ok {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = time.Duration(n) * time.Second
			}
		}
	}

	setInt(&c.Port, "server.port", "port")
	setStr(&c.DataDir, "server.data_dir", "data_dir")
	setStr(&c.QdrantURL, "qdrant.url")
	setStr(&c.QdrantCollection, "qdrant.collection")
	setInt(&c.VectorSize, "qdrant.vector_size", "vector_size")
	setDuration(&c.QdrantTimeout, "qdrant.timeout_seconds")
	setInt(&c.QdrantUpsertBatchSize, "qdrant.upsert_batch_size")
	setInt(&c.Workers, "ingestion.workers", "workers")
	setInt(&c.RAGChunkChars, "ingestion.chunk_chars", "rag.chunk_chars")
	setInt(&c.RAGChunkOverlapChars, "ingestion.chunk_overlap_chars", "rag.chunk_overlap_chars")
	setBool(&c.RAGRequireContext, "rag.require_context")
	setBool(&c.RAGHybridSearch, "rag.hybrid_search")
	setInt(&c.RAGVectorLimit, "rag.vector_limit")
	setInt(&c.RAGKeywordLimit, "rag.keyword_limit")
	setInt(&c.RAGRerankLimit, "rag.rerank_limit")
	setInt(&c.RAGFinalLimit, "rag.final_limit")
	setFloat(&c.RAGMinScore, "rag.min_score")
	setFloat(&c.RAGSemanticWeight, "rag.semantic_weight")
	setFloat(&c.RAGKeywordWeight, "rag.keyword_weight")
	setFloat(&c.RAGRerankWeight, "rag.rerank_weight")
	setFloat(&c.RAGMMRLambda, "rag.mmr_lambda")
	setBool(&c.RAGUseMultiQuery, "rag.multi_query")
	setInt(&c.RAGQueryVariants, "rag.query_variants")
	setInt(&c.RAGContextMaxChars, "rag.context_max_chars")
	setBool(&c.RerankerEnabled, "reranker.enabled")
	setStr(&c.RerankerProvider, "reranker.provider")
	setStr(&c.RerankerBaseURL, "reranker.base_url")
	setStr(&c.RerankerModel, "reranker.model")
	setStr(&c.RerankerAPIKey, "reranker.api_key")
	setDuration(&c.RerankerTimeout, "reranker.timeout_seconds")
	setInt(&c.RerankerTopN, "reranker.top_n")
	setBool(&c.RerankerNormalize, "reranker.normalize")
	setBool(&c.RerankerFailClosed, "reranker.fail_closed")
	setBool(&c.LLMEnabled, "llm.enabled")
	setStr(&c.LLMProvider, "llm.provider")
	setStr(&c.LLMBaseURL, "llm.base_url")
	setStr(&c.LLMModel, "llm.model")
	setStr(&c.LLMAPIKey, "llm.api_key")
	setDuration(&c.LLMTimeout, "llm.timeout_seconds")
	setBool(&c.LLMRequireContext, "llm.require_context")
	setFloat(&c.LLMTemperature, "llm.temperature")
	setInt(&c.LLMMaxContextChunks, "llm.max_context_chunks")
	setInt(&c.LLMMaxContextChars, "llm.max_context_chars")
	setInt(&c.LLMMaxAnswerTokens, "llm.max_answer_tokens")
	setBool(&c.ObjectStorageEnabled, "object_storage.enabled")
	setBool(&c.ObjectStorageRequired, "object_storage.required")
	setStr(&c.ObjectStorageProvider, "object_storage.provider")
	setStr(&c.ObjectStoragePARURL, "object_storage.par_url")
	setStr(&c.ObjectStoragePrefix, "object_storage.prefix")
	setStr(&c.ObjectStoragePublicBaseURL, "object_storage.public_base_url")
	setDuration(&c.ObjectStorageTimeout, "object_storage.timeout_seconds")
}

func loadYAMLSubset(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sections := map[int]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := stripComment(s.Text())
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		level := indent / 2
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}
		for k := range sections {
			if k >= level {
				delete(sections, k)
			}
		}
		if val == "" {
			sections[level] = key
			continue
		}
		prefix := []string{}
		for i := 0; i < level; i++ {
			if sections[i] != "" {
				prefix = append(prefix, sections[i])
			}
		}
		prefix = append(prefix, key)
		out[strings.ToLower(strings.Join(prefix, "."))] = unquote(val)
	}
	return out, s.Err()
}

func stripComment(line string) string {
	inQuote := rune(0)
	for i, r := range line {
		if r == '\'' || r == '"' {
			if inQuote == 0 {
				inQuote = r
			} else if inQuote == r {
				inQuote = 0
			}
		}
		if r == '#' && inQuote == 0 {
			return line[:i]
		}
	}
	return line
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

func getStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func getInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return fallback
}
func getFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			return parsed
		}
	}
	return fallback
}
func getBool(key string, fallback bool) bool { return parseBool(os.Getenv(key), fallback) }
func parseBool(v string, fallback bool) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return fallback
	}
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
