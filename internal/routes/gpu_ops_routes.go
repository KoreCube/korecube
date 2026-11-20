package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/korecube/korecube/internal/handlers"
)

// RegisterGPUOpsRoutes serves the GPU health, waste, and self-heal endpoints.
func RegisterGPUOpsRoutes(router *gin.RouterGroup, h *handlers.GPUOpsHandler) {
	if h == nil {
		return
	}
	g := router.Group("/gpu")
	{
		g.GET("/health", h.Health)
		g.GET("/waste", h.Waste)
		g.GET("/storm", h.Storm)
		g.GET("/decisions", h.Decisions)
		g.GET("/anomalies", h.Anomalies)
		g.GET("/capacity", h.Capacity)
		g.GET("/scaling", h.ScalingPlans)
		g.POST("/actions", h.Act)
		g.POST("/proposals", h.ProposeScaling)
		g.POST("/scaling", h.DecideScaling)
	}
}
