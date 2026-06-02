# rag — 检索增强生成（Retrieval-Augmented Generation）
import json
import logging
from dataclasses import dataclass
from typing import Callable, List, Optional

from config.config import APIConfig
from internal.infra.infra import Infrastructure
from internal.llm.llm import Client as LLMClient

logger = logging.getLogger(__name__)


@dataclass
class Chunk:
    id: int
    content: str


@dataclass
class SearchResult:
    chunk: Chunk
    similarity: float


class TextSplitter:
    """按字符窗口切分文本。"""

    def __init__(self, chunk_size: int = 200, overlap: int = 50):
        self.chunk_size = chunk_size
        self.overlap = overlap

    def split(self, text: str) -> List[Chunk]:
        step = self.chunk_size - self.overlap
        if step <= 0:
            step = self.chunk_size
        chunks: List[Chunk] = []
        idx = 0
        for i in range(0, len(text), step):
            end = i + self.chunk_size
            chunks.append(Chunk(id=idx, content=text[i:end]))
            idx += 1
            if end >= len(text):
                break
        return chunks


class Engine:
    """Python 版 RAG 引擎：切分、入库、检索、生成。"""

    def __init__(self, cfg: APIConfig, inf: Infrastructure):
        self.cfg = cfg
        self.inf = inf
        self.splitter = TextSplitter(cfg.chunk_size, cfg.chunk_overlap)
        self.loaded = False
        self._generate_fn: Optional[Callable[[str, str], str]] = None
        self._llm = LLMClient(cfg)
        self._check_existing_chunks()

    def set_generate_fn(self, fn: Callable[[str, str], str]):
        self._generate_fn = fn

    def _check_existing_chunks(self):
        try:
            if self.inf.ready.postgresql == "connected" and self.inf.count_rag_chunks() > 0:
                self.loaded = True
                logger.info("✅ 检测到知识库中已有文档")
        except Exception as e:
            logger.error("检查知识库文档失败: %s", e)

    def ingest(self, doc: str) -> int:
        chunks = self.splitter.split(doc)
        if not chunks:
            return 0

        contents = [c.content for c in chunks]
        embeddings = [self._llm.embed(content) for content in contents]

        pg_ids: List[int] = []
        doc_hash = f"doc_{abs(hash(doc))}"
        for i, chunk in enumerate(chunks):
            pg_id = self.inf.save_rag_chunk(doc_hash, i, chunk.content, json.dumps(embeddings[i]))
            if pg_id > 0:
                pg_ids.append(pg_id)

        if self.inf.ready.milvus == "connected" and pg_ids:
            self.inf.insert_rag_chunks(pg_ids, contents, embeddings)

        for i, pg_id in enumerate(pg_ids):
            self.inf.index_rag_chunk(pg_id, chunks[i].content, doc_hash, i)

        self.loaded = True
        self.inf.publish_event("rag.ingest", json.dumps({"chunk_count": len(chunks)}))
        return len(chunks)

    def query(self, question: str) -> tuple:
        if self.inf.ready.postgresql != "connected":
            return "PostgreSQL 未连接，无法查询知识库。", []

        query_emb = self._llm.embed(question)

        milvus_results = []
        if self.inf.ready.milvus == "connected":
            milvus_results = self.inf.milvus_search_with_scores("rag_embeddings", query_emb, self.cfg.top_k)

        es_results = []
        if self.inf.ready.elasticsearch == "connected":
            es_results = self.inf.search_rag_chunks(question, self.cfg.top_k)

        all_results = {}
        for r in milvus_results:
            pg_id = r.get("pg_id")
            if pg_id:
                all_results[pg_id] = {"content": r.get("content", ""), "score": r.get("score", 0.0), "source": "milvus"}

        for r in es_results:
            pg_id = r.get("pg_id")
            if not pg_id:
                continue
            if pg_id in all_results:
                all_results[pg_id]["score"] = (all_results[pg_id]["score"] + r.get("score", 0.0) * 0.5) / 1.5
            else:
                all_results[pg_id] = {"content": r.get("content", ""), "score": r.get("score", 0.0), "source": "es"}

        sorted_results = sorted(all_results.values(), key=lambda x: x["score"], reverse=True)
        top_results = sorted_results[: self.cfg.top_k]
        if not top_results:
            return "知识库中未找到相关内容。", []

        context = "\n\n".join([r["content"] for r in top_results if r["score"] > 0.01])
        if not context:
            return "知识库中未找到相关内容。", []

        if self._generate_fn:
            system_prompt = "你是一个基于知识库回答问题的助手。请仅根据提供的上下文内容回答问题，不要编造信息。如果上下文不足以回答，请说明。"
            user_msg = f"上下文：\n{context}\n\n问题：{question}"
            return self._generate_fn(system_prompt, user_msg), top_results

        return f"【知识库检索结果】\n{context}", top_results

    def get_chunks(self) -> List[Chunk]:
        return []
