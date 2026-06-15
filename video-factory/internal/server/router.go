package server

import (
	"github.com/gin-gonic/gin"

	"github.com/AGI-Core/AGI-saber/video-factory/internal/pkg/jwt"
	"github.com/AGI-Core/AGI-saber/video-factory/internal/service"
)

func NewRouter(auth *service.AuthHandler, script *service.ScriptHandler, j *jwt.JWT) *gin.Engine {
	r := gin.Default()
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	{
		api.POST("/auth/register", auth.Register)
		api.POST("/auth/login", auth.Login)

		authed := api.Group("", service.AuthMiddleware(j))
		authed.POST("/scripts", script.Create)
	}
	return r
}
