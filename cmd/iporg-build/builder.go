package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"iporg/pkg/iporgdb"
	"iporg/pkg/model"
	"iporg/pkg/sources/maxmind"
	"iporg/pkg/sources/rdap"
	"iporg/pkg/sources/ripe"
)

// Builder orchestrates the database build process
type Builder struct {
	cfg          *model.BuildConfig
	minPrefixV4  int
	minPrefixV6  int
	db           *iporgdb.DB
	maxmind      *maxmind.Readers
	ripeClient   *ripe.Client
	rdapClient   *rdap.CachedClient
	stats        BuildStats
}

// BuildStats tracks build progress
type BuildStats struct {
	StartTime          time.Time
	ASNsProcessed      int
	PrefixesFetched    int
	PrefixesProcessed  int
	RecordsWritten     int
	RecordsUpdated     int
	RecordsSkipped     int
	RDAPCacheHits      int
	RDAPCacheMisses    int
	Errors             int
}

// NewBuilder creates a new database builder
func NewBuilder(cfg *model.BuildConfig, minPrefixV4, minPrefixV6 int) *Builder {
	return &Builder{
		cfg:         cfg,
		minPrefixV4: minPrefixV4,
		minPrefixV6: minPrefixV6,
		stats: BuildStats{
			StartTime: time.Now(),
		},
	}
}

// Build executes the complete build pipeline
func (b *Builder) Build(ctx context.Context) error {
	log.Println("INFO: Starting build process...")

	// Step 1: Load ASNs
	asns, err := b.loadASNs()
	if err != nil {
		return fmt.Errorf("failed to load ASNs: %w", err)
	}
	log.Printf("INFO: Loaded %d ASNs", len(asns))

	// Step 2: Open database
	if err := b.openDatabase(); err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer b.db.Close()

	// Step 3: Open MaxMind databases
	if err := b.openMaxMind(); err != nil {
		return fmt.Errorf("failed to open MaxMind databases: %w", err)
	}
	defer b.maxmind.Close()

	// Step 4: Initialize API clients
	b.initializeClients()

	// Step 5: Initialize/update metadata
	if err := b.db.InitializeMetadata(version); err != nil {
		return fmt.Errorf("failed to initialize metadata: %w", err)
	}

	// Step 6: Fetch announced prefixes
	allPrefixes, err := b.fetchAnnouncedPrefixes(ctx, asns)
	if err != nil {
		return fmt.Errorf("failed to fetch announced prefixes: %w", err)
	}
	log.Printf("INFO: Fetched %d unique prefixes", len(allPrefixes))

	// Step 7: Enrich and write records
	if err := b.enrichAndWrite(ctx, allPrefixes); err != nil {
		return fmt.Errorf("failed to enrich and write records: %w", err)
	}

	// Step 8: Print summary
	b.printSummary()

	return nil
}

