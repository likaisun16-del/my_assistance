# rag — 检索增强生成（Retrieval-Augmented Generation）
# 包含：文本分割器、TF 词袋向量存储、余弦相似度检索、RAG 引擎
import math
import logging
from dataclasses import dataclass, field
from typing import List, Optional, Callable

from config.config import APIConfig
from internal.infra.infra import Infrastructure

logger = logging.getLogger(__name__)


# ─────────────────────────────── 文本分割 ────────────────────────────────

@dataclass
class Chunk:
    id: int
    content: str


class TextSplitter:
    """按字符窗口将长文本切成有重叠的 Chunk"""

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
            chunk_text = text[i:end]
            chunks.append(Chunk(id=idx, content=chunk_text))
            idx += 1
            if end >= len(text):
                break
        return chunks


# ─────────────────────────────── 向量存储 ────────────────────────────────

class VectorStore:
    """基于 TF 词袋的内存向量库"""

    def __init__(self):
        self.chunks: List[Chunk] = []
        self._vectors: List[List[float]] = []
        self._vocab_map: dict = {}
        self._vocab: List[str] = []

    def _build_vocab(self, chunks: List[Chunk]):
        for c in chunks:
            for t in self._tokenize(c.content):
                if t not in self._vocab_map:
                    self._vocab_map[t] = len(self._vocab)
                    self._vocab.append(t)

    def _text_to_vector(self, text: str) -> List[float]:
        vec = [0.0] * len(self._vocab_map)
        for t in self._tokenize(text):
            idx = self._vocab_map.get(t)
            if idx is not None and idx < len(vec):
                vec[idx] += 1.0
        return vec

    def index(self, chunks: List[Chunk]):
        self.chunks = chunks
        self._build_vocab(chunks)
        self._vectors = [self._text_to_vector(c.content) for c in chunks]

    def search(self, query: str, top_k: int = 3) -> List["SearchResult"]:
        qv = self._text_to_vector(query)
        results = [SearchResult(chunk=c, similarity=self._cosine(qv, cv)) 
                   for c, cv in zip(self.chunks, self._vectors)]
        results.sort(key=lambda r: r.similarity, reverse=True)
        return results[:top_k]

    def _tokenize(self, text: str) -> List[str]:
        tokens: List[str] = []
        word = ""
        for ch in text:
            cp = ord(ch)
            if 0x4E00 <= cp <= 0x9FFF:
                if word:
                    tokens.append(word.lower())
                    word = ""
                tokens.append(ch)
            elif ch.isalpha() or ch.isdigit():
                word += ch
            else:
                if word:
                    tokens.append(word.lower())
                    word = ""
        if word:
            tokens.append(word.lower())
        return tokens

    def _cosine(self, a: List[float], b: List[float]) -> float:
        if len(a) != len(b):
            return 0.0
        dot = sum(x * y for x, y in zip(a, b))
        na = math.sqrt(sum(x * x for x in a))
        nb = math.sqrt(sum(x * x for x in b))
        if na == 0 or nb == 0:
            return 0.0
        return dot / (na * nb)


@dataclass
class SearchResult:
    chunk: Chunk
    similarity: float


# ─────────────────────────────── RAG 引擎 ────────────────────────────────

class Engine:
    """整合文本分割、向量检索与答案生成"""

    def __init__(self, cfg: APIConfig, inf: Infrastructure):
        self.cfg = cfg
        self.store = VectorStore()
        self.splitter = TextSplitter(cfg.chunk_size, cfg.chunk_overlap)
        self.loaded: bool = False
        self.inf = inf
        self._generate_fn: Optional[Callable] = None

    def set_generate_fn(self, fn: Callable):
        """注入 LLM 调用回调，供 Query 合成答案"""
        self._generate_fn = fn

    def ingest(self, doc: str) -> int:
        """将文档切分并建立向量索引，返回切片数量"""
        chunks = self.splitter.split(doc)
        self.store.index(chunks)
        self.loaded = True
        self.inf.publish_event("rag.ingest", f'{{"chunk_count":{len(chunks)}}}')
        return len(chunks)

    def query(self, question: str) -> tuple:
        """检索知识库并返回 (answer, results)"""
        if not self.loaded:
            return "知识库为空，请先上传文档。", []
        results = self.store.search(question, self.cfg.top_k)
        parts = [r.chunk.content for r in results if r.similarity > 0.01]
        context = "\n\n".join(parts)
        if not context:
            return "知识库中未找到相关内容。", results
        if self._generate_fn:
            system_prompt = "你是一个基于知识库回答问题的助手。请仅根据提供的上下文内容回答问题，不要编造信息。如果上下文不足以回答，请说明。"
            user_msg = f"上下文：\n{context}\n\n问题：{question}"
            answer = self._generate_fn(system_prompt, user_msg)
            return answer, results
        return f"【知识库检索结果】\n{context}", results

    def get_chunks(self) -> List[Chunk]:
        return self.store.chunks
