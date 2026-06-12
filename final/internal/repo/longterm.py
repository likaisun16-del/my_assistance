# longterm — 长期记忆条目仓储（Postgres 实现）。
import json
import logging
from dataclasses import dataclass, field
from datetime import datetime
from typing import List, Optional

from internal.platform.postgres import PostgresClient

logger = logging.getLogger(__name__)


@dataclass
class Row:
    """长期记忆条目的领域模型。"""
    id: int = 0
    content: str = ""
    importance: float = 0.0
    embedding: List[float] = field(default_factory=list)
    created_at: Optional[datetime] = None
    last_accessed: Optional[datetime] = None
    category: str = ""
    tags: List[str] = field(default_factory=list)
    slot_hint: str = ""


class PGRepo:
    """Postgres 实现；client 不可用时返回安全默认值。"""

    def __init__(self, client: PostgresClient):
        self.client = client

    # 默认分类 "general" 写入
    def save(self, content: str, importance: float, embedding_json: bytes) -> int:
        return self.save_classified(content, importance, embedding_json, "general", None, "")

    # 带分类信息写入
    def save_classified(self, content: str, importance: float, embedding_json: bytes,
                        category: str, tags: Optional[List[str]], slot_hint: str) -> int:
        if self.client is None or not self.client.is_real() or self.client.conn is None:
            return -1
        if not category:
            category = "general"
        if tags is None:
            tags = []
        # embedding 以 JSONB 写入；接受 bytes / str / 已序列化对象
        emb_param = embedding_json
        if isinstance(emb_param, (bytes, bytearray)):
            try:
                emb_param = bytes(emb_param).decode("utf-8")
            except Exception:
                emb_param = "[]"
        try:
            with self.client.conn.cursor() as cur:
                cur.execute(
                    "INSERT INTO long_term_memory (content, importance, embedding, category, tags, slot_hint) "
                    "VALUES (%s, %s, %s, %s, %s, NULLIF(%s, '')) RETURNING id",
                    (content, importance, emb_param, category, list(tags), slot_hint),
                )
                row = cur.fetchone()
                return int(row[0]) if row else -1
        except Exception as e:
            logger.warning("⚠️  长期记忆保存失败: %s", e)
            return -1

    # 加载全部长期记忆条目
    def load(self) -> List[Row]:
        if self.client is None or not self.client.is_real():
            return []
        try:
            rows = self.client.query(
                "SELECT id, content, importance, embedding, "
                "COALESCE(created_at, NOW()), COALESCE(last_accessed, NOW()), "
                "COALESCE(category, 'general'), COALESCE(tags, '{}'::TEXT[]), COALESCE(slot_hint, '') "
                "FROM long_term_memory ORDER BY id"
            )
        except Exception as e:
            logger.warning("⚠️  加载长期记忆失败: %s", e)
            return []
        items: List[Row] = []
        for r in rows:
            try:
                rid, content, importance, emb_json, created_at, last_accessed, category, tags, slot_hint = (
                    r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7], r[8]
                )
                embedding: List[float] = []
                if emb_json:
                    try:
                        if isinstance(emb_json, (bytes, bytearray)):
                            embedding = json.loads(bytes(emb_json).decode("utf-8"))
                        elif isinstance(emb_json, str):
                            embedding = json.loads(emb_json)
                        elif isinstance(emb_json, list):
                            embedding = emb_json
                    except Exception:
                        embedding = []
                items.append(Row(
                    id=int(rid),
                    content=content or "",
                    importance=float(importance) if importance is not None else 0.0,
                    embedding=embedding,
                    created_at=created_at if isinstance(created_at, datetime) else None,
                    last_accessed=last_accessed if isinstance(last_accessed, datetime) else None,
                    category=category or "general",
                    tags=list(tags) if tags else [],
                    slot_hint=slot_hint or "",
                ))
            except Exception:
                continue
        return items

    # 修改一条长期记忆
    def update(self, item_id: int, content: str, importance: float, embedding_json: bytes) -> None:
        if self.client is None or not self.client.is_real():
            return
        emb_param = embedding_json
        if isinstance(emb_param, (bytes, bytearray)):
            try:
                emb_param = bytes(emb_param).decode("utf-8")
            except Exception:
                emb_param = "[]"
        try:
            self.client.exec(
                "UPDATE long_term_memory SET content = %s, importance = %s, embedding = %s, "
                "last_accessed = NOW() WHERE id = %s",
                (content, importance, emb_param, item_id),
            )
        except Exception as e:
            logger.warning("⚠️  长期记忆更新失败 (id=%d): %s", item_id, e)

    # 批量删除
    def delete(self, ids: List[int]) -> None:
        if self.client is None or not self.client.is_real() or not ids:
            return
        placeholders = ",".join(["%s"] * len(ids))
        query = f"DELETE FROM long_term_memory WHERE id IN ({placeholders})"
        try:
            self.client.exec(query, tuple(ids))
        except Exception as e:
            logger.warning("⚠️  长期记忆批量删除失败: %s", e)
