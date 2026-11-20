package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/korecube/korecube/internal/gpuops"
	"github.com/korecube/korecube/internal/service"
	"github.com/korecube/korecube/pkg/k8s"
	"github.com/korecube/korecube/pkg/utils"
)

// GPUOpsHandler serves GPU health, waste, alert aggregation, and tiered cordon/drain.
type GPUOpsHandler struct {
	svc     *service.GPUOpsService
	manager *k8s.ClusterManager
}

func NewGPUOpsHandler(svc *service.GPUOpsService, manager *k8s.ClusterManager) *GPUOpsHandler {
	return &GPUOpsHandler{svc: svc, manager: manager}
}

func (h *GPUOpsHandler) client(c *gin.Context) (*k8s.Client, bool) {
	return k8s.GetClientFromQuery(c, h.manager)
}

func zhFrom(c *gin.Context) bool {
	lang := c.Query("lang")
	if lang == "" {
		lang = c.GetHeader("Accept-Language")
	}
	return strings.HasPrefix(strings.ToLower(lang), "zh")
}

func (h *GPUOpsHandler) Health(c *gin.Context) {
	client, ok := h.client(c)
	if !ok {
		return
	}
	report, err := h.svc.Health(c.Request.Context(), client, zhFrom(c))
	if err != nil {
		utils.ApiError(c, http.StatusInternalServerError, "failed to build GPU health report", err.Error())
		return
	}
	utils.ApiSuccess(c, report, "ok")
}

func (h *GPUOpsHandler) Waste(c *gin.Context) {
	client, ok := h.client(c)
	if !ok {
		return
	}
	report, err := h.svc.Waste(c.Request.Context(), client)
	if err != nil {
		utils.ApiError(c, http.StatusInternalServerError, "failed to build GPU waste report", err.Error())
		return
	}
	utils.ApiSuccess(c, report, "ok")
}

func (h *GPUOpsHandler) Storm(c *gin.Context) {
	client, ok := h.client(c)
	if !ok {
		return
	}
	report, err := h.svc.Storm(c.Request.Context(), client)
	if err != nil {
		utils.ApiError(c, http.StatusInternalServerError, "failed to aggregate events", err.Error())
		return
	}
	utils.ApiSuccess(c, report, "ok")
}

func (h *GPUOpsHandler) Decisions(c *gin.Context) {
	utils.ApiSuccess(c, h.svc.Decisions(), "ok")
}

type gpuActionRequest struct {
	Node    string `json:"node"`
	Action  string `json:"action"`
	Confirm bool   `json:"confirm"`
}

func (h *GPUOpsHandler) Act(c *gin.Context) {
	client, ok := h.client(c)
	if !ok {
		return
	}
	var req gpuActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.ApiError(c, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	user, _ := c.Get("username")
	username, _ := user.(string)
	dec, err := h.svc.Act(c.Request.Context(), client, username, req.Node, req.Action, req.Confirm, zhFrom(c))
	if err != nil {
		utils.ApiError(c, gpuErrorStatus(err), err.Error(), dec.Detail)
		return
	}
	utils.ApiSuccess(c, dec, "executed")
}

func gpuErrorStatus(err error) int {
	switch {
	case errors.Is(err, gpuops.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, gpuops.ErrRejected):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func (h *GPUOpsHandler) Anomalies(c *gin.Context) {
	client, ok := h.client(c)
	if !ok {
		return
	}
	report, err := h.svc.Anomalies(c.Request.Context(), client)
	if err != nil {
		utils.ApiError(c, http.StatusInternalServerError, "failed to detect GPU anomalies", err.Error())
		return
	}
	utils.ApiSuccess(c, report, "ok")
}

func (h *GPUOpsHandler) Capacity(c *gin.Context) {
	client, ok := h.client(c)
	if !ok {
		return
	}
	report, err := h.svc.Capacity(c.Request.Context(), client, zhFrom(c))
	if err != nil {
		utils.ApiError(c, http.StatusInternalServerError, "failed to build GPU capacity report", err.Error())
		return
	}
	utils.ApiSuccess(c, report, "ok")
}

func (h *GPUOpsHandler) ScalingPlans(c *gin.Context) {
	client, ok := h.client(c)
	if !ok {
		return
	}
	utils.ApiSuccess(c, h.svc.ScalingPlans(client), "ok")
}

func (h *GPUOpsHandler) ProposeScaling(c *gin.Context) {
	client, ok := h.client(c)
	if !ok {
		return
	}
	var req gpuops.ScalingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.ApiError(c, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	plan, err := h.svc.ProposeScaling(c.Request.Context(), client, req, zhFrom(c))
	if err != nil {
		utils.ApiError(c, gpuErrorStatus(err), err.Error(), "")
		return
	}
	utils.ApiSuccess(c, plan, "proposed")
}

type scalingDecisionRequest struct {
	ID      string `json:"id"`
	Approve bool   `json:"approve"`
}

func (h *GPUOpsHandler) DecideScaling(c *gin.Context) {
	client, ok := h.client(c)
	if !ok {
		return
	}
	var req scalingDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.ApiError(c, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	user, _ := c.Get("username")
	username, _ := user.(string)
	plan, err := h.svc.DecideScaling(c.Request.Context(), client, username, req.ID, req.Approve)
	if err != nil {
		utils.ApiError(c, gpuErrorStatus(err), err.Error(), plan.Detail)
		return
	}
	utils.ApiSuccess(c, plan, plan.Status)
}
