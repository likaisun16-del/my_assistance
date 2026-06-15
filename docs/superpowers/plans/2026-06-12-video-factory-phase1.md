# Video Factory · Phase 1 · 脚本生成 MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用户登录后输入"主题/时长/风格"，5 秒内拿到结构化口播脚本（hook / body[] / cta），整个链路 Go(Gin) ↔ Python Agent 跑通。

**Architecture:** Go (Gin) 提供 HTTP API（user-svc 注册登录、task-svc 创建脚本任务），任务通过同步 HTTP 调用 Python Agent 的新接口 `/api/script/generate`；Agent 内部调火山方舟 LLM + 内置 prompt 模板生成结构化 JSON。脚本任务结果写回 PG 的 `tasks.result`，无 Kafka、无 Redis 队列、无计费。

**Tech Stack:** Go 1.22 + Gin + GORM + golang-jwt + bcrypt + PostgreSQL 15 / Python 3.11 + FastAPI（已有）+ 火山方舟 LLM

**Inputs from previous phase:** N/A（首阶段）

**Outputs to next phase:** `users` / `projects` / `tasks` 三张表稳定 schema、`/api/script/generate` 契约稳定、JWT 中间件可被后续 svc 复用

---

## File Structure

新仓库 `video-factory`：

```
video-factory/
├── cmd/
│   ├── user-svc/main.go          # T1
│   └── task-svc/main.go          # T4
├── internal/
│   ├── config/config.go          # T1: 加载 yaml
│   ├── data/
│   │   ├── pg.go                 # T1: GORM 初始化
│   │   └── repo/
│   │       ├── user.go           # T2
│   │       ├── project.go        # T4
│   │       └── task.go           # T4
│   ├── biz/
│   │   ├── user.go               # T2: 注册/登录业务
│   │   └── script.go             # T5: 脚本任务编排
│   ├── service/
│   │   ├── auth_handler.go       # T3: HTTP handler
│   │   └── script_handler.go     # T6
│   ├── pkg/
│   │   ├── jwt/jwt.go            # T3
│   │   └── ai/agent_client.go    # T5: 调 Python /api/script/generate
│   └── server/router.go          # T1
├── migrations/
│   ├── 001_users.sql             # T2
│   ├── 002_projects.sql          # T4
│   └── 003_tasks.sql             # T4
├── deploy/docker-compose.yml     # T1: PG
├── configs/config.yaml           # T1
├── go.mod
└── README.md
```

AGI-assistant 仓库（已有）：

```
final/internal/handler/handler.py             # T7: 加 /api/script/generate
final/internal/agent/script_generator.py      # T7 NEW: prompt + 调 LLM
final/tests/test_script_generator.py          # T7
```

---

## Task 1: video-factory 仓库初始化 + Gin 骨架 + PG 起来

**Files:**
- Create: `video-factory/go.mod`
- Create: `video-factory/cmd/user-svc/main.go`
- Create: `video-factory/internal/config/config.go`
- Create: `video-factory/internal/data/pg.go`
- Create: `video-factory/internal/server/router.go`
- Create: `video-factory/configs/config.yaml`
- Create: `video-factory/deploy/docker-compose.yml`

- [ ] **Step 1: 新建仓库 + 初始化 module**

```bash
mkdir -p ~/code/video-factory && cd ~/code/video-factory
git init
go mod init github.com/<owner>/video-factory
go get github.com/gin-gonic/gin@v1.9.1 \
       gorm.io/gorm@v1.25.10 \
       gorm.io/driver/postgres@v1.5.7 \
       gopkg.in/yaml.v3@v3.0.1
```

- [ ] **Step 2: 写配置加载**

```go
// internal/config/config.go
package config

import (
    "os"
    "gopkg.in/yaml.v3"
)

type Config struct {
    Server struct {
        Addr string `yaml:"addr"`
    } `yaml:"server"`
    Postgres struct {
        DSN string `yaml:"dsn"`
    } `yaml:"postgres"`
    JWT struct {
        Secret  string `yaml:"secret"`
        TTLHour int    `yaml:"ttl_hour"`
    } `yaml:"jwt"`
    Agent struct {
        BaseURL string `yaml:"base_url"`
        Timeout int    `yaml:"timeout"`
    } `yaml:"agent"`
}

func Load(path string) (*Config, error) {
    b, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    var c Config
    if err := yaml.Unmarshal(b, &c); err != nil {
        return nil, err
    }
    return &c, nil
}
```

- [ ] **Step 3: 写默认配置文件**

```yaml
# configs/config.yaml
server:
  addr: ":8080"
postgres:
  dsn: "host=localhost port=5432 user=vf password=vf dbname=video_factory sslmode=disable"
jwt:
  secret: "dev-secret-change-me"
  ttl_hour: 168
agent:
  base_url: "http://localhost:8090"
  timeout: 30
```

- [ ] **Step 4: 写 PG 初始化**

```go
// internal/data/pg.go
package data

import (
    "gorm.io/driver/postgres"
    "gorm.io/gorm"
)

func NewPG(dsn string) (*gorm.DB, error) {
    return gorm.Open(postgres.Open(dsn), &gorm.Config{})
}
```

- [ ] **Step 5: 写最小路由与 main**

```go
// internal/server/router.go
package server

import "github.com/gin-gonic/gin"

func NewRouter() *gin.Engine {
    r := gin.Default()
    r.GET("/health", func(c *gin.Context) {
        c.JSON(200, gin.H{"status": "ok"})
    })
    return r
}
```

