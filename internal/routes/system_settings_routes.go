package routes

import (
	"github.com/korecube/korecube/configs"
	"github.com/korecube/korecube/internal/handlers"
	"github.com/korecube/korecube/pkg/auth"
	"github.com/gin-gonic/gin"
)

// RegisterSystemSettingsRoutes registers system settings routes for administrators
func RegisterSystemSettingsRoutes(router *gin.RouterGroup, config *configs.Config) {
	settingsHandler := handlers.NewSystemSettingsHandler(config)

	// Apply JWT middleware and admin permission check to all system settings routes
	settingsRoutes := router.Group("/settings")
	settingsRoutes.Use(auth.JWTAuthMiddleware(), auth.AdminRequiredMiddleware())
	{
		// System information
		settingsRoutes.GET("/system", settingsHandler.GetSystemInfo)

		// OAuth settings
		settingsRoutes.GET("/oauth", settingsHandler.GetOAuthSettings)
		settingsRoutes.PUT("/oauth", settingsHandler.UpdateOAuthSettings)

		// Security settings
		settingsRoutes.GET("/security", settingsHandler.GetSecuritySettings)
		settingsRoutes.PUT("/security", settingsHandler.UpdateSecuritySettings)

		// System preferences
		settingsRoutes.GET("/preferences", settingsHandler.GetSystemPreferences)
		settingsRoutes.PUT("/preferences", settingsHandler.UpdateSystemPreferences)

		// AI assistant
		settingsRoutes.GET("/ai", settingsHandler.GetAISettings)
		settingsRoutes.PUT("/ai", settingsHandler.UpdateAISettings)

		// Prometheus (time-series charts)
		settingsRoutes.GET("/prometheus", settingsHandler.GetPrometheusSettings)
		settingsRoutes.PUT("/prometheus", settingsHandler.UpdatePrometheusSettings)
	}
}
