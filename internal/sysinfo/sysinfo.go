// Package sysinfo 提供 CPU 和内存使用情况的读取（仅限 Linux）。
// CPU 使用率通过后台 goroutine 每 2 秒采样一次并缓存，避免每次请求阻塞。
package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stats 系统资源快照。
type Stats struct {
	CPUPercent    float64
	MemTotalBytes int64
	MemUsedBytes  int64
}

var (
	mu        sync.RWMutex
	cachedCPU float64
	startOnce sync.Once
)

type cpuStat struct {
	user, nice, system, idle, iowait, irq, softirq, steal int64
}

func (s cpuStat) total() int64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq + s.steal
}

func (s cpuStat) busy() int64 {
	return s.user + s.nice + s.system + s.irq + s.softirq + s.steal
}

func readCPUStat() (cpuStat, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuStat{}, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			break
		}
		var s cpuStat
		ptrs := []*int64{&s.user, &s.nice, &s.system, &s.idle, &s.iowait, &s.irq, &s.softirq, &s.steal}
		for i, p := range ptrs {
			v, _ := strconv.ParseInt(fields[i+1], 10, 64)
			*p = v
		}
		return s, nil
	}
	return cpuStat{}, nil
}

func sampleCPU() {
	prev, err := readCPUStat()
	if err != nil {
		return
	}
	for {
		time.Sleep(2 * time.Second)
		curr, err := readCPUStat()
		if err != nil {
			continue
		}
		totalDelta := curr.total() - prev.total()
		busyDelta := curr.busy() - prev.busy()
		var pct float64
		if totalDelta > 0 {
			pct = float64(busyDelta) / float64(totalDelta) * 100
		}
		mu.Lock()
		cachedCPU = pct
		mu.Unlock()
		prev = curr
	}
}

// Start 启动后台 CPU 采样 goroutine，重复调用无副作用。
func Start() {
	startOnce.Do(func() { go sampleCPU() })
}

func readMem() (total, used int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var memTotal, memAvailable int64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseInt(fields[1], 10, 64)
		v *= 1024 // kB → bytes
		switch fields[0] {
		case "MemTotal:":
			memTotal = v
		case "MemAvailable:":
			memAvailable = v
		}
	}
	return memTotal, memTotal - memAvailable
}

// Get 返回当前系统统计快照。
func Get() Stats {
	mu.RLock()
	cpu := cachedCPU
	mu.RUnlock()
	total, used := readMem()
	return Stats{
		CPUPercent:    cpu,
		MemTotalBytes: total,
		MemUsedBytes:  used,
	}
}