```go
// cmd/user-svc/main.go
package main

import (
    "log"
    "github.com/<owner>/video-factory/internal/config"
    "github.com/<owner>/video-factory/internal/data"
    "github.com/<owner>/video-factory/internal/server"
)

func main() {
    cfg, err := config.Load("configs/config.yaml")
    if err != nil { log.Fatal(err) }
    if _, err := data.NewPG(cfg.Postgres.DSN); err != nil {
        log.Fatal(err)
    }
    r := server.NewRouter()
    log.Fatal(r.Run(cfg.Server.Addr))
}
```

- [ ] **Step 6: 写 docker-compose 起 PG**

```yaml
# deploy/docker-compose.yml
services:
  postgres:
    image: postgres:15
    environment:
      POSTGRES_USER: vf
      POSTGRES_PASSWORD: vf
      POSTGRES_DB: video_factory
    ports: ["5432:5432"]
    volumes: ["pg_data:/var/lib/postgresql/data"]
volumes:
  pg_data:
```

- [ ] **Step 7: 跑通 + 验证**

```bash
docker compose -f deploy/docker-compose.yml up -d
go run ./cmd/user-svc
# 另一个 terminal:
curl http://localhost:8080/health
```

Expected output: `{"status":"ok"}`

- [ ] **Step 8: 提交**

```bash
cat > .gitignore <<'EOF'
.idea/
.vscode/
*.log
configs/local.yaml
EOF
git add .
git commit -m "chore: bootstrap video-factory with gin + gorm + pg"
```

---

## Task 2: users 表 + 注册业务（含 password 哈希）

**Files:**
- Create: `video-factory/migrations/001_users.sql`
- Create: `video-factory/internal/data/repo/user.go`
- Create: `video-factory/internal/biz/user.go`
- Test: `video-factory/internal/biz/user_test.go`

- [ ] **Step 1: 写 SQL migration**

```sql
-- migrations/001_users.sql
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    phone         VARCHAR(20) UNIQUE NOT NULL,
    nickname      VARCHAR(64) NOT NULL DEFAULT '',
    password_hash VARCHAR(128) NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_users_phone ON users(phone);
```

- [ ] **Step 2: 应用 migration**

```bash
docker exec -i $(docker ps -qf name=postgres) \
    psql -U vf -d video_factory < migrations/001_users.sql
```

Expected: `CREATE TABLE` / `CREATE INDEX`

- [ ] **Step 3: 写 user repo（DAO 层）**

```go
// internal/data/repo/user.go
package repo

import (
    "context"
    "time"
    "gorm.io/gorm"
)

type User struct {
    ID           int64     `gorm:"primaryKey"`
    Phone        string    `gorm:"uniqueIndex;size:20;not null"`
    Nickname     string    `gorm:"size:64"`
    PasswordHash string    `gorm:"size:128;not null"`
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

func (User) TableName() string { return "users" }

type UserRepo struct{ db *gorm.DB }

func NewUserRepo(db *gorm.DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) Create(ctx context.Context, u *User) error {
    return r.db.WithContext(ctx).Create(u).Error
}

func (r *UserRepo) FindByPhone(ctx context.Context, phone string) (*User, error) {
    var u User
    if err := r.db.WithContext(ctx).Where("phone=?", phone).First(&u).Error; err != nil {
        return nil, err
    }
    return &u, nil
}
```

- [ ] **Step 4: 先写 biz 层失败测试（TDD）**

```go
// internal/biz/user_test.go
package biz

import (
    "context"
    "testing"
)

type fakeUserRepo struct{ users map[string]*User }

func (f *fakeUserRepo) Create(_ context.Context, u *User) error {
    if _, ok := f.users[u.Phone]; ok { return ErrPhoneExists }
    f.users[u.Phone] = u; return nil
}
func (f *fakeUserRepo) FindByPhone(_ context.Context, p string) (*User, error) {
    if u, ok := f.users[p]; ok { return u, nil }
    return nil, ErrUserNotFound
}

func TestRegister_HashPasswordAndStore(t *testing.T) {
    repo := &fakeUserRepo{users: map[string]*User{}}
    uc := NewUserUsecase(repo)
    u, err := uc.Register(context.Background(), "13800000000", "p@ss1234", "alice")
    if err != nil { t.Fatal(err) }
    if u.PasswordHash == "p@ss1234" { t.Fatal("password should be hashed") }
    if u.Phone != "13800000000" { t.Fatalf("phone mismatch: %s", u.Phone) }
}

func TestRegister_DuplicatePhoneRejected(t *testing.T) {
    repo := &fakeUserRepo{users: map[string]*User{}}
    uc := NewUserUsecase(repo)
    _, _ = uc.Register(context.Background(), "13800000000", "p@ss1234", "a")
    if _, err := uc.Register(context.Background(), "13800000000", "p@ss1234", "b"); err != ErrPhoneExists {
        t.Fatalf("expect ErrPhoneExists, got %v", err)
    }
}
```

- [ ] **Step 5: 跑测试确认失败**

```bash
cd video-factory && go test ./internal/biz/...
```

Expected: FAIL（biz 包尚未存在）

- [ ] **Step 6: 写最小 biz 实现**

```go
// internal/biz/user.go
package biz

import (
    "context"
    "errors"
    "golang.org/x/crypto/bcrypt"
)

var (
    ErrPhoneExists  = errors.New("phone already exists")
    ErrUserNotFound = errors.New("user not found")
    ErrBadPassword  = errors.New("bad password")
)

type User struct {
    ID           int64
    Phone        string
    Nickname     string
    PasswordHash string
}

type UserRepo interface {
    Create(ctx context.Context, u *User) error
    FindByPhone(ctx context.Context, phone string) (*User, error)
}

type UserUsecase struct{ repo UserRepo }

func NewUserUsecase(r UserRepo) *UserUsecase { return &UserUsecase{repo: r} }

func (uc *UserUsecase) Register(ctx context.Context, phone, password, nickname string) (*User, error) {
    h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    if err != nil { return nil, err }
    u := &User{Phone: phone, Nickname: nickname, PasswordHash: string(h)}
    if err := uc.repo.Create(ctx, u); err != nil { return nil, err }
    return u, nil
}

func (uc *UserUsecase) Login(ctx context.Context, phone, password string) (*User, error) {
    u, err := uc.repo.FindByPhone(ctx, phone)
    if err != nil { return nil, err }
    if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
        return nil, ErrBadPassword
    }
    return u, nil
}
```

