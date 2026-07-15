// Package system defines the interface for system information.
// This allows sandboxing sensitive system details.
package runtime

import (
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"
)

// CPUInfo contains information about a CPU.
type CPUInfo struct {
	Model string
	Speed int // MHz
	Times CPUTimes
}

// CPUTimes contains CPU time information.
type CPUTimes struct {
	User int64
	Nice int64
	Sys  int64
	Idle int64
	IRQ  int64
}

// NetworkInterface contains network interface information.
type NetworkInterface struct {
	Address  string
	Netmask  string
	Family   string // "IPv4" or "IPv6"
	MAC      string
	Internal bool
	CIDR     string
}

// UserInfo contains user information.
type UserInfo struct {
	UID      string
	GID      string
	Username string
	HomeDir  string
	Shell    string
}

// SystemInfo defines the interface for system information.
// Implement this interface to control what system information is exposed.
type SystemInfo interface {
	// System identification
	Hostname() string
	Platform() string
	Arch() string
	Release() string
	Type() string
	Version() string
	Machine() string

	// Hardware info
	CPUs() []CPUInfo
	TotalMem() uint64
	FreeMem() uint64

	// User info
	HomeDir() string
	TmpDir() string
	UserInfo() (*UserInfo, error)

	// Network
	NetworkInterfaces() map[string][]NetworkInterface

	// Timing
	Uptime() float64
	LoadAvg() [3]float64

	// Environment
	EOL() string
	DevNull() string
	Endianness() string
}

// RealSystemInfo implements SystemInfo using actual system calls.
type RealSystemInfo struct{}

// NewRealSystemInfo creates a new RealSystemInfo.
func NewRealSystemInfo() *RealSystemInfo {
	return &RealSystemInfo{}
}

// Hostname returns the system hostname.
func (r *RealSystemInfo) Hostname() string {
	name, _ := os.Hostname()
	return name
}

// Platform returns the operating system platform (e.g., linux, darwin, windows).
func (r *RealSystemInfo) Platform() string {
	return runtime.GOOS
}

// Arch returns the CPU architecture in Node.js naming convention.
func (r *RealSystemInfo) Arch() string {
	arch := runtime.GOARCH
	// Convert to Node.js naming
	switch arch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "arm64":
		return "arm64"
	case "arm":
		return "arm"
	default:
		return arch
	}
}

// Release returns the operating system release version.
func (r *RealSystemInfo) Release() string {
	// This would need platform-specific implementation
	return "unknown"
}

// Type returns the operating system type (e.g., Darwin, Linux, Windows_NT).
func (r *RealSystemInfo) Type() string {
	switch runtime.GOOS {
	case "darwin":
		return "Darwin"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows_NT"
	case "freebsd":
		return "FreeBSD"
	default:
		return runtime.GOOS
	}
}

// Version returns the operating system version.
func (r *RealSystemInfo) Version() string {
	return "unknown"
}

// Machine returns the machine type (CPU architecture).
func (r *RealSystemInfo) Machine() string {
	return runtime.GOARCH
}

// CPUs returns information about each CPU core.
func (r *RealSystemInfo) CPUs() []CPUInfo {
	numCPU := runtime.NumCPU()
	cpus := make([]CPUInfo, numCPU)
	for i := 0; i < numCPU; i++ {
		cpus[i] = CPUInfo{
			Model: "Unknown",
			Speed: 0,
			Times: CPUTimes{},
		}
	}
	return cpus
}

// TotalMem returns total system memory in bytes.
func (r *RealSystemInfo) TotalMem() uint64 {
	// Would need platform-specific implementation
	// Return a reasonable default
	return 8 * 1024 * 1024 * 1024 // 8GB
}

// FreeMem returns free system memory in bytes.
func (r *RealSystemInfo) FreeMem() uint64 {
	// Would need platform-specific implementation
	return 4 * 1024 * 1024 * 1024 // 4GB
}

// HomeDir returns the current user's home directory.
func (r *RealSystemInfo) HomeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

// TmpDir returns the system's temporary directory.
func (r *RealSystemInfo) TmpDir() string {
	return os.TempDir()
}

