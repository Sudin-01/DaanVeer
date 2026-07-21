// Command e3 measures consensus latency against quorum size.
//
//	QUORUM=3 ./experiments/testbed.sh up 5
//	go run ./experiments/e3 -rounds 20 -nodes 5
//
// The mine endpoint reports consensus_ms: the interval from the proposer having
// a signed block to the quorum being met. That isolates the cost of agreement
// from block assembly and from storage.
//
// Sweeping quorum requires restarting the network, since the quorum is part of
// the chain configuration. Use experiments/e3_sweep.sh to run the full sweep;
// this command measures one configuration.
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
	"time"
)

type mineResponse struct {
	Attestations int     `json:"attestations"`
	Quorum       int     `json:"quorum"`
	ConsensusMS  float64 `json:"consensus_ms"`
	Block        struct {
		Height uint64 `json:"height"`
	} `json:"block"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func apiPort(node int) int { return 8080 + (node-1)*10 }

func main() {
	rounds := flag.Int("rounds", 20, "measurement rounds")
	warmup := flag.Int("warmup", 10, "rounds to run and discard before measuring")
	nodes := flag.Int("nodes", 3, "nodes in the running testbed")
	out := flag.String("out", "", "CSV output path (appends if it exists)")
	label := flag.String("label", "", "label recorded with each observation")
	flag.Parse()

	client := &http.Client{Timeout: 30 * time.Second}

	dest, err := address(client, apiPort(2))
	if err != nil {
		fmt.Printf("could not reach node2: %v\n(is the testbed running?)\n", err)
		os.Exit(1)
	}

	// Discard the first rounds. A freshly started network is measurably slower
	// -- Go runtime warmup, connection setup, page cache -- and in an earlier
	// sweep that transient dominated the quantity being measured: whichever
	// configuration ran first looked slowest, regardless of its quorum.
	if *warmup > 0 {
		fmt.Printf("warmup: %d discarded rounds\n", *warmup)
		for i := 0; i < *warmup; i++ {
			if err := donate(client, apiPort(1), dest); err != nil {
				fmt.Printf("warmup donation rejected: %v\n", err)
				break
			}
			if _, err := mine(client, apiPort(1)); err != nil {
				fmt.Printf("warmup mine failed: %v\n", err)
				break
			}
		}
	}

	var latencies []float64
	var quorum, attestations int
	failures := 0

	for round := 1; round <= *rounds; round++ {
		if err := donate(client, apiPort(1), dest); err != nil {
			fmt.Printf("round %d: donation rejected: %v\n", round, err)
			break
		}

		result, err := mine(client, apiPort(1))
		if err != nil {
			failures++
			fmt.Printf("round %2d: %v\n", round, err)
			continue
		}
		latencies = append(latencies, result.ConsensusMS)
		quorum, attestations = result.Quorum, result.Attestations
		fmt.Printf("round %2d: %.2f ms (%d/%d attestations, height %d)\n",
			round, result.ConsensusMS, result.Attestations, result.Quorum, result.Block.Height)
	}

	if len(latencies) == 0 {
		fmt.Println("no successful rounds")
		os.Exit(1)
	}

	mn, med, mean, p95, mx, sd := stats(latencies)
	fmt.Printf("\nnodes=%d quorum=%d attestations=%d n=%d failures=%d\n",
		*nodes, quorum, attestations, len(latencies), failures)
	fmt.Printf("consensus latency ms: min=%.2f median=%.2f mean=%.2f sd=%.2f p95=%.2f max=%.2f\n",
		mn, med, mean, sd, p95, mx)

	if *out != "" {
		if err := appendCSV(*out, *label, *nodes, quorum, latencies); err != nil {
			fmt.Printf("could not write CSV: %v\n", err)
		} else {
			fmt.Printf("appended %d observations to %s\n", len(latencies), *out)
		}
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

func donate(c *http.Client, port int, dest string) error {
	body := fmt.Sprintf(`{"destination":%q,"amount":1}`, dest)
	resp, err := c.Post(fmt.Sprintf("http://127.0.0.1:%d/transaction/new", port),
		"application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", strings.TrimSpace(string(raw)))
	}
	return nil
}

func mine(c *http.Client, port int) (*mineResponse, error) {
	resp, err := c.Post(fmt.Sprintf("http://127.0.0.1:%d/block/mine", port),
		"application/json", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var e errorResponse
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("mine returned %d", resp.StatusCode)
	}
	var result mineResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func appendCSV(path, label string, nodes, quorum int, latencies []float64) error {
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
		if err := w.Write([]string{"label", "nodes", "quorum", "round", "consensus_ms"}); err != nil {
			return err
		}
	}
	for i, v := range latencies {
		if err := w.Write([]string{
			label,
			strconv.Itoa(nodes),
			strconv.Itoa(quorum),
			strconv.Itoa(i + 1),
			strconv.FormatFloat(v, 'f', 3, 64),
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
