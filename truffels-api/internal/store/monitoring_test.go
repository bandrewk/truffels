package store

import (
	"testing"
	"time"

	"truffels-api/internal/model"
)

func TestInsertAndGetMetricSnapshots(t *testing.T) {
	s := newTestStore(t)

	if err := s.InsertMetricSnapshot(50.0, 60.0, 55.0, 70.0, 3000, 50); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMetricSnapshot(55.0, 65.0, 58.0, 72.0, 3500, 60); err != nil {
		t.Fatal(err)
	}

	snaps, err := s.GetMetricSnapshots(time.Now().Add(-1*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}
	if snaps[0].FanRPM != 3000 || snaps[0].FanPercent != 50 {
		t.Fatalf("fan data mismatch: rpm=%d pct=%d", snaps[0].FanRPM, snaps[0].FanPercent)
	}
}

func TestMetricSnapshots_Downsample(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 20; i++ {
		if err := s.InsertMetricSnapshot(float64(i), 50, 55, 70, 3000, 50); err != nil {
			t.Fatal(err)
		}
	}

	snaps, err := s.GetMetricSnapshots(time.Now().Add(-1*time.Hour), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) > 6 {
		t.Fatalf("expected ~5 downsampled snapshots, got %d", len(snaps))
	}
}

func TestPruneMetricSnapshots(t *testing.T) {
	s := newTestStore(t)

	if err := s.InsertMetricSnapshot(50, 60, 55, 70, 3000, 50); err != nil {
		t.Fatal(err)
	}

	// Prune everything older than the future = nothing pruned
	if err := s.PruneMetricSnapshots(time.Now().Add(1 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	snaps, _ := s.GetMetricSnapshots(time.Now().Add(-1*time.Hour), 100)
	if len(snaps) != 0 {
		t.Fatalf("expected 0 after prune, got %d", len(snaps))
	}
}

func TestInsertAndGetContainerSnapshots(t *testing.T) {
	s := newTestStore(t)

	snaps := []model.ContainerSnapshot{
		{Container: "truffels-bitcoind", CPUPercent: 65.7, MemUsageMB: 2083, MemLimitMB: 3500, NetRxBytes: 909000000, NetTxBytes: 30700000000},
		{Container: "truffels-electrs", CPUPercent: 0.5, MemUsageMB: 64, MemLimitMB: 2048, NetRxBytes: 3140000000, NetTxBytes: 87000000},
	}

	if err := s.InsertContainerSnapshots(snaps); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetContainerSnapshots(time.Now().Add(-1*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 container snapshots, got %d", len(got))
	}
	if got[0].Container != "truffels-bitcoind" {
		t.Fatalf("expected bitcoind, got %q", got[0].Container)
	}
	if got[0].CPUPercent != 65.7 {
		t.Fatalf("expected 65.7 CPU, got %v", got[0].CPUPercent)
	}
	if got[1].NetRxBytes != 3140000000 {
		t.Fatalf("expected 3140000000 rx bytes, got %d", got[1].NetRxBytes)
	}
}

func TestContainerSnapshots_Downsample(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 50; i++ {
		snap := []model.ContainerSnapshot{
			{Container: "test-container", CPUPercent: float64(i), MemUsageMB: 100, MemLimitMB: 512, NetRxBytes: int64(i * 1000), NetTxBytes: int64(i * 500)},
		}
		if err := s.InsertContainerSnapshots(snap); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.GetContainerSnapshots(time.Now().Add(-1*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 12 {
		t.Fatalf("expected ~10 downsampled, got %d", len(got))
	}
}

func TestPruneContainerSnapshots(t *testing.T) {
	s := newTestStore(t)

	snaps := []model.ContainerSnapshot{
		{Container: "test", CPUPercent: 10, MemUsageMB: 100, MemLimitMB: 512, NetRxBytes: 1000, NetTxBytes: 500},
	}
	if err := s.InsertContainerSnapshots(snaps); err != nil {
		t.Fatal(err)
	}

	// Prune with future cutoff removes everything
	if err := s.PruneContainerSnapshots(time.Now().Add(1 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetContainerSnapshots(time.Now().Add(-1*time.Hour), 100)
	if len(got) != 0 {
		t.Fatalf("expected 0 after prune, got %d", len(got))
	}
}

func TestContainerSnapshots_EmptyBatch(t *testing.T) {
	s := newTestStore(t)

	// Empty batch should succeed
	if err := s.InsertContainerSnapshots(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertContainerSnapshots([]model.ContainerSnapshot{}); err != nil {
		t.Fatal(err)
	}
}
