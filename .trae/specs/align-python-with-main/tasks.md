# Tasks

> 本任务列表对齐 spec.md 的 5 期目标。每期内任务从 P0 → P2 排序；同期内**无依赖的任务允许并行**，跨期任务**必须按 Phase 顺序推进**（Phase 2 依赖 Phase 1 的 Splitter 行为，Phase 3 依赖 Phase 2 的 Memory 接口）。

## Phase 1：消除假实现 / 死代码 / 行为不一致（P0）

- [x] Task 1: 重写 RecursiveSplitter 为真递归分隔符栈
  - [x] SubTask 1.1: 在 `final/internal/rag/splitter.py` 实现分隔符栈 `["\n\n", "\n", "。", "！", "？", "；", " ", ""]` 的递归切分
  - [x] SubTask 1.2: Markdown 标题（`^#{1,6} `）与 ` ``` ` 围栏代码块识别为不可切原子
  - [x] SubTask 1.3: tail-rune overlap：用 `list(s)[-N:]` 拼接而非 byte slice，保证中文/emoji 不切半
  - [x] SubTask 1.4: 在 `final/tests/test_rag_alignment.py` 增加 3 条用例（普通/标题/代码块）

- [x] Task 2: 改写 Rewriter / Reranker prompt
  - [x] SubTask 2.1: `final/internal/rag/rewriter.py` 加 5 条硬约束（严格 JSON / 数量等于 N / ≤50 字 / 首条独立 / 不臆造实体）
  - [x] SubTask 2.2: `final/internal/rag/reranker.py` 加 0-10 详细打分准则与"不依赖自身知识"约束
  - [x] SubTask 2.3: 解析层补"得分数必须等于候选数"的校验

- [x] Task 3: 接通 `final/internal/repo/` 死代码
  - [x] SubTask 3.1: 梳理 `repo/` 与 `infra.py` 重叠方法清单
  - [x] SubTask 3.2: agent / memory / rag / handler 改用 repo 接口
  - [x] SubTask 3.3: 从 `infra.py` 移除被 repo 覆盖的方法

- [x] Task 4: chat_history 真持久化
  - [x] SubTask 4.1: `final/internal/agent/agent.py` 在每次 chat 完成后写 user / assistant 两行
  - [x] SubTask 4.2: 引入 `repo.chat_history.append(role, content, ts)` 接口

- [x] Task 5: ragchunk Idempotent Upsert
  - [x] SubTask 5.1: `infra.save_rag_chunk_with_parent` 改 `ON CONFLICT (doc_hash, chunk_idx) DO UPDATE` 并 RETURNING id
  - [x] SubTask 5.2: 验证重复 ingest 不报 UNIQUE

- [x] Task 6: Milvus 集合对齐
  - [x] SubTask 6.1: 集合名 `rag_embeddings` → `rag_chunks`
  - [x] SubTask 6.2: 显式 schema：pg_id PK Int64 / content VarChar(4096) / embedding FloatVector(dim)
  - [x] SubTask 6.3: IVF_FLAT + L2 + nlist=128 索引
  - [x] SubTask 6.4: 启动期 PK 字段名 / 维度漂移自检并打印 warning

- [x] Task 7: 真 SSE 流式 LLM
  - [x] SubTask 7.1: `final/internal/llm/llm.py` 新增 `chat_stream_context(ctx, system, user, on_token)`
  - [x] SubTask 7.2: 用 `requests.post(stream=True)` 解析 `data:` 帧
  - [x] SubTask 7.3: handler `/api/chat/stream` 切换到流式接口
  - [x] SubTask 7.4: 透传 CancelToken，cancel 时关连接

## Phase 2：Memory 层全面对齐（P0）

- [x] Task 8: cosine similarity 替换 Jaccard
  - [x] SubTask 8.1: `final/internal/memory/memory.py:_compute_similarity` 改 cosine（`dot/(|a|*|b|)`）
  - [x] SubTask 8.2: embedding 缺失时回退 TF 词袋而非字符切词

- [x] Task 9: LongTerm.Item 补 6 字段 + 迁移
  - [x] SubTask 9.1: 数据类补 `created_at / last_accessed / category / tags / slot_hint / score`
  - [x] SubTask 9.2: PG `long_term_items` 表新增列（含 default）
  - [x] SubTask 9.3: `restore_from_db` 完整恢复 6 字段

- [x] Task 10: store_classified + 入库前 dedup
  - [x] SubTask 10.1: 实现 `store_classified(content, importance, emb, category, tags, slot_hint)`
  - [x] SubTask 10.2: cosine ≥ DedupThreshold 时只更新 importance/Tags/Category/SlotHint

- [x] Task 11: 三阶段 consolidate
  - [x] SubTask 11.1: 阶段 1 按条目 created_at 衰减
  - [x] SubTask 11.2: 阶段 2 dedup + **merge**（content 拼接、embedding 加权平均）
  - [x] SubTask 11.3: 阶段 3 双条件 `days>TTL && importance<min_importance` 淘汰
  - [x] SubTask 11.4: 返回 `ConsolidationResult{deduped, merged, expired, delete_from_db, update_in_db}`

- [x] Task 12: recall_by_filter
  - [x] SubTask 12.1: 实现 `LongTerm.recall_by_filter(query, q_emb, RecallFilter)`
  - [x] SubTask 12.2: `score = sim*0.7 + importance*0.3` 排序
  - [x] SubTask 12.3: 命中时回写 `last_accessed`
  - [x] SubTask 12.4: 在 promptctx 中使用，删除 RecallSource 死代码

- [x] Task 13: filter_by_category 真过滤

- [x] Task 14: 高入度保护接通
  - [x] SubTask 14.1: consolidate 第 3 阶段调 `graph_memory.filter_protected(delete_ids, threshold=3)`
  - [x] SubTask 14.2: 受保护 id 从 delete_from_db 列表移除

- [x] Task 15: MemoryManager.recall 重写为单方法
  - [x] SubTask 15.1: `ltm.recall_by_filter` → `graph_memory.find_related` 1-hop 扩展
  - [x] SubTask 15.2: 扩展项应用 categories filter 并标 `score=0.45`
  - [x] SubTask 15.3: 全集排序截 TopK

- [x] Task 16: graph_memory.add_to_graph 异步化（含 panic recover）

- [x] Task 17: ShortTerm 加 timestamp + RLock + deque

- [x] Task 18: Preference 拆独立包
  - [x] SubTask 18.1: 新建 `final/internal/memory/preference.py`
  - [x] SubTask 18.2: `extract_and_save("我喜欢/我爱/我叫" 规则)`
  - [x] SubTask 18.3: `build_context()` 渲染【用户偏好】块

- [x] Task 19: LongTerm/GraphMemory 访问器补齐
  - [x] SubTask 19.1: LongTerm: `sync_last_item_pg_id / find_by_id / snapshot / last_id / last_item`
  - [x] SubTask 19.2: GraphMemory: `sync_prev_id / set_consolidation_config / need_consolidation`

## Phase 3：Agent 编排对齐（P0）

- [x] Task 20: 重写 memory_writer
  - [x] SubTask 20.1: `classify_memory_content` 4 规则（identity/preference/tool_failure/policy）
  - [x] SubTask 20.2: `llm_classify_memory` 7 类 6 槽
  - [x] SubTask 20.3: `sync_consolidation_to_db(DeleteFromDB, UpdateInDB)`
  - [x] SubTask 20.4: 调 `graph_mem.store_classified` / `ltm.save_classified` / `sync_last_item_pg_id`

- [x] Task 21: 重写 restore.py
  - [x] SubTask 21.1: 恢复 LTM rows 含 ID/CreatedAt/LastAccessed
  - [x] SubTask 21.2: 恢复 Preference
  - [x] SubTask 21.3: KG client 复用 + `sync_prev_id` + attachGraph 三件套

- [x] Task 22: 删除老 ReAct 迭代循环，统一图调度

- [x] Task 23: agent 主入口拆 prepare → dispatch → finalize
  - [x] SubTask 23.1: 三段拆分
  - [x] SubTask 23.2: finalize 根据 graph_memory 切 `GraphAwareConsolidate` / 普通 `Consolidate`

- [x] Task 24: mode_tool 补 `fill_params_from_preference`（5 偏好键多参数名映射）

## Phase 4：抽象与并发（P1）

- [x] Task 25: cancel.py 多任务管理
  - [x] SubTask 25.1: 由单 token 改 `dict[int64, CancelToken]`
  - [x] SubTask 25.2: 周期 snapshot 防泄漏

- [x] Task 26: 抽 mem_stack.py + ConsolidationConfig（7 字段）

- [x] Task 27: ToolExecutor 注册表加 RLock

- [x] Task 28: bootstrap 4 路并发（rag_chunk init / restore_from_db / restore_rag_from_db / init_sandbox）

- [x] Task 29: agent.status() 统一可观测出口

- [x] Task 30: Postgres ThreadedConnectionPool（min/max=5/25）

- [x] Task 31: memory.consolidate / longterm.update 等位置补 `inf.publish_event` 审计事件

## Phase 5：包结构与小修（P2）

- [x] Task 32: DAG.NodeType 补 THINK / AGGREGATE

- [x] Task 33: graph_runtime race_group 改 first-success cancel token

- [x] Task 34: 抽 sandbox/factory.py

- [x] Task 35: config.py 用 pydantic 严格解析（拼错字段报错）

- [x] Task 36: main.py 显式装配 KGStore 注入 agent + 引入 Deps dataclass 容器

- [x] Task 37: 删除 `/api/rag/query`（保留 `/health`）

- [x] Task 38: rag.py 删 `_rrf_fuse` 双路 fallback，统一走 `HybridStore.search_multi`

- [x] Task 39: 删 hybrid 中 `_normalized_weights` / `_materialize_kg_only`

- [x] Task 40: rag.ingest 把 PG/ES/Milvus 写入收敛到 hybrid 层

- [x] Task 41: KG 写入异步线程 + panic recover

# Task Dependencies

- Task 4 depends on Task 3（chat_history 走 repo 层接口）
- Task 7 SubTask 7.4 depends on Task 25（CancelToken 多任务化）
- Task 10 / 11 / 12 depend on Task 8 + Task 9（cosine + 完整字段是入库分类、merge、recall 的前提）
- Task 14 depends on Task 11（先有三阶段才能在阶段 3 接 filter_protected）
- Task 15 depends on Task 12 + Task 16（recall_by_filter + 异步图写入）
- Task 18 → Task 24（fill_params_from_preference 依赖独立 preference 包）
- Task 20 / 21 depend on Task 9 / 10 / 11 / 19（memory_writer 与 restore 调用 LongTerm 新接口）
- Task 22 / 23 depend on Task 20 / 21（先有 memory_writer / restore 才好删迭代版本）
- Task 28 depends on Task 21（bootstrap 并发恢复依赖 restore 重写完毕）
- Task 36 depends on Task 35（pydantic config）
- Task 38 / 39 / 40 depend on Task 1（splitter 切片行为先定）
- Task 41 depends on Task 16 的异步 panic recover 模式
