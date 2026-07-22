// Command e2 measures committed throughput and end-to-end donation latency.
//
//	./experiments/testbed.sh up 1
//	go run ./experiments/e2 -workers 8 -duration 30s
//
// Design. A closed-loop generator runs `workers` clients, each submitting a
// donation and immediately submitting the next once the previous is accepted.
// A miner loop packages the mempool into blocks. A watcher tails the chain and
// timestamps each block as it becomes the tip.
//
// Latency is measured end to end: from a client submitting a donation to the
// block containing it becoming canonical. That is the quantity a donor
// experiences, and it includes mempool wait, block assembly, consensus and
// commit. Throughput counts only committed transactions -- submissions that
// never make it into a block are not throughput.
//
// The watcher identifies committed transactions by the txID field the block
// endpoint reports, so a transaction is only counted once it is genuinely on
// the canonical chain.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type blockJSON struct {
	Height       uint64 `json:"height"`
	BlockHash    string `json:"block_hash"`
	Transactions []struct {
		TxID string `json:"txID"`
	} `json:"transactions"`
}

type submission struct {
	at time.Time
}

type tracker struct {
	mu        sync.Mutex
	pending   map[string]submission
	latencies []time.Duration
	committed int
	submitted int
	rejected  int
}

func newTracker() *tracker {
	return &tracker{pending: map[string]submission{}}
}

func (t *tracker) record(txID string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[txID] = submission{at: at}
	t.submitted++
}

func (t *tracker) reject() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rejected++
}

// commit marks a transaction as canonical, at the moment its block was observed.
func (t *tracker) commit(txID string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sub, found := t.pending[txID]
	if !found {
		return
	}
	delete(t.pending, txID)
	t.latencies = append(t.latencies, at.Sub(sub.at))
	t.committed++
}

func (t *tracker) snapshot() ([]time.Duration, int, int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]time.Duration(nil), t.latencies...)
	return out, t.committed, t.submitted, t.rejected
}

func newClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{MaxIdleConnsPerHost: 64, DisableCompression: true},
		Timeout:   30 * time.Second,
	}
}

func main() {
	port := flag.Int("port", 8080, "API port of the node under test")
	workers := flag.Int("workers", 8, "concurrent donation clients")
	duration := flag.Duration("duration", 30*time.Second, "measurement window")
	warmup := flag.Duration("warmup", 5*time.Second, "warmup discarded before measuring")
	mineEvery := flag.Duration("mine-every", 200*time.Millisecond, "interval between mine attempts")
	label := flag.String("label", "", "label recorded with the result")
	out := flag.String("out", "", "CSV output path (appends)")
	flag.Parse()

	control := newClient()
	dest, err := address(control, *port)
	if err != nil {
		fmt.Printf("could not reach node on %d: %v\n(is the testbed running?)\n", *port, err)
		os.Exit(1)
	}

	fmt.Printf("E2: %d workers, %v warmup + %v measured, mining every %v\n",
		*workers, *warmup, *duration, *mineEvery)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Miner: package whatever is pending into blocks.
	wg.Add(1)
	go func() {
		defer wg.Done()
		c := newClient()
		ticker := time.NewTicker(*mineEvery)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_, _ = post(c, *port, "/block/mine", "")
			}
		}
	}()

	// Warmup: load without measurement, so JIT, connection setup and page
	// cache are not attributed to the measured window.
	warm := newTracker()
	warmStop := make(chan struct{})
	startWorkers(*workers, *port, dest, warm, warmStop, &wg)
	time.Sleep(*warmup)
	close(warmStop)

	// Measured window.
	track := newTracker()
	watchStop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		watchChain(*port, track, watchStop)
	}()

	loadStop := make(chan struct{})
	startWorkers(*workers, *port, dest, track, loadStop, &wg)

	start := time.Now()
	time.Sleep(*duration)
	close(loadStop)

	// Let in-flight transactions commit rather than counting them as losses.
	time.Sleep(2 * time.Second)
	for i := 0; i < 5; i++ {
		_, _ = post(control, *port, "/block/mine", "")
		time.Sleep(200 * time.Millisecond)
	}
	elapsed := time.Since(start)

	close(watchStop)
	close(stop)
	wg.Wait()

	latencies, committed, submitted, rejected := track.snapshot()
	if len(latencies) == 0 {
		fmt.Println("no transactions committed; nothing to report")
		os.Exit(1)
	}

	tps := float64(committed) / elapsed.Seconds()
	values := make([]float64, len(latencies))
	for i, d := range latencies {
		values[i] = float64(d.Microseconds()) / 1000.0
	}
	mn, med, mean, p95, mx, sd := stats(values)

	fmt.Printf("\nsubmitted=%d committed=%d rejected=%d over %.1fs\n",
		submitted, committed, rejected, elapsed.Seconds())
	fmt.Printf("throughput: %.2f committed tx/s\n", tps)
	fmt.Printf("latency ms: min=%.1f median=%.1f mean=%.1f sd=%.1f p95=%.1f max=%.1f\n",
		mn, med, mean, sd, p95, mx)

	if *out != "" {
		if err := appendCSV(*out, *label, *workers, tps, committed, submitted, rejected, values); err != nil {
			fmt.Printf("could not write CSV: %v\n", err)
		} else {
			fmt.Printf("appended result to %s\n", *out)
		}
	}
}

