package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/auth"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// apiKeyRecentRequest 是密钥概览最近请求的精简视图, 只保留展示所需字段。
type apiKeyRecentRequest struct {
	Time         time.Time `json:"time"`           // 请求到达时间。
	Status       string    `json:"status"`         // 请求终态。
	Model        string    `json:"model"`          // 实际请求上游的模型名称, 未选出目标时为请求的分组名称。
	InputToken   int64     `json:"input_token"`    // 输入词元, 含缓存部分。
	OutputToken  int64     `json:"output_token"`   // 输出词元。
	CacheRead    int64     `json:"cache_read"`     // 命中缓存读取的输入词元。
	CacheWrite   int64     `json:"cache_write"`    // 写入缓存的输入词元。
	FirstTokenMS int64     `json:"first_token_ms"` // 首字节耗时毫秒, 非流式响应为零。
	TPS          float64   `json:"tps"`            // 输出词元每秒, 无法计算时为零。
	Cost         float64   `json:"cost"`           // 请求费用。
}

func init() {
	router.NewGroupRouter("/api/v1/apikey").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createAPIKey),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listAPIKey),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Handle(updateAPIKey),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteAPIKey),
		)
	router.NewGroupRouter("/api/v1/apikey").
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/stats", http.MethodGet).
				Handle(getStatsAPIKeyById),
		).
		AddRoute(
			router.NewRoute("/login", http.MethodGet).
				Handle(loginAPIKey),
		)
}

func createAPIKey(c *gin.Context) {
	var req model.APIKey
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if strings.TrimSpace(req.APIKey) == "" {
		req.APIKey = auth.GenerateAPIKey()
	}
	if err := op.APIKeyCreate(&req, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, req)
}

func listAPIKey(c *gin.Context) {
	apiKeys, err := op.APIKeyList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, apiKeys)
}

func updateAPIKey(c *gin.Context) {
	var req model.APIKey
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.APIKeyUpdate(&req, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, req)
}

func deleteAPIKey(c *gin.Context) {
	id := c.Param("id")
	idNum, err := strconv.Atoi(id)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if err := op.APIKeyDelete(idNum, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	relay.ForgetAPIKey(idNum)
	resp.Success(c, nil)
}

func getStatsAPIKeyById(c *gin.Context) {
	id := c.GetInt("api_key_id")
	stats := op.StatsAPIKeyGet(id)
	info, err := op.APIKeyGet(id, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	models := op.GroupListModel()
	if len(info.SupportedModels) > 0 {
		models = lo.Filter(models, func(m string, _ int) bool {
			return lo.Contains(info.SupportedModels, m)
		})
	}
	info.SupportedModels = models

	// 每日统计与首页热力图取同一 54 周窗口。
	now := time.Now()
	since := now.AddDate(0, 0, -(int(now.Weekday()) + 53*7)).Format("20060102")

	records := relay.APIKeyRecentRequests(id)
	recent := make([]apiKeyRecentRequest, 0, len(records))
	for _, record := range records {
		usage := record.Usage
		generation := record.StreamDuration
		if generation == 0 {
			generation = record.ResponseDuration
		}
		tps := 0.0
		if generation > 0 {
			tps = float64(usage.CompletionTokens) / generation.Seconds()
		}
		targetModel := record.TargetModel
		if targetModel == "" {
			targetModel = record.Model
		}
		cacheRead, cacheWrite := int64(0), int64(0)
		if usage.PromptTokensDetails != nil {
			cacheRead = usage.PromptTokensDetails.CachedTokens
			cacheWrite = usage.PromptTokensDetails.WriteCachedTokens
		}
		recent = append(recent, apiKeyRecentRequest{
			Time:         record.StartedAt,
			Status:       string(record.Status),
			Model:        targetModel,
			InputToken:   usage.PromptTokens,
			OutputToken:  usage.CompletionTokens,
			CacheRead:    cacheRead,
			CacheWrite:   cacheWrite,
			FirstTokenMS: record.FirstTokenDuration.Milliseconds(),
			TPS:          tps,
			Cost:         record.Cost,
		})
	}

	resp.Success(c, map[string]any{
		"stats":  stats,
		"info":   info,
		"daily":  op.StatsAPIKeyDailyList(id, since),
		"models": op.StatsAPIKeyModelList(id),
		"recent": recent,
	})
}

func loginAPIKey(c *gin.Context) {
	resp.Success(c, nil)
}
