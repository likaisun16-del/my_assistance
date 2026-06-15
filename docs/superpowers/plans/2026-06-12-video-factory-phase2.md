# Video Factory · Phase 2 · TTS + 字幕（半成片）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.
>
> **⚠️ 状态：中等细度 plan**。Task 级骨架已写清，每个 task 内部的"step + 代码"在 Phase 1 验收 + 火山 TTS 联调后再细化（避免依赖未稳定时的纸面方案）。开始本 phase 前应该把每个 task 拆到 2-5 分钟一步的颗粒度（参考 phase1 的写法）。

**Goal:** 用户提交主题后，30 秒内拿到 mp3（口播配音）+ srt（字幕）；任务从同步执行升级为 Kafka 异步 + 进度回查。

**Architecture:** 引入 `task-svc` 独立进程 + Kafka topic + Python Agent worker。Go 收到请求 → 写入 tasks 表 + 投递 Kafka → Python worker 消费 → 调火山 TTS 流式接口 → 文件写 MinIO → 写回结果到 PG → 通过 SSE 推前端进度。

**Tech Stack:** + sarama（Kafka）+ minio-go + 火山方舟 TTS HTTP / WebSocket + whisper-timestamps（字幕兜底）

**Inputs from Phase 1:**
- `users` / `projects` / `tasks` 三张表 + JWT
- Agent `/api/script/generate` 契约
- Go agent_client 模式

**Outputs to Phase 3:**
- `task_steps` 表（DAG 步骤追踪）
- `assets` 表 + MinIO 预签名 URL
- TTS 文件命名规则 `{user_id}/{task_id}/audio.mp3`
- Kafka topic 命名规则 `video-factory.task.{type}`

---

## File Structure

video-factory（新增/修改）：

```
cmd/
├── user-svc/main.go              # 不变（or 拆出 task-svc）
├── task-svc/main.go              # NEW
├── render-worker/main.go         # NEW（先空壳，phase 3 实现）
└── tts-worker/main.go            # NEW（消费 Kafka 调 Agent）
internal/
├── pkg/
│   ├── mq/kafka.go               # NEW
│   └── storage/minio.go          # NEW
├── data/repo/
│   ├── asset.go                  # NEW
│   └── task_step.go              # NEW
├── biz/
│   ├── tts.go                    # NEW
│   └── pipeline.go               # NEW（DAG 编排）
└── service/
    ├── asset_handler.go          # NEW（预签名上传/下载）
    └── task_progress_sse.go      # NEW（SSE 推送进度）
migrations/
├── 004_assets.sql                # NEW
└── 005_task_steps.sql            # NEW
```

AGI-assistant：

```
final/internal/agent/
├── tts.py                        # NEW: 火山方舟 TTS 流式封装
└── subtitle_aligner.py           # NEW: srt 生成
final/internal/handler/handler.py # 加 /api/tts/synthesize, /api/subtitle/align
final/internal/worker/            # NEW 目录
└── tts_worker.py                 # NEW: 消费 Kafka 跑 TTS
```

---

## Task 1: 数据模型扩展（assets + task_steps）

**Files:**
- Create: `migrations/004_assets.sql`、`migrations/005_task_steps.sql`
- Create: `internal/data/repo/asset.go`、`internal/data/repo/task_step.go`
- Test: `internal/data/repo/asset_test.go`

**Step 概要**：
- assets schema：`id, user_id, project_id, kind('audio'/'subtitle'/'image'/'video'), mime, size, url(MinIO key), meta jsonb`
- task_steps schema：`id, task_id, step_name, status, started_at, ended_at, error, output jsonb`
- repo 层 CRUD（参考 Phase 1 task repo）

待开始时按 Phase 1 颗粒度拆步骤。

---

## Task 2: MinIO 客户端 + 预签名 URL handler

**Files:**
- Create: `internal/pkg/storage/minio.go`
- Create: `internal/service/asset_handler.go`
- Modify: `deploy/docker-compose.yml`（加 minio + console）
- Modify: `configs/config.yaml`

**Step 概要**：
- minio-go 初始化 + bucket 创建
- 预签名上传 PUT URL（15 分钟有效）+ 预签名下载 GET URL
- handler：`POST /api/v1/assets/presign`（请求 kind+mime → 返回 PUT URL + asset_id）
- 写测试用 minio testcontainer 或 mock httptest

---

## Task 3: Kafka 客户端封装

**Files:**
- Create: `internal/pkg/mq/kafka.go`
- Modify: `deploy/docker-compose.yml`（加 kafka + zookeeper）
- Modify: `configs/config.yaml`

**Step 概要**：
- Producer：sarama SyncProducer，`Send(ctx, topic, key, payload)` 返回 partition+offset
- Consumer Group：抽出 `Subscribe(topic, handler func(msg) error)` 接口
- 失败重试 + DLQ topic（`{topic}.dlq`）
- 测试：sarama mock broker

---

## Task 4: 火山方舟 TTS Python 封装

**Files:**
- Create: `final/internal/agent/tts.py`
- Modify: `final/internal/handler/handler.py`（加 `/api/tts/synthesize`）
- Test: `final/tests/test_tts.py`

**Step 概要**：
- 接口：`tts_synthesize(text, voice='zh_female_qingxin', speed=1.0) -> bytes`
- 协议：先用 HTTP 一次性合成（火山 v3 voice/synthesize），后期升级到 WebSocket 流式
- 切片：超长文本按句号切成 < 2KB 的 chunk，分别合成后用 pydub 拼接
- 测试：mock requests，断言 Authorization 头 + payload schema

