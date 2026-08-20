package admission

import "fmt"

// Input holds the data needed to evaluate an admission decision.
type Input struct {
	RunningP95MB   []int
	NewFloorMB     int
	PhysicalRAMMB  int
	ReserveMB      int
	NewMinDiskGB   int
	FreeDiskGB     int
	GrowthBufferGB int
}

// Decision is the result of an admission check.
type Decision struct {
	Allowed        bool   `json:"allowed"`
	Reason         string `json:"reason"`
	RequiredRAMMB  int    `json:"required_ram_mb"`
	UsableRAMMB    int    `json:"usable_ram_mb"`
	RequiredDiskGB int    `json:"required_disk_gb"`
	FreeDiskGB     int    `json:"free_disk_gb"`
}

// Check evaluates whether a new container can be admitted based on
// RAM and disk capacity. It is a pure function with no external
// dependencies.
func Check(in Input) Decision {
	usable := in.PhysicalRAMMB - in.ReserveMB
	required := in.NewFloorMB
	for _, v := range in.RunningP95MB {
		required += v
	}
	requiredDisk := in.NewMinDiskGB + in.GrowthBufferGB

	d := Decision{
		RequiredRAMMB:  required,
		UsableRAMMB:    usable,
		RequiredDiskGB: requiredDisk,
		FreeDiskGB:     in.FreeDiskGB,
	}

	if required > usable {
		d.Allowed = false
		d.Reason = fmt.Sprintf("needs ~%d MB RAM, %d MB usable", required, usable)
		return d
	}

	if requiredDisk > in.FreeDiskGB {
		d.Allowed = false
		d.Reason = fmt.Sprintf("needs ~%d GB disk, %d GB free", requiredDisk, in.FreeDiskGB)
		return d
	}

	d.Allowed = true
	d.Reason = "ok"
	return d
}
