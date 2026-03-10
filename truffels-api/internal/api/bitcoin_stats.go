package api

import "net/http"

func (s *Server) handleBitcoindStats(w http.ResponseWriter, r *http.Request) {
	if s.btcRPC == nil {
		writeError(w, http.StatusServiceUnavailable, "bitcoin RPC not configured")
		return
	}

	stats, err := s.btcRPC.GetStats()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "bitcoin RPC error: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stats)
}
