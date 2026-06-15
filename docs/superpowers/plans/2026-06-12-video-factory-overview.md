# Video Factory · 总览 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.
> 本文是 epic 总览，每个 Phase 有独立 plan：
> - [Phase 1 · 脚本生成 MVP](./2026-06-12-video-factory-phase1.md)
> - [Phase 2 · TTS + 字幕（半成片）](./2026-06-12-video-factory-phase2.md)
> - [Phase 3 · B-roll + 封面 + 成片](./2026-06-12-video-factory-phase3.md)
> - [Phase 4 · 风格学习 + 商业化](./2026-06-12-video-factory-phase4.md)

**Goal:** 构建一个"主题 → 30 秒口播短视频"的端到端 AIGC SaaS 平台，把 AGI-assistant 的 Agent 内核作为 AI 服务层接入。

**Architecture:** Go (Gin) 后端做用户/任务/资产/计费/编排，Python AI Agent（复用 final/）做脚本生成/风格学习/质检；通过 HTTP+SSE（同步）和 Kafka（异步）双通道协作。基础设施：PostgreSQL/Redis/Kafka/MinIO/Milvus/Neo4j/ES。

**Tech Stack:**
- Backend: Go 1.22 + Gin + GORM + sqlx + go-redis + sarama + minio-go
- AI: Python 3.11 + FastAPI + 火山方舟 LLM/TTS/Embedding（已有）
- Storage: PostgreSQL 15 / Redis 7 / Kafka 3 / MinIO / Milvus 2.4 / Neo4j 5
- Render: ffmpeg + 自部署 worker pool（后期 K8s）
- Infra: Docker Compose（开发）→ K8s（生产）

---

## 产品定位

**一句话**：用户输入"主题/痛点/卖点"，30 秒内产出一条可直接发布的口播短视频（脚本+配音+字幕+B-roll+封面）。

**目标用户**（按付费意愿排序）：
1. 知识博主 / 自媒体作者：C 端订阅 99-299 元/月
2. 电商商家：带货脚本 + 探店脚本，299-999 元/月
3. MCN / 培训公司：团队版 9999+ 元/年

**关键差异点**（避开 Captions/Pictory）：
- **图记忆**：记住每个用户的口头禅、爆款节奏、品牌词，越用越个性化
- **风格学习**：抓 3 条用户旧作品 → 学风格 → 生成新内容像本人
- **沙箱**：跑爬虫拉热点 + 跑 ffmpeg 合成，全自动闭环

---

## 仓库与服务拓扑

**Monorepo 模式**：直接放在当前 `AGI-assistant` 仓库下，与 `final/`（Python AI 内核）并列，方便联调。

```
AGI-assistant/
├── final/                        # 现有 Python AI 内核
└── video-factory/                # NEW Go 全栈
├── cmd/
│   ├── gateway/                  # API 网关（鉴权/限流/路由）
│   ├── user-svc/                 # 账号/订阅/计费
│   ├── task-svc/                 # 项目/任务编排
│   ├── asset-svc/                # 文件上传/MinIO
│   ├── render-worker/            # ffmpeg 合成 worker
│   └── data-worker/              # 数据回流 worker
├── internal/
│   ├── biz/                      # 业务逻辑
│   ├── data/                     # 数据访问（PG/Redis/Kafka/MinIO）
│   ├── service/                  # HTTP handler
│   ├── pkg/
│   │   ├── ai/                   # 调 Python Agent 的 client
│   │   ├── tts/                  # 火山 TTS 封装
│   │   ├── ffmpeg/               # ffmpeg 命令封装
│   │   └── mq/                   # Kafka 封装
│   └── job/                      # DAG 任务编排
├── migrations/                   # PG schema
├── deploy/
│   ├── docker-compose.yml
│   └── k8s/
└── go.mod
```

Go module path：`github.com/AGI-Core/AGI-saber/video-factory`（沿用当前远端 repo）

**Go ↔ Python 交互**：
- 同步：Go gateway → HTTP/SSE → Python `final/` `:8090`
- 异步：Go service → Kafka topic → Python worker 消费 → 回写 Kafka

