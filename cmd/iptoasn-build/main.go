package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"iporg/pkg/iptoasn"
	"iporg/pkg/model"
)

const version = "0.1.0"

func main() {
	// Subcommands
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "fetch":
		runFetch()
	case "build":
		runBuild()
	case "all":
		runAll()
	case "stats":
		runStats()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: iptoasn-build <command> [options]

Commands:
  fetch   Download iptoasn data if changed (ETag/Last-Modified)
  build   Parse and build database from cached data
  all     Fetch + build (default workflow)
  stats   Show database statistics

Global options:
  --db=<path>           Database path (default: ./iptoasndb)
  --url=<url>           Source URL (default: https://iptoasn.com/data/ip2asn-v4.tsv.gz)
  --cache-dir=<path>    Cache directory (default: ./cache/iptoasn)
  --skip-download       Skip download, use cached file
  --collapse            Collapse adjacent prefixes per ASN (default: true)
  --workers=<n>         Concurrent workers (default: 4)
  --version             Show version

Examples:
  # Fetch + build in one step
  iptoasn-build all --db=./data/iptoasndb

  # Fetch only
  iptoasn-build fetch --cache-dir=./cache

  # Build from cached data
  iptoasn-build build --db=./data/iptoasndb --skip-download

  # Show stats
  iptoasn-build stats --db=./data/iptoasndb
`)
}

type Config struct {
	dbPath       string
	sourceURL    string
	cacheDir     string
	skipDownload bool
	collapse     bool
	workers      int
	showVersion  bool
}

func parseFlags(args []string) *Config {
	fs := flag.NewFlagSet("iptoasn-build", flag.ExitOnError)

	cfg := &Config{}
	fs.StringVar(&cfg.dbPath, "db", "./iptoasndb", "Database path")
	fs.StringVar(&cfg.sourceURL, "url", iptoasn.DefaultSourceURL, "Source URL")
	fs.StringVar(&cfg.cacheDir, "cache-dir", "./cache/iptoasn", "Cache directory")
	fs.BoolVar(&cfg.skipDownload, "skip-download", false, "Skip download, use cached file")
	fs.BoolVar(&cfg.collapse, "collapse", true, "Collapse adjacent prefixes per ASN")
	fs.IntVar(&cfg.workers, "workers", 4, "Concurrent workers")
	fs.BoolVar(&cfg.showVersion, "version", false, "Show version")

	fs.Parse(args)

	if cfg.showVersion {
		fmt.Printf("iptoasn-build version %s\n", version)
		os.Exit(0)
	}

	return cfg
}

func runFetch() {
	cfg := parseFlags(os.Args[2:])

	fetcher := iptoasn.NewFetcher(cfg.sourceURL, cfg.cacheDir)

	log.Printf("Fetching from %s...", cfg.sourceURL)
	meta, err := fetcher.Fetch(context.Background())
	if err != nil {
		log.Fatalf("Fetch failed: %v", err)
	}

	log.Printf("Fetch complete:")
	log.Printf("  Cache path: %s", meta.CachePath)
	log.Printf("  ETag: %s", meta.ETag)
	log.Printf("  Last-Modified: %s", meta.LastModified)
	log.Printf("  Fetched at: %s", meta.FetchedAt)
}

func runBuild() {
	cfg := parseFlags(os.Args[2:])

	builder := NewBuilder(cfg)

	log.Printf("Building database at %s...", cfg.dbPath)
	if err := builder.Build(context.Background()); err != nil {
		log.Fatalf("Build failed: %v", err)
	}

	log.Printf("Build complete!")
}

func runAll() {
	cfg := parseFlags(os.Args[2:])

	// Fetch
	if !cfg.skipDownload {
		fetcher := iptoasn.NewFetcher(cfg.sourceURL, cfg.cacheDir)
		log.Printf("Fetching from %s...", cfg.sourceURL)
		meta, err := fetcher.Fetch(context.Background())
		if err != nil {
			log.Fatalf("Fetch failed: %v", err)
		}
		log.Printf("Downloaded to %s", meta.CachePath)
	}

	// Build
	builder := NewBuilder(cfg)
	log.Printf("Building database at %s...", cfg.dbPath)
	if err := builder.Build(context.Background()); err != nil {
		log.Fatalf("Build failed: %v", err)
	}

	log.Printf("Build complete!")
}

func runStats() {
	cfg := parseFlags(os.Args[2:])

	store, err := iptoasn.Open(cfg.dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer store.Close()

	stats, err := store.GetStats()
	if err == model.ErrNotFound {
		log.Printf("No statistics found in database")
		return
	}
	if err != nil {
		log.Fatalf("Failed to get stats: %v", err)
	}

	fmt.Printf("IPToASN Database Statistics\n")
	fmt.Printf("===========================\n\n")
	fmt.Printf("Total prefixes:     %d\n", stats.TotalPrefixes)
	fmt.Printf("IPv4 prefixes:      %d\n", stats.IPv4Prefixes)
	fmt.Printf("Collapsed (IPv4):   %d\n", stats.CollapsedV4)
	fmt.Printf("Unique ASNs:        %d\n", stats.UniqueASNs)
	fmt.Printf("\nSource URL:         %s\n", stats.SourceURL)
	fmt.Printf("Last modified:      %s\n", stats.LastModified.Format(time.RFC3339))
	fmt.Printf("Built at:           %s\n", stats.BuiltAt.Format(time.RFC3339))

	if len(stats.ByRegistry) > 0 {
		fmt.Printf("\nBy Registry:\n")
		for reg, count := range stats.ByRegistry {
			fmt.Printf("  %-10s %d\n", reg, count)
		}
	}
}
