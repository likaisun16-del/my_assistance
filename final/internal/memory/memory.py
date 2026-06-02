# memory — 三层记忆系统（短期 / 长期 / 用户偏好）
import json
import logging
import math
import re
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
        self.max_turns = max_turns
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


class LongTerm:
    """长期记忆 - 基于 embedding 的语义记忆。"""

    def __init__(self, cfg: APIConfig, inf: Infrastructure):
        self.cfg = cfg
        self.inf = inf
        self.items: List[Item] = []
        self._embed_fn: Optional[Any] = None

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
        emb_json = json.dumps(embedding) if embedding else "null"
        self.inf.save_long_term_item(content, importance, emb_json)

    def store(self, content: str, importance: float, embedding: Optional[List[float]]) -> bool:
        """兼容主分支命名的存储接口。"""
        try:
            self.items.append(Item(content=content, importance=importance, embedding=embedding))
            self.inf.save_long_term_item(content, importance, json.dumps(embedding) if embedding else "null")
            return True
        except Exception as e:
            logger.warning("⚠️  长期记忆存储失败: %s", e)
            return False

    def sync_last_item_pgid(self, pg_id: int):
        """兼容主分支的 PGID 同步接口，Python 版默认不需要。"""
        return

    def recall(self, query: str, top_k: int = 3) -> List[Item]:
        if not self.items:
            return []

        query_emb = None
        if self._embed_fn:
            try:
                query_emb = self._embed_fn(query)
            except Exception as e:
                logger.warning("⚠️  查询向量化失败: %s", e)
                return []

        if not query_emb:
            return self.items[:top_k]

        results = []
        for item in self.items:
            if item.embedding:
                sim = self._cosine_similarity(query_emb, item.embedding)
                score = sim * 0.7 + item.importance * 0.3
                results.append((item, score))

        results.sort(key=lambda x: x[1], reverse=True)
        return [item for item, score in results[:top_k] if score >= 0.4]

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
        return len(self.items) >= max(1, self.cfg.memory_consolidation_trigger)

    def NeedConsolidation(self) -> bool:
        return self.need_consolidation()

    def consolidate(self):
        if len(self.items) < self.cfg.memory_consolidation_trigger:
            return

        logger.info("🔄 开始记忆合并...")
        to_remove = set()
        for i in range(len(self.items)):
            if i in to_remove:
                continue
            for j in range(i + 1, len(self.items)):
                if j in to_remove:
                    continue
                sim = self._compute_similarity(self.items[i].content, self.items[j].content)
                if sim >= self.cfg.memory_consolidation_dedup:
                    if self.items[i].importance < self.items[j].importance:
                        to_remove.add(i)
                    else:
                        to_remove.add(j)

        self.items = [item for i, item in enumerate(self.items) if i not in to_remove]

        for item in self.items:
            item.importance *= self.cfg.memory_consolidation_decay_rate

        self.items = [item for item in self.items if item.importance >= self.cfg.memory_consolidation_min_import]
        logger.info("✅ 记忆合并完成，剩余 %d 条", len(self.items))

    def _compute_similarity(self, a: str, b: str) -> float:
        tokens_a = set(a.lower().split())
        tokens_b = set(b.lower().split())
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
        self.preferences[key] = value
        self.inf.save_preference(self.user_id, key, value)

    def save_batch(self, kvs: Dict[str, str]):
        for k, v in kvs.items():
            self.set(k, v)

    def get(self, key: str, default: str = "") -> str:
        return self.preferences.get(key, default)

    def get_all(self) -> Dict[str, str]:
        return self.preferences.copy()

    def extract_and_save(self, content: str):
        """兼容主分支风格：从单条内容中提取偏好并保存。"""
        key, value = None, None
        if not content:
            return None, None, False
        match = re.search(r"我叫\s*(\S+)", content)
        if match:
            key, value = "name", match.group(1)
        else:
            match = re.search(r"喜欢\s*(\S+)", content)
            if match:
                key, value = "like", match.group(1)
        if key and value:
            self.set(key, value)
            return key, value, True
        return None, None, False

    def update_from_messages(self, messages: List[Dict[str, str]]):
        extracted_info = []
        for msg in messages:
            content = msg.get("content", "")
            if not content:
                continue

            if "我叫" in content:
                match = re.search(r"我叫\s*(\S+)", content)
                if match:
                    self.set("name", match.group(1))
                    extracted_info.append(f"name={match.group(1)}")

            if "喜欢" in content:
                match = re.search(r"喜欢\s*(\S+)", content)
                if match:
                    self.set("like", match.group(1))
                    extracted_info.append(f"like={match.group(1)}")

            if "讨厌" in content or "不喜欢" in content:
                match = re.search(r"(讨厌|不喜欢)\s*(\S+)", content)
                if match:
                    self.set("dislike", match.group(2))
                    extracted_info.append(f"dislike={match.group(2)}")

        if extracted_info:
            return "已记住：" + ", ".join(extracted_info)
        return ""
