package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"iporg/pkg/ripebulk"
)

const version = "0.1.0"

func main() {
	var (
		dbPath      = flag.String("db", "data/ripe-bulk.ldb", "Path to output LevelDB database")
		cacheDir    = flag.String("cache", "cache/ripe", "Path to cache directory for RIPE dumps")
		baseURL     = flag.String("url", ripebulk.DefaultBaseURL, "RIPE FTP base URL")
		skipFetch   = flag.Bool("skip-fetch", false, "Skip fetching, use cached files only")
		showVersion = flag.Bool("version", false, "Show version and exit")
	)

	flag.Parse()

	if *showVersion {
		fmt.Printf("ripe-bulk-build v%s\n", version)
		os.Exit(0)
	}

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("INFO: RIPE Bulk Database Builder v%s", version)

	startTime := time.Now()

	// Create context
	ctx := context.Background()

	// Fetch RIPE dumps
	var inetnumPath, orgPath string
	var err error

	if *skipFetch {
		log.Printf("INFO: Skipping fetch, using cached files")
		inetnumPath = filepath.Join(*cacheDir, ripebulk.InetnumFile)
		orgPath = filepath.Join(*cacheDir, ripebulk.OrganisationFile)

		// Verify cache files exist
		if _, err := os.Stat(inetnumPath); err != nil {
			log.Fatalf("ERROR: Cached inetnum file not found: %s", inetnumPath)
		}
		if _, err := os.Stat(orgPath); err != nil {
			log.Fatalf("ERROR: Cached organisation file not found: %s", orgPath)
		}
	} else {
		fetcher := ripebulk.NewFetcher(*baseURL, *cacheDir)
		inetnumPath, orgPath, err = fetcher.FetchAll(ctx)
		if err != nil {
			log.Fatalf("ERROR: Failed to fetch RIPE dumps: %v", err)
		}
	}

	// Parse organisations first
	log.Printf("INFO: Parsing organisations from %s", filepath.Base(orgPath))
	orgFile, err := ripebulk.OpenGzipFile(orgPath)
	if err != nil {
		log.Fatalf("ERROR: Failed to open organisation file: %v", err)
	}

	orgs, err := ripebulk.ParseOrganisations(orgFile)
	orgFile.Close()
	if err != nil {
		log.Fatalf("ERROR: Failed to parse organisations: %v", err)
	}
	log.Printf("INFO: Parsed %d organisations", len(orgs))

	// Parse inetnums
	log.Printf("INFO: Parsing inetnums from %s", filepath.Base(inetnumPath))
	inetnumFile, err := ripebulk.OpenGzipFile(inetnumPath)
	if err != nil {
		log.Fatalf("ERROR: Failed to open inetnum file: %v", err)
	}

	inetnums, err := ripebulk.ParseInetnums(inetnumFile)
	inetnumFile.Close()
	if err != nil {
		log.Fatalf("ERROR: Failed to parse inetnums: %v", err)
	}
	log.Printf("INFO: Parsed %d inetnums", len(inetnums))

	// Remove existing database if present
	if _, err := os.Stat(*dbPath); err == nil {
		log.Printf("INFO: Removing existing database at %s", *dbPath)
		if err := os.RemoveAll(*dbPath); err != nil {
			log.Fatalf("ERROR: Failed to remove existing database: %v", err)
		}
	}

	// Create database directory
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0755); err != nil {
		log.Fatalf("ERROR: Failed to create database directory: %v", err)
	}

	// Build database
	db, err := ripebulk.BuildDatabase(*dbPath, inetnums, orgs)
	if err != nil {
		log.Fatalf("ERROR: Failed to build database: %v", err)
	}
	defer db.Close()

	// Verify database
	log.Printf("INFO: Verifying database...")
	meta, err := db.GetMetadata()
	if err != nil {
		log.Fatalf("ERROR: Failed to read metadata: %v", err)
	}

	log.Printf("INFO: ============================================")
	log.Printf("INFO: Database build complete!")
	log.Printf("INFO: Path:              %s", *dbPath)
	log.Printf("INFO: Schema version:    %d", meta.SchemaVersion)
	log.Printf("INFO: Build time:        %s", meta.BuildTime.Format(time.RFC3339))
	log.Printf("INFO: Inetnum count:     %d", meta.InetnumCount)
	log.Printf("INFO: Organisation count: %d", meta.OrgCount)
	log.Printf("INFO: Source URL:        %s", meta.SourceURL)
	log.Printf("INFO: Elapsed time:      %s", time.Since(startTime))
	log.Printf("INFO: ============================================")

	// Sanity check: lookup test
	log.Printf("INFO: Running sanity check...")
	testIP := "31.90.1.1" // Known EE/BT range in RIPE
	match, err := db.LookupIP(mustParseIP(testIP))
	if err != nil {
		log.Printf("WARN: Sanity check lookup failed (may be expected): %v", err)
	} else {
		log.Printf("INFO: Sanity check successful:")
		log.Printf("INFO:   IP: %s", testIP)
		log.Printf("INFO:   Range: %s - %s", match.Start, match.End)
		log.Printf("INFO:   Org: %s (%s)", match.OrgName, match.OrgID)
		log.Printf("INFO:   Status: %s", match.Status)
		log.Printf("INFO:   Country: %s", match.Country)
	}
}

func mustParseIP(s string) netip.Addr {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		panic(err)
	}
	return addr
}
