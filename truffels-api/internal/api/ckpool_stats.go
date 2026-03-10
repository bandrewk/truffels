package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

type PoolStatus struct {
	Runtime    int `json:"runtime"`
	LastUpdate int `json:"lastupdate"`
	Users      int `json:"Users"`
	Workers    int `json:"Workers"`
	Idle       int `json:"Idle"`
}

type PoolHashrates struct {
	Hashrate1m  string `json:"hashrate1m"`
	Hashrate5m  string `json:"hashrate5m"`
	Hashrate15m string `json:"hashrate15m"`
	Hashrate1hr string `json:"hashrate1hr"`
	Hashrate6hr string `json:"hashrate6hr"`
	Hashrate1d  string `json:"hashrate1d"`
	Hashrate7d  string `json:"hashrate7d"`
}

type PoolShares struct {
	Diff      float64 `json:"diff"`
	Accepted  int64   `json:"accepted"`
	Rejected  int64   `json:"rejected"`
	BestShare int64   `json:"bestshare"`
	SPS1m     float64 `json:"SPS1m"`
	SPS5m     float64 `json:"SPS5m"`
	SPS15m    float64 `json:"SPS15m"`
	SPS1h     float64 `json:"SPS1h"`
}

type CkpoolStats struct {
	Status    *PoolStatus    `json:"status"`
	Hashrates *PoolHashrates `json:"hashrates"`
	Shares    *PoolShares    `json:"shares"`
}

func (s *Server) handleCkpoolStats(w http.ResponseWriter, r *http.Request) {
	dataRoot := os.Getenv("TRUFFELS_DATA_ROOT")
	if dataRoot == "" {
		dataRoot = "/srv/truffels/data"
	}

	statusFile := filepath.Join(dataRoot, "ckpool", "logs", "pool", "pool.status")
	data, err := os.ReadFile(statusFile)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "cannot read pool.status: "+err.Error())
		return
	}

	// pool.status contains 3 JSON objects, one per line
	lines := splitLines(data)
	if len(lines) < 3 {
		writeError(w, http.StatusServiceUnavailable, "incomplete pool.status")
		return
	}

	var status PoolStatus
	var hashrates PoolHashrates
	var shares PoolShares

	if err := json.Unmarshal(lines[0], &status); err != nil {
		writeError(w, http.StatusInternalServerError, "parse status: "+err.Error())
		return
	}
	if err := json.Unmarshal(lines[1], &hashrates); err != nil {
		writeError(w, http.StatusInternalServerError, "parse hashrates: "+err.Error())
		return
	}
	if err := json.Unmarshal(lines[2], &shares); err != nil {
		writeError(w, http.StatusInternalServerError, "parse shares: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, CkpoolStats{
		Status:    &status,
		Hashrates: &hashrates,
		Shares:    &shares,
	})
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			line := data[start:i]
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
