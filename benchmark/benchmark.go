// Command benchmark load-tests the pygoserver HTTP server.
//
// It exercises every route the server supports: static HTML/assets,
// the template engine (/todos), the JSON API (GET/POST/DELETE
// /api/todos), 404 handling and a weighted mixed workload.
//
// The server must be running before starting the benchmark:
//
//	go run ./cmd/pygoserver &
//	go run ./benchmark -url http://localhost:8080 -c 50 -d 3s
package main

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// SecureRandomString returns a random alphanumeric string of the given length.
func SecureRandomString(length int) (string, error) {
	b := make([]byte, length)
	if _, err := crand.Read(b); err != nil {
		return "", err
	}
	for i, v := range b {
		b[i] = charset[v%byte(len(charset))]
	}
	return string(b), nil
}

// requestFn performs one benchmark iteration and returns the HTTP status
// code, the response body size and the latency that should be recorded.
type requestFn func(client *http.Client) (status int, bodyBytes int64, latency time.Duration, err error)

type scenario struct {
	name   string
	expect map[int]bool // acceptable status codes; anything else counts as an error
	fn     requestFn
}

type result struct {
	name         string
	requests     int
	errors       int
	bytesRead    int64
	statusCounts map[int]int
	latencies    []time.Duration
	duration     time.Duration
}

func newClient() *http.Client {
	// The server closes the connection after every response and sends no
	// Content-Length for HTML routes, so keep-alive must be disabled or the
	// client would waste time retrying dead connections.
	return &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		Timeout:   10 * time.Second,
	}
}

func doRequest(client *http.Client, method, url string, body []byte) (int, int64, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return 0, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, n, err
}

// timed wraps a simple request so its full round-trip is measured.
func timed(method, url string, body []byte) requestFn {
	return func(client *http.Client) (int, int64, time.Duration, error) {
		start := time.Now()
		status, n, err := doRequest(client, method, url, body)
		return status, n, time.Since(start), err
	}
}

// createTodo POSTs a new todo and returns its ID.
func createTodo(client *http.Client, baseURL string) (string, error) {
	title, err := SecureRandomString(10)
	if err != nil {
		return "", err
	}
	payload := fmt.Sprintf(`{"title":%q,"completed":false}`, title)

	resp, err := client.Post(baseURL+"/api/todos", "application/json", strings.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}

	var item struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &item); err != nil {
		return "", fmt.Errorf("decode create response: %w", err)
	}
	return item.ID, nil
}

// createScenario measures POST /api/todos with a random payload.
func createScenario(baseURL string) requestFn {
	return func(client *http.Client) (int, int64, time.Duration, error) {
		title, err := SecureRandomString(10)
		if err != nil {
			return 0, 0, 0, err
		}
		payload := fmt.Sprintf(`{"title":%q,"completed":false}`, title)
		return timed(http.MethodPost, baseURL+"/api/todos", []byte(payload))(client)
	}
}

// deleteScenario creates a todo (untimed setup) and then measures
// DELETE /api/todos?id=<id> in isolation.
func deleteScenario(baseURL string) requestFn {
	return func(client *http.Client) (int, int64, time.Duration, error) {
		id, err := createTodo(client, baseURL)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("setup create: %w", err)
		}
		return timed(http.MethodDelete, baseURL+"/api/todos?id="+id, nil)(client)
	}
}

// mixedScenario hits all routes with realistic weights:
// 30% static HTML, 15% API list, 15% template page, 20% assets,
// 10% API create, 10% API delete.
func mixedScenario(baseURL string) requestFn {
	root := timed(http.MethodGet, baseURL+"/", nil)
	css := timed(http.MethodGet, baseURL+"/assets/css/styles.css", nil)
	js := timed(http.MethodGet, baseURL+"/assets/js/script.js", nil)
	page := timed(http.MethodGet, baseURL+"/todos", nil)
	list := timed(http.MethodGet, baseURL+"/api/todos", nil)
	create := createScenario(baseURL)
	deleteCycle := deleteScenario(baseURL)

	return func(client *http.Client) (int, int64, time.Duration, error) {
		switch r := rand.IntN(100); {
		case r < 30:
			return root(client)
		case r < 45:
			return list(client)
		case r < 60:
			return page(client)
		case r < 70:
			return css(client)
		case r < 80:
			return js(client)
		case r < 90:
			return create(client)
		default:
			return deleteCycle(client)
		}
	}
}

func runScenario(s scenario, concurrency int, duration time.Duration) result {
	client := newClient()

	type workerStats struct {
		latencies    []time.Duration
		statusCounts map[int]int
		errors       int
		bytesRead    int64
	}

	stats := make([]workerStats, concurrency)
	deadline := time.Now().Add(duration)
	var wg sync.WaitGroup

	start := time.Now()
	for i := range stats {
		stats[i].statusCounts = make(map[int]int)
		wg.Add(1)
		go func(st *workerStats) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				status, n, latency, err := s.fn(client)
				if err != nil {
					st.errors++
					continue
				}
				st.bytesRead += n
				st.statusCounts[status]++
				if !s.expect[status] {
					st.errors++
					continue
				}
				st.latencies = append(st.latencies, latency)
			}
		}(&stats[i])
	}
	wg.Wait()
	elapsed := time.Since(start)

	res := result{name: s.name, statusCounts: make(map[int]int), duration: elapsed}
	for i := range stats {
		st := &stats[i]
		res.errors += st.errors
		res.bytesRead += st.bytesRead
		res.latencies = append(res.latencies, st.latencies...)
		for code, count := range st.statusCounts {
			res.statusCounts[code] += count
			res.requests += count
		}
	}
	return res
}