func startWorkers(n, port int, dest string, track *tracker, stop <-chan struct{}, wg *sync.WaitGroup) {
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := newClient()
			for {
				select {
				case <-stop:
					return
				default:
				}
				at := time.Now()
				txID, err := donate(c, port, dest)
				if err != nil {
					track.reject()
					// Back off briefly so a persistent rejection (out of funds,
					// say) does not spin the CPU and distort the measurement.
					time.Sleep(5 * time.Millisecond)
					continue
				}
				track.record(txID, at)
			}
		}()
	}
}

// watchChain tails the tip and timestamps transactions as they become canonical.
func watchChain(port int, track *tracker, stop <-chan struct{}) {
	c := newClient()
	seen := map[string]bool{}
	for {
		select {
		case <-stop:
			return
		default:
		}
		blk, err := lastBlock(c, port)
		if err == nil && !seen[blk.BlockHash] {
			seen[blk.BlockHash] = true
			at := time.Now()
			for _, tx := range blk.Transactions {
				track.commit(tx.TxID, at)
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func address(c *http.Client, port int) (string, error) {
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/my-wallet/address", port))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload struct {
		Address string `json:"address"`
	}
	err = json.NewDecoder(resp.Body).Decode(&payload)
	return payload.Address, err
}

func lastBlock(c *http.Client, port int) (*blockJSON, error) {
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/block/last", port))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var blk blockJSON
	if err := json.NewDecoder(resp.Body).Decode(&blk); err != nil {
		return nil, err
	}
	return &blk, nil
}

func donate(c *http.Client, port int, dest string) (string, error) {
	body := fmt.Sprintf(`{"destination":%q,"amount":1}`, dest)
	raw, err := post(c, port, "/transaction/new", body)
	if err != nil {
		return "", err
	}
	var tx struct {
		TxID string `json:"txID"`
	}
	if err := json.Unmarshal(raw, &tx); err != nil {
		return "", err
	}
	if tx.TxID == "" {
		return "", fmt.Errorf("no txID in response")
	}
	return tx.TxID, nil
}

func post(c *http.Client, port int, path, body string) ([]byte, error) {
	resp, err := c.Post(fmt.Sprintf("http://127.0.0.1:%d%s", port, path),
		"application/json", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s -> %d", path, resp.StatusCode)
	}
	return raw, nil
}

func appendCSV(path, label string, workers int, tps float64, committed, submitted, rejected int, latencies []float64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, statErr := os.Stat(path)
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer fh.Close()

	w := csv.NewWriter(fh)
	defer w.Flush()
	if os.IsNotExist(statErr) {
		_ = w.Write([]string{"label", "workers", "tps", "committed", "submitted",
			"rejected", "median_ms", "mean_ms", "p95_ms", "max_ms"})
	}
	_, med, mean, p95, mx, _ := stats(latencies)
	return w.Write([]string{
		label,
		strconv.Itoa(workers),
		strconv.FormatFloat(tps, 'f', 2, 64),
		strconv.Itoa(committed),
		strconv.Itoa(submitted),
		strconv.Itoa(rejected),
		strconv.FormatFloat(med, 'f', 2, 64),
		strconv.FormatFloat(mean, 'f', 2, 64),
		strconv.FormatFloat(p95, 'f', 2, 64),
		strconv.FormatFloat(mx, 'f', 2, 64),
	})
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	k := (float64(len(sorted)) - 1) * p
	lo, hi := int(math.Floor(k)), int(math.Ceil(k))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(k-float64(lo))
}

func stats(values []float64) (min, median, mean, p95, max, stddev float64) {
	if len(values) == 0 {
		return
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	min, max = sorted[0], sorted[len(sorted)-1]
	median = percentile(sorted, 0.5)
	p95 = percentile(sorted, 0.95)
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))
	for _, v := range values {
		stddev += (v - mean) * (v - mean)
	}
	stddev = math.Sqrt(stddev / float64(len(values)))
	return
}
