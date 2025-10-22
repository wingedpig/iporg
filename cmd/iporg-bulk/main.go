package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"iporg/pkg/iporgdb"
	"iporg/pkg/model"
	"iporg/pkg/util/workers"
)

const version = "1.0.0"

func main() {
	// Parse flags
	dbPath := flag.String("db", "./iporgdb", "Path to LevelDB database")
	inputFile := flag.String("input", "", "Input file (one IP per line, default: stdin)")
	outputFile := flag.String("output", "", "Output file (JSONL format, default: stdout)")
	workerCount := flag.Int("workers", 10, "Number of concurrent workers")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("iporg-bulk version %s\n", version)
		return
	}

	// Open database
	db, err := iporgdb.Open(*dbPath)
	if err != nil {
		log.Fatalf("ERROR: Failed to open database: %v", err)
	}
	defer db.Close()

	// Setup input
	var input *os.File
	if *inputFile == "" {
		input = os.Stdin
		log.Println("INFO: Reading from stdin (one IP per line)")
	} else {
		f, err := os.Open(*inputFile)
		if err != nil {
			log.Fatalf("ERROR: Failed to open input file: %v", err)
		}
		defer f.Close()
		input = f
		log.Printf("INFO: Reading from %s", *inputFile)
	}

	// Setup output
	var output *os.File
	if *outputFile == "" {
		output = os.Stdout
	} else {
		f, err := os.Create(*outputFile)
		if err != nil {
			log.Fatalf("ERROR: Failed to create output file: %v", err)
		}
		defer f.Close()
		output = f
		log.Printf("INFO: Writing to %s", *outputFile)
	}

	// Read all IPs
	var ips []string
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ips = append(ips, line)
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("ERROR: Failed to read input: %v", err)
	}

	log.Printf("INFO: Processing %d IPs with %d workers", len(ips), *workerCount)

	// Process IPs
	ctx := context.Background()
	pool := workers.NewPool(ctx, workers.Config{
		Workers:   *workerCount,
		RateLimit: 0, // No rate limit for local lookups
	})

	type result struct {
		index int
		data  *model.LookupResult
		err   error
	}

	results := make([]result, len(ips))
	var mu sync.Mutex
	var processed, found, notFound, errors int

	for i, ip := range ips {
		idx := i
		currentIP := ip

		pool.Submit(idx, func(ctx context.Context) error {
			rec, err := db.LookupString(currentIP)
			if err != nil {
				if err == model.ErrNotFound {
					mu.Lock()
					notFound++
					mu.Unlock()
					results[idx] = result{
						index: idx,
						data: &model.LookupResult{
							IP: currentIP,
						},
						err: err,
					}
				} else {
					mu.Lock()
					errors++
					mu.Unlock()
					results[idx] = result{
						index: idx,
						err:   err,
					}
				}
				return nil
			}

			mu.Lock()
			found++
			mu.Unlock()

			results[idx] = result{
				index: idx,
				data:  iporgdb.ToLookupResult(currentIP, rec),
			}
			return nil
		})
	}

	// Wait for completion
	pool.Wait()

	// Write results in order
	for _, res := range results {
		processed++

		if res.err != nil {
			if res.err == model.ErrNotFound {
				// Write not found result
				data := map[string]interface{}{
					"ip":    res.data.IP,
					"error": "not found",
				}
				writeJSON(output, data)
			} else {
				// Write error result
				data := map[string]interface{}{
					"error": res.err.Error(),
				}
				writeJSON(output, data)
			}
		} else {
			// Write successful result
			writeJSON(output, res.data)
		}
	}

	// Print summary to stderr
	if output != os.Stdout {
		log.Printf("INFO: Processed: %d, Found: %d, Not found: %d, Errors: %d",
			processed, found, notFound, errors)
	}
}

func writeJSON(w *os.File, data interface{}) {
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(data); err != nil {
		log.Printf("ERROR: Failed to write JSON: %v", err)
	}
}