func percentile(sorted []time.Duration, q float64) time.Duration {
	return sorted[int(q*float64(len(sorted)-1))]
}

func printResult(res result) {
	fmt.Printf("\n=== %s ===\n", res.name)
	fmt.Printf("  Duration:    %s\n", res.duration.Round(time.Millisecond))
	fmt.Printf("  Requests:    %d (%.1f req/s)\n", res.requests, float64(res.requests)/res.duration.Seconds())
	fmt.Printf("  Errors:      %d\n", res.errors)

	codes := make([]int, 0, len(res.statusCounts))
	for c := range res.statusCounts {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	var parts []string
	for _, c := range codes {
		parts = append(parts, fmt.Sprintf("%d x%d", c, res.statusCounts[c]))
	}
	fmt.Printf("  Statuses:    %s\n", strings.Join(parts, "  "))
	fmt.Printf("  Received:    %s\n", humanBytes(res.bytesRead))

	if len(res.latencies) == 0 {
		fmt.Println("  Latency:     no successful requests")
		return
	}

	sort.Slice(res.latencies, func(i, j int) bool { return res.latencies[i] < res.latencies[j] })
	var sum time.Duration
	for _, l := range res.latencies {
		sum += l
	}
	avg := sum / time.Duration(len(res.latencies))

	fmt.Printf("  Latency avg: %s   min: %s   max: %s\n",
		avg.Round(time.Microsecond),
		res.latencies[0].Round(time.Microsecond),
		res.latencies[len(res.latencies)-1].Round(time.Microsecond))
	fmt.Printf("  Latency p50: %s   p95: %s   p99: %s\n",
		percentile(res.latencies, 0.50).Round(time.Microsecond),
		percentile(res.latencies, 0.95).Round(time.Microsecond),
		percentile(res.latencies, 0.99).Round(time.Microsecond))
}

func printSummary(results []result) {
	fmt.Println("\n================================ SUMMARY ================================")
	fmt.Printf("%-50s %10s %10s %10s %10s %8s\n", "scenario", "req/s", "avg", "p95", "p99", "errors")
	for _, r := range results {
		var avg, p95, p99 time.Duration
		if len(r.latencies) > 0 { // already sorted by printResult
			var sum time.Duration
			for _, l := range r.latencies {
				sum += l
			}
			avg = sum / time.Duration(len(r.latencies))
			p95 = percentile(r.latencies, 0.95)
			p99 = percentile(r.latencies, 0.99)
		}
		name := r.name
		if len(name) > 50 {
			name = name[:47] + "..."
		}
		fmt.Printf("%-50s %10.1f %10s %10s %10s %8d\n",
			name,
			float64(r.requests)/r.duration.Seconds(),
			avg.Round(time.Microsecond),
			p95.Round(time.Microsecond),
			p99.Round(time.Microsecond),
			r.errors)
	}
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

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "base URL of the server under test")
	concurrency := flag.Int("c", 50, "number of concurrent workers per scenario")
	duration := flag.Duration("d", 3*time.Second, "duration of each scenario")
	flag.Parse()

	// Health check.
	probe := &http.Client{Timeout: 3 * time.Second}
	resp, err := probe.Get(*baseURL + "/")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach %s: %v\nStart the server first: go run ./cmd/pygoserver\n", *baseURL, err)
		os.Exit(1)
	}
	resp.Body.Close()

	// Seed a todo so the template page (/todos) and the API list have data.
	setup := newClient()
	seedID, err := createTodo(setup, *baseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not seed a todo, template scenarios will return 404: %v\n", err)
	} else {
		fmt.Printf("seeded todo %s\n", seedID)
	}

	ok := map[int]bool{http.StatusOK: true}
	scenarios := []scenario{
		{name: "GET / (static HTML file)", expect: ok, fn: timed(http.MethodGet, *baseURL+"/", nil)},
		{name: "GET /assets/css/styles.css (static asset)", expect: ok, fn: timed(http.MethodGet, *baseURL+"/assets/css/styles.css", nil)},
		{name: "GET /assets/js/script.js (static asset)", expect: ok, fn: timed(http.MethodGet, *baseURL+"/assets/js/script.js", nil)},
		{name: "GET /todos (template engine)", expect: ok, fn: timed(http.MethodGet, *baseURL+"/todos", nil)},
		{name: "GET /api/todos (API list)", expect: ok, fn: timed(http.MethodGet, *baseURL+"/api/todos", nil)},
		{name: "POST /api/todos (API create)", expect: ok, fn: createScenario(*baseURL)},
		{name: "DELETE /api/todos?id= (API delete)", expect: ok, fn: deleteScenario(*baseURL)},
		{name: "GET /missing (404 handling)", expect: map[int]bool{http.StatusNotFound: true}, fn: timed(http.MethodGet, *baseURL+"/missing", nil)},
		{name: "Mixed workload (HTML + assets + templates + API)", expect: ok, fn: mixedScenario(*baseURL)},
	}

	fmt.Printf("benchmarking %s with %d workers, %s per scenario\n", *baseURL, *concurrency, *duration)

	var results []result
	for _, s := range scenarios {
		fmt.Printf("running: %s ...\n", s.name)
		res := runScenario(s, *concurrency, *duration)
		printResult(res)
		results = append(results, res)
	}

	printSummary(results)
}
