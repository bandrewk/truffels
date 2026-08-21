package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"truffels-api/internal/admission"
)

func (s *Server) handleGetCatalog(w http.ResponseWriter, r *http.Request) {
	cat, err := s.catalogClient.All()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var entries []any
	for _, e := range cat {
		entries = append(entries, e)
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) getAdmissionInput(id string, newFloorMB int, newMinDiskGB int) (admission.Input, error) {
	usableRAM := 8192
	freeDiskGB := 999
	if s.collector != nil {
		metrics := s.collector.Collect()
		usableRAM = int(metrics.MemTotalMB)
		freeDiskGB = 0
		for _, d := range metrics.Disks {
			if d.Path == "/srv/truffels" || d.Path == "/" {
				if freeDiskGB == 0 || int(d.AvailGB) < freeDiskGB {
					freeDiskGB = int(d.AvailGB)
				}
			}
		}
		if freeDiskGB == 0 && len(metrics.Disks) > 0 {
			freeDiskGB = int(metrics.Disks[0].AvailGB)
		}
	}

	// Count only the containers of services that are NOT disabled. A stopped
	// but enabled service can start again and reclaim its memory, so it counts;
	// a disabled service never will, so it does not. Legacy and catalog
	// services alike come from the registry.
	enabled := make(map[string]bool)
	for _, tmpl := range s.registry.All() {
		if ok, _ := s.store.IsServiceEnabled(tmpl.ID); ok {
			for _, cn := range tmpl.ContainerNames {
				enabled[cn] = true
			}
		}
	}

	trend, err := s.store.GetContainerSnapshotsForTrend(time.Now().Add(-24 * time.Hour))
	if err != nil {
		return admission.Input{}, fmt.Errorf("snapshots: %w", err)
	}

	usage := make(map[string][]int)
	for _, snap := range trend {
		if !enabled[snap.Container] {
			continue
		}
		usage[snap.Container] = append(usage[snap.Container], int(snap.MemUsageMB))
	}

	var p95s []int
	for _, vals := range usage {
		if len(vals) == 0 {
			continue
		}
		sort.Ints(vals)
		idx := int(float64(len(vals)) * 0.95)
		if idx >= len(vals) {
			idx = len(vals) - 1
		}
		p95s = append(p95s, vals[idx])
	}

	growthBuffer := 20
	if s.btcRPC != nil {
		if info, err := s.btcRPC.GetBlockchainInfo(); err == nil && info.VerificationProgress < 0.9999 {
			growthBuffer = 300
		}
	}

	return admission.Input{
		RunningP95MB:   p95s,
		NewFloorMB:     newFloorMB,
		PhysicalRAMMB:  usableRAM,
		ReserveMB:      1500,
		NewMinDiskGB:   newMinDiskGB,
		FreeDiskGB:     freeDiskGB,
		GrowthBufferGB: growthBuffer,
	}, nil
}

func (s *Server) handleCatalogAdmission(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cat, err := s.catalogClient.All()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entry, ok := cat[id]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown catalog id")
		return
	}

	in, err := s.getAdmissionInput(id, entry.Resources.MemoryFloorMB, entry.Resources.MinDiskGB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	dec := admission.Check(in)
	writeJSON(w, http.StatusOK, dec)
}

func (s *Server) handleCatalogInstall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	cat, err := s.catalogClient.All()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entry, ok := cat[id]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown catalog id")
		return
	}

	_, ok, err = s.store.GetCatalogInstallation(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ok {
		writeError(w, http.StatusConflict, "already installed")
		return
	}

	in, err := s.getAdmissionInput(id, entry.Resources.MemoryFloorMB, entry.Resources.MinDiskGB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	dec := admission.Check(in)
	if !dec.Allowed {
		writeError(w, http.StatusConflict, dec.Reason)
		return
	}

	if err := s.catalogClient.Apply(id, body.Params); err != nil {
		writeError(w, http.StatusBadGateway, "agent apply failed: "+err.Error())
		return
	}

	if err := s.store.AddCatalogInstallation(id, id, body.Params); err != nil {
		_ = s.catalogClient.Remove(id, false) // rollback
		writeError(w, http.StatusInternalServerError, "db save failed: "+err.Error())
		return
	}

	if err := s.registry.Refresh(); err != nil {
		slog.Warn("registry refresh after catalog change failed", "err", err)
	}
	s.handleGetService(w, r)
}

func (s *Server) handleCatalogUninstall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		PurgeData bool `json:"purge_data"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // ignore error, optional

	inst, ok, err := s.store.GetCatalogInstallation(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not installed")
		return
	}
	// Delete the DB row before asking the agent to remove files. This keeps
	// symmetry with install (which rolls back on DB failure) and, crucially,
	// prevents a zombie: if the DB delete fails the service stays fully
	// installed; if the agent remove fails we roll the DB row back, so the
	// startup reconcile never revives a service whose files are already gone.
	if err := s.store.RemoveCatalogInstallation(id); err != nil {
		writeError(w, http.StatusInternalServerError, "db remove failed: "+err.Error())
		return
	}
	if err := s.catalogClient.Remove(id, body.PurgeData); err != nil {
		_ = s.store.AddCatalogInstallation(inst.ID, inst.CatalogID, inst.Params) // rollback
		writeError(w, http.StatusBadGateway, "agent remove failed: "+err.Error())
		return
	}
	if body.PurgeData {
		_ = s.store.SetServiceEnabled(id, false)
	}
	if err := s.registry.Refresh(); err != nil {
		slog.Warn("registry refresh after uninstall failed", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
