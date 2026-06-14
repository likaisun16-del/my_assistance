from config.config import default_config


def test_alignment_config_defaults(tmp_path):
    config_path = tmp_path / "empty.yaml"
    config_path.write_text("", encoding="utf-8")

    cfg = default_config(str(config_path))

    assert cfg.rag_rewrite_enabled is False
    assert cfg.rag_rewrite_num_queries == 3
    assert cfg.rag_rerank_enabled is False
    assert cfg.rag_rerank_preview_len == 200
    assert cfg.graph_max_parallel == 2
    assert cfg.graph_race_timeout_ms == 30000
    assert cfg.graph_enable_racing is True


def test_alignment_config_reads_rag_and_graph_runtime(tmp_path):
    config_path = tmp_path / "config.yaml"
    config_path.write_text(
        """
rag:
  rewrite:
    enabled: true
    num_queries: 4
  rerank:
    enabled: true
    preview_len: 320
graph_runtime:
  max_parallel: 5
  race_timeout_ms: 1234
  enable_racing: false
""",
        encoding="utf-8",
    )

    cfg = default_config(str(config_path))

    assert cfg.rag_rewrite_enabled is True
    assert cfg.rag_rewrite_num_queries == 4
    assert cfg.rag_rerank_enabled is True
    assert cfg.rag_rerank_preview_len == 320
    assert cfg.graph_max_parallel == 5
    assert cfg.graph_race_timeout_ms == 1234
    assert cfg.graph_enable_racing is False
