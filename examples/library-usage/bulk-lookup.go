package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"iporg/pkg/iporgdb"
	"iporg/pkg/model"
	"iporg/pkg/util/workers"
)

// Example: Process multiple IPs efficiently with concurrent lookups
func main() {
	db, err := iporgdb.Open("/var/groupsio/data/iporgdb")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Read IPs from stdin
	var ips []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		ip := scanner.Text()
		if ip != "" {
			ips = append(ips, ip)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("Failed to read input: %v", err)
	}

	fmt.Fprintf(os.Stderr, "Processing %d IPs with 10 workers...\n", len(ips))

	// Process IPs concurrently
	ctx := context.Background()
	pool := workers.NewPool(ctx, workers.Config{
		Workers:   10,
		RateLimit: 0, // No rate limit for local lookups
	})

	type result struct {
		index int
		ip    string
		rec   *model.Record
		err   error
	}

	results := make([]result, len(ips))
	var mu sync.Mutex

	for i, ip := range ips {
		idx := i
		currentIP := ip

		pool.Submit(idx, func(ctx context.Context) error {
			rec, err := db.LookupString(currentIP)
			mu.Lock()
			results[idx] = result{
				index: idx,
				ip:    currentIP,
				rec:   rec,
				err:   err,
			}
			mu.Unlock()
			return nil
		})
	}

	pool.Wait()

	// Output results in order (JSONL format)
	for _, res := range results {
		if res.err == model.ErrNotFound {
			output := map[string]interface{}{
				"ip":    res.ip,
				"error": "not found",
			}
			json.NewEncoder(os.Stdout).Encode(output)
		} else if res.err != nil {
			output := map[string]interface{}{
				"ip":    res.ip,
				"error": res.err.Error(),
			}
			json.NewEncoder(os.Stdout).Encode(output)
		} else {
			output := iporgdb.ToLookupResult(res.ip, res.rec)
			json.NewEncoder(os.Stdout).Encode(output)
		}
	}

	fmt.Fprintf(os.Stderr, "Processed %d IPs\n", len(ips))
}