---

## 阶段路线图

| Phase | 周期 | 交付 | Demo |
|---|---|---|---|
| **Phase 1** | 第 1-2 周 | 脚本生成 MVP（Go user/task svc + Agent /api/script/generate） | 输入主题 → 5 秒返回结构化脚本 |
| **Phase 2** | 第 3-4 周 | 脚本→TTS→字幕，三步串成 DAG | 输入主题 → 30 秒返回 mp3+srt |
| **Phase 3** | 第 5-8 周 | B-roll + 封面 + 视频合成 + 模板引擎 + 计费 | 完整 mp4，可直接发抖音 |
| **Phase 4** | 第 9-12 周 | 风格学习 + 数据回流 + 多账号矩阵 + 微信支付 | 同主题给两个用户生成不同风格 |

---

## 关键里程碑量化指标

写到简历里的目标值：
- 平均出片时间：从立意到 mp4 < 5 分钟
- 单条成本：< ¥0.8（LLM + TTS + 文生图 总和）
- Agent 质检通过率 > 85%
- 风格还原度（用户盲测）：70%+ 选择 Agent 版而非模板版
- 系统：QPS 100、网关 P99 < 200ms、Worker 吞吐 50 任务/分钟

---

## 顶层依赖与风险

**外部依赖**：
- 火山方舟 LLM/Embedding/TTS：已接通，按量计费
- Pexels API：免费 200 req/h，B-roll 视频
- Seedream / 即梦文生图：封面
- Suno API（Phase 4 可选）：背景音乐
- 微信支付（Phase 4）：申请商户号约 1 周

**风险**：
- TTS 成本：火山 ≈ ¥0.3/分钟 → 月活 1000 用户、人均 30 分钟 = ¥9000/月，要算账
- 视频合成：ffmpeg 单机 ≈ 1 分钟/任务，QPS 高时要 K8s 弹性
- 平台合规：抖音/小红书 AI 内容标注，必须内置水印
- 冷启动：先自用 + 拉 5 个 KOL 内测，避免上来花钱投流

---

## 共享核心数据模型（Phase 1 起逐步引入）

```sql
-- Phase 1 必须
users(id, phone, nickname, password_hash, created_at)
projects(id, user_id, title, brand_voice_id, created_at)
tasks(id, project_id, type, status, params jsonb, result jsonb, error, created_at, updated_at)

-- Phase 2 加入
assets(id, user_id, project_id, kind, mime, size, url, meta jsonb, created_at)
task_steps(id, task_id, step_name, status, started_at, ended_at, error, output jsonb)

-- Phase 3 加入
templates(id, name, kind, config jsonb, preview_url)
credits(user_id, balance, total_granted, total_used)
billing_records(id, user_id, type, amount, balance, ref_id, created_at)

-- Phase 4 加入
brand_voices(id, user_id, name, sample_asset_ids[], features jsonb)
subscriptions(id, user_id, plan, expire_at, created_at)
publishings(id, user_id, asset_id, platform, platform_id, posted_at)
metrics_daily(publishing_id, date, plays, likes, comments, shares, follows)
```

---

## Self-Review 结果

- 范围：4 个 phase 是阶段递进而非独立子系统，但**每个 phase 都能独立产出可工作软件**（Phase 1 = 可演示文本输出 / Phase 2 = mp3+srt / Phase 3 = mp4 / Phase 4 = 收费 + 闭环），符合 superpowers scope check 要求拆分独立 plan
- Phase 之间的接口契约见各 phase plan 顶部的"Inputs from previous phase"
- 总览不直接写代码，所有可执行步骤分散到 phase plan 中

---

## 下一步

按 superpowers writing-plans 规范，4 个 phase 子 plan 已分别落地：

1. 阅读 [Phase 1 plan](./2026-06-12-video-factory-phase1.md) 开始第一阶段
2. 完成 Phase 1 验收后，再开 Phase 2
3. 不要跨 phase 并发——每个 phase 都是下一阶段的契约源
