package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type MempoolBackendInfo struct {
	Version     string `json:"version"`
	Backend     string `json:"backend"`
	CoreVersion string `json:"coreVersion"`
}

type MempoolPoolStats struct {
	Count    int `json:"count"`
	VSize    int `json:"vsize"`
	TotalFee int `json:"total_fee"`
}

type MempoolStats struct {
	BackendInfo *MempoolBackendInfo `json:"backend_info"`
	Pool        *MempoolPoolStats   `json:"pool"`
	BlockHeight int                 `json:"block_height"`
}

const mempoolBaseURL = "http://truffels-mempool-backend:8999"

func (s *Server) handleMempoolStats(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: 3 * time.Second}

	var stats MempoolStats

	// Fetch backend info
	if resp, err := client.Get(mempoolBaseURL + "/api/v1/backend-info"); err == nil {
		defer func() { _ = resp.Body.Close() }()
		if body, err := io.ReadAll(resp.Body); err == nil {
			var info MempoolBackendInfo
			if json.Unmarshal(body, &info) == nil {
				stats.BackendInfo = &info
			}
		}
	}

	// Fetch mempool stats
	if resp, err := client.Get(mempoolBaseURL + "/api/v1/mempool"); err == nil {
		defer func() { _ = resp.Body.Close() }()
		if body, err := io.ReadAll(resp.Body); err == nil {
			var pool MempoolPoolStats
			if json.Unmarshal(body, &pool) == nil {
				stats.Pool = &pool
			}
		}
	}

	// Fetch block tip height
	if resp, err := client.Get(mempoolBaseURL + "/api/v1/blocks/tip/height"); err == nil {
		defer func() { _ = resp.Body.Close() }()
		if body, err := io.ReadAll(resp.Body); err == nil {
			if h, err := strconv.Atoi(strings.TrimSpace(string(body))); err == nil {
				stats.BlockHeight = h
			}
		}
	}

	// If we got nothing at all, the backend is unreachable
	if stats.BackendInfo == nil && stats.Pool == nil && stats.BlockHeight == 0 {
		writeError(w, http.StatusServiceUnavailable, "mempool backend unreachable")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}
