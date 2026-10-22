package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/ on the default mux
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// clockTicksPerSecond is the Linux CLK_TCK value on x86/ARM, used to convert
// /proc/self/stat CPU ticks into seconds.
const clockTicksPerSecond = 100.0

const (
	// How often we take a stats sample...
	sampleInterval = time.Second
	// ...and how many we keep. 300 samples at 1/second = 5 minutes
	// of history for the dashboard graphs.
	maxSamples = 300
)

var monitorStart = time.Now()

//
// Stats history
//
// A background goroutine samples the process stats once per second and
// appends them to a slice (dropping the oldest when full). This is what
// makes graphs possible: /debug/stats only knows "now", but a graph
// needs "the last N minutes".
//

// sample is one point in time of the server's resource usage.
type sample struct {
	Time       int64   `json:"time"`       // unix seconds
	CPUPercent float64 `json:"cpu"`        // % of one core (100 = one full core)
	Goroutines int     `json:"goroutines"` // active goroutines
	Threads    int     `json:"threads"`    // OS threads
	RSSBytes   uint64  `json:"rss"`        // RAM the OS says we hold
	HeapAlloc  uint64  `json:"heap_alloc"` // live Go heap (actual objects)
	HeapSys    uint64  `json:"heap_sys"`   // heap the runtime reserved from the OS
	GCCycles   uint32  `json:"gc_cycles"`  // completed GC cycles since start
	GCCPU      float64 `json:"gc_cpu"`     // % of total CPU spent in GC since start
}

var (
	statsMu      sync.RWMutex
	samples      []sample
	lastCPUTicks uint64
	lastTickTime time.Time
)

// StartMonitor serves Go runtime metrics, the dashboard and pprof on addr,
// separate from the hand-rolled HTTP server. Endpoints:
//
//	/dashboard        graphs of the last 5 minutes + benchmark button
//	/debug/stats      plain-text current stats
//	/debug/history    JSON array of recent samples (used by the dashboard)
//	/debug/benchmark  POST: runs `go run ./benchmark` and returns its output
//	/debug/pprof/     standard pprof profiles (heap, goroutine, cpu, ...)
func StartMonitor(addr string) {
	// Take a first sample right away so there is always data to show,
	// then keep sampling in the background.
	collectSample()

	go func() {
		for range time.Tick(sampleInterval) {
			collectSample()
		}
	}()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})
	http.HandleFunc("/dashboard", handleDashboard)
	http.HandleFunc("/debug/stats", handleStats)
	http.HandleFunc("/debug/history", handleHistory)
	http.HandleFunc("/debug/benchmark", handleRunBenchmark)

	go func() {
		log.Printf("monitor: dashboard at http://%s/dashboard", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("monitor: %v", err)
		}
	}()
}

// collectSample reads the current stats and appends them to the history.
func collectSample() {
	ticks, threads, rss := procStat()
	now := time.Now()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	statsMu.Lock()
	defer statsMu.Unlock()

	// CPU% = CPU time used since the previous sample / wall time elapsed.
	cpuPercent := 0.0
	if !lastTickTime.IsZero() && ticks >= lastCPUTicks {
		elapsedCPU := float64(ticks-lastCPUTicks) / clockTicksPerSecond
		elapsedWall := now.Sub(lastTickTime).Seconds()
		if elapsedWall > 0 {
			cpuPercent = 100 * elapsedCPU / elapsedWall
		}
	}
	lastCPUTicks = ticks
	lastTickTime = now

	samples = append(samples, sample{
		Time:       now.Unix(),
		CPUPercent: cpuPercent,
		Goroutines: runtime.NumGoroutine(),
		Threads:    threads,
		RSSBytes:   rss,
		HeapAlloc:  m.Alloc,
		HeapSys:    m.HeapSys,
		GCCycles:   m.NumGC,
		GCCPU:      m.GCCPUFraction,
	})

	// Ring buffer behavior: drop the oldest samples when full.
	if len(samples) > maxSamples {
		samples = samples[len(samples)-maxSamples:]
	}
}

func latestSample() sample {
	statsMu.RLock()
	defer statsMu.RUnlock()

	if len(samples) == 0 {
		return sample{}
	}

	return samples[len(samples)-1]
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	s := latestSample()

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "uptime:       %s\n", time.Since(monitorStart).Round(time.Second))
	fmt.Fprintf(w, "cpu:          %.1f%%\n", s.CPUPercent)
	fmt.Fprintf(w, "goroutines:   %d\n", s.Goroutines)
	fmt.Fprintf(w, "os threads:   %d\n", s.Threads)
	fmt.Fprintf(w, "rss (RAM):    %s\n", humanBytes(int64(s.RSSBytes)))
	fmt.Fprintf(w, "heap alloc:   %s\n", humanBytes(int64(s.HeapAlloc)))
	fmt.Fprintf(w, "heap sys:     %s\n", humanBytes(int64(s.HeapSys)))
	fmt.Fprintf(w, "gc cycles:    %d\n", s.GCCycles)
	fmt.Fprintf(w, "gc cpu:       %.2f%%\n", 100*s.GCCPU)
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	// Copy under the lock so the sampler can keep appending.
	statsMu.RLock()
	history := make([]sample, len(samples))
	copy(history, samples)
	statsMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

//
// Benchmark runner
//
// The dashboard button POSTs here; we run the benchmark command as a
// child process and send its output back when it finishes. While it runs
// (about 20 seconds), the dashboard graphs show the load live.
//

var benchmarkRunning atomic.Bool

func handleRunBenchmark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	// Only one benchmark at a time.
	if !benchmarkRunning.CompareAndSwap(false, true) {
		http.Error(w, "a benchmark is already running", http.StatusConflict)
		return
	}
	defer benchmarkRunning.Store(false)

	// "go run ./benchmark" only works from the project root, so we run it
	// in the directory this server was started from.
	workDir, err := os.Getwd()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = "/usr/local/go/bin/go" // common install location
	}

	cmd := exec.Command(goBin, "run", "./benchmark", "-d", "2s", "-c", "50")
	cmd.Dir = workDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, fmt.Sprintf("benchmark failed: %v\n\n%s", err, out), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write(out)
}

// procStat reads /proc/self/stat and returns total CPU ticks (utime+stime),
// the OS thread count and the resident set size in bytes.
func procStat() (cpuTicks uint64, threads int, rssBytes uint64) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, 0, 0
	}
	// Fields after the executable name (which may contain spaces/parens).
	rest := strings.Fields(string(data)[strings.LastIndexByte(string(data), ')')+1:])
	if len(rest) < 22 {
		return 0, 0, 0
	}
	// rest[0] is field 3 (state), so index = field number - 3.
	utime, _ := strconv.ParseUint(rest[11], 10, 64)     // field 14
	stime, _ := strconv.ParseUint(rest[12], 10, 64)     // field 15
	numThreads, _ := strconv.ParseInt(rest[17], 10, 64) // field 20
	rssPages, _ := strconv.ParseInt(rest[21], 10, 64)   // field 24
	return utime + stime, int(numThreads), uint64(rssPages) * uint64(os.Getpagesize())
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