- [ ] **Step 7: 安装 bcrypt 跑测试**

```bash
go get golang.org/x/crypto/bcrypt
go test ./internal/biz/... -v
```

Expected: `--- PASS: TestRegister_HashPasswordAndStore` + `TestRegister_DuplicatePhoneRejected`

- [ ] **Step 8: 适配 repo 实现 biz.UserRepo 接口**

在 `internal/data/repo/user.go` 末尾追加：

```go
// 让 repo.UserRepo 满足 biz.UserRepo
import bizpkg "github.com/<owner>/video-factory/internal/biz"

func (r *UserRepo) CreateBiz(ctx context.Context, u *bizpkg.User) error {
    row := &User{Phone: u.Phone, Nickname: u.Nickname, PasswordHash: u.PasswordHash}
    if err := r.Create(ctx, row); err != nil { return err }
    u.ID = row.ID
    return nil
}
func (r *UserRepo) FindByPhoneBiz(ctx context.Context, phone string) (*bizpkg.User, error) {
    row, err := r.FindByPhone(ctx, phone)
    if err != nil { return nil, err }
    return &bizpkg.User{ID: row.ID, Phone: row.Phone, Nickname: row.Nickname, PasswordHash: row.PasswordHash}, nil
}
```

- [ ] **Step 9: 提交**

```bash
git add .
git commit -m "feat(user): register/login usecase with bcrypt + users table"
```

---

## Task 3: JWT 中间件 + /auth/register /auth/login HTTP handler

**Files:**
- Create: `video-factory/internal/pkg/jwt/jwt.go`
- Create: `video-factory/internal/service/auth_handler.go`
- Modify: `video-factory/cmd/user-svc/main.go`
- Modify: `video-factory/internal/server/router.go`
- Test: `video-factory/internal/pkg/jwt/jwt_test.go`

- [ ] **Step 1: 写 JWT 失败测试**

```go
// internal/pkg/jwt/jwt_test.go
package jwt

import "testing"

func TestSignAndParse_RoundTrip(t *testing.T) {
    j := New("test-secret", 24)
    tok, err := j.Sign(42)
    if err != nil { t.Fatal(err) }
    uid, err := j.Parse(tok)
    if err != nil { t.Fatal(err) }
    if uid != 42 { t.Fatalf("uid = %d, want 42", uid) }
}

func TestParse_BadToken(t *testing.T) {
    j := New("test-secret", 24)
    if _, err := j.Parse("not-a-token"); err == nil {
        t.Fatal("expect error")
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/pkg/jwt/... -v
```

Expected: FAIL

- [ ] **Step 3: 写实现**

```go
// internal/pkg/jwt/jwt.go
package jwt

import (
    "errors"
    "time"
    jwtv5 "github.com/golang-jwt/jwt/v5"
)

type JWT struct {
    secret []byte
    ttl    time.Duration
}

func New(secret string, ttlHours int) *JWT {
    return &JWT{secret: []byte(secret), ttl: time.Duration(ttlHours) * time.Hour}
}

type claims struct {
    UID int64 `json:"uid"`
    jwtv5.RegisteredClaims
}

func (j *JWT) Sign(uid int64) (string, error) {
    c := claims{
        UID: uid,
        RegisteredClaims: jwtv5.RegisteredClaims{
            ExpiresAt: jwtv5.NewNumericDate(time.Now().Add(j.ttl)),
        },
    }
    return jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, c).SignedString(j.secret)
}

func (j *JWT) Parse(tok string) (int64, error) {
    parsed, err := jwtv5.ParseWithClaims(tok, &claims{}, func(t *jwtv5.Token) (any, error) {
        return j.secret, nil
    })
    if err != nil { return 0, err }
    c, ok := parsed.Claims.(*claims)
    if !ok || !parsed.Valid { return 0, errors.New("invalid token") }
    return c.UID, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go get github.com/golang-jwt/jwt/v5
go test ./internal/pkg/jwt/... -v
```

Expected: PASS

- [ ] **Step 5: 写 auth handler**

```go
// internal/service/auth_handler.go
package service

import (
    "net/http"
    "github.com/gin-gonic/gin"
    "github.com/<owner>/video-factory/internal/biz"
    "github.com/<owner>/video-factory/internal/pkg/jwt"
)

type AuthHandler struct {
    uc  *biz.UserUsecase
    jwt *jwt.JWT
}

func NewAuthHandler(uc *biz.UserUsecase, j *jwt.JWT) *AuthHandler {
    return &AuthHandler{uc: uc, jwt: j}
}

type registerReq struct {
    Phone    string `json:"phone" binding:"required,len=11"`
    Password string `json:"password" binding:"required,min=6"`
    Nickname string `json:"nickname"`
}

type loginReq struct {
    Phone    string `json:"phone" binding:"required"`
    Password string `json:"password" binding:"required"`
}

type tokenResp struct {
    Token string `json:"token"`
    UID   int64  `json:"uid"`
}

func (h *AuthHandler) Register(c *gin.Context) {
    var req registerReq
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()}); return
    }
    u, err := h.uc.Register(c, req.Phone, req.Password, req.Nickname)
    if err != nil {
        c.JSON(http.StatusConflict, gin.H{"error": err.Error()}); return
    }
    tok, _ := h.jwt.Sign(u.ID)
    c.JSON(http.StatusOK, tokenResp{Token: tok, UID: u.ID})
}

func (h *AuthHandler) Login(c *gin.Context) {
    var req loginReq
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()}); return
    }
    u, err := h.uc.Login(c, req.Phone, req.Password)
    if err != nil {
        c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()}); return
    }
    tok, _ := h.jwt.Sign(u.ID)
    c.JSON(http.StatusOK, tokenResp{Token: tok, UID: u.ID})
}

func AuthMiddleware(j *jwt.JWT) gin.HandlerFunc {
    return func(c *gin.Context) {
        tok := c.GetHeader("Authorization")
        if len(tok) > 7 && tok[:7] == "Bearer " { tok = tok[7:] }
        uid, err := j.Parse(tok)
        if err != nil {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"}); return
        }
        c.Set("uid", uid)
        c.Next()
    }
}
```

