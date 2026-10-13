package handlers

import (
	"errors"
	"net/http"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/update"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/update").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).
				Handle(latest),
		).
		AddRoute(
			router.NewRoute("/status", http.MethodGet).
				Handle(getUpdateStatus),
		).
		AddRoute(
			router.NewRoute("/now-version", http.MethodGet).
				Handle(getNowVersion),
		).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Handle(updateFunc),
		)
}

func latest(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	latestInfo, err := update.GetLatestInfo(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, *latestInfo)
}

func getNowVersion(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	resp.Success(c, conf.Version)
}

func getUpdateStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	resp.Success(c, update.GetStatus())
}

func updateFunc(c *gin.Context) {
	var request struct {
		Version string `json:"version"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.Version == "" {
		resp.Error(c, http.StatusBadRequest, "a target release version is required")
		return
	}
	installation, err := update.UpdateCore(c.Request.Context(), request.Version)
	if err != nil {
		if errors.Is(err, update.ErrUpdateInProgress) {
			resp.Error(c, http.StatusConflict, err.Error())
			return
		}
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, installation)
	c.Writer.Flush()
	go installation.Restart()
}
