package runtime

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ResourceSnapshot 是服务器宿主进程用量的某一时点视图。每个账号都在进程内服务，
// 所以服务器进程是网关拥有的唯一进程。CPU 百分比遵循 ps/top 惯例：
// 100 == 一个核心完全繁忙。
type ResourceSnapshot struct {
	SampledAt time.Time        `json:"sampled_at"`
	Server    ProcessResources `json:"server"`
}

type ProcessResources struct {
	PID        int     `json:"pid"`
	RSSBytes   int64   `json:"rss_bytes"`
	HeapBytes  int64   `json:"heap_bytes,omitempty"`
	Goroutines int     `json:"goroutines,omitempty"`
	CPUPercent float64 `json:"cpu_percent"`
}

// Resources 返回最近的资源快照。维护循环每个 tick 刷新它；如果它从未运行过
// （测试、极简嵌入），则同步采一次样。
func (m *Manager) Resources() ResourceSnapshot {
	if snap := m.resources.Load(); snap != nil {
		return *snap
	}
	return m.sampleResources()
}

// sampleResources 采集一份新鲜快照并发布它。并发调用方会被串行化；
// 第二个调用方复用这份新鲜快照。
func (m *Manager) sampleResources() ResourceSnapshot {
	m.resMu.Lock()
	defer m.resMu.Unlock()
	if snap := m.resources.Load(); snap != nil && time.Since(snap.SampledAt) < time.Second {
		return *snap
	}
	snap := collectResources(&m.cpuPrev)
	m.resources.Store(&snap)
	return snap
}

// procTime 是用于推导 CPU 百分比的上一次 utime+stime 采样。
type procTime struct {
	jiffies uint64
	at      time.Time
}

func collectResources(prev *map[int]procTime) ResourceSnapshot {
	now := time.Now().UTC()
	snap := ResourceSnapshot{SampledAt: now}

	selfPID := os.Getpid()
	pids := []int{selfPID}

	stats := readProcStats(pids)
	if stats == nil {
		stats = readPSStats(pids)
	}

	next := map[int]procTime{}
	for pid, st := range stats {
		cpu := st.cpuPercent
		if st.jiffies > 0 {
			if p, ok := (*prev)[pid]; ok {
				if dt := now.Sub(p.at).Seconds(); dt > 0 {
					cpu = float64(st.jiffies-p.jiffies) / clkTck() / dt * 100
				}
			}
			next[pid] = procTime{jiffies: st.jiffies, at: now}
		}
		if pid == selfPID {
			snap.Server = ProcessResources{PID: pid, RSSBytes: st.rssBytes, CPUPercent: round2(cpu)}
		}
	}
	*prev = next

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if snap.Server.PID == 0 {
		snap.Server.PID = selfPID
	}
	snap.Server.HeapBytes = int64(ms.HeapAlloc)
	snap.Server.Goroutines = runtime.NumGoroutine()
	return snap
}

type procStat struct {
	rssBytes   int64
	jiffies    uint64  // utime+stime；当 CPU 直接来自 ps 时为 0
	cpuPercent float64 // 由 ps 路径填充
}

// readProcStats 在 Linux 上读取 /proc/<pid>/stat + status。当 procfs 不可用时
// （macOS 等）返回 nil，使调用方能回退到 ps。
func readProcStats(pids []int) map[int]procStat {
	if runtime.GOOS != "linux" {
		return nil
	}
	out := map[int]procStat{}
	for _, pid := range pids {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			continue
		}
		line := string(raw)
		// comm 在括号内，可能包含空格；字段位于 ')' 之后。
		end := strings.LastIndex(line, ")")
		if end < 0 {
			continue
		}
		fields := strings.Fields(line[end+1:])
		// fields[0] 是 state；相对于 state，utime=11、stime=12、rss(页数)=21。
		if len(fields) < 22 {
			continue
		}
		utime, _ := strconv.ParseUint(fields[11], 10, 64)
		stime, _ := strconv.ParseUint(fields[12], 10, 64)
		pages, _ := strconv.ParseInt(fields[21], 10, 64)
		out[pid] = procStat{
			jiffies:  utime + stime,
			rssBytes: pages * int64(os.Getpagesize()),
		}
	}
	return out
}

// readPSStats 调用外部 ps 获取 RSS（KB）和生命周期 CPU 百分比。用于非 Linux
// 主机，并作为 /proc 读取失败时的回退。
func readPSStats(pids []int) map[int]procStat {
	args := make([]string, 0, len(pids))
	for _, pid := range pids {
		args = append(args, strconv.Itoa(pid))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "pid=,rss=,%cpu=", "-p", strings.Join(args, ",")).Output()
	if err != nil {
		return nil
	}
	stats := map[int]procStat{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		rssKB, _ := strconv.ParseInt(f[1], 10, 64)
		cpu, _ := strconv.ParseFloat(f[2], 64)
		if pid > 0 {
			stats[pid] = procStat{rssBytes: rssKB * 1024, cpuPercent: cpu}
		}
	}
	return stats
}

var (
	clkTckOnce sync.Once
	clkTckVal  float64
)

// clkTck 返回 USER_HZ/CLK_TCK（通常为 100），用于 jiffies->秒 的换算。
func clkTck() float64 {
	clkTckOnce.Do(func() {
		clkTckVal = 100
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "getconf", "CLK_TCK").Output(); err == nil {
			if v, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); err == nil && v > 0 {
				clkTckVal = v
			}
		}
	})
	return clkTckVal
}

func round2(v float64) float64 {
	if v < 0 {
		return 0
	}
	return float64(int(v*100+0.5)) / 100
}
