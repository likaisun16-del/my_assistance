# infra — 管理所有外部基础设施连接：Milvus / PostgreSQL / Elasticsearch / Kafka
# 每个连接失败时优雅降级，不影响应用启动。
import json
import logging
from dataclasses import dataclass, field
from typing import List, Optional, Dict

from config.config import APIConfig

logger = logging.getLogger(__name__)

# 尝试导入可选依赖，失败则标记不可用
try:
    import psycopg2
    import psycopg2.extras
    _HAS_PG = True
except ImportError:
    _HAS_PG = False

try:
    from elasticsearch import Elasticsearch
    _HAS_ES = True
except ImportError:
    _HAS_ES = False

try:
    from kafka import KafkaProducer
    _HAS_KAFKA = True
except ImportError:
    _HAS_KAFKA = False


@dataclass
class Status:
    milvus: str = "disconnected"
    postgresql: str = "disconnected"
    elasticsearch: str = "disconnected"
    kafka: str = "disconnected"


@dataclass
class LongTermRow:
    id: int
    content: str
    importance: float
    embedding: Optional[List[float]] = None


class Infrastructure:
    """持有所有外部连接句柄"""

    def __init__(self, cfg: APIConfig):
        self.cfg = cfg
        self.ready = Status()
        self._pg = None
        self._es = None
        self._kafka_producer = None

        self._connect_postgres()
        self._connect_es()
        self._connect_kafka()
        # Milvus 暂用内存向量库，连接逻辑预留
        self.ready.milvus = "memory-mode"

    # ─────────────────────────────── PostgreSQL ───────────────────────────────

    def _connect_postgres(self):
        if not _HAS_PG:
            logger.warning("⚠️  psycopg2 未安装，PostgreSQL 不可用")
            return
        if not self.cfg.pg_host:
            logger.warning("⚠️  PostgreSQL 未配置")
            return
        try:
            self._pg = psycopg2.connect(self.cfg.pg_dsn())
            self._pg.autocommit = True
            with self._pg.cursor() as cur:
                cur.execute("SELECT 1")
            self.ready.postgresql = "connected"
            self._init_pg_schema()
            logger.info("✅ PostgreSQL 已连接: %s", self.cfg.pg_dsn())
        except Exception as e:
            logger.warning("⚠️  PostgreSQL 连接失败: %s", e)
            self._pg = None

    def _init_pg_schema(self):
        if not self._pg:
            return
        ddls = [
            """CREATE TABLE IF NOT EXISTS user_preferences (
                user_id    TEXT NOT NULL,
                key        TEXT NOT NULL,
                value      TEXT NOT NULL,
                updated_at TIMESTAMP DEFAULT NOW(),
                PRIMARY KEY (user_id, key)
            )""",
            """CREATE TABLE IF NOT EXISTS task_snapshots (
                task_id    TEXT PRIMARY KEY,
                state      JSONB NOT NULL,
                created_at TIMESTAMP DEFAULT NOW()
            )""",
            """CREATE TABLE IF NOT EXISTS chat_history (
                id         SERIAL PRIMARY KEY,
                role       TEXT NOT NULL,
                content    TEXT NOT NULL,
                created_at TIMESTAMP DEFAULT NOW()
            )""",
            """CREATE TABLE IF NOT EXISTS long_term_memory (
                id          SERIAL PRIMARY KEY,
                content     TEXT NOT NULL,
                importance  FLOAT NOT NULL DEFAULT 0.5,
                embedding   JSONB,
                created_at  TIMESTAMP DEFAULT NOW()
            )""",
            """CREATE TABLE IF NOT EXISTS rag_chunks (
                id          SERIAL PRIMARY KEY,
                doc_hash    TEXT NOT NULL,
                chunk_idx   INT NOT NULL,
                content     TEXT NOT NULL,
                embedding   JSONB,
                created_at  TIMESTAMP DEFAULT NOW()
            )""",
        ]
        with self._pg.cursor() as cur:
            for ddl in ddls:
                try:
                    cur.execute(ddl)
                except Exception as e:
                    logger.warning("⚠️  PG 建表失败: %s", e)
        logger.info("✅ PostgreSQL 表结构已初始化")

    def save_preference(self, user_id: str, key: str, value: str):
        if not self._pg:
            return
        try:
            with self._pg.cursor() as cur:
                cur.execute(
                    """INSERT INTO user_preferences (user_id, key, value) VALUES (%s, %s, %s)
                       ON CONFLICT (user_id, key) DO UPDATE SET value = %s, updated_at = NOW()""",
                    (user_id, key, value, value),
                )
        except Exception as e:
            logger.warning("⚠️  偏好保存到 PG 失败: %s", e)

    def save_snapshot(self, task_id: str, state_json: str):
        if not self._pg:
            return
        try:
            with self._pg.cursor() as cur:
                cur.execute(
                    """INSERT INTO task_snapshots (task_id, state) VALUES (%s, %s)
                       ON CONFLICT (task_id) DO UPDATE SET state = %s, created_at = NOW()""",
                    (task_id, state_json, state_json),
                )
        except Exception as e:
            logger.warning("⚠️  快照保存到 PG 失败: %s", e)

    def load_preferences(self, user_id: str) -> Dict[str, str]:
        result: Dict[str, str] = {}
        if not self._pg:
            return result
        try:
            with self._pg.cursor() as cur:
                cur.execute("SELECT key, value FROM user_preferences WHERE user_id = %s", (user_id,))
                for k, v in cur.fetchall():
                    result[k] = v
        except Exception as e:
            logger.warning("⚠️  加载偏好失败: %s", e)
        return result

    def save_long_term_item(self, content: str, importance: float, embedding_json: str) -> int:
        if not self._pg:
            return -1
        try:
            with self._pg.cursor() as cur:
                cur.execute(
                    "INSERT INTO long_term_memory (content, importance, embedding) VALUES (%s, %s, %s) RETURNING id",
                    (content, importance, embedding_json),
                )
                row = cur.fetchone()
                return row[0] if row else -1
        except Exception as e:
            logger.warning("⚠️  长期记忆保存失败: %s", e)
            return -1

    def load_long_term_items(self) -> List[LongTermRow]:
        if not self._pg:
            return []
        try:
            with self._pg.cursor() as cur:
                cur.execute("SELECT id, content, importance, embedding FROM long_term_memory ORDER BY id")
                rows = []
                for rid, content, importance, emb_json in cur.fetchall():
                    emb = None
                    if emb_json:
                        try:
                            emb = json.loads(emb_json) if isinstance(emb_json, str) else emb_json
                        except Exception:
                            pass
                    rows.append(LongTermRow(id=rid, content=content, importance=importance, embedding=emb))
                return rows
        except Exception as e:
            logger.warning("⚠️  加载长期记忆失败: %s", e)
            return []

    def save_rag_chunk(self, doc_hash: str, chunk_idx: int, content: str, embedding_json: str) -> int:
        if not self._pg:
            return -1
        try:
            with self._pg.cursor() as cur:
                cur.execute(
                    "INSERT INTO rag_chunks (doc_hash, chunk_idx, content, embedding) VALUES (%s, %s, %s, %s) RETURNING id",
                    (doc_hash, chunk_idx, content, embedding_json),
                )
                row = cur.fetchone()
                return row[0] if row else -1
        except Exception as e:
            logger.warning("⚠️  RAG chunk 保存失败: %s", e)
            return -1

    def load_rag_chunks_by_ids(self, ids: List[int]) -> List[dict]:
        if not self._pg or not ids:
            return []
        try:
            with self._pg.cursor() as cur:
                placeholders = ",".join(["%s"] * len(ids))
                cur.execute(f"SELECT id, content FROM rag_chunks WHERE id IN ({placeholders})", tuple(ids))
                rows = []
                for rid, content in cur.fetchall():
                    rows.append({"id": rid, "content": content})
                return rows
        except Exception as e:
            logger.warning("⚠️  加载 RAG chunks 失败: %s", e)
            return []

    def delete_rag_chunks_by_doc_hash(self, doc_hash: str) -> List[int]:
        if not self._pg:
            return []
        try:
            with self._pg.cursor() as cur:
                cur.execute("SELECT id FROM rag_chunks WHERE doc_hash = %s", (doc_hash,))
                ids = [row[0] for row in cur.fetchall()]
                if ids:
                    placeholders = ",".join(["%s"] * len(ids))
                    cur.execute(f"DELETE FROM rag_chunks WHERE id IN ({placeholders})", tuple(ids))
                return ids
        except Exception as e:
            logger.warning("⚠️  删除 RAG chunks 失败: %s", e)
            return []

    # ─────────────────────────────── Elasticsearch ───────────────────────────

    def _connect_es(self):
        if not _HAS_ES:
            logger.warning("⚠️  elasticsearch-py 未安装，ES 不可用")
            return
        if not self.cfg.es_addresses:
            logger.warning("⚠️  Elasticsearch 未配置")
            return
        try:
            auth = (self.cfg.es_username, self.cfg.es_password) if self.cfg.es_username else None
            self._es = Elasticsearch(
                self.cfg.es_addresses,
                basic_auth=auth,
            )
            if self._es.ping():
                self.ready.elasticsearch = "connected"
                logger.info("✅ Elasticsearch 已连接: %s", self.cfg.es_addresses)
            else:
                self._es = None
        except Exception as e:
            logger.warning("⚠️  Elasticsearch 连接失败: %s", e)
            self._es = None

    def search_es(self, index: str, query_json: str) -> str:
        if not self._es:
            raise RuntimeError("elasticsearch not connected")
        resp = self._es.search(index=index, body=json.loads(query_json))
        return json.dumps(resp)

    def index_rag_chunk(self, pg_id: int, content: str, doc_hash: str, chunk_idx: int):
        if not self._es:
            return
        try:
            self._es.index(
                index="rag_chunks",
                id=pg_id,
                body={
                    "content": content,
                    "doc_hash": doc_hash,
                    "chunk_idx": chunk_idx,
                }
            )
        except Exception as e:
            logger.warning("⚠️  ES 索引 RAG chunk 失败: %s", e)

    def search_rag_chunks(self, query: str, top_k: int) -> List[dict]:
        if not self._es:
            return []
        try:
            resp = self._es.search(
                index="rag_chunks",
                body={
                    "query": {"match": {"content": query}},
                    "size": top_k,
                }
            )
            hits = []
            for hit in resp.get("hits", {}).get("hits", []):
                hits.append({
                    "pg_id": int(hit["_id"]),
                    "content": hit["_source"]["content"],
                    "score": hit["_score"],
                })
            return hits
        except Exception as e:
            logger.warning("⚠️  ES 检索失败: %s", e)
            return []

    def delete_rag_chunks_from_es(self, pg_ids: List[int]):
        if not self._es:
            return
        try:
            for pg_id in pg_ids:
                self._es.delete(index="rag_chunks", id=pg_id)
        except Exception as e:
            logger.warning("⚠️  ES 删除失败: %s", e)

    # ─────────────────────────────── Kafka ───────────────────────────────────

    def _connect_kafka(self):
        if not _HAS_KAFKA:
            logger.warning("⚠️  kafka-python 未安装，Kafka 不可用")
            return
        if not self.cfg.kafka_brokers:
            logger.warning("⚠️  Kafka 未配置")
            return
        try:
            self._kafka_producer = KafkaProducer(
                bootstrap_servers=self.cfg.kafka_brokers,
                value_serializer=lambda v: json.dumps(v).encode("utf-8"),
            )
            self.ready.kafka = "connected"
            logger.info("✅ Kafka 已连接: %s", self.cfg.kafka_brokers)
        except Exception as e:
            logger.warning("⚠️  Kafka 连接失败: %s (事件将输出到日志)", e)
            self._kafka_producer = None

    def publish_event(self, event_type: str, payload: str):
        if self._kafka_producer and self.ready.kafka == "connected":
            try:
                self._kafka_producer.send(
                    self.cfg.kafka_topic,
                    key=event_type.encode("utf-8"),
                    value=payload
                )
            except Exception as e:
                logger.warning("⚠️  Kafka 写入失败: %s", e)
        else:
            logger.info("📋 [Kafka-fallback] %s: %s", event_type, payload)

    # ─────────────────────────────── Milvus（内存模式）───────────────────────

    def milvus_search_with_scores(self, collection_name: str, query_emb: List[float], top_k: int) -> List[dict]:
        """内存模式下的向量检索"""
        return []

    def insert_rag_chunks(self, pg_ids: List[int], contents: List[str], embeddings: List[List[float]]):
        """内存模式下插入向量"""
        pass

    def delete_rag_chunks_from_milvus(self, pg_ids: List[int]):
        """内存模式下删除向量"""
        pass

    # ─────────────────────────────── 生命周期 ────────────────────────────────

    def close(self):
        if self._pg:
            self._pg.close()
        if self._kafka_producer:
            self._kafka_producer.close()
