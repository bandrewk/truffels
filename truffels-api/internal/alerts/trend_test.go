package alerts

import (
	"math"
	"testing"
	"time"

	"truffels-api/internal/model"
)

func TestLinearRegression_FlatLine(t *testing.T) {
	now := time.Now()
	points := []timeValue{
		{T: now, V: 50},
		{T: now.Add(1 * time.Hour), V: 50},
		{T: now.Add(2 * time.Hour), V: 50},
		{T: now.Add(3 * time.Hour), V: 50},
	}
	slope, _ := linearRegression(points)
	if math.Abs(slope) > 0.001 {
		t.Fatalf("expected ~0 slope for flat data, got %f", slope)
	}
}

func TestLinearRegression_LinearGrowth(t *testing.T) {
	now := time.Now()
	points := []timeValue{
		{T: now, V: 100},
		{T: now.Add(1 * time.Hour), V: 110},
		{T: now.Add(2 * time.Hour), V: 120},
		{T: now.Add(3 * time.Hour), V: 130},
	}
	slope, intercept := linearRegression(points)
	if math.Abs(slope-10) > 0.001 {
		t.Fatalf("expected slope ~10, got %f", slope)
	}
	if math.Abs(intercept-100) > 0.001 {
		t.Fatalf("expected intercept ~100, got %f", intercept)
	}
}

func TestLinearRegression_NegativeSlope(t *testing.T) {
	now := time.Now()
	points := []timeValue{
		{T: now, V: 200},
		{T: now.Add(1 * time.Hour), V: 180},
		{T: now.Add(2 * time.Hour), V: 160},
	}
	slope, _ := linearRegression(points)
	if math.Abs(slope-(-20)) > 0.001 {
		t.Fatalf("expected slope ~-20, got %f", slope)
	}
}

func TestLinearRegression_TooFewPoints(t *testing.T) {
	slope, intercept := linearRegression(nil)
	if slope != 0 || intercept != 0 {
		t.Fatalf("expected (0,0) for nil, got (%f, %f)", slope, intercept)
	}

	slope, intercept = linearRegression([]timeValue{{T: time.Now(), V: 42}})
	if slope != 0 || intercept != 0 {
		t.Fatalf("expected (0,0) for single point, got (%f, %f)", slope, intercept)
	}
}

func TestLinearRegression_NoisyData(t *testing.T) {
	now := time.Now()
	// Linear growth of 10/h with noise
	points := []timeValue{
		{T: now, V: 102},
		{T: now.Add(1 * time.Hour), V: 108},
		{T: now.Add(2 * time.Hour), V: 122},
		{T: now.Add(3 * time.Hour), V: 128},
		{T: now.Add(4 * time.Hour), V: 142},
		{T: now.Add(5 * time.Hour), V: 148},
	}
	slope, _ := linearRegression(points)
	// Should be roughly 10/h (within noise margin)
	if slope < 8 || slope > 12 {
		t.Fatalf("expected slope ~10 for noisy data, got %f", slope)
	}
}

func TestEvaluateTrend_ApproachingThreshold(t *testing.T) {
	now := time.Now()
	points := []timeValue{
		{T: now.Add(-3 * time.Hour), V: 700},
		{T: now.Add(-2 * time.Hour), V: 800},
		{T: now.Add(-1 * time.Hour), V: 900},
		{T: now, V: 1000},
	}
	// Threshold at 1200, slope 100/h → should hit in ~2h
	result := evaluateTrend(points, 1200)
	if result.Slope < 99 || result.Slope > 101 {
		t.Fatalf("expected slope ~100, got %f", result.Slope)
	}
	if result.HoursToHit < 1.9 || result.HoursToHit > 2.1 {
		t.Fatalf("expected ~2h to hit, got %f", result.HoursToHit)
	}
	if result.Current != 1000 {
		t.Fatalf("expected current=1000, got %f", result.Current)
	}
}

func TestEvaluateTrend_NegativeSlope(t *testing.T) {
	now := time.Now()
	points := []timeValue{
		{T: now.Add(-2 * time.Hour), V: 900},
		{T: now.Add(-1 * time.Hour), V: 800},
		{T: now, V: 700},
	}
	result := evaluateTrend(points, 1024)
	if !math.IsInf(result.HoursToHit, 1) {
		t.Fatalf("expected Inf for negative slope, got %f", result.HoursToHit)
	}
}

func TestEvaluateTrend_AlreadyExceeded(t *testing.T) {
	now := time.Now()
	points := []timeValue{
		{T: now.Add(-2 * time.Hour), V: 900},
		{T: now.Add(-1 * time.Hour), V: 1000},
		{T: now, V: 1100},
	}
	result := evaluateTrend(points, 1024)
	if result.HoursToHit != 0 {
		t.Fatalf("expected 0 for already exceeded, got %f", result.HoursToHit)
	}
}

