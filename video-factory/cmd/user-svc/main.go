package main

import (
	"log"
	"time"

	"github.com/AGI-Core/AGI-saber/video-factory/internal/biz"
	"github.com/AGI-Core/AGI-saber/video-factory/internal/config"
	"github.com/AGI-Core/AGI-saber/video-factory/internal/data"
	"github.com/AGI-Core/AGI-saber/video-factory/internal/data/repo"
	"github.com/AGI-Core/AGI-saber/video-factory/internal/pkg/ai"
	jwtpkg "github.com/AGI-Core/AGI-saber/video-factory/internal/pkg/jwt"
	"github.com/AGI-Core/AGI-saber/video-factory/internal/server"
	"github.com/AGI-Core/AGI-saber/video-factory/internal/service"
)

func main() {
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		log.Fatal(err)
	}
	db, err := data.NewPG(cfg.Postgres.DSN)
	if err != nil {
		log.Fatal(err)
	}

	userRepo := repo.NewUserRepo(db)
	projectRepo := repo.NewProjectRepo(db)
	taskRepo := repo.NewTaskRepo(db)

	userUC := biz.NewUserUsecase(userRepo)

	agentTimeout := time.Duration(cfg.Agent.Timeout) * time.Second
	if agentTimeout == 0 {
		agentTimeout = 30 * time.Second
	}
	agentClient := ai.NewClient(cfg.Agent.BaseURL, agentTimeout)
	scriptUC := biz.NewScriptUsecase(projectRepo, taskRepo, agentClient)

	j := jwtpkg.New(cfg.JWT.Secret, cfg.JWT.TTLHour)
	authHandler := service.NewAuthHandler(userUC, j)
	scriptHandler := service.NewScriptHandler(scriptUC)

	r := server.NewRouter(authHandler, scriptHandler, j)
	log.Printf("video-factory listening on %s", cfg.Server.Addr)
	if err := r.Run(cfg.Server.Addr); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
