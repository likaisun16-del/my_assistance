# Align Python Branch With Main (Go) Branch Spec

## Why

Saber 项目当前同时维护 Go（main 分支）和 Python（python 分支）两套实现。经过 15 个模块逐文件级对账，python 分支存在大量与 main 不一致或缺失的功能：包括假实现（`RecursiveSplitter` 是定长滑窗）、死代码（`final/internal/repo/` 整层未被 import、`PromptCtx.RecallSource` 协议未被 `LongTerm` 满足）、关键能力缺失（cosine 替成 Jaccard、`consolidate` 无 merge 阶段、伪流式 SSE）、行为不一致（`ragchunk` 写库无 `ON CONFLICT`、Milvus 集合名错位）等共 50+ 项差异。

本 spec 的目标是让 python 分支在功能、行为、可观测性三个维度上与 main 分支严格对齐，**不再以简历点为基线，以 main 分支真实代码为唯一基线**。

## What Changes

### Phase 1：消除假实现 / 死代码 / 行为不一致（P0 阻塞项）

- 重写 `final/internal/rag/splitter.py` 为真递归分隔符栈 + Markdown 标题/代码块保护 + tail-rune overlap，**BREAKING**：现有 chunk_id 序列会变化。
- 改写 `final/internal/rag/rewriter.py` 与 `reranker.py` 的 system prompt，补齐 main 的 5 条硬约束 + 0-10 详细打分准则。
- 决策 `final/internal/repo/` 死代码去留：**接通方案**——agent / memory / rag / handler 改用 repo 层接口，废弃 `infra.py` 重叠的持久化方法。
- `agent.py` 真写 `chat_history` 表（main 真持久化、python 表建了没人写）。
- `infra.save_rag_chunk_with_parent` 加 `ON CONFLICT (doc_hash, chunk_idx) DO UPDATE`。
- Milvus 集合名 `rag_embeddings` → `rag_chunks`，显式 schema (pg_id PK Int64 / content VarChar 4096 / embedding FloatVector dim) + IVF_FLAT + L2 + nlist=128 索引；启动期主键/维度漂移自检。
- `LLMClient` 实现真 SSE 流式：`requests.post(stream=True)` + `data:` 帧解析 + token 回调，让 `/api/chat/stream` 不再是伪流式。

### Phase 2：Memory 层全面对齐（P0 核心）

- `memory.py:_compute_similarity` 由 Jaccard 字符串切词改为 cosine embedding + TF 词袋 fallback。
- `LongTerm.Item` 补 6 字段：`created_at / last_accessed / category / tags / slot_hint / score`，并迁移 PG schema。
- 实现 `LongTerm.store_classified(content, importance, emb, category, tags, slot_hint)`，含入库前 cosine dedup（`>=DedupThreshold` 时只更新 importance/Tags/Category/SlotHint，不插入新行）。
- 重写 `LongTerm.consolidate` 三阶段：(1) 按每条 `created_at` 单独衰减 `importance *= decay^days`；(2) 两两比对，dedup + **merge 阶段**（content 用"；"拼接，embedding 加权平均）；(3) 双条件淘汰 `days>TTL && importance<min_importance`；返回 `ConsolidationResult{deduped, merged, expired, delete_from_db, update_in_db}`。
- 实现 `LongTerm.recall_by_filter(query, q_emb, RecallFilter{categories,require_tags,max_age_hours,min_score,top_k})`，含 TF 词袋 fallback + `score = sim*0.7 + importance*0.3` + 写 `last_accessed`。
- `filter_by_category` 真按 categories 过滤（当前忽略参数）。
- `consolidate` 接通 `graph_memory.filter_protected(threshold=3)`，将高入度记忆从 delete 列表移除。
- `MemoryManager.recall` 重写为单方法封装：`ltm.recall_by_filter` → `graph_memory.find_related` 1-hop 扩展 → 扩展项应用 categories filter → 标 `score=0.45` → 全集排序截 TopK。
- `graph_memory.add_to_graph` 改异步（main 用 `goSafe`），含 panic recover。
- `ShortTerm` 补 `timestamp` 字段 + RLock + `collections.deque` 替 list pop(0)。
- 抽出独立 `internal/memory/preference.py`，补 `extract_and_save("我喜欢/我爱/我叫" 规则)` 与 `build_context()` 渲染"【用户偏好】"块。
- `LongTerm` 补 `sync_last_item_pg_id / find_by_id / snapshot / last_id / last_item` 等访问器。
- `GraphMemory` 补 `sync_prev_id / set_consolidation_config / need_consolidation` 代理。

### Phase 3：Agent 编排对齐（P0）

- 重写 `agent/memory_writer.py`：补 `classify_memory_content`（identity/preference/tool_failure/policy 4 规则）、`llm_classify_memory`（7 类 6 槽）、`sync_consolidation_to_db(DeleteFromDB/UpdateInDB)`、调 `graph_mem.store_classified`、`ltm.save_classified`、`sync_last_item_pg_id`。
- 重写 `agent/restore.py`：恢复 LTM rows（含 ID/CreatedAt/LastAccessed）、Preference、KG client 复用、SyncPrevID、attachGraph 三件套。
- `agent.py` 删除老 ReAct 迭代循环，统一走图调度（main 已废弃迭代版本）。
- 主入口拆 `prepare → dispatch → finalize` 三段，finalize 阶段根据 graph_memory 是否可用切 `GraphAwareConsolidate` / 普通 `Consolidate`。
- `mode_tool` 补 `fill_params_from_preference`（5 偏好键 → 多参数名映射）。

