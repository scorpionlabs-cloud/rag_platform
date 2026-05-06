package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"rag-unified-realtime/internal/ai"
	"rag-unified-realtime/internal/config"
	"rag-unified-realtime/internal/httpapi"
	"rag-unified-realtime/internal/ingestion"
	"rag-unified-realtime/internal/llm"
	"rag-unified-realtime/internal/logging"
	"rag-unified-realtime/internal/repository"
	"rag-unified-realtime/internal/rerank"
	"rag-unified-realtime/internal/search"
	"rag-unified-realtime/internal/storage"
	"rag-unified-realtime/internal/vector"
)

func main() {
	cfg := config.Load()
	log := logging.New()
	log.Info("configuration loaded", "data_dir", cfg.DataDir, "qdrant_url", cfg.QdrantURL, "collection", cfg.QdrantCollection, "vector_size", cfg.VectorSize, "rag_require_context", cfg.RAGRequireContext, "rag_hybrid_search", cfg.RAGHybridSearch, "rag_multi_query", cfg.RAGUseMultiQuery, "rag_vector_limit", cfg.RAGVectorLimit, "rag_rerank_limit", cfg.RAGRerankLimit, "llm_enabled", cfg.LLMEnabled, "llm_provider", cfg.LLMProvider, "llm_model", cfg.LLMModel, "reranker_enabled", cfg.RerankerEnabled, "reranker_provider", cfg.RerankerProvider, "reranker_model", cfg.RerankerModel, "object_storage_enabled", cfg.ObjectStorageEnabled, "object_storage_provider", cfg.ObjectStorageProvider, "object_storage_required", cfg.ObjectStorageRequired)

	repo := repository.NewMemoryRepository()
	aiProvider := ai.NewLocalProvider(cfg.VectorSize)
	llmClient := llm.New(llm.Config{Enabled: cfg.LLMEnabled, Provider: cfg.LLMProvider, BaseURL: cfg.LLMBaseURL, Model: cfg.LLMModel, APIKey: cfg.LLMAPIKey, Timeout: cfg.LLMTimeout, Temperature: cfg.LLMTemperature, MaxAnswerTokens: cfg.LLMMaxAnswerTokens})
	rerankerClient := rerank.New(rerank.Config{Enabled: cfg.RerankerEnabled, Provider: cfg.RerankerProvider, BaseURL: cfg.RerankerBaseURL, Model: cfg.RerankerModel, APIKey: cfg.RerankerAPIKey, Timeout: cfg.RerankerTimeout, TopN: cfg.RerankerTopN, Normalize: cfg.RerankerNormalize, FailClosed: cfg.RerankerFailClosed})
	qdrant := vector.NewQdrantClient(cfg.QdrantURL, cfg.QdrantCollection, cfg.VectorSize, vector.WithTimeout(cfg.QdrantTimeout), vector.WithUpsertBatchSize(cfg.QdrantUpsertBatchSize))

	var objectStore storage.Store = storage.DisabledStore{}
	if cfg.ObjectStorageEnabled {
		switch cfg.ObjectStorageProvider {
		case "", "oci-par":
			objectStore = storage.NewPARStore(cfg.ObjectStoragePARURL, cfg.ObjectStoragePrefix, cfg.ObjectStoragePublicBaseURL, cfg.ObjectStorageTimeout)
		default:
			log.Error("unsupported object storage provider; object storage disabled", "provider", cfg.ObjectStorageProvider)
		}
		if cfg.ObjectStorageRequired && !objectStore.Enabled() {
			log.Error("object storage is required but not configured", "provider", cfg.ObjectStorageProvider)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := qdrant.EnsureCollection(ctx); err != nil {
		log.Error("qdrant setup failed", "error", err.Error())
	} else {
		log.Info("qdrant collection ready", "collection", cfg.QdrantCollection)
	}

	ingestionSvc := ingestion.NewService(repo, aiProvider, qdrant, cfg.DataDir, log)
	ingestionSvc.ConfigureChunking(cfg.RAGChunkChars, cfg.RAGChunkOverlapChars)
	ingestionSvc.Start(context.Background(), cfg.Workers)

	searchSvc := search.NewServiceWithLLMAndReranker(repo, aiProvider, llmClient, rerankerClient, qdrant, log, search.Options{RequireContext: cfg.RAGRequireContext, HybridSearch: cfg.RAGHybridSearch, VectorLimit: cfg.RAGVectorLimit, KeywordLimit: cfg.RAGKeywordLimit, RerankLimit: cfg.RAGRerankLimit, FinalLimit: cfg.RAGFinalLimit, MinScore: cfg.RAGMinScore, SemanticWeight: cfg.RAGSemanticWeight, KeywordWeight: cfg.RAGKeywordWeight, RerankWeight: cfg.RAGRerankWeight, MMRLambda: cfg.RAGMMRLambda, UseMultiQuery: cfg.RAGUseMultiQuery, QueryVariants: cfg.RAGQueryVariants, ContextMaxChars: cfg.RAGContextMaxChars, LLMRequireContext: cfg.LLMRequireContext, LLMMaxContextChunks: cfg.LLMMaxContextChunks, LLMMaxContextChars: cfg.LLMMaxContextChars, RerankerEnabled: cfg.RerankerEnabled, RerankerProvider: cfg.RerankerProvider, RerankerModel: cfg.RerankerModel, RerankerFailClosed: cfg.RerankerFailClosed, DataDir: cfg.DataDir})
	wsHub := httpapi.NewHub(log)
	handler := httpapi.NewHandler(repo, ingestionSvc, searchSvc, cfg.DataDir, log, wsHub, objectStore, cfg.ObjectStorageRequired)

	mux := http.NewServeMux()
	handler.Register(mux)
	mux.Handle("/", noStore(http.FileServer(http.Dir("./web"))))

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Info("api listening", "addr", addr)
	if err := http.ListenAndServe(addr, httpapi.WithCORS(mux)); err != nil {
		log.Error("server stopped", "error", err.Error())
	}
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