**API 契约**（与 Go 对齐）：

```http
POST /api/tts/synthesize
{
  "text": "完整脚本文本（hook + body + cta 拼接）",
  "voice": "zh_female_qingxin",
  "speed": 1.0,
  "asset_key": "u123/t456/audio.mp3"   # MinIO key，Python 直接上传到 MinIO
}

200 OK
{
  "asset_key": "u123/t456/audio.mp3",
  "duration_ms": 60123,
  "size_bytes": 482001
}
```

---

## Task 5: 字幕对齐（srt 生成）

**Files:**
- Create: `final/internal/agent/subtitle_aligner.py`
- Modify: `final/internal/handler/handler.py`（加 `/api/subtitle/align`）
- Test: `final/tests/test_subtitle_aligner.py`

**Step 概要**：
- 优先使用火山 TTS 自带 timestamp（响应里的 `phoneme_timestamps`）→ 转 srt
- 兜底：若 TTS 不返回时间戳，调 whisper-timestamps（faster-whisper）做强制对齐
- 输出 srt 文件 → 上传 MinIO

**API 契约**：

```http
POST /api/subtitle/align
{
  "audio_asset_key": "u123/t456/audio.mp3",
  "script_text": "...",
  "output_asset_key": "u123/t456/subtitle.srt"
}

200 OK
{ "asset_key": "...", "cue_count": 12, "duration_ms": 60123 }
```

---

## Task 6: tts-worker（Python 端 Kafka 消费 → 调 TTS → 回写）

**Files:**
- Create: `final/internal/worker/tts_worker.py`
- Modify: `final/main.py`（可选：通过 env var 切换 worker 模式）

**Step 概要**：
- 消费 topic `video-factory.task.tts`
- 每条消息含 `task_id, project_id, user_id, script_text, voice, speed`
- 跑 TTS → 写 MinIO → 通过 HTTP 回写 Go：`POST /api/v1/tasks/{id}/callback`（携带 asset_id + 状态）
- 失败：重试 3 次，最终失败投 DLQ
- 测试：用 testcontainers Kafka 跑端到端

---

## Task 7: Go 端 DAG pipeline（script → tts → subtitle）

**Files:**
- Create: `internal/biz/pipeline.go`
- Modify: `internal/biz/script.go`（不再同步执行，改为入队 + 进度跟踪）
- Modify: `internal/service/script_handler.go`（返回 task_id 立即响应）

**Step 概要**：
- pipeline 节点：`script -> tts -> subtitle`
- 每节点完成 → 写 task_step + 推下一节点入队
- 节点失败 → pipeline 终止 + 标 task.status='failed'
- handler 改成 202 Accepted + Location 头指向 `/api/v1/tasks/{id}`

---

## Task 8: SSE 推送任务进度

**Files:**
- Create: `internal/service/task_progress_sse.go`
- Modify: `internal/server/router.go`

**Step 概要**：
- 路由 `GET /api/v1/tasks/{id}/events`（SSE）
- 数据源：tts-worker 通过 callback 写到 Redis pub/sub `task:progress:{id}`，handler 订阅转发
- 心跳：每 15s 一个 ping 事件
- 客户端断开 → 取消订阅

---

## Task 9: 端到端联调 + 验收脚本

**Files:**
- Create: `scripts/e2e_phase2.sh`

**E2E 流程**：

```bash
TOKEN=$(curl -s -X POST :8080/api/v1/auth/login -d '{...}' | jq -r .token)
TASK=$(curl -s -X POST :8080/api/v1/scripts \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"title":"x","topic":"...","duration":60,"render_audio":true}' | jq -r .task_id)
# SSE 监听
curl -N -H "Authorization: Bearer $TOKEN" :8080/api/v1/tasks/$TASK/events
# 应该看到：script.started, script.done, tts.started, tts.done, subtitle.done
# 拿下载链接
curl -s -H "Authorization: Bearer $TOKEN" :8080/api/v1/tasks/$TASK | jq .assets
# → [{kind:audio, url:"https://minio..."}, {kind:subtitle, url:"..."}]
```

---

## Phase 2 验收

- [ ] 单条任务 30 秒内产出 mp3 + srt（mp3 时长 60 秒为基准）
- [ ] Kafka 消费幂等（同一任务重复投递不会产生两份资产）
- [ ] tts-worker 进程被 kill 后 task 自动重试
- [ ] SSE 进度事件按节点推送，无丢失
- [ ] 单条 TTS 成本 ≤ ¥0.05（火山方舟现价 ≈ 0.3 元/分钟）

---

## Self-Review

**1. Spec coverage**
- ✅ asset-svc：MinIO + 预签名（T2）
- ✅ 任务编排 DAG（T7）
- ✅ assets/task_steps 表（T1）
- ✅ 火山 TTS（T4）
- ✅ 字幕对齐（T5）

**2. 中等细度的合理性** — Phase 2 的实现细节强依赖 Phase 1 的契约稳定 + 火山 TTS 实际响应格式（特别是 timestamp 字段是否存在）。在 Phase 1 验收前细化反而会出错。开始 phase 2 时按 phase 1 写法把每个 task 展开。

**3. Type consistency** — 所有 API 契约都给了 JSON schema；后续细化时以本文档为锚。

---

## 开始 Phase 2 前的前置条件

- Phase 1 验收全部通过（含端到端 curl 演示）
- 火山方舟 TTS API 已开通 + 取得 voice_id
- 本文档 task 1-9 已逐一展开到 step + 代码 + 命令颗粒度