### Phase 4：抽象与并发（P1）

- `cancel.py` 由单 token 改为 `dict[int64, CancelToken]` 多任务管理 + 周期 snapshot。
- 抽 `mem_stack.py` + `ConsolidationConfig`（7 字段：similarity_threshold / dedup_threshold / ttl_days / decay_rate / min_importance / trigger_interval / 兼容字段）。
- `ToolExecutor` 注册表加 `threading.RLock`。
- bootstrap 4 路并发（rag_chunk init / restore_from_db / restore_rag_from_db / init_sandbox）。
- `agent.status()` 统一可观测出口（rag_loaded / rag_mode / rag_chunks / 各 count / llm_model / embedding_model / is_mock / infrastructure）。
- Postgres 引入 `psycopg2.pool.ThreadedConnectionPool`（main 25 连接）。
- 在 `memory.consolidate / longterm.update` 等位置补 `inf.publish_event` 审计事件。

### Phase 5：包结构与小修（P2）

- `DAG.NodeType` 补 `THINK / AGGREGATE` 两种类型。
- `graph_runtime` race_group 改为 first-success 触发 cancel token（取代 post-hoc 比对）。
- 抽出 `sandbox/factory.py`。
- `config.py` 用 pydantic / 严格解析（拼错字段报错）。
- `main.py` 显式装配 KGStore 注入 agent；引入 Deps dataclass 容器。
- 删除 python 多出的 `/api/rag/query`，与 main 路由严格对齐（`/health` 保留）。
- 改写 `rag.py` 删除老 `_rrf_fuse` 双路 fallback，统一走 `HybridStore.search_multi`。
- 删除 hybrid 中 main 没有的 `_normalized_weights` / `_materialize_kg_only`。
- 改写 `rag.ingest` 把 PG/ES/Milvus 写入逻辑收敛到 hybrid 层。
- 改写 KG 写入为异步线程（main `goSafe`）+ panic recover。

## Impact

- Affected specs: 无前置 spec
- Affected code:
  - `final/internal/rag/{splitter,rewriter,reranker,rag,hybrid}.py`
  - `final/internal/memory/{memory,graph_memory}.py` + 新增 `preference.py`
  - `final/internal/agent/{agent,memory_writer,restore,cancel}.py` + 新增 `mem_stack.py` / `ctx_*.py` / `infra_*.py` 拆分文件
  - `final/internal/graph/task_graph.py`
  - `final/internal/sandbox/` 新增 `factory.py`
  - `final/internal/tools/tools.py`
  - `final/internal/repo/*.py`（接通到业务）
  - `final/internal/infra/infra.py`（精简，移除与 repo 重叠方法）
  - `final/internal/llm/llm.py`（新增 chat_stream_context）
  - `final/internal/handler/handler.py`（路由对齐 + 真 SSE）
  - `final/internal/platform/{milvus,postgres}.py`（集合名/索引/连接池）
  - `final/config/config.py`（严格解析）
  - `final/main.py`（显式 Deps 装配）
- 数据库迁移：PG `rag_chunks` 表保持兼容；`long_term_items` 表补 6 字段；Milvus 集合需 drop-and-recreate（**BREAKING**）

## ADDED Requirements

### Requirement: 真递归 Markdown 切片器

The system SHALL provide a `RecursiveSplitter` that splits text recursively along a separator stack, protects fenced code blocks as atomic units, and uses tail-rune overlap.

#### Scenario: Markdown 标题保护
- **WHEN** 输入文本含 `\n## 二级标题\n` 段
- **THEN** 切片不会在标题之后立刻断开，标题与下文保持在同一 chunk 中（除非超出 chunk_size）

#### Scenario: 代码块保护
- **WHEN** 输入文本含 ``` ... ``` 三反引号代码块
- **THEN** 代码块作为不可切原子，不会被分隔符切断

#### Scenario: tail-rune overlap
- **WHEN** chunk overlap > 0
- **THEN** 下一 chunk 的开头复用上一 chunk 末尾的 N 个 rune（中文/emoji 安全），不会切到字符中间

### Requirement: LongTerm Memory 完整字段与三阶段 Consolidate

The system SHALL store each long-term memory item with `id, content, importance, embedding, score, created_at, last_accessed, category, tags, slot_hint`, and SHALL run consolidate in three phases: per-item decay, dedup+merge, double-condition expiry.

#### Scenario: 按条目年龄衰减
- **WHEN** consolidate 触发
- **THEN** 每条 item 的 `importance` 按 `decay_rate^days_since_created_at` 衰减，而不是用全局 elapsed_days

