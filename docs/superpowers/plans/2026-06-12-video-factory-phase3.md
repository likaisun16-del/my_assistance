# Video Factory · Phase 3 · B-roll + 封面 + 成片 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development。
>
> **⚠️ 状态：骨架 plan**。Phase 1 验收 + Phase 2 联调完成后再细化（避免依赖未稳定）。本文档列出 Phase 3 必须覆盖的 task + 验收口径，开始时按 phase1 颗粒度展开。

**Goal:** 完整 mp4 输出（口播 + 字幕 + B-roll + BGM），用户可一键下载并发抖音/小红书。

**Architecture:** 在 Phase 2 的 DAG 后追加节点：B-roll 选择 → 封面生成 → 视频合成。引入 render-worker（Go + ffmpeg），消费 Kafka 拉取所有 asset → 模板 + ffmpeg 合成。

**Tech Stack:** + Pexels API（B-roll 视频） + Seedream/即梦（封面文生图） + ffmpeg + 模板引擎（自研，基于 ffmpeg filter_complex DSL）

**Inputs from Phase 2:**
- assets/task_steps 表
- MinIO 资产存储
- Kafka topic 命名约定
- SSE 进度推送

**Outputs to Phase 4:**
- `templates` 表 + 模板 DSL schema
- `credits` / `billing_records` 表
- 计费扣减接口
- 视频元数据（时长 / 比特率 / 平台尺寸）规范

---

## File Structure（待 Phase 2 完成后细化）

```
video-factory/
├── cmd/render-worker/main.go
├── internal/
│   ├── pkg/
│   │   ├── ffmpeg/                # NEW: command 封装 + filter graph DSL
│   │   ├── pexels/                # NEW: B-roll 搜索
│   │   └── billing/               # NEW: credit 扣减
│   ├── biz/
│   │   ├── render.go              # 视频合成业务
│   │   ├── template.go            # 模板渲染
│   │   └── credit.go              # 计费
│   └── data/repo/
│       ├── template.go
│       ├── credit.go
│       └── billing.go
├── migrations/
│   ├── 006_templates.sql
│   ├── 007_credits.sql
│   └── 008_billing_records.sql
└── templates/                     # 内置模板 JSON
    ├── 9x16-classic.json
    ├── 9x16-fast-cut.json
    └── 1x1-product.json

AGI-assistant/final/
├── internal/agent/
│   ├── broll_picker.py            # NEW: 段落 → 配图 prompt → Pexels/文生图
│   └── cover_designer.py          # NEW: 标题 → 封面 prompt → Seedream
├── internal/handler/handler.py    # 加 /api/broll/pick, /api/cover/generate
└── internal/worker/
    └── creative_worker.py         # 消费 Kafka 跑 broll/cover
```

---

## Task 列表（待开始时细化到 step）

### T1：模板 DSL 设计 + 三个内置模板
- 模板 JSON schema：scenes[]、每 scene 含 audio_segment + visuals[] + transitions
- 至少三套模板：竖屏 9:16 经典口播、9:16 快剪 vlog、1:1 商品介绍
- 校验工具 + 单元测试

### T2：Pexels API 客户端 + B-roll 搜索
- API 限制：免费 200 req/h，要做缓存（PG `broll_cache` 表）
- 搜索 → 下载到 MinIO → 返回 asset_key
- 失败兜底：调文生图（Seedream）生成静态图

### T3：B-roll 选择 Agent（Python 侧）
- 输入：脚本 body 段落 + 时长
- 输出：每个段落对应的 visual_clip 列表
- 实现：LLM 抽取每段关键词 → Pexels 搜索 → 选最相关
- 缓存：相同关键词 → 复用 asset

### T4：封面生成 Agent（Python 侧）
- 输入：标题 + 主题
- 输出：封面 image_asset_key
- 实现：LLM 写 prompt → Seedream 文生图 → 上传 MinIO

### T5：ffmpeg 命令封装（Go 侧）
- 抽象层：`type FFmpeg struct{}` + `Render(ctx, plan RenderPlan) -> output_path`
- RenderPlan: 输入资产列表 + filter graph + 输出参数
- 进度回调：解析 ffmpeg stderr 的 `time=` 字段 → 推送 0-100%
- 资源限制：单进程 -threads 2 + ulimit

### T6：模板引擎（DSL → ffmpeg filter graph）
- 把模板 JSON + 实际资产 → 编译成 ffmpeg 命令
- 支持：concat、overlay 字幕、画中画 B-roll、BGM 混音、淡入淡出
- 单元测试：固定输入产出固定 filter 字符串

### T7：render-worker
- 消费 `video-factory.task.render` topic
- 步骤：拉所有 asset 到本地 tmp → 调 ffmpeg → 上传 mp4 → 写 assets 表 → 推 SSE
- 失败重试：3 次，DLQ
- 资源管理：worker pool（默认并发 2）+ 磁盘清理

### T8：Credit 计费体系
- credits 表：user_id, balance, total_granted, total_used
- billing_records 流水表
- 任务入队前扣 credit（脚本=1, mp3=3, mp4=10, 封面=2），失败自动退款
- Redis 缓存 balance（避免每次查 PG）

### T9：套餐与配额
- 免费 50 credits/月 / 入门 ¥99-500 / 专业 ¥299-2000 / 团队 ¥999-10000
- 月底 cron 重置（quartz / robfig/cron）
- 续订：Phase 4 接微信支付时实现，phase 3 用手工 SQL 充值

### T10：端到端联调
- 完整流程：脚本 → TTS → 字幕 → B-roll → 封面 → 合成 mp4
- 验证 mp4 可在抖音 PC 端预览正常
- 测量端到端时延 + 成本

---

## Phase 3 验收

- [ ] 1 分钟成片端到端 ≤ 5 分钟（脚本 + TTS + B-roll + 合成）
- [ ] 单条成本 ≤ ¥0.8（详细打点：LLM ¥0.1 + TTS ¥0.3 + 封面 ¥0.1 + 合成 ¥0.05 + 网络 ¥0.05 + buffer ¥0.2）
- [ ] 三套模板各产出至少一条样片
- [ ] credit 扣减/退款幂等（同一任务重复触发不会重复扣）
- [ ] render-worker 杀死后 task 能续跑

---

## Self-Review

**1. Spec coverage**
- ✅ render-svc + ffmpeg pool（T5/T7）
- ✅ 模板引擎（T1/T6）
- ✅ B-roll Agent（T2/T3）
- ✅ 封面 Agent（T4）
- ✅ credit 计费（T8/T9）

**2. 风险**
- ffmpeg 单机吞吐：1 分钟视频 ≈ 30s 渲染，QPS 1 时 worker pool 2 够；流量大要 K8s 弹性（Phase 4）
- Pexels 配额：要主动限流 + 长缓存（key=md5(query)，TTL 30 天）
- 抖音/小红书合规：底部加 "AI 生成" 水印，模板内置
