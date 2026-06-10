# memory — 三层记忆系统（短期 / 长期 / 用户偏好）
import json
import logging
import math
import re
import time
from dataclasses import dataclass
from typing import Any, Dict, List, Optional

from config.config import APIConfig
from internal.infra.infra import Infrastructure

logger = logging.getLogger(__name__)


@dataclass
class Item:
    content: str
    importance: float = 0.5
    embedding: Optional[List[float]] = None


class ShortTerm:
    """短期记忆 - 滑动窗口存储最近 N 轮对话。"""

    def __init__(self, max_turns: int = 10):
        self.max_turns = max(1, max_turns)
        self.messages: List[Dict[str, str]] = []

    def add(self, role: str, content: str):
        self.messages.append({"role": role, "content": content})
        while len(self.messages) > self.max_turns * 2:
            self.messages.pop(0)

    def get(self) -> List[Dict[str, str]]:
        return self.messages.copy()

    def clear(self):
        self.messages = []

    def count(self) -> int:
        return len(self.messages)


def _tokenize_zh(text: str) -> List[str]:
    """中英文混合分词：中文按字、英文/数字按词。"""
    tokens: List[str] = []
    word = ""
    for ch in text:
        cp = ord(ch)
        if 0x4E00 <= cp <= 0x9FFF:
            if word:
                tokens.append(word.lower())
                word = ""
            tokens.append(ch)
        elif ch.isalnum():
            word += ch
        else:
            if word:
                tokens.append(word.lower())
                word = ""
    if word:
        tokens.append(word.lower())
    return tokens


class LongTerm:
    """长期记忆 - 基于 embedding 的语义记忆。"""

    def __init__(self, cfg: APIConfig, inf: Infrastructure):
        self.cfg = cfg
        self.inf = inf
        self.items: List[Item] = []
        self._embed_fn: Optional[Any] = None
        self._last_consolidate_ts = 0.0
        self._items_since_last = 0

    def set_embed_fn(self, fn):
        self._embed_fn = fn

    def load_from_storage(self):
        rows = self.inf.load_long_term_items()
        self.items = [Item(content=r.content, importance=r.importance, embedding=r.embedding) for r in rows]
        logger.info("✅ 从存储恢复了 %d 条长期记忆", len(self.items))

    def add(self, content: str, importance: float = 0.5):
        embedding = None
        if self._embed_fn:
            try:
                embedding = self._embed_fn(content)
            except Exception as e:
                logger.warning("⚠️  向量化失败: %s", e)

        item = Item(content=content, importance=importance, embedding=embedding)
        self.items.append(item)
        self._items_since_last += 1
        emb_json = json.dumps(embedding) if embedding else "null"
        self.inf.save_long_term_item(content, importance, emb_json)

    def recall(self, query: str, top_k: int = 3) -> List[Item]:
        if not self.items:
            return []

        query_emb = None
        if self._embed_fn:
            try:
                query_emb = self._embed_fn(query)
            except Exception as e:
                logger.warning("⚠️  查询向量化失败: %s", e)
                query_emb = None

        if not query_emb:
            return self.items[:top_k]

        scored: List[tuple] = []
        for item in self.items:
            if item.embedding:
                sim = self._cosine_similarity(query_emb, item.embedding)
                score = sim * 0.7 + item.importance * 0.3
                scored.append((item, score))

        scored.sort(key=lambda x: x[1], reverse=True)
        return [item for item, score in scored[:top_k] if score >= 0.4]

    def _cosine_similarity(self, a: List[float], b: List[float]) -> float:
        if len(a) != len(b):
            return 0.0
        dot = sum(x * y for x, y in zip(a, b))
        na = math.sqrt(sum(x * x for x in a))
        nb = math.sqrt(sum(x * x for x in b))
        if na == 0 or nb == 0:
            return 0.0
        return dot / (na * nb)

    def need_consolidation(self) -> bool:
        return self._items_since_last >= max(1, self.cfg.memory_consolidation_trigger)

    def consolidate(self):
        """合并/去重 + 衰减 + TTL 淘汰。仅在 need_consolidation 为真时调用。"""
        if not self.items:
            return

        logger.info("🔄 开始记忆合并...")
        now = time.time()
        elapsed_days = (now - self._last_consolidate_ts) / 86400.0 if self._last_consolidate_ts > 0 else 1.0
        self._last_consolidate_ts = now
        self._items_since_last = 0

        # 1) 重复项合并：保留 importance 较高者
        to_remove = set()
        for i in range(len(self.items)):
            if i in to_remove:
                continue
            for j in range(i + 1, len(self.items)):
                if j in to_remove:
                    continue
                sim = self._compute_similarity(self.items[i].content, self.items[j].content)
                if sim >= self.cfg.memory_consolidation_dedup:
                    drop = i if self.items[i].importance < self.items[j].importance else j
                    to_remove.add(drop)
        self.items = [item for i, item in enumerate(self.items) if i not in to_remove]

        # 2) 按时间间隔衰减（避免每轮对话都被衰减一次）
        decay = self.cfg.memory_consolidation_decay_rate ** max(elapsed_days, 0.0)
        for item in self.items:
            item.importance *= decay

        # 3) TTL 淘汰
        self.items = [item for item in self.items if item.importance >= self.cfg.memory_consolidation_min_import]
        logger.info("✅ 记忆合并完成，剩余 %d 条", len(self.items))

    def _compute_similarity(self, a: str, b: str) -> float:
        """中英文 Jaccard 相似度。"""
        tokens_a = set(_tokenize_zh(a))
        tokens_b = set(_tokenize_zh(b))
        if not tokens_a or not tokens_b:
            return 0.0
        return len(tokens_a & tokens_b) / len(tokens_a | tokens_b)


class Preference:
    """用户偏好管理。"""

    def __init__(self, user_id: str, inf: Infrastructure):
        self.user_id = user_id
        self.inf = inf
        self.preferences: Dict[str, str] = {}
        self.load_from_storage()

    @property
    def data(self) -> Dict[str, str]:
        return self.preferences

    def load_from_storage(self):
        self.preferences = self.inf.load_preferences(self.user_id)
        logger.info("✅ 加载用户 %s 的偏好: %s", self.user_id, self.preferences)

    def set(self, key: str, value: str):
        if not key or value is None:
            return
        self.preferences[key] = value
        self.inf.save_preference(self.user_id, key, value)

    def save_batch(self, kvs: Dict[str, str]):
        for k, v in kvs.items():
            self.set(str(k), str(v))

    def get(self, key: str, default: str = "") -> str:
        return self.preferences.get(key, default)

    def get_all(self) -> Dict[str, str]:
        return self.preferences.copy()
