// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/wingedpig/iporg/pkg/iptoasn"
	"github.com/wingedpig/iporg/pkg/model"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "asn":
		runASN()
	case "walk":
		runWalk()
	case "list-asns":
		runListASNs()
	case "--version":
		fmt.Printf("iptoasn-query version %s\n", version)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: iptoasn-query <command> [options]

Commands:
  asn <number>     List all prefixes for an ASN
  walk             Iterate all prefixes in order
  list-asns        List all ASNs in database

Options:
  --db=<path>         Database path (default: ./iptoasndb)
  --collapsed         Show collapsed prefixes (for asn command)
  --json              Output as JSON (default: true)
  --limit=<n>         Limit number of results
  --offset-key=<key>  Resume walk from this key (for walk command)
  --version           Show version

Examples:
  # List all prefixes for AS2856
  iptoasn-query asn 2856

  # List collapsed prefixes for AS2856
  iptoasn-query asn 2856 --collapsed

  # Walk all prefixes (first 100)
  iptoasn-query walk --limit=100

  # List all ASNs
  iptoasn-query list-asns
`)
}

type Config struct {
	dbPath    string
	collapsed bool
	json      bool
	limit     int
	offsetKey string
}

func parseFlags(args []string) *Config {
	fs := flag.NewFlagSet("iptoasn-query", flag.ExitOnError)

	cfg := &Config{}
	fs.StringVar(&cfg.dbPath, "db", "./iptoasndb", "Database path")
	fs.BoolVar(&cfg.collapsed, "collapsed", false, "Show collapsed prefixes")
	fs.BoolVar(&cfg.json, "json", true, "Output as JSON")
	fs.IntVar(&cfg.limit, "limit", 0, "Limit number of results (0 = no limit)")
	fs.StringVar(&cfg.offsetKey, "offset-key", "", "Resume walk from this key")

	fs.Parse(args)
	return cfg
}

func runASN() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Error: ASN number required\n")
		fmt.Fprintf(os.Stderr, "Usage: iptoasn-query asn <number> [options]\n")
		os.Exit(1)
	}

	asnStr := os.Args[2]
	asn, err := strconv.Atoi(asnStr)
	if err != nil {
		log.Fatalf("Invalid ASN number: %s", asnStr)
	}

	cfg := parseFlags(os.Args[3:])

	// Open database
	store, err := iptoasn.Open(cfg.dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer store.Close()

	// Get ASN index first
	idx, err := store.GetASNIndex(asn)
	if err == model.ErrNotFound {
		fmt.Fprintf(os.Stderr, "AS%d not found in database\n", asn)
		os.Exit(1)
	}
	if err != nil {
		log.Fatalf("Failed to get ASN index: %v", err)
	}

	// List prefixes
	prefixes, err := store.ListByASN(context.Background(), asn, cfg.collapsed)
	if err != nil {
		log.Fatalf("Failed to list prefixes: %v", err)
	}

	// Apply limit if specified
	if cfg.limit > 0 && len(prefixes) > cfg.limit {
		prefixes = prefixes[:cfg.limit]
	}

	// Output
	if cfg.json {
		output := map[string]interface{}{
			"asn":       asn,
			"collapsed": cfg.collapsed,
			"count":     len(prefixes),
			"total":     idx.V4Count,
			"prefixes":  prefixes,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(output); err != nil {
			log.Fatalf("Failed to encode JSON: %v", err)
		}
	} else {
		fmt.Printf("AS%d (%d prefixes total, showing %d%s):\n",
			asn, idx.V4Count, len(prefixes),
			map[bool]string{true: " collapsed", false: ""}[cfg.collapsed])
		for _, p := range prefixes {
			fmt.Printf("  %s  %s  %s\n", p.CIDR, p.Country, p.Registry)
		}
	}
}

func runWalk() {
	cfg := parseFlags(os.Args[2:])

	// Open database
	store, err := iptoasn.Open(cfg.dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer store.Close()

	var startKey []byte
	if cfg.offsetKey != "" {
		startKey = []byte(cfg.offsetKey)
	}

	count := 0
	var prefixes []*model.CanonicalPrefix

	err = store.WalkV4(context.Background(), startKey, func(k []byte, p *model.CanonicalPrefix) (bool, error) {
		if cfg.limit > 0 && count >= cfg.limit {
			return false, nil
		}

		prefixes = append(prefixes, p)
		count++

		return true, nil
	})

	if err != nil {
		log.Fatalf("Walk failed: %v", err)
	}

	// Output
	if cfg.json {
		output := map[string]interface{}{
			"count":    count,
			"prefixes": prefixes,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(output); err != nil {
			log.Fatalf("Failed to encode JSON: %v", err)
		}
	} else {
		fmt.Printf("Walked %d prefixes:\n", count)
		for _, p := range prefixes {
			fmt.Printf("  %s  AS%d  %s  %s\n", p.CIDR, p.ASN, p.Country, p.Registry)
		}
	}
}

func runListASNs() {
	cfg := parseFlags(os.Args[2:])

	// Open database
	store, err := iptoasn.Open(cfg.dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer store.Close()

	// List ASNs
	asns, err := store.ListASNs(context.Background())
	if err != nil {
		log.Fatalf("Failed to list ASNs: %v", err)
	}

	// Apply limit if specified
	if cfg.limit > 0 && len(asns) > cfg.limit {
		asns = asns[:cfg.limit]
	}

	// Output
	if cfg.json {
		output := map[string]interface{}{
			"count": len(asns),
			"asns":  asns,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(output); err != nil {
			log.Fatalf("Failed to encode JSON: %v", err)
		}
	} else {
		fmt.Printf("Found %d ASNs:\n", len(asns))
		for _, asn := range asns {
			// Try to get index for prefix count
			if idx, err := store.GetASNIndex(asn); err == nil {
				fmt.Printf("  AS%d (%d prefixes, %d collapsed)\n", asn, idx.V4Count, idx.V4Collapsed)
			} else {
				fmt.Printf("  AS%d\n", asn)
			}
		}
	}
}
