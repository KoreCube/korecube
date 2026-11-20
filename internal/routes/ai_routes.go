package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/korecube/korecube/configs"
	"github.com/korecube/korecube/internal/ai"
	"github.com/korecube/korecube/pkg/k8s"
)

func RegisterAIRoutes(router *gin.RouterGroup, cfg *configs.Config, manager *k8s.ClusterManager) {
	h := ai.NewHandler(cfg, manager)
	g := router.Group("/ai")
	{
		g.GET("/status", h.Status)
		g.POST("/chat", h.Chat)
	}
}