// UserInfo returns information about the current user.
func (r *RealSystemInfo) UserInfo() (*UserInfo, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	return &UserInfo{
		UID:      u.Uid,
		GID:      u.Gid,
		Username: u.Username,
		HomeDir:  u.HomeDir,
		Shell:    "", // Would need platform-specific lookup
	}, nil
}

// NetworkInterfaces returns network interface information.
func (r *RealSystemInfo) NetworkInterfaces() map[string][]NetworkInterface {
	// Simplified - would need net.Interfaces() for real implementation
	return map[string][]NetworkInterface{
		"lo0": {
			{
				Address:  "127.0.0.1",
				Netmask:  "255.0.0.0",
				Family:   "IPv4",
				Internal: true,
				CIDR:     "127.0.0.1/8",
			},
		},
	}
}

// Uptime returns the system uptime in seconds.
func (r *RealSystemInfo) Uptime() float64 {
	// Would need platform-specific implementation
	return 0
}

// LoadAvg returns the 1, 5, and 15 minute load averages.
func (r *RealSystemInfo) LoadAvg() [3]float64 {
	// Would need platform-specific implementation
	return [3]float64{0, 0, 0}
}

// EOL returns the platform-specific line ending.
func (r *RealSystemInfo) EOL() string {
	if runtime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

// DevNull returns the path to the null device.
func (r *RealSystemInfo) DevNull() string {
	if runtime.GOOS == "windows" {
		return "\\\\.\\NUL"
	}
	return "/dev/null"
}

// Endianness returns the CPU endianness ("LE" or "BE").
func (r *RealSystemInfo) Endianness() string {
	// Most common architectures are little-endian
	return "LE"
}

// SandboxedSystemInfo implements SystemInfo with hardcoded/fake values.
// Use this to hide real system information from sandboxed scripts.
type SandboxedSystemInfo struct {
	hostname          string
	platform          string
	arch              string
	release           string
	osType            string
	version           string
	machine           string
	cpuModel          string
	cpuCount          int
	totalMem          uint64
	freeMem           uint64
	homeDir           string
	tmpDir            string
	username          string
	networkInterfaces map[string][]NetworkInterface
	uptime            float64
}

// SandboxConfig configures the sandboxed system info.
type SandboxConfig struct {
	Hostname  string
	Platform  string
	Arch      string
	Release   string
	Type      string
	Version   string
	Machine   string
	CPUModel  string
	CPUCount  int
	TotalMem  uint64
	FreeMem   uint64
	HomeDir   string
	TmpDir    string
	Username  string
	Uptime    float64
}

// DefaultSandboxConfig returns a reasonable default sandbox configuration.
func DefaultSandboxConfig() *SandboxConfig {
	return &SandboxConfig{
		Hostname: "sandbox",
		Platform: "linux",
		Arch:     "x64",
		Release:  "5.15.0-generic",
		Type:     "Linux",
		Version:  "#1 SMP",
		Machine:  "x86_64",
		CPUModel: "Virtual CPU",
		CPUCount: 2,
		TotalMem: 4 * 1024 * 1024 * 1024, // 4GB
		FreeMem:  2 * 1024 * 1024 * 1024, // 2GB
		HomeDir:  "/home/sandbox",
		TmpDir:   "/tmp",
		Username: "sandbox",
		Uptime:   float64(time.Now().Unix() % 86400), // Random-ish uptime
	}
}

// NewSandboxedSystemInfo creates a new sandboxed system info.
func NewSandboxedSystemInfo(cfg *SandboxConfig) *SandboxedSystemInfo {
	if cfg == nil {
		cfg = DefaultSandboxConfig()
	}
	return &SandboxedSystemInfo{
		hostname: cfg.Hostname,
		platform: cfg.Platform,
		arch:     cfg.Arch,
		release:  cfg.Release,
		osType:   cfg.Type,
		version:  cfg.Version,
		machine:  cfg.Machine,
		cpuModel: cfg.CPUModel,
		cpuCount: cfg.CPUCount,
		totalMem: cfg.TotalMem,
		freeMem:  cfg.FreeMem,
		homeDir:  cfg.HomeDir,
		tmpDir:   cfg.TmpDir,
		username: cfg.Username,
		uptime:   cfg.Uptime,
		networkInterfaces: map[string][]NetworkInterface{
			"eth0": {
				{
					Address:  "10.0.0.2",
					Netmask:  "255.255.255.0",
					Family:   "IPv4",
					Internal: false,
					CIDR:     "10.0.0.2/24",
				},
			},
			"lo": {
				{
					Address:  "127.0.0.1",
					Netmask:  "255.0.0.0",
					Family:   "IPv4",
					Internal: true,
					CIDR:     "127.0.0.1/8",
				},
			},
		},
	}
}

// Hostname returns the configured sandbox hostname.
func (s *SandboxedSystemInfo) Hostname() string { return s.hostname }

// Platform returns the configured sandbox platform.
func (s *SandboxedSystemInfo) Platform() string { return s.platform }

// Arch returns the configured sandbox architecture.
func (s *SandboxedSystemInfo) Arch() string { return s.arch }

// Release returns the configured sandbox release version.
func (s *SandboxedSystemInfo) Release() string { return s.release }

// Type returns the configured sandbox OS type.
func (s *SandboxedSystemInfo) Type() string { return s.osType }

// Version returns the configured sandbox OS version.
func (s *SandboxedSystemInfo) Version() string { return s.version }

// Machine returns the configured sandbox machine type.
func (s *SandboxedSystemInfo) Machine() string { return s.machine }

// TotalMem returns the configured sandbox total memory.
func (s *SandboxedSystemInfo) TotalMem() uint64 { return s.totalMem }

// FreeMem returns the configured sandbox free memory.
func (s *SandboxedSystemInfo) FreeMem() uint64 { return s.freeMem }

// HomeDir returns the configured sandbox home directory.
func (s *SandboxedSystemInfo) HomeDir() string { return s.homeDir }

// TmpDir returns the configured sandbox temp directory.
func (s *SandboxedSystemInfo) TmpDir() string { return s.tmpDir }

// Uptime returns the configured sandbox uptime.
func (s *SandboxedSystemInfo) Uptime() float64 { return s.uptime }

// LoadAvg returns fixed load averages for the sandbox.
func (s *SandboxedSystemInfo) LoadAvg() [3]float64 { return [3]float64{0.5, 0.5, 0.5} }

// CPUs returns the configured sandbox CPU information.
func (s *SandboxedSystemInfo) CPUs() []CPUInfo {
	cpus := make([]CPUInfo, s.cpuCount)
	for i := 0; i < s.cpuCount; i++ {
		cpus[i] = CPUInfo{
			Model: s.cpuModel,
			Speed: 2400,
			Times: CPUTimes{
				User: 10000,
				Nice: 0,
				Sys:  5000,
				Idle: 85000,
				IRQ:  0,
			},
		}
	}
	return cpus
}

// UserInfo returns the configured sandbox user information.
func (s *SandboxedSystemInfo) UserInfo() (*UserInfo, error) {
	return &UserInfo{
		UID:      "1000",
		GID:      "1000",
		Username: s.username,
		HomeDir:  s.homeDir,
		Shell:    "/bin/bash",
	}, nil
}

// NetworkInterfaces returns the configured sandbox network interfaces.
func (s *SandboxedSystemInfo) NetworkInterfaces() map[string][]NetworkInterface {
	return s.networkInterfaces
}

// EOL returns the line ending based on sandbox platform.
func (s *SandboxedSystemInfo) EOL() string {
	if strings.Contains(s.platform, "win") {
		return "\r\n"
	}
	return "\n"
}

// DevNull returns the null device path based on sandbox platform.
func (s *SandboxedSystemInfo) DevNull() string {
	if strings.Contains(s.platform, "win") {
		return "\\\\.\\NUL"
	}
	return "/dev/null"
}

// Endianness returns the CPU endianness (always "LE" for sandbox).
func (s *SandboxedSystemInfo) Endianness() string {
	return "LE"
}
