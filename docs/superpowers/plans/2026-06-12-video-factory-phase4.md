# Video Factory · Phase 4 · 风格学习 + 数据闭环 + 商业化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development。
>
> **⚠️ 状态：骨架 plan**。Phase 1-3 验收完成后再细化。本文档列出 Phase 4 必须覆盖的 task + 验收口径。

**Goal:** 上线收费 + 用户越用越懂自己。同主题给两个不同用户生成的脚本风格明显不同（盲测 70%+ 选 Agent 版而非模板版）。

**Architecture:** 商业化拼图（微信支付 + 多账号矩阵）+ AI 个性化拼图（图记忆驱动的风格画像 + 数据回流 + A/B）。

**Tech Stack:** + 微信/支付宝商户 SDK + 抖音 / 小红书开放平台 API + Neo4j 图记忆（已有）+ A/B 实验框架

**Inputs from Phase 3:**
- 完整成片产出能力
- credits/billing 体系
- 三套模板
- task_steps + assets 完整数据

**Outputs:**
- 上线可收费产品（月活 ≥ 100 即可证明 PMF）
- 风格化简历指标
- 数据闭环驱动的 RAG 与图记忆持续进化

---

## File Structure（待 Phase 3 完成后细化）

```
video-factory/
├── cmd/
│   ├── data-worker/main.go               # NEW: 数据回流
│   └── billing-worker/main.go            # NEW: 续订/退款
├── internal/
│   ├── pkg/
│   │   ├── wxpay/                        # NEW: 微信支付
│   │   ├── douyin/                       # NEW: 抖音开放平台
│   │   └── xhs/                          # NEW: 小红书开放平台
│   ├── biz/
│   │   ├── subscription.go               # 订阅
│   │   ├── publishing.go                 # 多账号发布
│   │   ├── style.go                      # 风格画像
│   │   └── ab_experiment.go              # A/B
│   └── data/repo/
│       ├── subscription.go
│       ├── brand_voice.go
│       ├── publishing.go
│       └── metrics_daily.go
└── migrations/
    ├── 009_subscriptions.sql
    ├── 010_brand_voices.sql
    ├── 011_publishings.sql
    └── 012_metrics_daily.sql

AGI-assistant/final/
├── internal/agent/
│   ├── style_extractor.py                # NEW: 抽取用户风格特征
│   └── style_writer.py                   # NEW: 风格化生成
├── internal/memory/
│   └── style_memory.py                   # NEW: 写入图记忆 + 读取
└── internal/worker/
    └── style_learner.py                  # NEW: 增量学习 worker
```

---

## Task 列表（开始时细化到 step）

### T1：风格画像抽取
- 输入：用户已发布的 3+ 条视频（脚本 + 数据）
- 抽取维度：开头模式、emoji 频率、句长分布、品牌词、情绪倾向、CTA 模式
- 存储：写 `brand_voices.features jsonb` + 同步到 Neo4j 图记忆（Person → STYLE_OF → BrandVoice）

### T2：风格化生成（Phase 1 /api/script/generate 升级）
- 接口加参数：`brand_voice_id`
- 内部：拉用户图记忆 + 风格画像 → 注入 prompt → 生成
- 验证：盲测 ≥ 70% 用户选风格化版

### T3：数据回流 worker
- 接抖音/小红书开放平台（OAuth2 + token 自刷新）
- 发布后第 1/3/7/30 天拉播放/点赞/评论/分享 → 写 metrics_daily
- 推 Kafka topic `video-factory.metrics.collected`

### T4：效果反哺 RAG
- 高分作品（综合分 > 阈值）→ 标记 `quality:high`
- 写回 RAG（Phase 1 那个"爆款话术库"）+ 风格画像 + 图记忆
- A/B 实验：同主题给同一用户出 3 版（不同风格 / 不同模板），用户选最爱反哺

### T5：A/B 实验框架
- 维度：模板 / 风格强度 / B-roll 风格 / 封面风格
- 流量切分：用户 ID hash → 实验组
- 指标：CTR / 完播率 / 转粉率
- 看板：简化版（Grafana 接 PG metrics_daily）

### T6：微信支付接入
- 申请商户号（提前 1 周）
- 下单接口 + 回调验签 + 异步对账 cron
- 退款流程
- 测试用沙箱号

### T7：订阅与续订
- subscriptions 表
- 月底 cron 续订扣款（失败 3 次降级到免费版）
- 升级/降级当下生效，按比例退差额

### T8：多账号矩阵
- 同一用户绑定 N 个抖音/小红书账号
- 一键多平台发布
- 平台 access_token 加密存储 + 自动刷新

### T9：审批流（团队版）
- 团队 owner 创建审批策略（金额/平台/品类）
- 子账号提交 → owner 审批 → 自动发布

### T10：质检 Agent
- 合规：敏感词检测 + 平台政策检查（AI 标注水印验证）
- 质量：流畅度评分 + 重复度
- 风险评分 < 阈值才允许发布

### T11：可观测性 + 看板
- OpenTelemetry：Go + Python 全链路 trace
- 指标看板：单条成本、出片时延、模型调用、credit 消耗
- 计费精度对账：金额差额报警

---

## Phase 4 验收（也是项目里程碑）

简历可写指标：
- [ ] 月活用户 ≥ 100，付费转化率 ≥ 5%
- [ ] 单条平均成本 ≤ ¥0.8（含所有外部调用）
- [ ] 平均出片时间 ≤ 5 分钟
- [ ] Agent 质检通过率 ≥ 85%
- [ ] 风格还原度盲测 ≥ 70%
- [ ] 系统：QPS 100、网关 P99 ≤ 200ms、render worker 吞吐 ≥ 50 任务/分钟
- [ ] 计费对账：每日金额误差 ≤ ¥1
- [ ] 数据闭环：用户每发布一条 → 7 天内更新风格画像（自动）

---

## 风险与应对

- **TTS 成本**：月活 1000 / 人均 30 分钟 ≈ ¥9000/月，要在套餐设计上把 TTS 时长配额设够
- **平台 API 政策**：抖音/小红书开放能力会变，要做 feature flag + 优雅降级（手动复制粘贴）
- **支付合规**：商户号必须用公司主体，个人户要先注册个体工商户
- **数据隐私**：用户作品 + 评论数据要明确告知 + 加密存储，参考 GDPR 要点

---

## Self-Review

**1. Spec coverage**
- ✅ 订阅/计费 + 微信支付（T6/T7）
- ✅ 多账号矩阵（T8）
- ✅ 数据回流（T3）
- ✅ 风格学习 + A/B + 效果反哺（T1/T2/T4/T5）

**2. 商业化关键路径**
T6（微信支付）+ T7（订阅）是收费阻塞点，必须在 phase 4 第 1-2 周搞定 → 后续 task 才能跑通"用户付费 → 出片 → 数据回流 → 风格优化"完整闭环。

**3. 简历叙事**
完成 Phase 4 后，可以写出：
- "从 0 到 1 设计并实现 AIGC 视频 SaaS，覆盖 Go 后端 / Python AI / 全链路计费"
- "通过图记忆 + 风格画像，盲测 70%+ 用户选 AI 风格化版本"
- "服务 100+ 月活付费用户，单条成本 ≤ ¥0.8，端到端出片 ≤ 5 分钟"
- "全链路 OpenTelemetry trace，网关 QPS 100、P99 < 200ms"
