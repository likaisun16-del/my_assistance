# video-factory

Phase 1 MVP：Go (Gin) 后端 + Python AI Agent（[`final/`](../final)）联合产出口播脚本。

## 架构概览

```
client ──HTTP──▶ video-factory (Go :8080) ──HTTP──▶ AGI-assistant final/ (Python :8090)
                          │                                      │
                          ▼                                      ▼
                    PostgreSQL                              火山方舟 LLM
```

## 一键启动

前置：本机已装 PostgreSQL 15（`brew install postgresql@15`），Python 3 venv 装好 `final/requirements.txt`。

```bash
# 1. 起 PG（若已起则跳过）
LC_ALL=en_US.UTF-8 /opt/homebrew/opt/postgresql@15/bin/pg_ctl \
  -D /opt/homebrew/var/postgresql@15 -l /tmp/pg.log start

# 2. 建 vf 用户与 video_factory 库（首次）
/opt/homebrew/opt/postgresql@15/bin/createuser -s vf
/opt/homebrew/opt/postgresql@15/bin/createdb -O vf video_factory

# 3. 跑 migrations
make migrate

# 4. 起 Python Agent（另开终端）
cd ../final && python3 main.py

# 5. 起 Go 服务
make run
```

## 端到端流程

```bash
# 注册
curl -s -X POST http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"phone":"13800000001","password":"hello123","nickname":"alice"}'

# 登录
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"phone":"13800000001","password":"hello123"}' | python3 -c "import sys,json;print(json.load(sys.stdin)['token'])")

# 创建脚本任务（同步，等待 LLM 出结果）
curl -s -X POST http://localhost:8080/api/v1/scripts \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"title":"first","topic":"如何在 5 分钟讲清 RAG","duration":60,"style":"口播"}' \
  | python3 -m json.tool
```

返回结构：

```json
{
  "task_id": 1,
  "project_id": 1,
  "status": "succeeded",
  "result": {
    "hook": "...",
    "body": ["...", "..."],
    "cta": "...",
    "duration_estimate": 60
  }
}
```

> 若 `final/config/config.yaml` 中 `llm.api_key` 未填，Agent 走 Mock，脚本任务会以 `failed` 状态返回（mock 不输出合法 JSON），属预期；填上火山方舟 Key 后即可走通。

## 目录结构

```
video-factory/
├── cmd/user-svc/main.go      # 入口
├── internal/
│   ├── biz/                  # UserUsecase / ScriptUsecase
│   ├── config/               # YAML 配置
│   ├── data/
│   │   ├── pg.go             # GORM 初始化
│   │   └── repo/             # users/projects/tasks repo
│   ├── pkg/
│   │   ├── ai/               # 调 Python /api/script/generate 的 client
│   │   └── jwt/              # JWT 签名/校验
│   ├── server/router.go      # Gin 路由
│   └── service/              # HTTP handler（auth + script）
├── migrations/
│   ├── 001_users.sql
│   ├── 002_projects.sql
│   └── 003_tasks.sql
└── configs/config.yaml
```

## 测试

```bash
go test ./...                                                                  # 单元测试
TEST_PG_DSN="host=127.0.0.1 port=5432 user=vf password=vf dbname=video_factory sslmode=disable" \
  go test ./internal/data/repo/...                                             # 集成测试
```

## 后续阶段

- [Phase 2 · TTS + 字幕](../docs/superpowers/plans/2026-06-12-video-factory-phase2.md)
- [Phase 3 · B-roll + 封面 + 成片](../docs/superpowers/plans/2026-06-12-video-factory-phase3.md)
- [Phase 4 · 风格学习 + 商业化](../docs/superpowers/plans/2026-06-12-video-factory-phase4.md)
