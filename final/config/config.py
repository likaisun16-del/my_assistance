# config — 配置管理（与主分支 Go 版字段对齐的 Python 配置骨架）
import logging
import os
from typing import Any, Dict, List

import yaml

logger = logging.getLogger(__name__)


class APIConfig:
    """整合 Python 版主干能力的运行配置。"""

    def __init__(self):
        # API / 模型
        self.llm_api_url = ""
        self.llm_api_key = ""
        self.llm_model = ""
        self.temperature = 0.7
        self.embedding_api_url = ""
        self.embedding_api_key = ""
        self.embedding_model = ""

        # 基础设施
        self.milvus_host = ""
        self.milvus_port = 19530
        self.pg_host = ""
        self.pg_port = 5432
        self.pg_user = ""
        self.pg_password = ""
        self.pg_database = ""
        self.es_addresses: List[str] = []
        self.es_username = ""
        self.es_password = ""
        self.kafka_brokers: List[str] = []
        self.kafka_topic = ""

        # RAG
        self.chunk_size = 200
        self.chunk_overlap = 50
        self.top_k = 3
        self.rrf_constant_k = 60
        self.semantic_weight = 0.7
        self.enable_hybrid_search = False
        self.rag_milvus_dim = 1024

        # Memory
        self.short_term_max_turns = 10
        self.long_term_top_k = 3
        self.memory_consolidation_similarity = 0.80
        self.memory_consolidation_dedup = 0.95
        self.memory_consolidation_ttl_days = 30
        self.memory_consolidation_decay_rate = 0.995
        self.memory_consolidation_min_import = 0.3
        self.memory_consolidation_trigger = 5

        # Harness / runtime
        self.max_retries = 3
        self.retry_delay_ms = 1000
        self.step_timeout_ms = 30000
        self.max_iterations = 10

        # Search
        self.search_api_key = ""
        self.search_api_url = ""

        # Server
        self.server_port = "8080"

    def is_real_llm(self) -> bool:
        return bool(self.llm_api_key)

    def is_real_embedding(self) -> bool:
        return bool(self.embedding_api_key)

    def pg_dsn(self) -> str:
        return f"postgres://{self.pg_user}:{self.pg_password}@{self.pg_host}:{self.pg_port}/{self.pg_database}?sslmode=disable"

    def milvus_addr(self) -> str:
        return f"{self.milvus_host}:{self.milvus_port}"


def _read_yaml(path: str) -> Dict[str, Any]:
    try:
        with open(path, "r", encoding="utf-8") as f:
            return yaml.safe_load(f) or {}
    except Exception as e:
        logger.warning("读取 %s 失败，使用默认值: %s", path, e)
        return {}


def default_config() -> APIConfig:
    """从 `config/config.yaml` 加载配置。"""
    c = APIConfig()
    data = _read_yaml("config/config.yaml")

    if llm := data.get("llm"):
        c.llm_api_url = llm.get("api_url", "")
        c.llm_api_key = llm.get("api_key", "")
        c.llm_model = llm.get("model", "")
        c.temperature = llm.get("temperature", 0.7)

    if emb := data.get("embedding"):
        c.embedding_api_url = emb.get("api_url", "")
        c.embedding_api_key = emb.get("api_key", "")
        c.embedding_model = emb.get("model", "")

    if milvus := data.get("milvus"):
        c.milvus_host = milvus.get("host", "")
        c.milvus_port = milvus.get("port", 19530)

    if pg := data.get("postgres"):
        c.pg_host = pg.get("host", "")
        c.pg_port = pg.get("port", 5432)
        c.pg_user = pg.get("user", "")
        c.pg_password = pg.get("password", "")
        c.pg_database = pg.get("database", "")

    if es := data.get("elasticsearch"):
        c.es_addresses = es.get("addresses", [])
        c.es_username = es.get("username", "")
        c.es_password = es.get("password", "")

    if kafka := data.get("kafka"):
        c.kafka_brokers = kafka.get("brokers", [])
        c.kafka_topic = kafka.get("topic", "")

    if rag := data.get("rag"):
        c.chunk_size = rag.get("chunk_size", 200)
        c.chunk_overlap = rag.get("chunk_overlap", 50)
        c.top_k = rag.get("top_k", 3)
        c.rrf_constant_k = rag.get("rrf_constant_k", 60)
        c.semantic_weight = rag.get("semantic_weight", 0.7)
        c.enable_hybrid_search = rag.get("enable_hybrid_search", False)
        c.rag_milvus_dim = rag.get("rag_milvus_dim", 1024)

    if memory := data.get("memory"):
        c.short_term_max_turns = memory.get("short_term_max_turns", 10)
        c.long_term_top_k = memory.get("long_term_top_k", 3)
        cons = memory.get("consolidation", {})
        c.memory_consolidation_similarity = cons.get("similarity_threshold", 0.80)
        c.memory_consolidation_dedup = cons.get("dedup_threshold", 0.95)
        c.memory_consolidation_ttl_days = cons.get("ttl_days", 30)
        c.memory_consolidation_decay_rate = cons.get("decay_rate", 0.995)
        c.memory_consolidation_min_import = cons.get("min_importance", 0.3)
        c.memory_consolidation_trigger = cons.get("trigger_interval", 5)

    if harness := data.get("harness"):
        c.max_retries = harness.get("max_retries", 3)
        c.retry_delay_ms = harness.get("retry_delay_ms", 1000)
        c.step_timeout_ms = harness.get("step_timeout_ms", 30000)
        c.max_iterations = harness.get("max_iterations", 10)

    if search := data.get("search"):
        c.search_api_key = search.get("api_key", "")
        c.search_api_url = search.get("api_url", "")

    if server := data.get("server"):
        c.server_port = server.get("port", "8080")

    if c.rrf_constant_k <= 0:
        c.rrf_constant_k = 60
    if c.semantic_weight <= 0:
        c.semantic_weight = 0.7
    if c.rag_milvus_dim <= 0:
        c.rag_milvus_dim = 1024
    if c.memory_consolidation_similarity <= 0:
        c.memory_consolidation_similarity = 0.80
    if c.memory_consolidation_dedup <= 0:
        c.memory_consolidation_dedup = 0.95
    if c.memory_consolidation_ttl_days <= 0:
        c.memory_consolidation_ttl_days = 30
    if c.memory_consolidation_decay_rate <= 0:
        c.memory_consolidation_decay_rate = 0.995
    if c.memory_consolidation_min_import <= 0:
        c.memory_consolidation_min_import = 0.3
    if c.memory_consolidation_trigger <= 0:
        c.memory_consolidation_trigger = 5

    return c
