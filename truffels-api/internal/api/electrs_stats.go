package api

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ElectrsStats struct {
	IndexHeight int `json:"index_height"`
}

func (s *Server) handleElectrsStats(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://truffels-electrs:4224/")
	if err != nil {
		// A stopped electrs has no container, so Docker's DNS returns
		// "no such host"; surface that as a plain not-running state instead
		// of a raw resolver error.
		writeError(w, http.StatusServiceUnavailable, "electrs is not running")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read metrics: "+err.Error())
		return
	}

	height := parsePrometheusGauge(string(body), `electrs_index_height{type="tip"}`)

	writeJSON(w, http.StatusOK, ElectrsStats{
		IndexHeight: height,
	})
}

func parsePrometheusGauge(body, metric string) int {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, metric) {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				v, err := strconv.ParseFloat(parts[len(parts)-1], 64)
				if err == nil {
					return int(v)
				}
				fmt.Println("parse error", err)
			}
		}
	}
	return 0
}