#### Scenario: Merge 阶段
- **WHEN** 两条 item 相似度 `>= similarity_threshold` 但未达 dedup_threshold
- **THEN** 合并为一条：`content` 用"；"拼接，`embedding` 按 importance 加权平均，importance 取较大值

#### Scenario: 双条件淘汰
- **WHEN** consolidate 第三阶段
- **THEN** 仅当 `days_since_created > ttl_days` 且 `importance < min_importance` 时才淘汰

### Requirement: RecallByFilter

The system SHALL expose `LongTerm.recall_by_filter(query, q_emb, filter)` and `GraphMemory.recall_by_filter(...)` returning items filtered by categories / require_tags / max_age_hours / min_score and ranked by `sim*0.7 + importance*0.3`.

#### Scenario: 按 category 过滤
- **WHEN** filter.categories=["preference"]
- **THEN** 仅返回 category 为 "preference" 的 item

#### Scenario: 1-hop 图扩展
- **WHEN** GraphMemory 可用
- **THEN** 在 LTM seed 命中后扩展 1 跳邻居，扩展项也应用 categories filter，扩展项 `score=0.45`

### Requirement: 高入度保护接通

The system SHALL invoke `graph_memory.filter_protected(delete_ids, indegree_threshold=3)` inside `LongTerm.consolidate` (or `GraphAwareConsolidate` wrapper) to remove high-centrality memories from the delete list.

#### Scenario: 高入度记忆豁免
- **WHEN** memory 节点 indegree >= 3 且原本被 consolidate 标记为待删
- **THEN** 该节点从 delete_from_db 列表中移除，PG/Neo4j 都不删

### Requirement: 真 SSE 流式

The system SHALL implement `LLMClient.chat_stream_context(ctx, system, user, on_token)` that uses `requests.post(stream=True)` and parses `data:` frames incrementally, emitting tokens as they arrive.

#### Scenario: 流式 token
- **WHEN** 用户调 `/api/chat/stream`
- **THEN** SSE 响应按 token 实时推送，而不是同步生成完整 response 再一次性 emit

### Requirement: ragchunk Idempotent Upsert

The system SHALL write rag chunks to PG using `INSERT ... ON CONFLICT (doc_hash, chunk_idx) DO UPDATE SET content, parent_content, embedding RETURNING id`.

#### Scenario: 重复 ingest 同一文档
- **WHEN** 同一 doc_hash 二次 ingest
- **THEN** 不报 UNIQUE 冲突，已有行被更新，pg_id 不变

### Requirement: 真流式 LLM Cancel

The system SHALL pass `CancelToken` through to `LLMClient.chat_context` so that an in-flight HTTP request can be canceled when the user invokes `/api/chat/cancel`.

#### Scenario: 取消进行中的请求
- **WHEN** chat 进行中收到 cancel
- **THEN** 底层 HTTP 连接被断开（`requests.Session.close()` 或 `httpx` async cancel）

## MODIFIED Requirements

### Requirement: RAG Engine Ingest 路径

The system SHALL delegate all ingest writes (PG / ES / Milvus / KG) to `HybridStore.index_with_parents` (or equivalent), with KG writes performed in a background thread guarded by panic recover. `Engine.ingest` SHALL no longer contain direct PG/ES/Milvus calls.

### Requirement: RAG Engine Query 路径

The system SHALL only use `HybridStore.search_multi` for query. The legacy `_rrf_fuse` dual-path fallback SHALL be removed; mode switching (hybrid / semantic-only / keyword-only / unavailable) SHALL be decided inside `HybridStore` based on platform availability.

### Requirement: Rewriter / Reranker Prompt

The system SHALL use prompts that include:
- Rewriter: 5 hard constraints (output strict JSON; total queries equals N; each ≤50 chars; first query must be standalone; do not invent entities not in history).
- Reranker: 0-10 grading rubric (10 directly answers / 7-9 explicit fact / 4-6 weakly related / 1-3 co-occurrence / 0 unrelated); "scores count must equal candidate count"; "do not rely on your own knowledge".

## REMOVED Requirements

### Requirement: 旧定长滑窗 Splitter
**Reason**: main 早已删除，python 残留实现破坏 chunk 语义。
**Migration**: 现有 PG 内的 chunk 在迁移后 `chunk_idx` 序列变化；建议在迁移当晚 drop `rag_chunks` + Milvus 集合 `rag_chunks` 后重新 ingest 全量文档。

### Requirement: 旧 ReAct 迭代循环
**Reason**: main 已废弃迭代 ReAct，全部走图调度；python 双轨并存导致行为分叉。
**Migration**: 删除 `agent.py` 内的迭代分支，统一走 `graph_runtime`。

### Requirement: hybrid `_normalized_weights` / `_materialize_kg_only`
**Reason**: main 没有这两条逻辑，python 多出的优化导致权重行为与 main 不一致。
**Migration**: 删除两函数，统一用 main 的"所有 RRF 路径默认 1.0 权重"。

### Requirement: `/api/rag/query` 路由
**Reason**: main 无此路由，与对齐目标矛盾。
**Migration**: 删除 handler 注册；前端如有依赖改用 `/api/chat` + `use_rag=true`。