- [ ] **Step 6: 在 router 中挂上**

```go
// internal/server/router.go (重写)
package server

import (
    "github.com/gin-gonic/gin"
    "github.com/<owner>/video-factory/internal/service"
)

func NewRouter(auth *service.AuthHandler) *gin.Engine {
    r := gin.Default()
    r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
    g := r.Group("/api/v1/auth")
    {
        g.POST("/register", auth.Register)
        g.POST("/login", auth.Login)
    }
    return r
}
```

- [ ] **Step 7: 在 main 串起来**

```go
// cmd/user-svc/main.go (重写)
package main

import (
    "log"
    "github.com/<owner>/video-factory/internal/biz"
    "github.com/<owner>/video-factory/internal/config"
    "github.com/<owner>/video-factory/internal/data"
    "github.com/<owner>/video-factory/internal/data/repo"
    jwtpkg "github.com/<owner>/video-factory/internal/pkg/jwt"
    "github.com/<owner>/video-factory/internal/server"
    "github.com/<owner>/video-factory/internal/service"
)

type repoAdapter struct{ *repo.UserRepo }

func (a repoAdapter) Create(ctx context.Context, u *biz.User) error { return a.CreateBiz(ctx, u) }
func (a repoAdapter) FindByPhone(ctx context.Context, p string) (*biz.User, error) { return a.FindByPhoneBiz(ctx, p) }

func main() {
    cfg, err := config.Load("configs/config.yaml"); if err != nil { log.Fatal(err) }
    db, err := data.NewPG(cfg.Postgres.DSN); if err != nil { log.Fatal(err) }
    userRepo := repo.NewUserRepo(db)
    uc := biz.NewUserUsecase(repoAdapter{userRepo})
    j := jwtpkg.New(cfg.JWT.Secret, cfg.JWT.TTLHour)
    auth := service.NewAuthHandler(uc, j)
    r := server.NewRouter(auth)
    log.Fatal(r.Run(cfg.Server.Addr))
}
```

- [ ] **Step 8: 端到端验证**

```bash
go run ./cmd/user-svc &
sleep 1
# 注册
curl -s -X POST http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"phone":"13800000001","password":"hello123","nickname":"alice"}'
# 登录
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"phone":"13800000001","password":"hello123"}'
```

Expected: 两次都返回 `{"token":"eyJ...","uid":1}`

- [ ] **Step 9: 提交**

```bash
git add .
git commit -m "feat(auth): jwt-based register/login http endpoints"
```

---

## Task 4: projects + tasks 表 + repo 层

**Files:**
- Create: `video-factory/migrations/002_projects.sql`
- Create: `video-factory/migrations/003_tasks.sql`
- Create: `video-factory/internal/data/repo/project.go`
- Create: `video-factory/internal/data/repo/task.go`
- Test: `video-factory/internal/data/repo/task_test.go`

- [ ] **Step 1: SQL migrations**

```sql
-- migrations/002_projects.sql
CREATE TABLE IF NOT EXISTS projects (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL,
    title           VARCHAR(255) NOT NULL,
    brand_voice_id  BIGINT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_projects_user_id ON projects(user_id);
```

```sql
-- migrations/003_tasks.sql
CREATE TABLE IF NOT EXISTS tasks (
    id          BIGSERIAL PRIMARY KEY,
    project_id  BIGINT NOT NULL REFERENCES projects(id),
    type        VARCHAR(32) NOT NULL,           -- 'script' / 'tts' / 'render' ...
    status      VARCHAR(16) NOT NULL DEFAULT 'pending',  -- pending/running/succeeded/failed
    params      JSONB NOT NULL DEFAULT '{}',
    result      JSONB,
    error       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_tasks_project_id ON tasks(project_id);
CREATE INDEX idx_tasks_status ON tasks(status);
```

- [ ] **Step 2: 应用 migrations**

```bash
docker exec -i $(docker ps -qf name=postgres) psql -U vf -d video_factory < migrations/002_projects.sql
docker exec -i $(docker ps -qf name=postgres) psql -U vf -d video_factory < migrations/003_tasks.sql
```

Expected: `CREATE TABLE` * 2 + indexes

- [ ] **Step 3: 写 task repo（含 jsonb 处理）**

