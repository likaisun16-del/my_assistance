# Python Branch Main Alignment Design

## Goal

Bring the `python` branch to feature parity with the latest `origin/main` Go implementation while preserving the Python branch's runnable `final/` project layout.

Parity means the Python branch should expose equivalent behavior and operational controls for the main runtime capabilities: RAG retrieval, graph-based ReAct execution, prompt context assembly, memory integration, configuration, and persistence. It does not require copying Go package names or moving Python files to the exact Go directory tree.

## Current State

The Python branch already contains a broad translation of the older `final/` implementation:

- `final/internal/rag/hybrid.py` supports Milvus, Elasticsearch, Neo4j, and RRF fusion.
- `final/internal/memory/graph_memory.py` supports Neo4j-backed graph memory edges such as `FOLLOWS` and `SIMILAR_TO`.
- `final/internal/promptctx/` contains a Python prompt context assembly package, but it is not wired into `UnifiedAgent`.
- `final/internal/agent/agent.py` still runs a serial ReAct loop instead of the Go main branch's DAG runtime.

The latest Go main branch adds several capabilities not yet implemented or not yet wired in Python:

- RAG query rewriting, multi-query retrieval, reranking, recursive parent/child chunking, and small-to-big parent context recovery.
- A graph runtime with `TaskGraph`, dependency-aware topological scheduling, race groups, cancellation, snapshots, and task memory/tool-state tracking.
- Schema-driven prompt context assembly used by chat, tool, ReAct, and RAG flows.
- Additional config fields and persistence schema support, especially `rag_chunks.parent_content`.

## Scope

### In Scope

1. Translate the latest Go RAG behavior into Python:
   - Recursive splitter and parent/child chunking.
   - `Rewriter` and `LLMRewriter`.
   - `Reranker` and `LLMReranker`.
   - `HybridStore.search_multi`.
   - Parent content return and small-to-big context assembly.
   - `Engine.query_with_history`.
   - RAG document delete and restore parity where supported by the existing infrastructure.

2. Translate the latest Go graph runtime behavior into Python:
   - `TaskGraph`, `Node`, statuses, dependency validation, topological levels, and race group helpers.
   - Planner output with `id`, `tool`, `params`, `reason`, `depends_on`, and `race_group`.
   - `GraphRuntime` with per-level parallel execution, race groups, retry, cancellation, snapshots, and result aggregation.
   - Agent ReAct path changed from serial loop to Planner -> TaskGraph -> GraphRuntime.

3. Wire Python `promptctx` into `UnifiedAgent`:
   - Build an agent-level prompt context object with assembler, task memory, and tool tracker.
   - Register sources for profile, planner state, task memory, tool state, sandbox constraints, and recall.
   - Use assembled context in chat, tool, ReAct planner, ReAct finalization, and RAG answer generation.

4. Align config and persistence:
   - Add Python config fields for RAG rewrite/rerank and graph runtime.
   - Parse matching YAML keys from `final/config/config.yaml`.
   - Ensure PostgreSQL schema supports `rag_chunks.parent_content`.
   - Add infrastructure/repository helpers for saving, loading, deleting, and restoring parent-aware RAG chunks.

5. Add focused tests:
   - RAG splitter, rewrite fallback, rerank parsing/fallback, search multi merge, and small-to-big assembly.
   - TaskGraph validation, topological levels, race grouping, and runtime execution.
   - Prompt context assembly wiring and source behavior.
   - Config parsing defaults for new fields.

### Out of Scope

- Rewriting the Python branch into the exact Go directory layout.
- Implementing the portfolio claim around RAGAS, Golden Queries, or benchmark metrics unless executable support already exists in main. The current branch comparison did not find a runnable main-branch benchmark implementation to translate.
- Changing frontend UX unless backend response shapes require minimal compatibility updates.
- Adding new external services beyond the existing Milvus, Elasticsearch, Neo4j, PostgreSQL, Kafka, LLM, and Tavily integrations.

## Architecture

The Python branch will keep `final/` as the project root and use focused Python modules that mirror the Go main branch's responsibilities.

### RAG

RAG remains under `final/internal/rag/`.

- `splitter.py` will provide recursive parent and child splitting.
- `rewriter.py` will provide history-aware multi-query rewrite with strict JSON parsing and fallback to the original query.
- `reranker.py` will provide listwise reranking with strict JSON parsing and fallback to RRF order.
- `hybrid.py` will be extended with `search_multi`, rerank support, parent content on `HybridResult`, and parent-aware loading.
- `rag.py` will orchestrate parent/child ingest, optional KG indexing with PG IDs, query rewriting, multi-query retrieval, reranking, small-to-big context selection, and answer generation.

