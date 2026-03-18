package alerts

import (
	"fmt"
	"math"
	"time"

	"truffels-api/internal/model"
	"truffels-api/internal/store"
)

// timeValue pairs a timestamp with a numeric value for regression.
type timeValue struct {
	T time.Time
	V float64
}

// trendResult holds the output of a linear regression analysis.
type trendResult struct {
	Slope      float64 // units per hour (positive = growing)
	Current    float64 // most recent value
	Threshold  float64 // the ceiling we're approaching
	HoursToHit float64 // estimated hours until threshold (Inf if slope <= 0 or already exceeded)
	DataPoints int     // number of samples used
}

// linearRegression computes slope (units/hour) and intercept from timestamped values.
// Returns (0, 0) if fewer than 2 points.
func linearRegression(points []timeValue) (slope, intercept float64) {
	n := len(points)
	if n < 2 {
		return 0, 0
	}

	// Use hours since first point as x-axis
	t0 := points[0].T
	var sumX, sumY, sumXX, sumXY float64
	for _, p := range points {
		x := p.T.Sub(t0).Hours()
		y := p.V
		sumX += x
		sumY += y
		sumXX += x * x
		sumXY += x * y
	}

	fn := float64(n)
	denom := fn*sumXX - sumX*sumX
	if denom == 0 {
		return 0, sumY / fn
	}

	slope = (fn*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / fn
	return slope, intercept
}

// evaluateTrend runs regression on data points and returns a trendResult.
func evaluateTrend(points []timeValue, threshold float64) trendResult {
	result := trendResult{
		Threshold:  threshold,
		DataPoints: len(points),
		HoursToHit: math.Inf(1),
	}

	if len(points) == 0 {
		return result
	}

	result.Current = points[len(points)-1].V
	slope, intercept := linearRegression(points)
	result.Slope = slope

	if slope <= 0 {
		return result
	}

	// Extrapolate: threshold = intercept + slope * x_hours
	// x_hours is relative to t0, so hours-to-hit from now:
	t0 := points[0].T
	now := points[len(points)-1].T
	nowX := now.Sub(t0).Hours()
	hitX := (threshold - intercept) / slope

	if hitX > nowX {
		result.HoursToHit = hitX - nowX
	} else {
		// Already past threshold
		result.HoursToHit = 0
	}

	return result
}

type pendingAlert struct {
	AlertType string
	ServiceID string
	Message   string
}

// evaluateContainerMemoryTrends checks each container's memory usage trend.
func evaluateContainerMemoryTrends(s *store.Store, lookbackHours, horizon float64) []pendingAlert {
	since := time.Now().Add(-time.Duration(lookbackHours) * time.Hour)
	snaps, err := s.GetContainerSnapshotsForTrend(since)
	if err != nil || len(snaps) == 0 {
		return nil
	}

	// Group by container
	byContainer := make(map[string][]model.ContainerSnapshot)
	for _, snap := range snaps {
		byContainer[snap.Container] = append(byContainer[snap.Container], snap)
	}

	var alerts []pendingAlert
	for container, snapshots := range byContainer {
		if len(snapshots) < 2 {
			continue
		}

		// Use the memory limit from the most recent snapshot
		limit := snapshots[len(snapshots)-1].MemLimitMB
		if limit <= 0 {
			continue
		}

		points := make([]timeValue, len(snapshots))
		for i, snap := range snapshots {
			points[i] = timeValue{T: snap.Timestamp, V: snap.MemUsageMB}
		}

		result := evaluateTrend(points, limit)
		if result.HoursToHit < horizon && result.Slope > 0 {
			alerts = append(alerts, pendingAlert{
				AlertType: "memory_trend",
				ServiceID: container,
				Message: fmt.Sprintf("%s memory trending toward limit — estimated to hit %.0fMB in ~%.0fh (current: %.0fMB, rate: %+.0fMB/h)",
					container, limit, result.HoursToHit, result.Current, result.Slope),
			})
		}
	}
	return alerts
}

// evaluateHostTrends checks host disk, memory, and temperature trends.
func evaluateHostTrends(s *store.Store, lookbackHours, horizon, tempCritical float64) []pendingAlert {
	since := time.Now().Add(-time.Duration(lookbackHours) * time.Hour)
	snaps, err := s.GetMetricSnapshotsForTrend(since)
	if err != nil || len(snaps) == 0 {
		return nil
	}

	var diskPoints, memPoints, tempPoints []timeValue
	for _, snap := range snaps {
		diskPoints = append(diskPoints, timeValue{T: snap.Timestamp, V: snap.DiskPercent})
		memPoints = append(memPoints, timeValue{T: snap.Timestamp, V: snap.MemPercent})
		tempPoints = append(tempPoints, timeValue{T: snap.Timestamp, V: snap.TempC})
	}

	var alerts []pendingAlert

	// Disk trend → 95%
	if result := evaluateTrend(diskPoints, 95); result.HoursToHit < horizon && result.Slope > 0 {
		alerts = append(alerts, pendingAlert{
			AlertType: "disk_trend",
			ServiceID: "",
			Message: fmt.Sprintf("Disk usage trending toward 95%% — estimated to hit in ~%.0fh (current: %.1f%%, rate: %+.2f%%/h)",
				result.HoursToHit, result.Current, result.Slope),
		})
	}

	// Host memory trend → 95%
	if result := evaluateTrend(memPoints, 95); result.HoursToHit < horizon && result.Slope > 0 {
		alerts = append(alerts, pendingAlert{
			AlertType: "memory_trend",
			ServiceID: "",
			Message: fmt.Sprintf("Host memory trending toward 95%% — estimated to hit in ~%.0fh (current: %.1f%%, rate: %+.2f%%/h)",
				result.HoursToHit, result.Current, result.Slope),
		})
	}

	// Temperature trend → critical threshold
	if result := evaluateTrend(tempPoints, tempCritical); result.HoursToHit < horizon && result.Slope > 0 {
		alerts = append(alerts, pendingAlert{
			AlertType: "temp_trend",
			ServiceID: "",
			Message: fmt.Sprintf("CPU temperature trending toward %.0f°C — estimated to hit in ~%.0fh (current: %.1f°C, rate: %+.1f°C/h)",
				tempCritical, result.HoursToHit, result.Current, result.Slope),
		})
	}

	return alerts
}
