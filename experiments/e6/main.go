// Command e6 measures block propagation delay across a running testbed.
//
//	./experiments/testbed.sh up 5
//	go run ./experiments/e6 -rounds 20 -nodes 5
//
// Method: node 1 receives a donation and mines it. The instant the mine request
// returns, one goroutine per peer polls that peer's /block/last until its height
// reaches the new tip. Timing therefore covers propagation only, excluding block
// assembly.
//
// This harness exists because an earlier shell implementation polled peers
// sequentially with one curl process per sample. Process spawn cost ~400 ms,
// which both dominated the absolute figures and manufactured a per-peer stagger
// that looked exactly like sequential relay. Here every peer is polled
// concurrently by a goroutine over a keep-alive connection, so sampling overhead
// is sub-millisecond and peers do not interfere with each other.
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

type blockResponse struct {
	Height uint64 `json:"height"`
}

type observation struct {
	Round int
	Node  int
	Delay time.Duration
	Timed bool // true if it timed out
}

func apiPort(node int) int { return 8080 + (node-1)*10 }

// client uses keep-alive so repeated polls do not pay connection setup.
func newClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConnsPerHost: 4,
		DisableCompression:  true,
	}
	return &http.Client{Transport: transport, Timeout: 2 * time.Second}
}

func height(c *http.Client, port int) (uint64, error) {
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/block/last", port))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var blk blockResponse
	if err := json.NewDecoder(resp.Body).Decode(&blk); err != nil {
		return 0, err
	}
	return blk.Height, nil
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
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	return payload.Address, nil
}

func post(c *http.Client, port int, path, body string) error {
	resp, err := c.Post(fmt.Sprintf("http://127.0.0.1:%d%s", port, path),
		"application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s -> %d: %s", path, resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	return nil
}

func main() {
	rounds := flag.Int("rounds", 20, "number of measurement rounds")
	nodes := flag.Int("nodes", 3, "number of nodes in the running testbed")
	pollEvery := flag.Duration("poll", time.Millisecond, "interval between polls")
	timeout := flag.Duration("timeout", 30*time.Second, "per-round timeout")
	out := flag.String("out", "experiments/results/e6_propagation.csv", "CSV output path")
	flag.Parse()

	if *nodes < 2 {
		fmt.Println("need at least 2 nodes")
		os.Exit(1)
	}

	control := newClient()
	dest, err := address(control, apiPort(2))
	if err != nil {
		fmt.Printf("could not reach node2: %v\n(is the testbed running?)\n", err)
		os.Exit(1)
	}

	// Sampling overhead, reported so the resolution floor is explicit.
	probeStart := time.Now()
	const probes = 200
	for i := 0; i < probes; i++ {
		if _, err := height(control, apiPort(1)); err != nil {
			fmt.Printf("probe failed: %v\n", err)
			os.Exit(1)
		}
	}
	perPoll := time.Since(probeStart) / probes
	fmt.Printf("sampling overhead: %v per poll (%d probes)\n", perPoll.Round(time.Microsecond), probes)
	fmt.Printf("E6: %d rounds across %d nodes, poll interval %v\n\n", *rounds, *nodes, *pollEvery)

	var observations []observation

	for round := 1; round <= *rounds; round++ {
		start, err := height(control, apiPort(1))
		if err != nil {
			fmt.Printf("round %d: node1 unreachable: %v\n", round, err)
			break
		}
		target := start + 1

		if err := post(control, apiPort(1), "/transaction/new",
			fmt.Sprintf(`{"destination":%q,"amount":1}`, dest)); err != nil {
			fmt.Printf("round %d: donation rejected: %v\n", round, err)
			break
		}

		// Pollers start before the mine so none of the propagation window is
		// missed while goroutines are being scheduled.
		var wg sync.WaitGroup
		results := make([]observation, *nodes+1)
		begin := make(chan time.Time)

		for node := 2; node <= *nodes; node++ {
			wg.Add(1)
			go func(node int) {
				defer wg.Done()
				c := newClient()
				t0 := <-begin
				deadline := t0.Add(*timeout)
				for time.Now().Before(deadline) {
					if h, err := height(c, apiPort(node)); err == nil && h >= target {
						results[node] = observation{round, node, time.Since(t0), false}
						return
					}
					time.Sleep(*pollEvery)
				}
				results[node] = observation{round, node, *timeout, true}
			}(node)
		}

		if err := post(control, apiPort(1), "/block/mine", ""); err != nil {
			fmt.Printf("round %d: mine failed: %v\n", round, err)
			close(begin)
			wg.Wait()
			break
		}
		t0 := time.Now()
		for node := 2; node <= *nodes; node++ {
			begin <- t0
		}
		wg.Wait()

		failed := 0
		for node := 2; node <= *nodes; node++ {
			observations = append(observations, results[node])
			if results[node].Timed {
				failed++
			}
		}
		if failed > 0 {
			fmt.Printf("round %2d: %d node(s) timed out\n", round, failed)
		} else {
			fmt.Printf("round %2d ok\n", round)
		}
	}

	if len(observations) == 0 {
		fmt.Println("no observations collected")
		os.Exit(1)
	}

	if err := writeCSV(*out, observations); err != nil {
		fmt.Printf("could not write CSV: %v\n", err)
	} else {
		fmt.Printf("\nwrote %d observations to %s\n", len(observations), *out)
	}
	report(observations, *nodes, perPoll)
}

func writeCSV(path string, observations []observation) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fh.Close()

	w := csv.NewWriter(fh)
	defer w.Flush()
	if err := w.Write([]string{"round", "node", "delay_ms", "timed_out"}); err != nil {
		return err
	}
	for _, o := range observations {
		if err := w.Write([]string{
			strconv.Itoa(o.Round),
			fmt.Sprintf("node%d", o.Node),
			strconv.FormatFloat(float64(o.Delay.Microseconds())/1000.0, 'f', 3, 64),
			strconv.FormatBool(o.Timed),
		}); err != nil {
			return err
		}
	}
	return nil
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

func report(observations []observation, nodes int, perPoll time.Duration) {
	byNode := map[int][]float64{}
	timeouts := map[int]int{}
	var all []float64

	for _, o := range observations {
		if o.Timed {
			timeouts[o.Node]++
			continue
		}
		ms := float64(o.Delay.Microseconds()) / 1000.0
		byNode[o.Node] = append(byNode[o.Node], ms)
		all = append(all, ms)
	}

	fmt.Printf("\n%-8s%5s%10s%10s%10s%10s%10s%10s\n",
		"node", "n", "min", "median", "mean", "sd", "p95", "max")
	fmt.Println(strings.Repeat("-", 73))
	for node := 2; node <= nodes; node++ {
		v := byNode[node]
		if len(v) == 0 {
			continue
		}
		mn, med, mean, p95, mx, sd := stats(v)
		fmt.Printf("node%-4d%5d%10.1f%10.1f%10.1f%10.1f%10.1f%10.1f\n",
			node, len(v), mn, med, mean, sd, p95, mx)
	}
	fmt.Println(strings.Repeat("-", 73))
	if len(all) > 0 {
		mn, med, mean, p95, mx, sd := stats(all)
		fmt.Printf("%-8s%5d%10.1f%10.1f%10.1f%10.1f%10.1f%10.1f\n",
			"all", len(all), mn, med, mean, sd, p95, mx)
		fmt.Printf("\nAll figures in milliseconds. Sampling overhead %v per poll.\n",
			perPoll.Round(time.Microsecond))
	}
	total := 0
	for _, c := range timeouts {
		total += c
	}
	if total > 0 {
		fmt.Printf("timeouts: %d\n", total)
	}
}
