package system

import (
	"runtime"
	"testing"
)

func TestRealSystemInfo(t *testing.T) {
	info := NewRealSystemInfo()

	// Test Hostname
	hostname := info.Hostname()
	if hostname == "" {
		t.Error("Hostname should not be empty")
	}

	// Test Platform
	platform := info.Platform()
	if platform != runtime.GOOS {
		t.Errorf("Platform mismatch: got %q, want %q", platform, runtime.GOOS)
	}

	// Test Arch
	arch := info.Arch()
	if arch == "" {
		t.Error("Arch should not be empty")
	}

	// Test Type
	osType := info.Type()
	if osType == "" {
		t.Error("Type should not be empty")
	}

	// Test CPUs
	cpus := info.CPUs()
	if len(cpus) == 0 {
		t.Error("CPUs should not be empty")
	}

	// Test TotalMem
	totalMem := info.TotalMem()
	if totalMem == 0 {
		t.Error("TotalMem should not be 0")
	}

	// Test FreeMem
	freeMem := info.FreeMem()
	if freeMem > totalMem {
		t.Error("FreeMem should not exceed TotalMem")
	}

	// Test HomeDir
	homeDir := info.HomeDir()
	if homeDir == "" {
		t.Error("HomeDir should not be empty")
	}

	// Test TmpDir
	tmpDir := info.TmpDir()
	if tmpDir == "" {
		t.Error("TmpDir should not be empty")
	}

	// Test Uptime
	uptime := info.Uptime()
	if uptime < 0 {
		t.Error("Uptime should not be negative")
	}

	// Test LoadAvg
	loadAvg := info.LoadAvg()
	if len(loadAvg) != 3 {
		t.Errorf("LoadAvg should have 3 values, got %d", len(loadAvg))
	}

	// Test UserInfo
	userInfo, err := info.UserInfo()
	if err != nil {
		t.Errorf("UserInfo failed: %v", err)
	}
	if userInfo.Username == "" {
		t.Error("UserInfo.Username should not be empty")
	}

	// Test EOL
	eol := info.EOL()
	if eol == "" {
		t.Error("EOL should not be empty")
	}

	// Test DevNull
	devNull := info.DevNull()
	if devNull == "" {
		t.Error("DevNull should not be empty")
	}
}

func TestSandboxedSystemInfo(t *testing.T) {
	sandboxed := NewSandboxedSystemInfo(nil) // Use default config

	if sandboxed.Hostname() != "sandbox" {
		t.Errorf("Sandboxed hostname should be 'sandbox', got %q", sandboxed.Hostname())
	}

	if sandboxed.Platform() != "linux" {
		t.Errorf("Sandboxed platform should be 'linux', got %q", sandboxed.Platform())
	}

	if sandboxed.Arch() != "x64" {
		t.Errorf("Sandboxed arch should be 'x64', got %q", sandboxed.Arch())
	}

	if sandboxed.Type() != "Linux" {
		t.Errorf("Sandboxed type should be 'Linux', got %q", sandboxed.Type())
	}

	// CPUs should have count from config (default 2)
	cpus := sandboxed.CPUs()
	if len(cpus) != 2 {
		t.Errorf("Sandboxed CPUs should have 2 cores, got %d", len(cpus))
	}

	// UserInfo should be sandboxed
	userInfo, err := sandboxed.UserInfo()
	if err != nil {
		t.Errorf("UserInfo failed: %v", err)
	}
	if userInfo.Username != "sandbox" {
		t.Errorf("Sandboxed UserInfo.Username should be 'sandbox', got %q", userInfo.Username)
	}
}

func TestSandboxedSystemInfo_CustomConfig(t *testing.T) {
	cfg := &SandboxConfig{
		Hostname: "custom-host",
		Platform: "darwin",
		Arch:     "arm64",
		CPUCount: 8,
		TotalMem: 16 * 1024 * 1024 * 1024,
		Username: "customuser",
	}
	sandboxed := NewSandboxedSystemInfo(cfg)

	if sandboxed.Hostname() != "custom-host" {
		t.Errorf("Custom hostname mismatch: got %q", sandboxed.Hostname())
	}

	if sandboxed.Platform() != "darwin" {
		t.Errorf("Custom platform mismatch: got %q", sandboxed.Platform())
	}

	cpus := sandboxed.CPUs()
	if len(cpus) != 8 {
		t.Errorf("Custom CPU count mismatch: got %d", len(cpus))
	}

	if sandboxed.TotalMem() != 16*1024*1024*1024 {
		t.Errorf("Custom TotalMem mismatch: got %d", sandboxed.TotalMem())
	}
}

func TestCPUInfo(t *testing.T) {
	cpu := CPUInfo{
		Model: "Intel Core i7",
		Speed: 2800,
		Times: CPUTimes{
			User: 1000,
			Nice: 100,
			Sys:  500,
			Idle: 8000,
			IRQ:  50,
		},
	}

	if cpu.Model != "Intel Core i7" {
		t.Errorf("CPU Model mismatch: got %q", cpu.Model)
	}
	if cpu.Speed != 2800 {
		t.Errorf("CPU Speed mismatch: got %d", cpu.Speed)
	}
	if cpu.Times.User != 1000 {
		t.Errorf("CPU Times.User mismatch: got %d", cpu.Times.User)
	}
}

func TestUserInfo(t *testing.T) {
	user := UserInfo{
		UID:      "1000",
		GID:      "1000",
		Username: "testuser",
		HomeDir:  "/home/testuser",
		Shell:    "/bin/bash",
	}

	if user.UID != "1000" {
		t.Errorf("UserInfo UID mismatch: got %q", user.UID)
	}
	if user.Username != "testuser" {
		t.Errorf("UserInfo Username mismatch: got %q", user.Username)
	}
}

func TestNetworkInterface(t *testing.T) {
	iface := NetworkInterface{
		Address:  "192.168.1.100",
		Netmask:  "255.255.255.0",
		Family:   "IPv4",
		MAC:      "00:11:22:33:44:55",
		Internal: false,
	}

	if iface.Address != "192.168.1.100" {
		t.Errorf("NetworkInterface Address mismatch: got %q", iface.Address)
	}
	if iface.Family != "IPv4" {
		t.Errorf("NetworkInterface Family mismatch: got %q", iface.Family)
	}
}

func TestDefaultSandboxConfig(t *testing.T) {
	cfg := DefaultSandboxConfig()

	if cfg.Hostname != "sandbox" {
		t.Errorf("Default hostname should be 'sandbox', got %q", cfg.Hostname)
	}
	if cfg.Platform != "linux" {
		t.Errorf("Default platform should be 'linux', got %q", cfg.Platform)
	}
	if cfg.CPUCount < 1 {
		t.Errorf("Default CPU count should be >= 1, got %d", cfg.CPUCount)
	}
	if cfg.TotalMem == 0 {
		t.Error("Default TotalMem should not be 0")
	}
}
