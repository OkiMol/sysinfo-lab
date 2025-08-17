package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"

	"golang.org/x/sys/unix"
)

type DiskInfo struct {
	Mountpoint string
	FSType     string
	Total      uint64
	Free       uint64
}

type SysInfo struct {
	FDCount  int        `json:"fd_count"`
	VmRSS    int        `json:"vmrss_bytes"`
	ExePath  string     `json:"exe_path"`
	CPUModel string     `json:"cpu_model"`
	CPUCores int        `json:"cpu_cores"`
	MemTotal int        `json:"mem_total_kb"`
	Mounts   []DiskInfo `json:"mounts"`
	CgroupV1 *CgroupV1  `json:"cgroup_v1,omitempty"`
}

type CgroupV1 struct {
	MemoryLimitBytes *uint64  `json:"memory_limit_bytes,omitempty"`
	CPULimitCores    *float64 `json:"cpu_limit_cores,omitempty"`
}

func main() {
	var jsonOutput = flag.Bool("json", false, "output in JSON format")
	flag.Parse()
	fds, err := countFDs()
	if err != nil {
		fmt.Println("FDs counting error:\t", err)
		return
	}
	vmrss, err := getRSS()
	if err != nil {
		fmt.Println("VmRRS getting error:\t", err)
		return
	}
	path, err := getBinPath()
	if err != nil {
		fmt.Println("Path getting error:\t", err)
	}
	model, cores, err := getCPUInfo()
	if err != nil {
		fmt.Println("CPU info getting error:\t", err)
	}
	memTotal, err := getMemInfo()
	if err != nil {
		fmt.Println("Mem info getting error:\t", err)
	}
	disks, err := getDisksInfo()
	if err != nil {
		fmt.Println("Disk info getting error:\t", err)
	}
	memLimit, err := readCgroupMemoryLimit()
	if err != nil {
		fmt.Println("cgroup memory limit error:\t", err)
	}
	cpuLimit, err := readCgroupCPULimit()
	if err != nil {
		fmt.Println("cgroup CPU limit error:\t", err)
	}
	info := SysInfo{
		FDCount:  fds,
		VmRSS:    vmrss,
		ExePath:  path,
		CPUModel: model,
		CPUCores: cores,
		MemTotal: memTotal,
		Mounts:   disks,
	}
	info.CgroupV1 = &CgroupV1{
		MemoryLimitBytes: memLimit,
		CPULimitCores:    cpuLimit,
	}

	if *jsonOutput {
		out, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "JSON marshal error:", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "FDs count:\t", fds)
		fmt.Fprintln(w, "VmRSS:\t", vmrss, "B")
		fmt.Fprintln(w, "EXE path:\t", path)
		fmt.Fprintln(w, "CPU model:\t", model)
		fmt.Fprintln(w, "CPU cores:\t", cores)
		fmt.Fprintln(w, "MemTotal:\t", memTotal, "kB")
		if info.CgroupV1 != nil {
			if info.CgroupV1.MemoryLimitBytes == nil {
				fmt.Fprintln(w, "Cgroup (v1) MemLimit:\t", "unlimited")
			} else {
				fmt.Fprintln(w, "Cgroup (v1) MemLimit:\t", humanMB(*info.CgroupV1.MemoryLimitBytes))
			}
			if info.CgroupV1.CPULimitCores == nil {
				fmt.Fprintln(w, "Cgroup (v1) CPULimit:\t", "unlimited")
			} else {
				fmt.Fprintf(w, "Cgroup (v1) CPULimit:\t%.2f cores\n", *info.CgroupV1.CPULimitCores)
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, "Mounts count:\t", len(disks))
		fmt.Fprintln(w)

		fmt.Fprintln(w, "Mount:\tFS:\tTotal:\tFree:")

		for _, d := range disks {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				d.Mountpoint, d.FSType, humanMB(d.Total), humanMB(d.Free))
		}

		w.Flush()
	}
}

func humanMB(b uint64) string { return fmt.Sprintf("%d MB", b/1024/1024) }

func countFDs() (int, error) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

func getRSS() (int, error) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "VmRSS:") {
			var rss int
			fmt.Sscanf(line, "VmRSS: %d kB", &rss)
			return rss, nil
		}
	}
	return 0, fmt.Errorf("VmRSS not found")
}

func getBinPath() (string, error) {
	path, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return "", err
	}
	return path, nil
}

func getCPUInfo() (string, int, error) {
	cpuData, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "", 0, err
	}

	lines := strings.Split(string(cpuData), "\n")
	var model string
	for _, line := range lines {
		if strings.HasPrefix(line, "model name") {
			_, right, found := strings.Cut(line, ":")
			if found {
				model = strings.TrimSpace(right)
				break
			}
		}
	}
	cores := runtime.NumCPU()
	return model, cores, nil
}

func getMemInfo() (int, error) {
	memData, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(memData), "\n")
	var memTotal int
	for _, line := range lines {
		if strings.HasPrefix(line, "MemTotal:") {
			fmt.Sscanf(line, "MemTotal: %d kB", &memTotal)
		}
	}
	return memTotal, nil
}

func getDisksInfo() ([]DiskInfo, error) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, err
	}

	var disks []DiskInfo
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		mountpoint := fields[1]
		fsType := fields[2]

		var stat unix.Statfs_t
		if err := unix.Statfs(mountpoint, &stat); err != nil {
			continue
		}

		if fsType == "proc" || fsType == "sysfs" || fsType == "cgroup" {
			continue
		}

		total := stat.Blocks * uint64(stat.Bsize)
		free := stat.Bfree * uint64(stat.Bsize)

		disks = append(disks, DiskInfo{
			Mountpoint: mountpoint,
			FSType:     fsType,
			Total:      total,
			Free:       free,
		})
	}
	return disks, nil
}

func readTrim(path string) (string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(bytes)), nil
}

func readCgroupMemoryLimit() (*uint64, error) {
	value, err := readTrim("/sys/fs/cgroup/memory/memory.limit_in_bytes")
	if err != nil {
		return nil, err
	}
	num, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil, err
	}
	const unlimitedThreshold = uint64(1<<63) - 4096
	if num >= unlimitedThreshold {
		return nil, nil
	}
	return &num, nil
}

func readCgroupCPULimit() (*float64, error) {
	quotaStr, err := readTrim("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	if err != nil {
		return nil, err
	}
	periodStr, err := readTrim("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if err != nil {
		return nil, err
	}
	if quotaStr == "-1" {
		return nil, nil
	}

	quota, err := strconv.ParseFloat(quotaStr, 64)
	if err != nil {
		return nil, err
	}
	period, err := strconv.ParseFloat(periodStr, 64)
	if err != nil {
		return nil, err
	}
	if period == 0 {
		return nil, fmt.Errorf("cpu.cfs_period_us is zero")
	}

	cores := quota / period
	return &cores, nil
}