All LLM-assisted layers must degrade gracefully. If rewrite or rerank parsing fails, the request continues with the original query or RRF order.

### Graph Runtime

Graph runtime will live under `final/internal/graph/` or `final/internal/agent/`, depending on existing import boundaries:

- `final/internal/graph/task_graph.py` owns graph data structures and validation.
- `final/internal/agent/graph_runtime.py` owns execution because it depends on the agent, tools, snapshots, and prompt context.
- `final/internal/agent/planner.py` will return graph nodes instead of only linear plan items.
- `final/internal/agent/agent.py` will replace serial ReAct execution with Planner -> TaskGraph -> GraphRuntime.

The runtime will execute independent nodes in parallel using Python threads, because current tools are synchronous. Race groups use first successful result wins. Cancellation will use the existing cancel token registry.

### Prompt Context

The existing `final/internal/promptctx/` package will become part of the main agent flow.

`UnifiedAgent` will create a prompt context bundle at startup with:

- `ContextAssembler`
- `TaskMemBuffer`
- `ToolStateTracker`
- `SourceRegistry`

The agent will use this bundle through one method, such as `_build_context_prefix(query, mode)`, so prompt context construction is centralized and each mode gets consistent context.

GraphRuntime will push each tool observation into task memory and record tool call state after each node completes or fails.

### Persistence

The Python infrastructure layer will expose parent-aware RAG chunk operations:

- Save child chunk with optional parent content.
- Load chunks by IDs with parent content.
- Load all chunks with parent content.
- Delete all chunks for a document hash.

PostgreSQL initialization should add `parent_content TEXT` with `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, matching the Go branch's backward-compatible migration.

## Data Flow

### RAG Ingest

1. User uploads or ingests a document.
2. Parent splitter creates larger context chunks.
3. Child splitter creates smaller retrieval chunks for each parent.
4. Child chunks are saved to PostgreSQL with parent content.
5. Child chunks are indexed into Elasticsearch and Milvus when available.
6. KG indexing runs best-effort with real PG IDs so graph hits can participate in RRF.

### RAG Query

1. Agent gathers recent chat history.
2. Rewriter converts the question into one or more retrieval queries.
3. HybridStore searches each query and merges the candidates with cross-query RRF.
4. Optional reranker reorders the candidate pool and truncates to `top_k`.
5. Parent content replaces child content when available.
6. LLM answer generation receives the small-to-big context.

### ReAct Query

1. Agent assembles context for planning.
2. Planner LLM emits graph nodes with dependencies and race groups.
3. TaskGraph validates the plan; invalid dependency plans degrade to an all-parallel graph.
4. GraphRuntime executes one topological level at a time.
5. Nodes in the same race group run concurrently and the first successful node wins.
6. Tool observations update task memory and tool state.
7. Final answer is generated from graph observations and assembled context.

## Error Handling

- RAG rewrite and rerank never block the main answer path. Parse failures and LLM failures fall back to original query and original RRF order.
- Milvus, Elasticsearch, and Neo4j remain optional. Search paths skip unavailable backends and continue with available ones.
- Graph planning falls back to rule-based nodes if LLM planning fails or emits invalid JSON.
- Invalid graph dependencies are degraded into a graph with no dependencies, preserving tool execution when possible.
- Runtime node failures are captured in node results and prompt context; other independent nodes continue unless cancellation is requested.
- Persistence helpers catch service-specific failures where the existing code already treats infrastructure as best-effort.

## Testing Strategy

Tests will be written before implementation for each behavior group.

RAG tests will use fake infrastructure and fake LLM callbacks so they do not require live Milvus, Elasticsearch, Neo4j, or PostgreSQL. They will verify parent/child ingest, search multi merge, rewrite fallback, rerank ordering, and parent context use.

Graph runtime tests will use fake tools with deterministic delays and failures. They will verify topological ordering, race winner selection, cancellation status propagation, retry behavior, and task memory/tool tracking calls.

Prompt context tests will use fake sources and agent dependencies to verify that `UnifiedAgent` assembles mode-specific context and that GraphRuntime records observations.

Config tests will load temporary YAML files and assert the new rewrite, rerank, and graph runtime defaults and overrides.

## Acceptance Criteria

- Python branch exposes equivalent main-branch behavior for RAG rewrite/rerank/SearchMulti/small-to-big.
- Python ReAct flow uses a DAG runtime with dependency and race-group support.
- Python prompt context package is wired into agent flows and runtime observations.
- New config fields parse from YAML and have sensible defaults.
- Parent-aware RAG chunk persistence is supported without breaking old rows.
- Focused Python tests pass.
- Basic import/startup checks for `final/main.py` and key modules pass.

