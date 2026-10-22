// Command crashharness is the Stage 1 fault-injection harness for the
// persistence engine. It answers one question with evidence:
//
//	"Is data.godb always a valid, complete, loadable file — even when
//	the process is killed with SIGKILL at a random moment, including
//	in the middle of Persist?"
//
// It works in two roles:
//
//	crashharness            parent: runs the test rounds
//	crashharness child DIR  child: tight loop of Put + Persist, printing
//	                        "ACK <n>" after every completed Persist
//
// The parent spawns the child as a separate process (so it can be
// killed for real), lets it write for a random interval, kill -9s it,
// and then inspects the wreckage:
//
//  1. data.godb parses and its checksum matches the data (never torn)
//  2. the file never contains FEWER items than the child acknowledged
//  3. LoadData restores exactly what the file contains (round-trip)
//  4. timestamps survive the round trip (Item.CreatedAt unchanged)
//  5. no junk/orphan files accumulate in the directory
//
// Run from the project root:
//
//	go run ./cmd/crashharness          (25 rounds)
//	go run ./cmd/crashharness 100      (100 rounds)
package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/generalpy101/pygoserver/internal/store"
)

const charset = "abcdefghijklmnopqrstuvwxyz"

// mirror of the unexported fileContent struct in the store package.
type dbFile struct {
	Checksum string       `json:"checksum"`
	Data     []store.Item `json:"data"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "child" {
		runChild(os.Args[2])
		return
	}

	rounds := 25
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil {
			rounds = n
		}
	}

	fmt.Println("== phase 1: determinism (issue #1) ==")
	checkDeterminism()

	fmt.Println()
	fmt.Println("== phase 2: race detector ==")
	runRaceDetector()

	fmt.Println()
	fmt.Printf("== phase 3: crash rounds (kill -9 mid-write) x%d ==\n", rounds)
	runCrashRounds(rounds)
}

// Phase 1: repeated Persist with unchanged data must never rewrite
// the file. A nondeterministic serialization (map iteration order)
// fails this.
func checkDeterminism() {
	dir, err := os.MkdirTemp("", "godb-determinism")
	if err != nil {
		fmt.Println("setup failed:", err)
		return
	}
	defer os.RemoveAll(dir)

	datastore := store.New()
	for i := 0; i < 50; i++ {
		datastore.Put("todo", "", map[string]any{"title": fmt.Sprintf("task %d", i)})
	}
	engine := store.NewPersistenceEngine(dir, datastore)

	if err := engine.Persist(); err != nil {
		fmt.Println("FAIL: first persist:", err)
		return
	}

	before, _ := os.Stat(filepath.Join(dir, "data.godb"))

	rewrites := 0
	for i := 0; i < 100; i++ {
		if err := engine.Persist(); err != nil {
			fmt.Println("FAIL: persist:", err)
			return
		}
		after, _ := os.Stat(filepath.Join(dir, "data.godb"))
		if !after.ModTime().Equal(before.ModTime()) {
			rewrites++
			before = after
		}
	}

	if rewrites == 0 {
		fmt.Println("PASS: 100 unchanged persists, 0 rewrites (deterministic)")
	} else {
		fmt.Printf("FAIL: %d/100 unchanged persists rewrote the file (nondeterministic serialization)\n", rewrites)
	}
}

// Phase 2: go test -race over the store package. The persistence loop
// runs a goroutine, so data races on e.g. the running flag show here.
func runRaceDetector() {
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = "/usr/local/go/bin/go" // common install location
	}

	cmd := exec.Command(goBin, "test", "-race", "-count=1", "./internal/store/")
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("FAIL: go test -race: %v\n%s\n", err, out)
		return
	}
	fmt.Println("PASS: go test -race clean")
}

//
// Phase 3
//

type roundResult struct {
	corrupt    bool // file missing despite ACKs, unparseable, or bad checksum
	dataLoss   bool // file holds fewer items than the child acknowledged
	loadFail   bool // LoadData could not restore the file
	timeWarp   bool // timestamps changed across the round trip
	leftovers  []string
	fileCount  int
	ackedCount int
}

func runCrashRounds(rounds int) {
	var failures int

	for round := 1; round <= rounds; round++ {
		res := runRound()

		status := "ok"
		if res.corrupt || res.dataLoss || res.loadFail {
			failures++
			status = "FAIL"
		}

		fmt.Printf("round %02d: acked=%d inFile=%d corrupt=%v dataLoss=%v loadFail=%v timeWarp=%v leftovers=%v -> %s\n",
			round, res.ackedCount, res.fileCount, res.corrupt, res.dataLoss, res.loadFail, res.timeWarp, res.leftovers, status)
	}

	fmt.Println()
	fmt.Printf("summary: %d/%d rounds clean\n", rounds-failures, rounds)
	if failures > 0 {
		os.Exit(1)
	}
}

func runRound() roundResult {
	var res roundResult

	dir, err := os.MkdirTemp("", "godb-crash")
	if err != nil {
		fmt.Println("setup failed:", err)
		return res
	}
	defer os.RemoveAll(dir)

	// Spawn the writer child and let it run for a random interval,
	// then SIGKILL it — no cleanup, no flush, like a real crash.
	var childOut bytes.Buffer
	cmd := exec.Command(os.Args[0], "child", dir)
	cmd.Stdout = &childOut
	if err := cmd.Start(); err != nil {
		fmt.Println("spawn failed:", err)
		return res
	}

	time.Sleep(time.Duration(50+rand.Intn(1400)) * time.Millisecond)
	cmd.Process.Kill()
	cmd.Wait()

	// How many writes did the child acknowledge before dying?
	res.ackedCount = lastAck(childOut.String())

	// What is left in the directory?
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.Name() != "data.godb" {
			res.leftovers = append(res.leftovers, entry.Name())
		}
	}

	content, err := os.ReadFile(filepath.Join(dir, "data.godb"))

	if os.IsNotExist(err) {
		// Only acceptable if the child never acknowledged a persist.
		if res.ackedCount > 0 {
			res.corrupt = true // file vanished after successful persists
		}
		return res
	}
	if err != nil {
		res.corrupt = true
		return res
	}

	// Check 1: the file parses.
	var parsed dbFile
	if err := json.Unmarshal(content, &parsed); err != nil {
		res.corrupt = true // torn write: file is not valid JSON
		return res
	}

	// Check 2: the stored checksum matches the data. Item marshaling is
	// deterministic (struct field order, slice order preserved), so we
	// can recompute the checksum the same way Persist computed it.
	dataJSON, _ := json.Marshal(parsed.Data)
	sum := md5.Sum(dataJSON)
	if hex.EncodeToString(sum[:]) != parsed.Checksum {
		res.corrupt = true // checksum mismatch: data was altered after writing
		return res
	}

	res.fileCount = len(parsed.Data)

	// Check 3: the file must never hold FEWER items than the child
	// acknowledged (that would be losing a write we confirmed as done).
	// It may hold one MORE (crash between Persist and the ACK print).
	if res.fileCount < res.ackedCount {
		res.dataLoss = true
	}

	// Check 4: round trip through LoadData.
	loaded := store.New()
	engine := store.NewPersistenceEngine(dir, loaded)
	if err := engine.LoadData(); err != nil {
		res.loadFail = true
		return res
	}
	if len(loaded.GetAll()) != res.fileCount {
		res.loadFail = true
		return res
	}

	// Check 5 (observation): do timestamps survive? The file recorded
	// the original CreatedAt; compare against the reloaded item.
	if res.fileCount > 0 {
		want := parsed.Data[0]
		got, err := loaded.Get(want.Kind, want.ID)
		if err != nil || !got.CreatedAt.Equal(want.CreatedAt) {
			res.timeWarp = true
		}
	}

	return res
}

// lastAck extracts the highest "ACK <n>" the child printed.
func lastAck(output string) int {
	last := 0
	for line := range strings.Lines(output) {
		if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "ACK"))); err == nil {
			last = n
		}
	}
	return last
}

// The child: a tight loop of Put + Persist, printing ACK <n> after every
// completed Persist. Large payloads make every persist write a real
// amount of data, widening the window in which a kill lands mid-write.
func runChild(dir string) {
	datastore := store.New()
	engine := store.NewPersistenceEngine(dir, datastore)

	payload := randomString(20 * 1024)

	for n := 1; ; n++ {
		if _, err := datastore.Put("todo", "", map[string]any{"title": payload, "n": n}); err != nil {
			fmt.Fprintln(os.Stderr, "put failed:", err)
			os.Exit(1)
		}

		if err := engine.Persist(); err != nil {
			fmt.Fprintln(os.Stderr, "persist failed:", err)
			os.Exit(1)
		}

		// ACK means: this persist finished. Anything after the last
		// ACK was never confirmed, so losing it is legitimate.
		fmt.Printf("ACK %d\n", n)
	}
}

func randomString(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
