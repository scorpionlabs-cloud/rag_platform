import os
from typing import List, Optional

from fastapi import FastAPI
from pydantic import BaseModel, Field
from FlagEmbedding import FlagReranker

MODEL_NAME = os.getenv("RERANKER_MODEL", "BAAI/bge-reranker-base")
MAX_LENGTH = int(os.getenv("RERANKER_MAX_LENGTH", "512"))

app = FastAPI(title="BGE CPU Reranker")
reranker = FlagReranker(MODEL_NAME, use_fp16=False)

class RerankRequest(BaseModel):
    query: str
    texts: List[str]
    top_n: Optional[int] = Field(default=None)

@app.get("/health")
def health():
    return {"status": "ok", "model": MODEL_NAME, "device": "cpu"}

@app.post("/rerank")
def rerank(req: RerankRequest):
    pairs = [[req.query, text] for text in req.texts]
    scores = reranker.compute_score(pairs, normalize=True, max_length=MAX_LENGTH)
    if not isinstance(scores, list):
        scores = [scores]
    results = [{"index": i, "score": float(score), "text": req.texts[i]} for i, score in enumerate(scores)]
    results.sort(key=lambda x: x["score"], reverse=True)
    if req.top_n is not None and req.top_n > 0:
        results = results[: req.top_n]
    return {"model": MODEL_NAME, "results": results}

@app.post("/v1/rerank")
def rerank_v1(req: RerankRequest):
    return rerank(req)