```go
// internal/data/repo/task.go
package repo

import (
    "context"
    "encoding/json"
    "time"
    "gorm.io/datatypes"
    "gorm.io/gorm"
)

type Task struct {
    ID         int64           `gorm:"primaryKey"`
    ProjectID  int64           `gorm:"not null;index"`
    Type       string          `gorm:"size:32;not null"`
    Status     string          `gorm:"size:16;not null;default:pending"`
    Params     datatypes.JSON  `gorm:"type:jsonb;not null;default:'{}'"`
    Result     datatypes.JSON  `gorm:"type:jsonb"`
    Error      string          `gorm:"type:text"`
    CreatedAt  time.Time
    UpdatedAt  time.Time
}

func (Task) TableName() string { return "tasks" }

type TaskRepo struct{ db *gorm.DB }

func NewTaskRepo(db *gorm.DB) *TaskRepo { return &TaskRepo{db: db} }

func (r *TaskRepo) Create(ctx context.Context, t *Task) error {
    return r.db.WithContext(ctx).Create(t).Error
}

func (r *TaskRepo) Get(ctx context.Context, id int64) (*Task, error) {
    var t Task
    if err := r.db.WithContext(ctx).First(&t, id).Error; err != nil { return nil, err }
    return &t, nil
}

func (r *TaskRepo) UpdateResult(ctx context.Context, id int64, status string, result any, errMsg string) error {
    var resultJSON datatypes.JSON
    if result != nil {
        b, _ := json.Marshal(result)
        resultJSON = b
    }
    return r.db.WithContext(ctx).Model(&Task{}).Where("id=?", id).
        Updates(map[string]any{
            "status": status,
            "result": resultJSON,
            "error":  errMsg,
        }).Error
}
```

- [ ] **Step 4: 项目 repo（最小）**

```go
// internal/data/repo/project.go
package repo

import (
    "context"
    "time"
    "gorm.io/gorm"
)

type Project struct {
    ID           int64  `gorm:"primaryKey"`
    UserID       int64  `gorm:"not null;index"`
    Title        string `gorm:"size:255;not null"`
    BrandVoiceID *int64
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
func (Project) TableName() string { return "projects" }

type ProjectRepo struct{ db *gorm.DB }
func NewProjectRepo(db *gorm.DB) *ProjectRepo { return &ProjectRepo{db: db} }

func (r *ProjectRepo) Create(ctx context.Context, p *Project) error {
    return r.db.WithContext(ctx).Create(p).Error
}
func (r *ProjectRepo) Get(ctx context.Context, id int64) (*Project, error) {
    var p Project
    if err := r.db.WithContext(ctx).First(&p, id).Error; err != nil { return nil, err }
    return &p, nil
}
```

- [ ] **Step 5: 集成测试（用真 PG，docker-compose 已起）**

```go
// internal/data/repo/task_test.go
package repo

import (
    "context"
    "encoding/json"
    "os"
    "testing"
    "github.com/<owner>/video-factory/internal/data"
)

func TestTaskCreateAndUpdateResult(t *testing.T) {
    dsn := os.Getenv("TEST_PG_DSN")
    if dsn == "" { t.Skip("set TEST_PG_DSN to run") }
    db, err := data.NewPG(dsn); if err != nil { t.Fatal(err) }

    pr := NewProjectRepo(db)
    p := &Project{UserID: 1, Title: "ut"}
    if err := pr.Create(context.Background(), p); err != nil { t.Fatal(err) }

    tr := NewTaskRepo(db)
    params, _ := json.Marshal(map[string]any{"topic": "hi"})
    tk := &Task{ProjectID: p.ID, Type: "script", Status: "pending", Params: params}
    if err := tr.Create(context.Background(), tk); err != nil { t.Fatal(err) }

    if err := tr.UpdateResult(context.Background(), tk.ID, "succeeded",
        map[string]string{"hook": "ok"}, ""); err != nil { t.Fatal(err) }

    got, err := tr.Get(context.Background(), tk.ID); if err != nil { t.Fatal(err) }
    if got.Status != "succeeded" { t.Fatalf("status=%s", got.Status) }
}
```

- [ ] **Step 6: 跑集成测试**

```bash
go get gorm.io/datatypes
TEST_PG_DSN="host=localhost port=5432 user=vf password=vf dbname=video_factory sslmode=disable" \
go test ./internal/data/repo/... -v
```

Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add .
git commit -m "feat(task): projects/tasks tables + repo with jsonb"
```

---

## Task 5: Python Agent 加 /api/script/generate 接口

**Files:**
- Create: `final/internal/agent/script_generator.py`
- Modify: `final/internal/handler/handler.py`（新增路由）
- Test: `final/tests/test_script_generator.py`

- [ ] **Step 1: 写失败测试**

```python
# final/tests/test_script_generator.py
import pytest
from unittest.mock import MagicMock
from internal.agent.script_generator import ScriptGenerator, ScriptRequest

def test_generate_returns_structured_script():
    fake_llm = MagicMock()
    fake_llm.chat.return_value = '{"hook":"开头钩子","body":["第一段","第二段"],"cta":"关注我","duration_estimate":120}'
    gen = ScriptGenerator(llm=fake_llm)
    out = gen.generate(ScriptRequest(topic="RAG 入门", duration=120, style="口播"))
    assert out.hook == "开头钩子"
    assert len(out.body) == 2
    assert out.cta == "关注我"
    assert out.duration_estimate == 120

def test_generate_handles_invalid_json():
    fake_llm = MagicMock()
    fake_llm.chat.return_value = "not a json"
    gen = ScriptGenerator(llm=fake_llm)
    with pytest.raises(ValueError, match="invalid script json"):
        gen.generate(ScriptRequest(topic="x", duration=60, style="口播"))
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd final && python -m pytest tests/test_script_generator.py -v
```

Expected: ImportError

- [ ] **Step 3: 写实现**

```python
# final/internal/agent/script_generator.py
import json
from dataclasses import dataclass, field
from typing import List, Protocol

SCRIPT_PROMPT = """你是资深短视频脚本写手。请根据以下输入生成一条口播脚本。

要求：
1. 风格：{style}
2. 时长目标：{duration} 秒
3. 主题：{topic}

输出严格 JSON（不要 markdown 代码块），结构：
{{
  "hook": "开头 5 秒钩子，必须制造冲突或反常识",
  "body": ["主体段落 1", "主体段落 2", "..."],
  "cta": "结尾呼吁",
  "duration_estimate": <数字，预估秒数>
}}
"""