// loadASNs loads ASNs from the input file
func (b *Builder) loadASNs() ([]int, error) {
	file, err := os.Open(b.cfg.ASNFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var asns []int
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Remove "AS" prefix if present
		line = strings.TrimPrefix(line, "AS")
		line = strings.TrimPrefix(line, "as")

		asn, err := strconv.Atoi(line)
		if err != nil {
			log.Printf("WARN: Invalid ASN on line %d: %s", lineNum, scanner.Text())
			continue
		}

		if asn <= 0 {
			log.Printf("WARN: Invalid ASN on line %d: %d", lineNum, asn)
			continue
		}

		asns = append(asns, asn)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(asns) == 0 {
		return nil, fmt.Errorf("no valid ASNs found in file")
	}

	return asns, nil
}

// openDatabase opens or creates the LevelDB database
func (b *Builder) openDatabase() error {
	db, err := iporgdb.Open(b.cfg.DBPath)
	if err != nil {
		return err
	}
	b.db = db
	log.Printf("INFO: Opened database at %s", b.cfg.DBPath)
	return nil
}

// openMaxMind opens the MaxMind database readers
func (b *Builder) openMaxMind() error {
	readers, err := maxmind.Open(b.cfg.MMDBASNPath, b.cfg.MMDBCityPath)
	if err != nil {
		return err
	}
	b.maxmind = readers
	log.Println("INFO: Opened MaxMind databases")
	return nil
}

// initializeClients initializes API clients
func (b *Builder) initializeClients() {
	// RIPE client
	b.ripeClient = ripe.NewClient(
		b.cfg.RIPEBaseURL,
		b.cfg.UserAgent,
		10.0, // 10 req/s for RIPEstat
	)

	// RDAP client with caching
	rdapClient := rdap.NewClient(
		b.cfg.RDAPBootstrapURL,
		b.cfg.UserAgent,
		b.cfg.RDAPRateLimit,
	)
	b.rdapClient = rdap.NewCachedClient(rdapClient, b.db, b.cfg.CacheTTL)

	log.Println("INFO: Initialized API clients")
}

// fetchAnnouncedPrefixes fetches all announced prefixes for the given ASNs
func (b *Builder) fetchAnnouncedPrefixes(ctx context.Context, asns []int) ([]string, error) {
	log.Printf("INFO: Fetching announced prefixes for %d ASNs...", len(asns))

	if b.cfg.IPv4Only {
		log.Println("INFO: IPv4-only mode enabled - skipping IPv6 prefixes")
	}

	asnPrefixes, err := b.ripeClient.FetchAnnouncedPrefixesForASNs(ctx, asns, b.cfg.Workers)
	if err != nil {
		return nil, err
	}

	// Deduplicate and collect all prefixes
	prefixSet := make(map[string]bool)
	var skippedIPv6 int

	for asn, prefixes := range asnPrefixes {
		b.stats.ASNsProcessed++
		var ipv4Count, ipv6Count int

		for _, prefix := range prefixes {
			// Skip IPv6 if IPv4-only mode is enabled
			if b.cfg.IPv4Only && isIPv6Prefix(prefix) {
				skippedIPv6++
				ipv6Count++
				continue
			}

			prefixSet[prefix] = true
			if isIPv6Prefix(prefix) {
				ipv6Count++
			} else {
				ipv4Count++
			}
		}

		b.stats.PrefixesFetched += len(prefixes)
		if b.cfg.IPv4Only {
			log.Printf("INFO: AS%d: %d IPv4 prefixes (%d IPv6 skipped)", asn, ipv4Count, ipv6Count)
		} else {
			log.Printf("INFO: AS%d: %d IPv4, %d IPv6 prefixes", asn, ipv4Count, ipv6Count)
		}
	}

	if skippedIPv6 > 0 {
		log.Printf("INFO: Skipped %d IPv6 prefixes (IPv4-only mode)", skippedIPv6)
	}

	// Convert to slice
	allPrefixes := make([]string, 0, len(prefixSet))
	for prefix := range prefixSet {
		allPrefixes = append(allPrefixes, prefix)
	}

	return allPrefixes, nil
}

// isIPv6Prefix checks if a CIDR prefix is IPv6
func isIPv6Prefix(prefix string) bool {
	return strings.Contains(prefix, ":")
}

// enrichAndWrite enriches prefixes with org/geo data and writes to database
func (b *Builder) enrichAndWrite(ctx context.Context, prefixes []string) error {
	log.Printf("INFO: Enriching and writing %d prefixes...", len(prefixes))

	// Sort prefixes by specificity (least specific first)
	// This ensures we process larger blocks first and skip overlapping smaller ones
	sortedPrefixes := sortPrefixesBySpecificity(prefixes)
	log.Printf("INFO: Sorted prefixes by specificity (least to most specific)")

	if b.cfg.SplitByMaxMind {
		log.Printf("INFO: Mode B enabled: splitting by MaxMind city blocks")
		return b.enrichAndWriteModeB(ctx, sortedPrefixes)
	} else {
		log.Printf("INFO: Mode A: one record per announced prefix")
		return b.enrichAndWriteModeA(ctx, sortedPrefixes)
	}
}

// sortPrefixesBySpecificity sorts prefixes from least specific to most specific
// (i.e., /8 before /16 before /24)
func sortPrefixesBySpecificity(prefixes []string) []string {
	type prefixWithLen struct {
		prefix string
		bits   int
	}

	// Parse all prefixes and get their lengths
	parsed := make([]prefixWithLen, 0, len(prefixes))
	for _, p := range prefixes {
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			log.Printf("WARN: Failed to parse prefix %s: %v", p, err)
			continue
		}
		parsed = append(parsed, prefixWithLen{
			prefix: p,
			bits:   prefix.Bits(),
		})
	}

	// Sort by bits (ascending = least specific first)
	sort.Slice(parsed, func(i, j int) bool {
		if parsed[i].bits != parsed[j].bits {
			return parsed[i].bits < parsed[j].bits
		}
		// If same length, sort by prefix string for determinism
		return parsed[i].prefix < parsed[j].prefix
	})

	// Extract sorted prefixes
	result := make([]string, len(parsed))
	for i, p := range parsed {
		result[i] = p.prefix
	}

	return result
}

// printSummary prints build statistics
func (b *Builder) printSummary() {
	elapsed := time.Since(b.stats.StartTime)

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("BUILD SUMMARY")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Duration:               %s\n", elapsed.Round(time.Second))
	fmt.Printf("ASNs processed:         %d\n", b.stats.ASNsProcessed)
	fmt.Printf("Prefixes fetched:       %d\n", b.stats.PrefixesFetched)
	fmt.Printf("Prefixes processed:     %d\n", b.stats.PrefixesProcessed)
	fmt.Printf("Records written:        %d\n", b.stats.RecordsWritten)
	fmt.Printf("Records updated:        %d\n", b.stats.RecordsUpdated)
	fmt.Printf("Records skipped:        %d\n", b.stats.RecordsSkipped)
	fmt.Printf("RDAP cache hits:        %d\n", b.stats.RDAPCacheHits)
	fmt.Printf("RDAP cache misses:      %d\n", b.stats.RDAPCacheMisses)
	fmt.Printf("Errors:                 %d\n", b.stats.Errors)
	fmt.Println(strings.Repeat("=", 60))

	if b.stats.Errors > 0 {
		fmt.Printf("\nWARN: Build completed with %d errors (see log above)\n", b.stats.Errors)
	}
}
