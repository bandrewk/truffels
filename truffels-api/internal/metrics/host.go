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

	// cached disk I/O stats for delta calculation
	lastDiskIO     diskIOStat
	lastDiskIOTime time.Time
}

type cpuStat struct {
	user, nice, system, idle, iowait, irq, softirq int64
}

type diskIOStat struct {
	sectorsRead  int64
	sectorsWrite int64
	ioMs         int64 // ms spent doing I/O
}

func NewCollector(procPath, sysPath, diskPath string) *Collector {
	c := &Collector{
		procPath: procPath,
		sysPath:  sysPath,
		diskPath: diskPath,
	}
	// Prime the CPU and disk I/O stats
	c.lastCPU, _ = c.readCPUStat()
	c.lastDiskIO, _ = c.readDiskIOStat()
	now := time.Now()
	c.lastCPUTime = now
	c.lastDiskIOTime = now
	return c
}

func (c *Collector) Collect() model.HostMetrics {
	m := model.HostMetrics{}

	m.CPUPercent = c.collectCPU()
	c.collectMemory(&m)
	m.Temperature = c.collectTemp()
	m.FanRPM, m.FanPercent = c.collectFan()
	m.Disks = c.collectDisk()
	m.UptimeSeconds = c.collectUptime()
	m.NetRxBytes, m.NetTxBytes = c.collectNetIO()
	m.DiskReadBytes, m.DiskWriteBytes, m.DiskIOPercent = c.collectDiskIO()

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

func (c *Collector) collectFan() (rpm int, percent int) {
	// hwmon number is not stable across reboots, so scan for it
	base := c.sysPath + "/devices/platform/cooling_fan/hwmon"
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		dir := base + "/" + e.Name()
		if data, err := os.ReadFile(dir + "/fan1_input"); err == nil {
			rpm, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if data, err := os.ReadFile(dir + "/pwm1"); err == nil {
			pwm, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			percent = pwm * 100 / 255
		}
		if rpm > 0 {
			return rpm, percent
		}
	}
	return 0, 0
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

// collectNetIO reads cumulative rx/tx bytes from /proc/net/dev.
// Returns total across all physical interfaces (excludes lo, docker, veth, br-).
func (c *Collector) collectNetIO() (rxBytes, txBytes int64) {
	data, err := os.ReadFile(c.procPath + "/net/dev")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// Skip header lines and virtual interfaces
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" || strings.HasPrefix(iface, "docker") ||
			strings.HasPrefix(iface, "veth") || strings.HasPrefix(iface, "br-") {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 10 {
			continue
		}
		rx, _ := strconv.ParseInt(fields[0], 10, 64)
		tx, _ := strconv.ParseInt(fields[8], 10, 64)
		rxBytes += rx
		txBytes += tx
	}
	return rxBytes, txBytes
}

// readDiskIOStat reads NVMe disk I/O counters from /proc/diskstats.
func (c *Collector) readDiskIOStat() (diskIOStat, error) {
	data, err := os.ReadFile(c.procPath + "/diskstats")
	if err != nil {
		return diskIOStat{}, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		// diskstats format: major minor name reads_completed reads_merged sectors_read ms_reading
		//                   writes_completed writes_merged sectors_written ms_writing ios_in_progress ms_doing_io ...
		if len(fields) < 14 {
			continue
		}
		name := fields[2]
		// Match the whole disk device, not partitions
		if name != "nvme0n1" && name != "sda" && name != "mmcblk0" {
			continue
		}
		var s diskIOStat
		s.sectorsRead, _ = strconv.ParseInt(fields[5], 10, 64)
		s.sectorsWrite, _ = strconv.ParseInt(fields[9], 10, 64)
		s.ioMs, _ = strconv.ParseInt(fields[12], 10, 64)
		return s, nil
	}
	return diskIOStat{}, fmt.Errorf("disk device not found in diskstats")
}

// collectDiskIO returns cumulative read/write bytes and I/O utilization %.
func (c *Collector) collectDiskIO() (readBytes, writeBytes int64, ioPercent float64) {
	cur, err := c.readDiskIOStat()
	if err != nil {
		return 0, 0, 0
	}

	prev := c.lastDiskIO
	elapsed := time.Since(c.lastDiskIOTime)
	c.lastDiskIO = cur
	c.lastDiskIOTime = time.Now()

	// Cumulative bytes (sector = 512 bytes)
	readBytes = cur.sectorsRead * 512
	writeBytes = cur.sectorsWrite * 512

	// I/O utilization: delta ioMs / delta wall time
	if elapsed.Milliseconds() > 0 {
		deltaIO := cur.ioMs - prev.ioMs
		ioPercent = float64(deltaIO) / float64(elapsed.Milliseconds()) * 100
		if ioPercent > 100 {
			ioPercent = 100
		}
		if ioPercent < 0 {
			ioPercent = 0
		}
	}

	return readBytes, writeBytes, ioPercent
}