func TestEvaluateTrend_FlatLine(t *testing.T) {
	now := time.Now()
	points := []timeValue{
		{T: now.Add(-2 * time.Hour), V: 500},
		{T: now.Add(-1 * time.Hour), V: 500},
		{T: now, V: 500},
	}
	result := evaluateTrend(points, 1024)
	if !math.IsInf(result.HoursToHit, 1) {
		t.Fatalf("expected Inf for flat data, got %f", result.HoursToHit)
	}
}

func TestEvaluateTrend_EmptyPoints(t *testing.T) {
	result := evaluateTrend(nil, 1024)
	if result.DataPoints != 0 {
		t.Fatalf("expected 0 data points, got %d", result.DataPoints)
	}
	if !math.IsInf(result.HoursToHit, 1) {
		t.Fatalf("expected Inf for empty, got %f", result.HoursToHit)
	}
}

// Integration test: insert synthetic container snapshots, run trend evaluator
func TestEvaluateContainerMemoryTrends_Integration(t *testing.T) {
	s := newTestStore(t)

	// Insert 3 hours of data for a container with linear memory growth: 500→800MB, limit 1024
	now := time.Now()
	for i := 0; i <= 180; i++ { // every minute for 3h
		ts := now.Add(-time.Duration(180-i) * time.Minute)
		mem := 500 + float64(i)*300.0/180.0 // 500 → 800 over 3h
		snap := model.ContainerSnapshot{
			Timestamp:  ts,
			Container:  "truffels-ckpool",
			MemUsageMB: mem,
			MemLimitMB: 1024,
		}
		if err := s.InsertContainerSnapshots([]model.ContainerSnapshot{snap}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Rate is 100MB/h, current ~800, limit 1024 → ~2.24h to hit
	alerts := evaluateContainerMemoryTrends(s, 6, 6)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 trend alert, got %d", len(alerts))
	}
	if alerts[0].AlertType != "memory_trend" {
		t.Fatalf("expected memory_trend, got %q", alerts[0].AlertType)
	}
	if alerts[0].ServiceID != "truffels-ckpool" {
		t.Fatalf("expected truffels-ckpool, got %q", alerts[0].ServiceID)
	}
}

func TestEvaluateContainerMemoryTrends_NoAlert_FlatMemory(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	for i := 0; i <= 120; i++ {
		ts := now.Add(-time.Duration(120-i) * time.Minute)
		snap := model.ContainerSnapshot{
			Timestamp:  ts,
			Container:  "truffels-ckpool",
			MemUsageMB: 500, // flat
			MemLimitMB: 1024,
		}
		if err := s.InsertContainerSnapshots([]model.ContainerSnapshot{snap}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	alerts := evaluateContainerMemoryTrends(s, 6, 6)
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts for flat memory, got %d", len(alerts))
	}
}

func TestEvaluateHostTrends_DiskGrowth(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	for i := 0; i <= 120; i++ {
		ts := now.Add(-time.Duration(120-i) * time.Minute)
		disk := 85 + float64(i)*10.0/120.0 // 85% → 95% over 2h
		if err := s.InsertMetricSnapshot(50, 60, 55, disk, 1000, 50, 0, 0, 0, 0, 0); err != nil {
			t.Fatalf("insert: %v", err)
		}
		// Fix the timestamp (InsertMetricSnapshot uses NOW())
		_, _ = s.DB().Exec(`UPDATE metric_snapshots SET timestamp = ? WHERE id = (SELECT MAX(id) FROM metric_snapshots)`,
			ts.UTC().Format("2006-01-02 15:04:05"))
	}

	alerts := evaluateHostTrends(s, 6, 6, 80)
	found := false
	for _, a := range alerts {
		if a.AlertType == "disk_trend" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected disk_trend alert, got %v", alerts)
	}
}

func TestEvaluateHostTrends_NoAlert_StableMetrics(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	for i := 0; i <= 120; i++ {
		ts := now.Add(-time.Duration(120-i) * time.Minute)
		if err := s.InsertMetricSnapshot(50, 60, 55, 50, 1000, 50, 0, 0, 0, 0, 0); err != nil {
			t.Fatalf("insert: %v", err)
		}
		_, _ = s.DB().Exec(`UPDATE metric_snapshots SET timestamp = ? WHERE id = (SELECT MAX(id) FROM metric_snapshots)`,
			ts.UTC().Format("2006-01-02 15:04:05"))
	}

	alerts := evaluateHostTrends(s, 6, 6, 80)
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts for stable metrics, got %d", len(alerts))
	}
}

func TestCheckTrends_Integration(t *testing.T) {
	e, s := newTestEngine(t)

	// Enable trend alerts
	_ = s.SetSetting("trend_alert_enabled", "true")
	_ = s.SetSetting("trend_alert_horizon_hours", "6")
	_ = s.SetSetting("trend_alert_lookback_hours", "6")
	_ = s.SetSetting("trend_alert_min_data_hours", "0") // disable min data check for test

	// Insert container snapshots with growing memory
	now := time.Now()
	for i := 0; i <= 180; i++ {
		ts := now.Add(-time.Duration(180-i) * time.Minute)
		snap := model.ContainerSnapshot{
			Timestamp:  ts,
			Container:  "truffels-ckpool",
			MemUsageMB: 500 + float64(i)*300.0/180.0,
			MemLimitMB: 1024,
		}
		if err := s.InsertContainerSnapshots([]model.ContainerSnapshot{snap}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	e.checkTrends()

	alerts, _ := s.GetActiveAlerts()
	found := false
	for _, a := range alerts {
		if a.Type == "memory_trend" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected memory_trend alert after checkTrends, got %v", alerts)
	}
}

func TestCheckTrends_Disabled(t *testing.T) {
	e, s := newTestEngine(t)

	_ = s.SetSetting("trend_alert_enabled", "false")

	// Insert some growing data
	now := time.Now()
	for i := 0; i <= 180; i++ {
		ts := now.Add(-time.Duration(180-i) * time.Minute)
		snap := model.ContainerSnapshot{
			Timestamp:  ts,
			Container:  "truffels-ckpool",
			MemUsageMB: 500 + float64(i)*300.0/180.0,
			MemLimitMB: 1024,
		}
		_ = s.InsertContainerSnapshots([]model.ContainerSnapshot{snap})
	}

	e.checkTrends()

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts when trend alerts disabled, got %d", len(alerts))
	}
}

func TestCheckTrends_AutoResolves(t *testing.T) {
	e, s := newTestEngine(t)

	_ = s.SetSetting("trend_alert_enabled", "true")
	_ = s.SetSetting("trend_alert_horizon_hours", "6")
	_ = s.SetSetting("trend_alert_lookback_hours", "6")
	_ = s.SetSetting("trend_alert_min_data_hours", "0")

	// First: insert growing data to trigger alert
	now := time.Now()
	for i := 0; i <= 180; i++ {
		ts := now.Add(-time.Duration(180-i) * time.Minute)
		snap := model.ContainerSnapshot{
			Timestamp:  ts,
			Container:  "truffels-ckpool",
			MemUsageMB: 500 + float64(i)*300.0/180.0,
			MemLimitMB: 1024,
		}
		_ = s.InsertContainerSnapshots([]model.ContainerSnapshot{snap})
	}

	e.checkTrends()
	alerts, _ := s.GetActiveAlerts()
	if len(alerts) == 0 {
		t.Fatal("expected trend alert to be created")
	}

	// Now replace with flat data (delete old, insert flat)
	_, _ = s.DB().Exec(`DELETE FROM container_snapshots`)
	for i := 0; i <= 180; i++ {
		ts := now.Add(-time.Duration(180-i) * time.Minute)
		snap := model.ContainerSnapshot{
			Timestamp:  ts,
			Container:  "truffels-ckpool",
			MemUsageMB: 500, // flat
			MemLimitMB: 1024,
		}
		_ = s.InsertContainerSnapshots([]model.ContainerSnapshot{snap})
	}

	e.checkTrends()
	alerts, _ = s.GetActiveAlerts()
	for _, a := range alerts {
		if a.Type == "memory_trend" && !a.Resolved {
			t.Fatalf("expected memory_trend to be resolved after flat data")
		}
	}
}

func TestPruneRetention7Days(t *testing.T) {
	s := newTestStore(t)

	// Insert a snapshot 5 days ago (should survive 7-day retention)
	if err := s.InsertMetricSnapshot(50, 60, 55, 50, 1000, 50, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("insert: %v", err)
	}
	fiveDaysAgo := time.Now().Add(-5 * 24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	_, _ = s.DB().Exec(`UPDATE metric_snapshots SET timestamp = ? WHERE id = (SELECT MAX(id) FROM metric_snapshots)`, fiveDaysAgo)

	// Prune with 7-day cutoff
	if err := s.PruneMetricSnapshots(time.Now().Add(-168 * time.Hour)); err != nil {
		t.Fatalf("prune: %v", err)
	}

	snaps, err := s.GetMetricSnapshots(time.Now().Add(-168*time.Hour), 100)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot to survive 7-day retention, got %d", len(snaps))
	}

	// Insert a snapshot 8 days ago (should be pruned)
	if err := s.InsertMetricSnapshot(50, 60, 55, 50, 1000, 50, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("insert: %v", err)
	}
	eightDaysAgo := time.Now().Add(-8 * 24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	_, _ = s.DB().Exec(`UPDATE metric_snapshots SET timestamp = ? WHERE id = (SELECT MAX(id) FROM metric_snapshots)`, eightDaysAgo)

	if err := s.PruneMetricSnapshots(time.Now().Add(-168 * time.Hour)); err != nil {
		t.Fatalf("prune: %v", err)
	}

	snaps, err = s.GetMetricSnapshots(time.Now().Add(-168*time.Hour), 100)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected only 5-day snapshot to remain, got %d", len(snaps))
	}
}
