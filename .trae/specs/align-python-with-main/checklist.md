# Checklist

> 每个 checkpoint 都对应 spec.md 中的一项 ADDED / MODIFIED / REMOVED Requirement 或 Task 验证点。Phase 之间的 checkpoint 须按序验证。

## Phase 1：消除假实现 / 死代码 / 行为不一致

- [ ] `RecursiveSplitter` 已重写为分隔符栈递归切分；不再是定长滑窗
- [ ] Splitter 对 ` ``` ` 代码块整体保留，不会被 `\n` 切断
- [ ] Splitter 对 `^#{1,6} ` 标题保护：标题与下一段尽量同 chunk
- [ ] tail-rune overlap 用 rune（中文/emoji 不切半），有单测覆盖
- [ ] `rewriter.py` 系统提示包含 5 条硬约束（严格 JSON / 数量等于 N / ≤50 字 / 首条独立 / 不臆造实体）
- [ ] `reranker.py` 系统提示包含 0-10 五档评分准则与"不依赖自身知识"约束
- [ ] reranker 解析层校验"得分数必须等于候选数"，不一致时报警/截断
- [ ] `final/internal/repo/` 下文件被 agent / memory / rag / handler 真实 import 调用
- [ ] `infra.py` 移除与 repo 重叠的持久化方法
- [ ] `chat_history` 表每次对话写入 user / assistant 两行
- [ ] `infra.save_rag_chunk_with_parent` SQL 使用 `ON CONFLICT (doc_hash, chunk_idx) DO UPDATE ... RETURNING id`
- [ ] 重复 ingest 相同 doc_hash 不报 UNIQUE 错误
- [ ] Milvus 集合名为 `rag_chunks`（非 `rag_embeddings`）
- [ ] Milvus 集合 schema 含 pg_id PK Int64 / content VarChar(4096) / embedding FloatVector(dim)
- [ ] Milvus 索引为 IVF_FLAT + L2 + nlist=128
- [ ] 启动期检测到 PK 字段名 / 维度漂移会打 warning
- [ ] `LLMClient.chat_stream_context` 使用 `requests.post(stream=True)` + `data:` 帧解析，token 即出
- [ ] handler `/api/chat/stream` 真按 token 实时推送（curl 抓包验证）
- [ ] CancelToken 透传，cancel 时 HTTP 连接关闭

## Phase 2：Memory 层全面对齐

- [ ] `memory._compute_similarity` 实现为 cosine（dot/(|a|*|b|)）
- [ ] embedding 不可用时回退到 TF 词袋而非字符切词
- [ ] `LongTerm.Item` 含 `created_at / last_accessed / category / tags / slot_hint / score`
- [ ] PG `long_term_items` 表已迁移上述 6 列
- [ ] `restore_from_db` 完整恢复 6 字段
- [ ] `LongTerm.store_classified` 已实现，含入库前 cosine dedup
- [ ] dedup 命中时只更新 importance/Tags/Category/SlotHint，不插新行
- [ ] `consolidate` 阶段 1 按每条 `created_at` 单独衰减
- [ ] `consolidate` 阶段 2 含 dedup + **merge**（content 拼接、embedding 加权平均）
- [ ] `consolidate` 阶段 3 双条件淘汰 `days>TTL && importance<min_importance`
- [ ] `consolidate` 返回 `ConsolidationResult{deduped, merged, expired, delete_from_db, update_in_db}`
- [ ] `LongTerm.recall_by_filter` 已实现，按 categories/require_tags/max_age_hours/min_score 过滤
- [ ] recall 排序使用 `sim*0.7 + importance*0.3`
- [ ] recall 命中后回写 `last_accessed`
- [ ] PromptCtx 中 `RecallSource` 死代码已删除，统一调 recall_by_filter
- [ ] `filter_by_category` 真按 categories 过滤（参数生效）
- [ ] `consolidate` 调用 `graph_memory.filter_protected(threshold=3)`
- [ ] indegree>=3 的记忆从 `delete_from_db` 列表移除
- [ ] `MemoryManager.recall` 单方法实现 LTM → graph 1-hop 扩展 → 排序截 TopK
- [ ] 扩展项标 `score=0.45` 且应用 categories filter
- [ ] `graph_memory.add_to_graph` 异步执行，含 panic recover
- [ ] `ShortTerm` 含 `timestamp` 字段、`RLock`、`collections.deque`
- [ ] `final/internal/memory/preference.py` 新建，含 `extract_and_save` + `build_context`
- [ ] preference `build_context` 渲染【用户偏好】块
- [ ] LongTerm 暴露 `sync_last_item_pg_id / find_by_id / snapshot / last_id / last_item`
- [ ] GraphMemory 暴露 `sync_prev_id / set_consolidation_config / need_consolidation`