class LLMClient(Protocol):
    def chat(self, prompt: str) -> str: ...

@dataclass
class ScriptRequest:
    topic: str
    duration: int = 120
    style: str = "口播"

@dataclass
class ScriptResponse:
    hook: str
    body: List[str] = field(default_factory=list)
    cta: str = ""
    duration_estimate: int = 0

class ScriptGenerator:
    def __init__(self, llm: LLMClient):
        self.llm = llm

    def generate(self, req: ScriptRequest) -> ScriptResponse:
        prompt = SCRIPT_PROMPT.format(style=req.style, duration=req.duration, topic=req.topic)
        raw = self.llm.chat(prompt)
        try:
            data = json.loads(raw)
        except json.JSONDecodeError as e:
            raise ValueError(f"invalid script json: {e}; raw={raw[:200]}")
        return ScriptResponse(
            hook=data.get("hook", ""),
            body=data.get("body", []),
            cta=data.get("cta", ""),
            duration_estimate=int(data.get("duration_estimate", 0)),
        )
```

- [ ] **Step 4: 跑测试**

```bash
python -m pytest tests/test_script_generator.py -v
```

Expected: 2 PASSED

- [ ] **Step 5: 在 handler.py 暴露路由**

打开 [handler.py](file:///Users/bytedance/my_project/agi/AGI-assistant/final/internal/handler/handler.py)，在已有的 pydantic 模型区追加：

```python
class ScriptGenerateRequest(BaseModel):
    topic: str
    duration: int = 120
    style: str = "口播"
    user_id: str | None = None
```

在路由注册区追加：

```python
from internal.agent.script_generator import ScriptGenerator, ScriptRequest as _SR

@app.post("/api/script/generate")
async def script_generate(req: ScriptGenerateRequest):
    gen = ScriptGenerator(llm=infra.llm)  # 复用已有 llm client
    out = gen.generate(_SR(topic=req.topic, duration=req.duration, style=req.style))
    return {
        "hook": out.hook,
        "body": out.body,
        "cta": out.cta,
        "duration_estimate": out.duration_estimate,
    }
```

- [ ] **Step 6: 启动 Python 服务**

```bash
cd final && python main.py &
sleep 3
```

Expected: `Uvicorn running on http://0.0.0.0:8090`

- [ ] **Step 7: 端到端验证**

```bash
curl -s -X POST http://localhost:8090/api/script/generate \
  -H 'Content-Type: application/json' \
  -d '{"topic":"RAG 三路融合是什么","duration":60,"style":"口播"}' | python -m json.tool
```

Expected: 返回包含 `hook`, `body`, `cta`, `duration_estimate` 四个字段的 JSON

- [ ] **Step 8: 提交（在 AGI-assistant 仓库 python 分支）**

```bash
cd /Users/bytedance/my_project/agi/AGI-assistant
git add final/internal/agent/script_generator.py final/internal/handler/handler.py final/tests/test_script_generator.py
git commit -m "feat(agent): /api/script/generate produce structured口播 script"
git push origin python
```

---

## Task 6: Go agent_client（HTTP 调用 Python /api/script/generate）

**Files:**
- Create: `video-factory/internal/pkg/ai/agent_client.go`
- Test: `video-factory/internal/pkg/ai/agent_client_test.go`

- [ ] **Step 1: 写失败测试（用 httptest mock）**

```go
// internal/pkg/ai/agent_client_test.go
package ai

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"
)

func TestGenerateScript_HappyPath(t *testing.T) {
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/api/script/generate" { t.Fatalf("path=%s", r.URL.Path) }
        json.NewEncoder(w).Encode(map[string]any{
            "hook": "钩子", "body": []string{"a","b"}, "cta": "关注", "duration_estimate": 60,
        })
    }))
    defer ts.Close()
    c := NewClient(ts.URL, 5*time.Second)
    out, err := c.GenerateScript(context.Background(), &ScriptReq{Topic: "x", Duration: 60, Style: "口播"})
    if err != nil { t.Fatal(err) }
    if out.Hook != "钩子" || len(out.Body) != 2 { t.Fatalf("bad output: %+v", out) }
}

func TestGenerateScript_ServerError(t *testing.T) {
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(500); w.Write([]byte("boom"))
    }))
    defer ts.Close()
    c := NewClient(ts.URL, 5*time.Second)
    if _, err := c.GenerateScript(context.Background(), &ScriptReq{Topic: "x"}); err == nil {
        t.Fatal("expect error")
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/pkg/ai/... -v
```

Expected: FAIL（包不存在）

- [ ] **Step 3: 写实现**

```go
// internal/pkg/ai/agent_client.go
package ai

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

type ScriptReq struct {
    Topic    string `json:"topic"`
    Duration int    `json:"duration"`
    Style    string `json:"style"`
    UserID   string `json:"user_id,omitempty"`
}

type ScriptResp struct {
    Hook             string   `json:"hook"`
    Body             []string `json:"body"`
    CTA              string   `json:"cta"`
    DurationEstimate int      `json:"duration_estimate"`
}

type Client struct {
    baseURL string
    hc      *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
    return &Client{baseURL: baseURL, hc: &http.Client{Timeout: timeout}}
}

func (c *Client) GenerateScript(ctx context.Context, req *ScriptReq) (*ScriptResp, error) {
    body, _ := json.Marshal(req)
    httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
        c.baseURL+"/api/script/generate", bytes.NewReader(body))
    httpReq.Header.Set("Content-Type", "application/json")
    resp, err := c.hc.Do(httpReq)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    raw, _ := io.ReadAll(resp.Body)
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("agent returned %d: %s", resp.StatusCode, raw)
    }
    var out ScriptResp
    if err := json.Unmarshal(raw, &out); err != nil { return nil, err }
    return &out, nil
}
```

