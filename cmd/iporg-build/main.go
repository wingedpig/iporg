package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/wingedpig/iporg/pkg/model"
)

const version = "1.0.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "build":
		buildCmd()
	case "verify":
		verifyCmd()
	case "stats":
		statsCmd()
	case "debug":
		debugCmd()
	case "version":
		fmt.Printf("iporg-build version %s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`iporg-build - Build IP organization database from free sources

Usage:
  iporg-build build [options]       Build or update the database
  iporg-build verify [options]      Verify database consistency
  iporg-build stats [options]       Show database statistics
  iporg-build debug [options]       Debug IP lookup issues
  iporg-build version                Show version
  iporg-build help                   Show this help

Build Options:
  --asn-file string              Path to ASN list file (one ASN per line)
  --mmdb-asn string              Path to MaxMind GeoLite2-ASN.mmdb
  --mmdb-city string             Path to MaxMind GeoLite2-City.mmdb
  --db string                    Path to LevelDB database (default: ./iporgdb)
  --iptoasn-db string            Use iptoasn database for prefixes (default: RIPEstat API)
  --ripe-bulk-db string          Use RIPE bulk database for RIPE region (default: RDAP)
  --workers int                  Number of concurrent workers (default: 16)
  --cache-ttl duration           Cache TTL for RDAP (default: 168h)
  --ipv4-only                    Skip IPv6 prefixes (default: true)
  --split-by-maxmind             Enable Mode B: split by MaxMind city blocks
  --min-prefix-v4 int            Minimum IPv4 prefix length for Mode B (default: 20)
  --min-prefix-v6 int            Minimum IPv6 prefix length for Mode B (default: 32)
  --ripe-base string             RIPEstat base URL (default: https://stat.ripe.net)
  --rdap-bootstrap string        RDAP bootstrap URL (default: https://rdap.db.ripe.net)
  --rdap-rate-limit float        RDAP requests per second (default: 5.0)
  --user-agent string            User-Agent header (default: iporg-build/version)
  --pprof string                 Enable pprof HTTP server (e.g., localhost:6060)

Examples:
  # Build database for specific ASNs (using RIPEstat API)
  iporg-build build --asn-file=asns.txt --mmdb-asn=GeoLite2-ASN.mmdb \
    --mmdb-city=GeoLite2-City.mmdb --db=./data/iporgdb

  # Build using iptoasn database (faster, no API rate limits)
  iporg-build build --asn-file=asns.txt --mmdb-asn=GeoLite2-ASN.mmdb \
    --mmdb-city=GeoLite2-City.mmdb --db=./data/iporgdb --iptoasn-db=./iptoasndb

  # Build with Mode B (better geo accuracy)
  iporg-build build --asn-file=asns.txt --mmdb-asn=... --mmdb-city=... \
    --split-by-maxmind --min-prefix-v4=24

  # Verify database
  iporg-build verify --db=./data/iporgdb

  # Show statistics
  iporg-build stats --db=./data/iporgdb`)
}

func buildCmd() {
	fs := flag.NewFlagSet("build", flag.ExitOnError)

	cfg := &model.BuildConfig{
		UserAgent: fmt.Sprintf("iporg-build/%s", version),
	}

	// Required flags
	fs.StringVar(&cfg.ASNFile, "asn-file", "", "Path to ASN list file (required unless --all-asns)")
	fs.StringVar(&cfg.MMDBASNPath, "mmdb-asn", "", "Path to MaxMind GeoLite2-ASN.mmdb (required)")
	fs.StringVar(&cfg.MMDBCityPath, "mmdb-city", "", "Path to MaxMind GeoLite2-City.mmdb (required)")

	// Optional flags
	fs.StringVar(&cfg.DBPath, "db", "./iporgdb", "Path to LevelDB database")
	fs.BoolVar(&cfg.AllASNs, "all-asns", false, "Build for all ASNs from iptoasn database")
	fs.BoolVar(&cfg.BulkOnly, "bulk-only", false, "Only process prefixes with bulk database coverage (faster)")
	var iptoasnDB string
	fs.StringVar(&iptoasnDB, "iptoasn-db", "", "Use iptoasn database for prefixes instead of RIPEstat API")
	var ripeBulkDB string
	fs.StringVar(&ripeBulkDB, "ripe-bulk-db", "", "Use RIPE bulk database for RIPE region instead of RDAP")
	var arinBulkDB string
	fs.StringVar(&arinBulkDB, "arin-bulk-db", "", "Use ARIN bulk database for ARIN region instead of RDAP")
	fs.IntVar(&cfg.Workers, "workers", 16, "Number of concurrent workers")
	var cacheTTL string
	fs.StringVar(&cacheTTL, "cache-ttl", "168h", "Cache TTL for RDAP")
	fs.BoolVar(&cfg.SplitByMaxMind, "split-by-maxmind", false, "Enable Mode B: split by MaxMind city blocks")
	fs.BoolVar(&cfg.IPv4Only, "ipv4-only", true, "Skip IPv6 prefixes (default: true)")

	var minPrefixV4, minPrefixV6 int
	fs.IntVar(&minPrefixV4, "min-prefix-v4", 20, "Minimum IPv4 prefix length for Mode B")
	fs.IntVar(&minPrefixV6, "min-prefix-v6", 32, "Minimum IPv6 prefix length for Mode B")

	fs.StringVar(&cfg.RIPEBaseURL, "ripe-base", "https://stat.ripe.net", "RIPEstat base URL")
	fs.StringVar(&cfg.RDAPBootstrapURL, "rdap-bootstrap", "https://rdap.db.ripe.net", "RDAP bootstrap URL")
	fs.Float64Var(&cfg.RDAPRateLimit, "rdap-rate-limit", 5.0, "RDAP requests per second")
	fs.StringVar(&cfg.UserAgent, "user-agent", cfg.UserAgent, "User-Agent header")

	// Profiling flag
	var pprofAddr string
	fs.StringVar(&pprofAddr, "pprof", "", "Enable pprof HTTP server on address (e.g., localhost:6060)")

	fs.Parse(os.Args[2:])

	// Start pprof server if requested
	if pprofAddr != "" {
		go func() {
			log.Printf("INFO: Starting pprof server on http://%s/debug/pprof/", pprofAddr)
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				log.Printf("WARN: pprof server failed: %v", err)
			}
		}()
	}

	// Validate required flags
	if !cfg.AllASNs && cfg.ASNFile == "" {
		log.Fatal("ERROR: --asn-file is required (or use --all-asns)")
	}
	if cfg.AllASNs && iptoasnDB == "" {
		log.Fatal("ERROR: --all-asns requires --iptoasn-db")
	}
	if cfg.BulkOnly && ripeBulkDB == "" && arinBulkDB == "" {
		log.Fatal("ERROR: --bulk-only requires at least one of --ripe-bulk-db or --arin-bulk-db")
	}
	if cfg.MMDBASNPath == "" {
		log.Fatal("ERROR: --mmdb-asn is required")
	}
	if cfg.MMDBCityPath == "" {
		log.Fatal("ERROR: --mmdb-city is required")
	}

	// Parse cache TTL
	var err error
	cfg.CacheTTL, err = time.ParseDuration(cacheTTL)
	if err != nil {
		log.Fatalf("ERROR: Invalid cache-ttl: %v", err)
	}

	// Set iptoasn database path
	cfg.IPtoASNDBPath = iptoasnDB

	// Set RIPE bulk database path
	cfg.RIPEBulkDBPath = ripeBulkDB

	// Set ARIN bulk database path
	cfg.ARINBulkDBPath = arinBulkDB

	// Run the build
	ctx := context.Background()
	builder := NewBuilder(cfg, minPrefixV4, minPrefixV6)
	if err := builder.Build(ctx); err != nil {
		log.Fatalf("ERROR: Build failed: %v", err)
	}

	log.Println("INFO: Build completed successfully")
}

func verifyCmd() {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	dbPath := fs.String("db", "./iporgdb", "Path to LevelDB database")
	fs.Parse(os.Args[2:])

	ctx := context.Background()
	if err := RunVerify(ctx, *dbPath); err != nil {
		log.Fatalf("ERROR: Verification failed: %v", err)
	}

	log.Println("INFO: Verification completed successfully")
}

func statsCmd() {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	dbPath := fs.String("db", "./iporgdb", "Path to LevelDB database")
	verbose := fs.Bool("verbose", false, "Show detailed statistics")
	fs.Parse(os.Args[2:])

	ctx := context.Background()
	if err := RunStats(ctx, *dbPath, *verbose); err != nil {
		log.Fatalf("ERROR: Stats failed: %v", err)
	}
}

func debugCmd() {
	fs := flag.NewFlagSet("debug", flag.ExitOnError)
	ip := fs.String("ip", "", "IP address to debug (required)")
	asn := fs.Int("asn", 0, "ASN to check announced prefixes")
	mmdbASN := fs.String("mmdb-asn", "", "Path to MaxMind GeoLite2-ASN.mmdb")
	mmdbCity := fs.String("mmdb-city", "", "Path to MaxMind GeoLite2-City.mmdb")
	dbPath := fs.String("db", "./iporgdb", "Path to LevelDB database")
	ripeBase := fs.String("ripe-base", "https://stat.ripe.net", "RIPEstat base URL")
	fs.Parse(os.Args[2:])

	if *ip == "" {
		fmt.Fprintf(os.Stderr, "ERROR: --ip is required\n\n")
		fmt.Fprintf(os.Stderr, "Usage: iporg-build debug --ip=<address> [options]\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  iporg-build debug --ip=86.150.233.24 --asn=2856 \\\n")
		fmt.Fprintf(os.Stderr, "    --mmdb-asn=GeoLite2-ASN.mmdb --mmdb-city=GeoLite2-City.mmdb \\\n")
		fmt.Fprintf(os.Stderr, "    --db=./iporgdb\n")
		os.Exit(1)
	}

	ctx := context.Background()
	if err := RunDebug(ctx, *ip, *mmdbASN, *mmdbCity, *dbPath, *asn, *ripeBase); err != nil {
		log.Fatalf("ERROR: Debug failed: %v", err)
	}
}