## Phase 3：Agent 编排对齐

- [ ] `memory_writer.classify_memory_content` 含 4 条规则
- [ ] `memory_writer.llm_classify_memory` 7 类 6 槽
- [ ] `memory_writer.sync_consolidation_to_db` 处理 DeleteFromDB / UpdateInDB
- [ ] memory_writer 调用 `graph_mem.store_classified` / `ltm.save_classified` / `sync_last_item_pg_id`
- [ ] `restore.py` 恢复 LTM rows 含 ID/CreatedAt/LastAccessed
- [ ] `restore.py` 恢复 Preference
- [ ] `restore.py` 复用 KG client 并执行 attachGraph 三件套
- [ ] 旧 ReAct 迭代循环已从 `agent.py` 删除
- [ ] agent 主入口拆为 `prepare → dispatch → finalize` 三段
- [ ] finalize 根据 graph_memory 切换 `GraphAwareConsolidate` / 普通 `Consolidate`
- [ ] `mode_tool.fill_params_from_preference` 已实现 5 偏好键多参数名映射

## Phase 4：抽象与并发

- [ ] `cancel.py` 改为 `dict[int64, CancelToken]` + 周期 snapshot
- [ ] `mem_stack.py` 已抽出，含 `ConsolidationConfig` 7 字段
- [ ] `ToolExecutor` 注册表带 `threading.RLock`
- [ ] bootstrap 4 路并发（rag_chunk init / restore_from_db / restore_rag_from_db / init_sandbox）
- [ ] `agent.status()` 输出 rag_loaded / rag_mode / rag_chunks / 各 count / llm_model / embedding_model / is_mock / infrastructure
- [ ] PG 使用 `ThreadedConnectionPool`（min=5 / max=25）
- [ ] `memory.consolidate` 与 `longterm.update` 调 `inf.publish_event` 审计事件

## Phase 5：包结构与小修

- [ ] `DAG.NodeType` 含 `THINK` 与 `AGGREGATE`
- [ ] `graph_runtime` race_group 改用 first-success 触发 cancel token
- [ ] `final/internal/sandbox/factory.py` 已抽出
- [ ] `config.py` 使用 pydantic 严格解析，未知字段报错
- [ ] `main.py` 引入 Deps dataclass 容器，显式注入 KGStore 给 agent
- [ ] `/api/rag/query` 路由已删除；`/health` 保留
- [ ] `rag.py` 删除 `_rrf_fuse`，统一 `HybridStore.search_multi`
- [ ] hybrid 删除 `_normalized_weights` / `_materialize_kg_only`
- [ ] `rag.ingest` 不再直接写 PG/ES/Milvus，全部走 hybrid 层
- [ ] KG 写入异步线程 + panic recover

## REMOVED 验证

- [ ] 旧定长滑窗 splitter 实现已物理删除
- [ ] 旧 ReAct 迭代分支已物理删除
- [ ] hybrid `_normalized_weights` / `_materialize_kg_only` 已物理删除
- [ ] handler `/api/rag/query` 注册代码已删除