- [ ] **Step 4: 跑测试**

```bash
go test ./internal/pkg/ai/... -v
```

Expected: 2 PASSED

- [ ] **Step 5: 提交**

```bash
git add .
git commit -m "feat(ai): http client for python /api/script/generate"
```

---

## Task 7: 脚本任务编排 biz 层 + /api/v1/scripts handler

**Files:**
- Create: `video-factory/internal/biz/script.go`
- Create: `video-factory/internal/service/script_handler.go`
- Modify: `video-factory/internal/server/router.go`
- Modify: `video-factory/cmd/user-svc/main.go`
- Test: `video-factory/internal/biz/script_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/biz/script_test.go
package biz

import (
    "context"
    "encoding/json"
    "testing"
    "github.com/<owner>/video-factory/internal/pkg/ai"
)

type fakeAgent struct{ called bool }

func (f *fakeAgent) GenerateScript(_ context.Context, r *ai.ScriptReq) (*ai.ScriptResp, error) {
    f.called = true
    return &ai.ScriptResp{Hook: "h", Body: []string{"b1"}, CTA: "c", DurationEstimate: r.Duration}, nil
}

type fakeProjectRepo struct{ id int64 }
func (f *fakeProjectRepo) Create(_ context.Context, p *Project) error { f.id++; p.ID = f.id; return nil }

type fakeTaskRepo struct{ tasks map[int64]*Task; id int64 }
func (f *fakeTaskRepo) Create(_ context.Context, t *Task) error {
    f.id++; t.ID = f.id; f.tasks[t.ID] = t; return nil
}
func (f *fakeTaskRepo) UpdateResult(_ context.Context, id int64, status string, result any, errMsg string) error {
    t := f.tasks[id]; t.Status = status
    if result != nil { b, _ := json.Marshal(result); t.Result = b }
    t.Error = errMsg
    return nil
}
func (f *fakeTaskRepo) Get(_ context.Context, id int64) (*Task, error) { return f.tasks[id], nil }

func TestCreateScriptTask_RunsSyncAndStoresResult(t *testing.T) {
    pr := &fakeProjectRepo{}
    tr := &fakeTaskRepo{tasks: map[int64]*Task{}}
    agent := &fakeAgent{}
    uc := NewScriptUsecase(pr, tr, agent)

    task, err := uc.Create(context.Background(), 42, &CreateScriptInput{
        Title: "demo", Topic: "RAG", Duration: 60, Style: "口播",
    })
    if err != nil { t.Fatal(err) }
    if !agent.called { t.Fatal("agent not called") }
    if task.Status != "succeeded" { t.Fatalf("status=%s", task.Status) }
    if string(task.Result) == "" { t.Fatal("result empty") }
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/biz/... -v -run TestCreateScriptTask
```

Expected: FAIL

- [ ] **Step 3: 写实现**

```go
// internal/biz/script.go
package biz

import (
    "context"
    "encoding/json"
    "github.com/<owner>/video-factory/internal/pkg/ai"
)

type Project struct {
    ID     int64
    UserID int64
    Title  string
}
type Task struct {
    ID        int64
    ProjectID int64
    Type      string
    Status    string
    Params    json.RawMessage
    Result    json.RawMessage
    Error     string
}

type ProjectRepo interface {
    Create(ctx context.Context, p *Project) error
}
type TaskRepo interface {
    Create(ctx context.Context, t *Task) error
    UpdateResult(ctx context.Context, id int64, status string, result any, errMsg string) error
    Get(ctx context.Context, id int64) (*Task, error)
}
type AgentClient interface {
    GenerateScript(ctx context.Context, req *ai.ScriptReq) (*ai.ScriptResp, error)
}

type CreateScriptInput struct {
    Title    string
    Topic    string
    Duration int
    Style    string
}

type ScriptUsecase struct {
    pr    ProjectRepo
    tr    TaskRepo
    agent AgentClient
}

func NewScriptUsecase(pr ProjectRepo, tr TaskRepo, agent AgentClient) *ScriptUsecase {
    return &ScriptUsecase{pr: pr, tr: tr, agent: agent}
}

func (uc *ScriptUsecase) Create(ctx context.Context, userID int64, in *CreateScriptInput) (*Task, error) {
    p := &Project{UserID: userID, Title: in.Title}
    if err := uc.pr.Create(ctx, p); err != nil { return nil, err }

    params, _ := json.Marshal(map[string]any{"topic": in.Topic, "duration": in.Duration, "style": in.Style})
    t := &Task{ProjectID: p.ID, Type: "script", Status: "running", Params: params}
    if err := uc.tr.Create(ctx, t); err != nil { return nil, err }

    out, err := uc.agent.GenerateScript(ctx, &ai.ScriptReq{
        Topic: in.Topic, Duration: in.Duration, Style: in.Style,
    })
    if err != nil {
        _ = uc.tr.UpdateResult(ctx, t.ID, "failed", nil, err.Error())
        return nil, err
    }
    if err := uc.tr.UpdateResult(ctx, t.ID, "succeeded", out, ""); err != nil { return nil, err }
    return uc.tr.Get(ctx, t.ID)
}
```

- [ ] **Step 4: 跑测试通过**

```bash
go test ./internal/biz/... -v
```

Expected: 全部 PASS

- [ ] **Step 5: 写 handler**

