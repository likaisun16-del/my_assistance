# postgres — PostgreSQL 平台层薄封装：连接、健康检查、schema bootstrap、关键 SQL 操作。
# 失败时降级到 mock（self._conn 为 None），不阻塞应用启动。
import logging
from typing import Any, Iterable, List, Optional, Sequence, Tuple

from config.config import APIConfig

logger = logging.getLogger(__name__)

try:
    import psycopg2
    _HAS_PG = True
except ImportError:
    psycopg2 = None  # type: ignore
    _HAS_PG = False


# 业务表 DDL 集中在此处便于 schema review；启动期幂等执行一次。
_DDLS: List[str] = [
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
        id            SERIAL PRIMARY KEY,
        content       TEXT NOT NULL,
        importance    FLOAT NOT NULL DEFAULT 0.5,
        embedding     JSONB,
        created_at    DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW()),
        last_accessed DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW()),
        category      VARCHAR(64) NOT NULL DEFAULT '',
        tags          JSONB NOT NULL DEFAULT '[]'::jsonb,
        slot_hint     VARCHAR(64) NOT NULL DEFAULT '',
        score         DOUBLE PRECISION NOT NULL DEFAULT 0.0
    )""",
    "ALTER TABLE long_term_memory ADD COLUMN IF NOT EXISTS created_at    DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW())",
    "ALTER TABLE long_term_memory ADD COLUMN IF NOT EXISTS last_accessed DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW())",
    # Schema-driven 装配支持：分类 / 标签 / 槽位提示 / 召回分
    "ALTER TABLE long_term_memory ADD COLUMN IF NOT EXISTS category      VARCHAR(64) NOT NULL DEFAULT ''",
    "ALTER TABLE long_term_memory ADD COLUMN IF NOT EXISTS tags          JSONB NOT NULL DEFAULT '[]'::jsonb",
    "ALTER TABLE long_term_memory ADD COLUMN IF NOT EXISTS slot_hint     VARCHAR(64) NOT NULL DEFAULT ''",
    "ALTER TABLE long_term_memory ADD COLUMN IF NOT EXISTS score         DOUBLE PRECISION NOT NULL DEFAULT 0.0",
    "CREATE INDEX IF NOT EXISTS idx_lti_category ON long_term_memory(category)",
    "CREATE INDEX IF NOT EXISTS idx_lti_tags     ON long_term_memory USING GIN(tags)",
    """CREATE TABLE IF NOT EXISTS rag_chunks (
        id          BIGSERIAL PRIMARY KEY,
        doc_hash    TEXT NOT NULL,
        chunk_idx   INT NOT NULL,
        content     TEXT NOT NULL,
        embedding   JSONB,
        created_at  TIMESTAMP DEFAULT NOW(),
        UNIQUE(doc_hash, chunk_idx)
    )""",
]


class PostgresClient:
    """PostgreSQL 平台客户端：连接、ping、bootstrap、query/exec 通用方法。"""

    def __init__(self, cfg: APIConfig):
        self.cfg = cfg
        self._conn = None
        self.status: str = "disconnected"
        self._connect()
        if self._conn is not None:
            self.bootstrap_schema()

    # ─── 连接 ───
    def _connect(self) -> None:
        if not _HAS_PG:
            logger.warning("⚠️  psycopg2 未安装，PostgreSQL 不可用")
            return
        if not self.cfg.pg_host:
            logger.warning("⚠️  PostgreSQL 未配置")
            return
        try:
            self._conn = psycopg2.connect(self.cfg.pg_dsn())
            # autocommit 简化使用；事务交给上层显式管理
            self._conn.autocommit = True
            with self._conn.cursor() as cur:
                cur.execute("SELECT 1")
            self.status = "connected"
            logger.info("✅ PostgreSQL 已连接: %s", self.cfg.pg_dsn())
        except Exception as e:
            logger.warning("⚠️  PostgreSQL 连接失败: %s", e)
            self._conn = None
            self.status = "disconnected"

    # ─── 状态判断 ───
    def is_real(self) -> bool:
        """返回是否真实连接（非 mock 模式）。"""
        return self._conn is not None

    # ─── Schema bootstrap ───
    def bootstrap_schema(self) -> None:
        """幂等地创建/升级所有业务表。"""
        if self._conn is None:
            return
        try:
            with self._conn.cursor() as cur:
                for ddl in _DDLS:
                    try:
                        cur.execute(ddl)
                    except Exception as e:
                        logger.warning("⚠️  PG 建表失败: %s", e)
            logger.info("✅ PostgreSQL 表结构已初始化")
        except Exception as e:
            logger.warning("⚠️  PG bootstrap 失败: %s", e)

    # ─── 通用 query / exec ───
    def query(self, sql: str, params: Optional[Sequence[Any]] = None) -> List[Tuple[Any, ...]]:
        """执行 SELECT，返回所有行。失败返回空列表。"""
        if self._conn is None:
            return []
        try:
            with self._conn.cursor() as cur:
                cur.execute(sql, params or ())
                return list(cur.fetchall())
        except Exception as e:
            logger.warning("⚠️  PG query 失败: %s", e)
            return []

    def query_one(self, sql: str, params: Optional[Sequence[Any]] = None) -> Optional[Tuple[Any, ...]]:
        """执行 SELECT，返回第一行（或 None）。"""
        if self._conn is None:
            return None
        try:
            with self._conn.cursor() as cur:
                cur.execute(sql, params or ())
                row = cur.fetchone()
                return row
        except Exception as e:
            logger.warning("⚠️  PG query_one 失败: %s", e)
            return None

    def exec(self, sql: str, params: Optional[Sequence[Any]] = None) -> int:
        """执行 INSERT/UPDATE/DELETE，返回受影响行数；失败返回 -1。"""
        if self._conn is None:
            return -1
        try:
            with self._conn.cursor() as cur:
                cur.execute(sql, params or ())
                return cur.rowcount
        except Exception as e:
            logger.warning("⚠️  PG exec 失败: %s", e)
            return -1

    def exec_many(self, sql: str, seq_of_params: Iterable[Sequence[Any]]) -> int:
        if self._conn is None:
            return -1
        try:
            with self._conn.cursor() as cur:
                cur.executemany(sql, list(seq_of_params))
                return cur.rowcount
        except Exception as e:
            logger.warning("⚠️  PG exec_many 失败: %s", e)
            return -1

    @property
    def conn(self):
        """返回底层 psycopg2 连接，供需要更精细控制（如 RETURNING）的调用方使用。"""
        return self._conn

    # ─── 关闭 ───
    def close(self) -> None:
        if self._conn is not None:
            try:
                self._conn.close()
            except Exception as e:
                logger.warning("⚠️  PG 关闭失败: %s", e)
            finally:
                self._conn = None
                self.status = "disconnected"
