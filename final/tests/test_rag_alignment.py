import json
from types import SimpleNamespace

from internal.rag.hybrid import HybridResult, HybridStore
from internal.rag.rag import Engine
from internal.rag.reranker import LLMReranker
from internal.rag.rewriter import HistoryMessage, LLMRewriter
from internal.rag.splitter import RecursiveSplitter


def test_recursive_splitter_creates_overlapping_chunks():
    splitter = RecursiveSplitter(chunk_size=10, overlap=3)

    chunks = splitter.split("abcdefghijklmnopqrstuvwxyz")

    assert [c.id for c in chunks] == [0, 1, 2, 3]
    assert [c.content for c in chunks] == [
        "abcdefghij",
        "hijklmnopq",
        "opqrstuvwx",
        "vwxyz",
    ]


def test_rewriter_returns_deduplicated_queries_from_json():
    def generate(system_prompt, user_msg):
        assert "最近对话历史" in user_msg
        assert "当前问题：那个怎么实现" in user_msg
        return json.dumps({"queries": ["图式 Runtime 怎么实现", "DAG Runtime 实现", "图式 Runtime 怎么实现"]})

    rewriter = LLMRewriter(generate, num_queries=3)

    queries = rewriter.rewrite(
        "那个怎么实现",
        [HistoryMessage(role="user", content="我们刚刚在讨论图式 Runtime")],
    )

    assert queries == ["图式 Runtime 怎么实现", "DAG Runtime 实现", "那个怎么实现"]


def test_rewriter_falls_back_to_original_query_on_bad_json():
    rewriter = LLMRewriter(lambda _system, _user: "not json", num_queries=3)

    assert rewriter.rewrite("查询 RAG", []) == ["查询 RAG"]


class _Result:
    def __init__(self, content, score):
        self.content = content
        self.score = score
        self.source = "hybrid"


def test_reranker_orders_by_llm_score_and_marks_source():
    def generate(_system_prompt, user_msg):
        assert "[0] weak" in user_msg
        assert "[1] strong" in user_msg
        return json.dumps({"scores": [{"idx": 0, "score": 2}, {"idx": 1, "score": 9}]})

    reranker = LLMReranker(generate, preview_len=100)
    results = [_Result("weak", 0.9), _Result("strong", 0.1)]

    reranked = reranker.rerank("question", results, top_k=2)

    assert [r.content for r in reranked] == ["strong", "weak"]
    assert [r.score for r in reranked] == [0.9, 0.2]
    assert all(r.source.endswith("+rerank") for r in reranked)


def test_reranker_falls_back_to_rrf_order_on_bad_json():
    reranker = LLMReranker(lambda _system, _user: "bad", preview_len=100)
    results = [_Result("first", 0.7), _Result("second", 0.6)]

    reranked = reranker.rerank("question", results, top_k=1)

    assert [r.content for r in reranked] == ["first"]


class _FakeInfra:
    def __init__(self):
        self.ready = SimpleNamespace(postgresql="connected", milvus="connected", elasticsearch="connected")
        self.saved_chunks = []
        self.indexed_chunks = []
        self.inserted_milvus = []
        self.rows = {
            1: {"id": 1, "content": "child a", "parent_content": "parent A"},
            2: {"id": 2, "content": "child b", "parent_content": "parent B"},
            3: {"id": 3, "content": "child c", "parent_content": ""},
        }
        self.next_id = 10
        self.events = []

    def count_rag_chunks(self):
        return len(self.saved_chunks)

    def load_rag_chunks_by_ids(self, ids):
        return [self.rows[i] for i in ids if i in self.rows]

    def milvus_search_with_scores(self, _collection, _embedding, _top_k):
        return [{"pg_id": 1, "score": 0.9}, {"pg_id": 2, "score": 0.8}]

    def search_rag_chunks(self, query, _top_k):
        if "alt" in query:
            return [{"pg_id": 2, "score": 10.0}, {"pg_id": 3, "score": 9.0}]
        return [{"pg_id": 1, "score": 10.0}, {"pg_id": 3, "score": 8.0}]

    def save_rag_chunk_with_parent(self, doc_hash, chunk_idx, content, parent_content, embedding_json):
        pg_id = self.next_id
        self.next_id += 1
        self.saved_chunks.append({
            "id": pg_id,
            "doc_hash": doc_hash,
            "chunk_idx": chunk_idx,
            "content": content,
            "parent_content": parent_content,
            "embedding_json": embedding_json,
        })
        self.rows[pg_id] = {"id": pg_id, "content": content, "parent_content": parent_content}
        return pg_id

    def save_rag_chunk(self, doc_hash, chunk_idx, content, embedding_json):
        return self.save_rag_chunk_with_parent(doc_hash, chunk_idx, content, "", embedding_json)

    def insert_rag_chunks(self, pg_ids, contents, embeddings):
        self.inserted_milvus.append((pg_ids, contents, embeddings))

    def index_rag_chunk(self, pg_id, content, doc_hash, chunk_idx):
        self.indexed_chunks.append({
            "pg_id": pg_id,
            "content": content,
            "doc_hash": doc_hash,
            "chunk_idx": chunk_idx,
        })

    def publish_event(self, event_type, payload):
        self.events.append((event_type, payload))


class _FakeCfg:
    chunk_size = 10
    chunk_overlap = 0
    top_k = 2
    rrf_constant_k = 60
    semantic_weight = 0.5
    kg_weight = 0.0
    kg_enabled = False
    enable_hybrid_search = True
    rag_milvus_dim = 3

    def is_real_embedding(self):
        return True