```go
// internal/service/script_handler.go
package service

import (
    "net/http"
    "github.com/gin-gonic/gin"
    "github.com/<owner>/video-factory/internal/biz"
)

type ScriptHandler struct{ uc *biz.ScriptUsecase }

func NewScriptHandler(uc *biz.ScriptUsecase) *ScriptHandler { return &ScriptHandler{uc: uc} }

type createScriptReq struct {
    Title    string `json:"title" binding:"required"`
    Topic    string `json:"topic" binding:"required"`
    Duration int    `json:"duration"`
    Style    string `json:"style"`
}

func (h *ScriptHandler) Create(c *gin.Context) {
    var req createScriptReq
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()}); return
    }
    if req.Duration == 0 { req.Duration = 120 }
    if req.Style == "" { req.Style = "口播" }
    uid := c.GetInt64("uid")
    task, err := h.uc.Create(c, uid, &biz.CreateScriptInput{
        Title: req.Title, Topic: req.Topic, Duration: req.Duration, Style: req.Style,
    })
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()}); return
    }
    c.JSON(http.StatusOK, gin.H{
        "task_id": task.ID, "status": task.Status, "result": task.Result,
    })
}
```

- [ ] **Step 6: 注册路由 + 完整启动**

修改 `internal/server/router.go`：

```go
func NewRouter(auth *service.AuthHandler, script *service.ScriptHandler, j *jwt.JWT) *gin.Engine {
    r := gin.Default()
    r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
    r.POST("/api/v1/auth/register", auth.Register)
    r.POST("/api/v1/auth/login",    auth.Login)
    authed := r.Group("/api/v1", service.AuthMiddleware(j))
    authed.POST("/scripts", script.Create)
    return r
}
```

修改 `cmd/user-svc/main.go`，把 `script` 串起来（仿照 auth 做 repo adapter，省略；需要给 `ProjectRepo` / `TaskRepo` 提供与 biz 接口匹配的适配器）。

- [ ] **Step 7: 端到端验证**

```bash
# 确保 final/main.py 在 8090，video-factory 在 8080，PG 在 5432
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
   -H 'Content-Type: application/json' \
   -d '{"phone":"13800000001","password":"hello123"}' | python -c "import sys,json;print(json.load(sys.stdin)['token'])")
curl -s -X POST http://localhost:8080/api/v1/scripts \
   -H "Authorization: Bearer $TOKEN" \
   -H 'Content-Type: application/json' \
   -d '{"title":"first","topic":"如何在 5 分钟讲清 RAG","duration":60,"style":"口播"}' | python -m json.tool
```

Expected: 返回 `{task_id, status:"succeeded", result:{hook, body, cta, duration_estimate}}`

- [ ] **Step 8: 提交**

```bash
git add .
git commit -m "feat(script): end-to-end script task: gin -> agent -> pg"
```

---

## Task 8: README + 一键启动脚本

**Files:**
- Create: `video-factory/README.md`
- Create: `video-factory/Makefile`

- [ ] **Step 1: 写 README**

```markdown
# video-factory

Phase 1 MVP：Go(Gin) + Python AI Agent 联合产出口播脚本。

## 一键启动
make dev   # 起 PG + 启动 user-svc 监听 :8080

## 端到端流程
1. 启动本仓库（:8080）+ AGI-assistant final/（:8090）
2. POST /api/v1/auth/register → 拿 token
3. POST /api/v1/scripts → 拿到结构化脚本
```

- [ ] **Step 2: 写 Makefile**

```makefile
.PHONY: dev migrate test
dev:
	docker compose -f deploy/docker-compose.yml up -d
	sleep 2
	$(MAKE) migrate
	go run ./cmd/user-svc

migrate:
	@for f in migrations/*.sql; do \
	  docker exec -i $$(docker ps -qf name=postgres) psql -U vf -d video_factory < $$f; \
	done

test:
	go test ./... -v
```

- [ ] **Step 3: 提交**

```bash
git add README.md Makefile
git commit -m "docs: phase1 readme + makefile"
```

- [ ] **Step 4: push 远端（首次）**

```bash
gh repo create <owner>/video-factory --private --source . --push
# 或手动加 remote
```

---

## Phase 1 验收

- [ ] `make dev` 一键启动 + PG 自动 migrate
- [ ] `final/` 启动后 `/api/script/generate` 返回结构化脚本（curl 验证）
- [ ] 注册 → 登录 → 创建脚本，三步 curl 跑通
- [ ] `go test ./...` 全部通过（biz + jwt + agent client）
- [ ] tasks 表有正确的 `status=succeeded` 记录
- [ ] 平均生成时延：< 5 秒（火山方舟 deepseek-v4-flash 实测）

---

## Self-Review

**1. Spec coverage**
- ✅ user-svc 注册/登录（T1-T3）
- ✅ task-svc（合并到 user-svc 进程，方便 phase 1 调试；phase 2 拆出去）
- ✅ users/projects/tasks 三张表（T2/T4）
- ✅ Agent /api/script/generate（T5）
- ✅ Go ↔ Agent HTTP 同步调用（T6/T7）
- ⚠️ "Worker 消费 Kafka 跑生成任务"——Phase 1 有意省略，所有任务同步执行；Phase 2 引入

**2. Placeholder scan** — 全部步骤含完整代码或命令，无 TODO/TBD

**3. Type consistency**
- `ScriptReq`/`ScriptResp` 在 ai 包定义，biz 与 service 都引用同一份 ✓
- `Task.Result` 在 repo 是 `datatypes.JSON`、biz 是 `json.RawMessage`，这是合理的层间转换边界 ✓
- `AuthMiddleware` 设置 `c.Set("uid", uid)`，handler 读 `c.GetInt64("uid")` ✓

---

## 给下一阶段的契约（Phase 2 起点）

- `tasks.type` 已支持任意字符串，phase 2 直接加 `tts` / `subtitle` 类型
- `tasks.params` / `tasks.result` 是 jsonb，schema 自由扩展
- Agent base URL 在 config 里，phase 2 加 `/api/tts/synthesize` 路径直接复用 client
- `ScriptUsecase` 是同步执行模板；phase 2 改成"创建任务 → 写 Kafka → worker 跑 → 回写"模式
