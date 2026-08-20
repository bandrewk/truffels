package admission

import "testing"

func TestCheck_Allowed(t *testing.T) {
	in := Input{
		RunningP95MB:   []int{100, 200},
		NewFloorMB:     50,
		PhysicalRAMMB:  1024,
		ReserveMB:      128,
		NewMinDiskGB:   10,
		FreeDiskGB:     50,
		GrowthBufferGB: 5,
	}
	d := Check(in)
	if !d.Allowed {
		t.Fatalf("expected Allowed=true, got false (reason: %s)", d.Reason)
	}
	if d.RequiredRAMMB != 350 {
		t.Errorf("expected RequiredRAMMB=350, got %d", d.RequiredRAMMB)
	}
	if d.UsableRAMMB != 896 {
		t.Errorf("expected UsableRAMMB=896, got %d", d.UsableRAMMB)
	}
	if d.RequiredDiskGB != 15 {
		t.Errorf("expected RequiredDiskGB=15, got %d", d.RequiredDiskGB)
	}
	if d.FreeDiskGB != 50 {
		t.Errorf("expected FreeDiskGB=50, got %d", d.FreeDiskGB)
	}
}

func TestCheck_RAMTooLow(t *testing.T) {
	in := Input{
		RunningP95MB:   []int{500, 600},
		NewFloorMB:     100,
		PhysicalRAMMB:  1024,
		ReserveMB:      128,
		NewMinDiskGB:   10,
		FreeDiskGB:     50,
		GrowthBufferGB: 5,
	}
	d := Check(in)
	if d.Allowed {
		t.Fatal("expected Allowed=false, got true")
	}
	if d.RequiredRAMMB != 1200 {
		t.Errorf("expected RequiredRAMMB=1200, got %d", d.RequiredRAMMB)
	}
	if d.UsableRAMMB != 896 {
		t.Errorf("expected UsableRAMMB=896, got %d", d.UsableRAMMB)
	}
	if d.Reason == "" {
		t.Error("expected non-empty Reason")
	}
}

func TestCheck_DiskTooLow(t *testing.T) {
	in := Input{
		RunningP95MB:   []int{100},
		NewFloorMB:     50,
		PhysicalRAMMB:  1024,
		ReserveMB:      128,
		NewMinDiskGB:   100,
		FreeDiskGB:     50,
		GrowthBufferGB: 10,
	}
	d := Check(in)
	if d.Allowed {
		t.Fatal("expected Allowed=false, got true")
	}
	if d.RequiredDiskGB != 110 {
		t.Errorf("expected RequiredDiskGB=110, got %d", d.RequiredDiskGB)
	}
	if d.FreeDiskGB != 50 {
		t.Errorf("expected FreeDiskGB=50, got %d", d.FreeDiskGB)
	}
	if d.Reason == "" {
		t.Error("expected non-empty Reason")
	}
}
