package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestCollectCPU(t *testing.T) {
	dir := t.TempDir()

	// First reading (priming)
	writeTempFile(t, dir, "stat", `cpu  10000 500 3000 50000 200 100 50 0 0 0
cpu0 5000 250 1500 25000 100 50 25 0 0 0
`)
	c := NewCollector(dir, dir, dir)

	// Second reading with increased values
	writeTempFile(t, dir, "stat", `cpu  12000 600 3500 52000 250 120 60 0 0 0
cpu0 6000 300 1750 26000 125 60 30 0 0 0
`)

	cpu := c.collectCPU()
	// Total delta: (12000+600+3500+52000+250+120+60) - (10000+500+3000+50000+200+100+50)
	// = 68530 - 63850 = 4680
	// Idle delta: 52000 - 50000 = 2000
	// CPU%: (4680-2000)/4680 * 100 ≈ 57.26%
	if cpu < 50 || cpu > 65 {
		t.Fatalf("expected CPU ~57%%, got %.1f%%", cpu)
	}
}

func TestCollectCPU_FirstCall(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "stat", `cpu  10000 500 3000 50000 200 100 50 0 0 0
`)
	c := &Collector{procPath: dir, sysPath: dir, diskPath: dir}
	c.lastCPU = cpuStat{} // uninitialized

	// When lastCPU is zero, first call should give 0 or a reasonable value
	// (since totalPrev = 0, totalDelta = totalCur, idleDelta = cur.idle)
	cpu := c.collectCPU()
	if cpu < 0 || cpu > 100 {
		t.Fatalf("CPU out of range: %.1f", cpu)
	}
}

func TestCollectMemory(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "meminfo", `MemTotal:        8053740 kB
MemFree:          345124 kB
MemAvailable:    2026640 kB
Buffers:          456789 kB
`)
	c := &Collector{procPath: dir}
	m := struct {
		MemTotalMB int64
		MemUsedMB  int64
		MemPercent float64
	}{}

	// Using the actual model struct
	c2 := NewCollector(dir, dir, "/tmp")
	metrics := c2.Collect()
	_ = c // suppress unused

	if metrics.MemTotalMB != 8053740/1024 {
		t.Fatalf("expected total %d MB, got %d", 8053740/1024, metrics.MemTotalMB)
	}
	expectedUsed := (8053740 - 2026640) / 1024
	if metrics.MemUsedMB != int64(expectedUsed) {
		t.Fatalf("expected used %d MB, got %d", expectedUsed, metrics.MemUsedMB)
	}
	if metrics.MemPercent < 70 || metrics.MemPercent > 80 {
		t.Fatalf("expected mem ~74.8%%, got %.1f%%", metrics.MemPercent)
	}
	_ = m
}

func TestCollectTemp(t *testing.T) {
	dir := t.TempDir()
	thermalDir := filepath.Join(dir, "class", "thermal", "thermal_zone0")
	_ = os.MkdirAll(thermalDir, 0755)
	_ = os.WriteFile(filepath.Join(thermalDir, "temp"), []byte("52300\n"), 0644)

	c := &Collector{sysPath: dir}
	temp := c.collectTemp()
	if temp < 52.2 || temp > 52.4 {
		t.Fatalf("expected ~52.3, got %.1f", temp)
	}
}

func TestCollectTemp_Missing(t *testing.T) {
	c := &Collector{sysPath: "/nonexistent"}
	temp := c.collectTemp()
	if temp != 0 {
		t.Fatalf("expected 0 for missing temp, got %.1f", temp)
	}
}

func TestCollectUptime(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "uptime", "123456.78 234567.89\n")

	c := &Collector{procPath: dir}
	uptime := c.collectUptime()
	if uptime < 123456 || uptime > 123457 {
		t.Fatalf("expected ~123456.78, got %.2f", uptime)
	}
}

func TestCollectUptime_Missing(t *testing.T) {
	c := &Collector{procPath: "/nonexistent"}
	uptime := c.collectUptime()
	if uptime != 0 {
		t.Fatalf("expected 0 for missing uptime, got %.2f", uptime)
	}
}

func TestParseMemInfoValue(t *testing.T) {
	tests := []struct {
		line string
		want int64
	}{
		{"MemTotal:        8053740 kB", 8053740},
		{"MemAvailable:    2026640 kB", 2026640},
		{"MemFree:", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseMemInfoValue(tt.line)
		if got != tt.want {
			t.Fatalf("parseMemInfoValue(%q) = %d, want %d", tt.line, got, tt.want)
		}
	}
}

func TestReadCPUStat(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "stat", `cpu  10000 500 3000 50000 200 100 50 0 0 0
cpu0 5000 250 1500 25000 100 50 25 0 0 0
intr 123456
`)

	c := &Collector{procPath: dir}
	stat, err := c.readCPUStat()
	if err != nil {
		t.Fatalf("read cpu stat: %v", err)
	}
	if stat.user != 10000 {
		t.Fatalf("expected user 10000, got %d", stat.user)
	}
	if stat.idle != 50000 {
		t.Fatalf("expected idle 50000, got %d", stat.idle)
	}
}

func TestReadCPUStat_Missing(t *testing.T) {
	c := &Collector{procPath: "/nonexistent"}
	_, err := c.readCPUStat()
	if err == nil {
		t.Fatal("expected error for missing stat file")
	}
}

func TestReadCPUStat_NoCPULine(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "stat", "intr 123456\nctxt 789\n")

	c := &Collector{procPath: dir}
	_, err := c.readCPUStat()
	if err == nil {
		t.Fatal("expected error when cpu line not found")
	}
}
