package metrics

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"truffels-api/internal/model"
)

type Collector struct {
	procPath string
	sysPath  string
	diskPath string

	// cached CPU stats for delta calculation
	lastCPU     cpuStat
	lastCPUTime time.Time
}

type cpuStat struct {
	user, nice, system, idle, iowait, irq, softirq int64
}

func NewCollector(procPath, sysPath, diskPath string) *Collector {
	c := &Collector{
		procPath: procPath,
		sysPath:  sysPath,
		diskPath: diskPath,
	}
	// Prime the CPU stats
	c.lastCPU, _ = c.readCPUStat()
	c.lastCPUTime = time.Now()
	return c
}

func (c *Collector) Collect() model.HostMetrics {
	m := model.HostMetrics{}

	m.CPUPercent = c.collectCPU()
	c.collectMemory(&m)
	m.Temperature = c.collectTemp()
	m.FanRPM = c.collectFanSpeed()
	m.Disks = c.collectDisk()
	m.UptimeSeconds = c.collectUptime()

	return m
}

func (c *Collector) collectCPU() float64 {
	cur, err := c.readCPUStat()
	if err != nil {
		return 0
	}

	prev := c.lastCPU
	c.lastCPU = cur
	c.lastCPUTime = time.Now()

	totalPrev := prev.user + prev.nice + prev.system + prev.idle + prev.iowait + prev.irq + prev.softirq
	totalCur := cur.user + cur.nice + cur.system + cur.idle + cur.iowait + cur.irq + cur.softirq
	totalDelta := totalCur - totalPrev
	if totalDelta == 0 {
		return 0
	}

	idleDelta := cur.idle - prev.idle
	return float64(totalDelta-idleDelta) / float64(totalDelta) * 100
}

func (c *Collector) readCPUStat() (cpuStat, error) {
	data, err := os.ReadFile(c.procPath + "/stat")
	if err != nil {
		return cpuStat{}, err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 8 {
				return cpuStat{}, fmt.Errorf("unexpected cpu stat format")
			}
			var s cpuStat
			s.user, _ = strconv.ParseInt(fields[1], 10, 64)
			s.nice, _ = strconv.ParseInt(fields[2], 10, 64)
			s.system, _ = strconv.ParseInt(fields[3], 10, 64)
			s.idle, _ = strconv.ParseInt(fields[4], 10, 64)
			s.iowait, _ = strconv.ParseInt(fields[5], 10, 64)
			s.irq, _ = strconv.ParseInt(fields[6], 10, 64)
			s.softirq, _ = strconv.ParseInt(fields[7], 10, 64)
			return s, nil
		}
	}
	return cpuStat{}, fmt.Errorf("cpu line not found")
}

func (c *Collector) collectMemory(m *model.HostMetrics) {
	data, err := os.ReadFile(c.procPath + "/meminfo")
	if err != nil {
		return
	}
	var totalKB, availKB int64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			totalKB = parseMemInfoValue(line)
		} else if strings.HasPrefix(line, "MemAvailable:") {
			availKB = parseMemInfoValue(line)
		}
	}
	m.MemTotalMB = totalKB / 1024
	m.MemUsedMB = (totalKB - availKB) / 1024
	if totalKB > 0 {
		m.MemPercent = float64(totalKB-availKB) / float64(totalKB) * 100
	}
}

func parseMemInfoValue(line string) int64 {
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		v, _ := strconv.ParseInt(fields[1], 10, 64)
		return v
	}
	return 0
}

func (c *Collector) collectTemp() float64 {
	data, err := os.ReadFile(c.sysPath + "/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	return v / 1000.0
}

func (c *Collector) collectFanSpeed() int {
	// hwmon number is not stable across reboots, so glob for it
	base := c.sysPath + "/devices/platform/cooling_fan/hwmon"
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		data, err := os.ReadFile(base + "/" + e.Name() + "/fan1_input")
		if err != nil {
			continue
		}
		v, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return v
	}
	return 0
}

func (c *Collector) collectDisk() []model.DiskUsage {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(c.diskPath, &stat); err != nil {
		return nil
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	availBytes := stat.Bavail * uint64(stat.Bsize)
	usedBytes := totalBytes - (stat.Bfree * uint64(stat.Bsize))

	du := model.DiskUsage{
		Path:    c.diskPath,
		TotalGB: float64(totalBytes) / (1 << 30),
		UsedGB:  float64(usedBytes) / (1 << 30),
		AvailGB: float64(availBytes) / (1 << 30),
	}
	if totalBytes > 0 {
		du.UsedPercent = float64(usedBytes) / float64(totalBytes) * 100
	}
	return []model.DiskUsage{du}
}

func (c *Collector) collectUptime() float64 {
	data, err := os.ReadFile(c.procPath + "/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}
