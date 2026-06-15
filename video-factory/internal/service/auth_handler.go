package service

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/AGI-Core/AGI-saber/video-factory/internal/biz"
	"github.com/AGI-Core/AGI-saber/video-factory/internal/pkg/jwt"
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, err := h.uc.Register(c.Request.Context(), req.Phone, req.Password, req.Nickname)
	if err != nil {
		if err == biz.ErrPhoneExists {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tok, err := h.jwt.Sign(u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, tokenResp{Token: tok, UID: u.ID})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, err := h.uc.Login(c.Request.Context(), req.Phone, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	tok, err := h.jwt.Sign(u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, tokenResp{Token: tok, UID: u.ID})
}

// AuthMiddleware extracts Bearer JWT and stores uid in gin context.
func AuthMiddleware(j *jwt.JWT) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		if strings.HasPrefix(raw, "Bearer ") {
			raw = strings.TrimPrefix(raw, "Bearer ")
		}
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		uid, err := j.Parse(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set("uid", uid)
		c.Next()
	}
}