class _FakeLLM:
    cfg = _FakeCfg()

    def embed(self, _text):
        return [0.1, 0.2, 0.3]


def test_hybrid_search_multi_merges_queries_and_uses_reranker():
    store = HybridStore(_FakeCfg(), _FakeInfra(), embed_fn=lambda _q: [0.1, 0.2, 0.3])
    store.set_reranker(LLMReranker(
        lambda _system, _user: json.dumps({"scores": [{"idx": 0, "score": 1}, {"idx": 1, "score": 10}]}),
        preview_len=100,
    ))

    results = store.search_multi(["main", "alt"], top_k=2)

    assert [r.pg_id for r in results] == [2, 1]
    assert results[0].parent == "parent B"
    assert results[0].source.endswith("+rerank")


def test_engine_ingest_saves_child_chunks_with_parent_content():
    inf = _FakeInfra()
    engine = Engine(_FakeCfg(), inf, _FakeLLM())

    count = engine.ingest("abcdefghijklmnopqrstuvwxyz")

    assert count == len(inf.saved_chunks)
    assert any(row["parent_content"] != row["content"] for row in inf.saved_chunks)
    assert all(row["parent_content"] for row in inf.saved_chunks)


def test_engine_ingest_writes_to_kg_when_available():
    """ingest 应在 KGStore 可用时同步把每个有效 chunk 写入 Neo4j。

    回归此前的隐性 bug：KGStore.search 早已实现，但 ingest 从未触发
    KGStore.index_document，导致 Neo4j 始终为空、_fetch_kg 永远返回空结果。
    """
    from internal.graph.types import ChunkRef

    class _FakeKG:
        def __init__(self, available=True):
            self._available = available
            self.calls = []

        def available(self):
            return self._available

        def index_document(self, doc_hash, refs):
            self.calls.append((doc_hash, list(refs)))

    inf = _FakeInfra()
    engine = Engine(_FakeCfg(), inf, _FakeLLM())
    kg = _FakeKG(available=True)
    engine.set_kg_store(kg)

    engine.ingest("abcdefghijklmnopqrstuvwxyz")

    assert len(kg.calls) == 1, "KGStore.index_document should be called exactly once"
    doc_hash, refs = kg.calls[0]
    assert isinstance(doc_hash, str) and len(doc_hash) == 16
    assert refs and all(isinstance(r, ChunkRef) for r in refs)
    assert all(r.pg_id > 0 and r.content for r in refs)


def test_engine_ingest_skips_kg_when_unavailable():
    """KGStore.available()=False 时不应调用 index_document，避免 Neo4j 故障阻塞主入库。"""

    class _FakeKG:
        def __init__(self):
            self.calls = []

        def available(self):
            return False

        def index_document(self, doc_hash, refs):
            self.calls.append((doc_hash, refs))

    inf = _FakeInfra()
    engine = Engine(_FakeCfg(), inf, _FakeLLM())
    kg = _FakeKG()
    engine.set_kg_store(kg)

    engine.ingest("abcdefghijklmnopqrstuvwxyz")

    assert kg.calls == [], "KG should be skipped when not available"


def test_engine_query_with_history_uses_rewrite_search_multi_and_parent_context():
    inf = _FakeInfra()
    engine = Engine(_FakeCfg(), inf, _FakeLLM())
    engine.loaded = True
    engine.set_rewriter(LLMRewriter(lambda _system, _user: json.dumps({"queries": ["main", "alt"]}), 2))
    captured = {}

    def generate(_system, user_msg):
        captured["user_msg"] = user_msg
        return "answer"

    engine.set_generate_fn(generate)

    answer, results = engine.query_with_history("原问题", [HistoryMessage(role="user", content="历史")])

    assert answer == "answer"
    assert "parent A" in captured["user_msg"]
    assert "parent B" in captured["user_msg"]
    assert [r["content"] for r in results] == ["parent A", "parent B"]


class _FailingEmbedLLM:
    cfg = _FakeCfg()

    def embed(self, _text):
        raise RuntimeError("embedding service down")


def test_engine_ingest_saves_pg_and_es_when_embedding_fails():
    inf = _FakeInfra()
    engine = Engine(_FakeCfg(), inf, _FailingEmbedLLM())

    count = engine.ingest("abcdefghijklmnopqrstuvwxyz")

    assert count == len(inf.saved_chunks)
    assert len(inf.indexed_chunks) == count
    assert inf.inserted_milvus == []
    assert all(row["embedding_json"] == "[]" for row in inf.saved_chunks)


def test_engine_compose_answer_deduplicates_same_display_content():
    engine = Engine(_FakeCfg(), _FakeInfra(), _FakeLLM())

    answer, results = engine._compose_answer("question", [
        {"pg_id": 1, "content": "same parent", "score": 0.9, "source": "keyword"},
        {"pg_id": 2, "content": "same parent", "score": 0.8, "source": "semantic"},
        {"pg_id": 3, "content": "other parent", "score": 0.7, "source": "keyword"},
    ])

    assert "same parent" in answer
    assert [r["content"] for r in results] == ["same parent", "other parent"]


def test_llm_embed_does_not_return_mock_vector_when_unconfigured():
    from config.config import APIConfig
    from internal.llm.llm import Client

    client = Client(APIConfig())

    try:
        client.embed("hello")
    except RuntimeError as e:
        assert "未配置" in str(e)
    else:
        raise AssertionError("embed should raise instead of returning a mock vector")
