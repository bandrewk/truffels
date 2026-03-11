package model

type HostMetrics struct {
	CPUPercent    float64     `json:"cpu_percent"`
	MemTotalMB    int64       `json:"mem_total_mb"`
	MemUsedMB     int64       `json:"mem_used_mb"`
	MemPercent    float64     `json:"mem_percent"`
	Temperature   float64     `json:"temperature_c"`
	FanRPM        int         `json:"fan_rpm"`
	FanPercent    int         `json:"fan_percent"`
	Disks         []DiskUsage `json:"disks"`
	UptimeSeconds float64     `json:"uptime_seconds"`
	NetRxBytes    int64       `json:"net_rx_bytes"`
	NetTxBytes    int64       `json:"net_tx_bytes"`
	DiskReadBytes  int64      `json:"disk_read_bytes"`
	DiskWriteBytes int64      `json:"disk_write_bytes"`
	DiskIOPercent  float64    `json:"disk_io_percent"`
}

type DiskUsage struct {
	Path       string  `json:"path"`
	TotalGB    float64 `json:"total_gb"`
	UsedGB     float64 `json:"used_gb"`
	AvailGB    float64 `json:"avail_gb"`
	UsedPercent float64 `json:"used_percent"`
}
