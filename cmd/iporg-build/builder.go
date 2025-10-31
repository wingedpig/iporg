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
	"sync/atomic"
	"time"

	"github.com/wingedpig/iporg/pkg/arinbulk"
	"github.com/wingedpig/iporg/pkg/iporgdb"
	"github.com/wingedpig/iporg/pkg/iptoasn"
	"github.com/wingedpig/iporg/pkg/model"
	"github.com/wingedpig/iporg/pkg/ripebulk"
	"github.com/wingedpig/iporg/pkg/sources/maxmind"
	"github.com/wingedpig/iporg/pkg/sources/rdap"
	"github.com/wingedpig/iporg/pkg/sources/ripe"
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
	ripeBulkDB   *ripebulk.Database // Optional: RIPE bulk database for RIPE region
	arinBulkDB   *arinbulk.Database // Optional: ARIN bulk database for ARIN region
	iptoasnStore *iptoasn.Store     // Optional: iptoasn database for prefix lookups
	stats        BuildStats
}

// BuildStats tracks build progress
type BuildStats struct {
	StartTime         time.Time
	ASNsProcessed     int
	PrefixesFetched   int
	PrefixesProcessed int
	RecordsWritten    int
	RecordsUpdated    int
	RecordsSkipped    int
	RDAPCacheHits     int
	RDAPCacheMisses   int
	RIPEBulkHits      int
	ARINBulkHits      int
	Errors            int
	// Timing breakdowns - use atomic int64 for nanosecond counts
	TimeMaxMindASNNanos   int64 // Accessed with atomic operations
	TimeMaxMindGeoNanos   int64
	TimeRIPEBulkNanos     int64
	TimeARINBulkNanos     int64
	TimeRDAPNanos         int64
	TimeDBWriteNanos      int64
	CallsMaxMindASN       int64
	CallsMaxMindGeo       int64
	CallsRIPEBulk         int64
	CallsARINBulk         int64
	CallsRDAP             int64
	CallsDBWrite          int64
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

	// Step 0.5: Open iptoasn database if configured (needed for --all-asns)
	if err := b.openIPtoASN(); err != nil {
		return fmt.Errorf("failed to open iptoasn database: %w", err)
	}
	if b.iptoasnStore != nil {
		defer b.iptoasnStore.Close()
	}

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

	// Step 4.5: Open RIPE bulk database (optional)
	if err := b.openRIPEBulk(); err != nil {
		return fmt.Errorf("failed to open RIPE bulk database: %w", err)
	}
	if b.ripeBulkDB != nil {
		defer b.ripeBulkDB.Close()
	}

	// Step 4.6: Open ARIN bulk database (optional)
	if err := b.openARINBulk(); err != nil {
		return fmt.Errorf("failed to open ARIN bulk database: %w", err)
	}
	if b.arinBulkDB != nil {
		defer b.arinBulkDB.Close()
	}

	// Step 5: Initialize/update metadata
	if err := b.db.InitializeMetadata(version); err != nil {
		return fmt.Errorf("failed to initialize metadata: %w", err)
	}

	// Step 6: Fetch announced prefixes
	var allPrefixes []string

	if b.cfg.IPtoASNDBPath != "" {
		log.Printf("INFO: Using iptoasn database: %s", b.cfg.IPtoASNDBPath)
		allPrefixes, err = b.fetchAnnouncedPrefixesFromIPtoASN(ctx, asns)
	} else {
		log.Printf("INFO: Using RIPEstat API for prefix discovery")
		allPrefixes, err = b.fetchAnnouncedPrefixes(ctx, asns)
	}

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

// loadASNs loads ASNs from the input file or enumerates all ASNs from iptoasn database
func (b *Builder) loadASNs() ([]int, error) {
	// If --all-asns is specified, enumerate from iptoasn database
	if b.cfg.AllASNs {
		if b.iptoasnStore == nil {
			return nil, fmt.Errorf("--all-asns requires iptoasn database, but it's not open")
		}

		log.Println("INFO: Enumerating all ASNs from iptoasn database...")
		ctx := context.Background()
		asns, err := b.iptoasnStore.ListASNs(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list ASNs from iptoasn: %w", err)
		}

		log.Printf("INFO: Found %d ASNs in iptoasn database", len(asns))
		return asns, nil
	}

	// Otherwise, load from file
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

// openRIPEBulk opens the RIPE bulk database (optional)
func (b *Builder) openRIPEBulk() error {
	if b.cfg.RIPEBulkDBPath == "" {
		log.Println("INFO: RIPE bulk database not configured (will use RDAP for RIPE region)")
		return nil
	}

	// Use large cache size (1GB) for better performance with high concurrency
	db, err := ripebulk.OpenDatabaseWithCache(b.cfg.RIPEBulkDBPath, 1024*1024*1024)
	if err != nil {
		return fmt.Errorf("failed to open RIPE bulk database at %s: %w", b.cfg.RIPEBulkDBPath, err)
	}

	b.ripeBulkDB = db
	log.Printf("INFO: Using 1GB cache for RIPE bulk database")

	// Log metadata
	meta, err := db.GetMetadata()
	if err != nil {
		log.Printf("WARN: Failed to read RIPE bulk metadata: %v", err)
	} else {
		log.Printf("INFO: Opened RIPE bulk database: %d inetnums, %d orgs (built %s)",
			meta.InetnumCount, meta.OrgCount, meta.BuildTime.Format("2006-01-02"))
	}

	return nil
}

// openARINBulk opens the ARIN bulk database (optional)
func (b *Builder) openARINBulk() error {
	if b.cfg.ARINBulkDBPath == "" {
		log.Println("INFO: ARIN bulk database not configured (will use RDAP for ARIN region)")
		return nil
	}

	// Use large cache size (1GB) for better performance with high concurrency
	db, err := arinbulk.OpenDatabaseWithCache(b.cfg.ARINBulkDBPath, 1024*1024*1024)
	if err != nil {
		return fmt.Errorf("failed to open ARIN bulk database at %s: %w", b.cfg.ARINBulkDBPath, err)
	}

	b.arinBulkDB = db
	log.Printf("INFO: Using 1GB cache for ARIN bulk database")

	// Log metadata
	meta, err := db.GetMetadata()
	if err != nil {
		log.Printf("WARN: Failed to read ARIN bulk metadata: %v", err)
	} else {
		log.Printf("INFO: Opened ARIN bulk database: %d networks, %d orgs (built %s)",
			meta.NetBlockCount, meta.OrgCount, meta.BuildTime.Format("2006-01-02"))
	}

	return nil
}

// openIPtoASN opens the iptoasn database (optional)
func (b *Builder) openIPtoASN() error {
	if b.cfg.IPtoASNDBPath == "" {
		return nil
	}

	store, err := iptoasn.Open(b.cfg.IPtoASNDBPath)
	if err != nil {
		return fmt.Errorf("failed to open iptoasn database at %s: %w", b.cfg.IPtoASNDBPath, err)
	}

	b.iptoasnStore = store

	// Log stats
	stats, err := store.GetStats()
	if err != nil {
		log.Printf("WARN: Failed to read iptoasn stats: %v", err)
	} else {
		log.Printf("INFO: Opened iptoasn database: %d prefixes, %d unique ASNs",
			stats.TotalPrefixes, stats.UniqueASNs)
	}

	return nil
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

// fetchAnnouncedPrefixesFromIPtoASN fetches prefixes from iptoasn database
func (b *Builder) fetchAnnouncedPrefixesFromIPtoASN(ctx context.Context, asns []int) ([]string, error) {
	log.Printf("INFO: Fetching prefixes for %d ASNs from iptoasn database...", len(asns))

	if b.iptoasnStore == nil {
		return nil, fmt.Errorf("iptoasn database is not open")
	}

	if b.cfg.IPv4Only {
		log.Println("INFO: IPv4-only mode enabled - skipping IPv6 prefixes")
	}

	if b.cfg.BulkOnly {
		log.Println("INFO: Bulk-only mode enabled - will skip prefixes without bulk coverage during enrichment")
	}

	// Fetch prefixes for each ASN
	prefixSet := make(map[string]bool)

	for _, asn := range asns {
		b.stats.ASNsProcessed++

		// Get prefixes for this ASN (raw, not collapsed)
		prefixes, err := b.iptoasnStore.ListByASN(ctx, asn, false)
		if err == model.ErrNotFound {
			log.Printf("WARN: AS%d not found in iptoasn database", asn)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to fetch prefixes for AS%d: %w", asn, err)
		}

		var ipv4Count, ipv6Count int
		for _, prefix := range prefixes {
			// Skip IPv6 if IPv4-only mode is enabled
			if b.cfg.IPv4Only && isIPv6Prefix(prefix.CIDR) {
				ipv6Count++
				continue
			}

			// Note: bulk-only filtering happens during enrichment to avoid
			// doing 1.6M database lookups here. We'll skip during enrichment.

			prefixSet[prefix.CIDR] = true

			if isIPv6Prefix(prefix.CIDR) {
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

	// Convert to slice
	allPrefixes := make([]string, 0, len(prefixSet))
	for prefix := range prefixSet {
		allPrefixes = append(allPrefixes, prefix)
	}

	log.Printf("INFO: Total unique prefixes from iptoasn: %d", len(allPrefixes))
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

// tryRIPEBulkLookup attempts to fetch org info from RIPE bulk database
// Returns nil if RIPE bulk is not configured or IP not found
func (b *Builder) tryRIPEBulkLookup(ip netip.Addr) *model.RDAPOrg {
	if b.ripeBulkDB == nil {
		return nil
	}

	// IPv6 not supported in RIPE bulk yet
	if !ip.Is4() {
		return nil
	}

	match, err := b.ripeBulkDB.LookupIP(ip)
	if err != nil || match == nil {
		// Not found in RIPE region, filtered out, or error
		return nil
	}

	// Filter out RIPE's placeholder entries for non-RIPE address space
	// These are ranges that RIPE tracks but doesn't manage (ARIN, APNIC, etc.)
	if isRIPEPlaceholder(match.OrgName) {
		return nil
	}

	// Convert RIPE bulk match to RDAPOrg format
	return &model.RDAPOrg{
		OrgName:     match.OrgName,
		RIR:         "RIPE",
		SourceRole:  "ripe_bulk", // Custom source role
		StatusLabel: match.Status,
	}
}

// tryRIPEBulkLookupPrefix attempts to fetch org info from RIPE bulk database for a prefix
// Returns nil if RIPE bulk is not configured or prefix not found
func (b *Builder) tryRIPEBulkLookupPrefix(prefix netip.Prefix) *model.RDAPOrg {
	if b.ripeBulkDB == nil {
		return nil
	}

	// IPv6 not supported in RIPE bulk yet
	if !prefix.Addr().Is4() {
		return nil
	}

	match, err := b.ripeBulkDB.LookupPrefix(prefix)
	if err != nil || match == nil {
		// Not found in RIPE region, filtered out, or error
		return nil
	}

	// Filter out RIPE's placeholder entries for non-RIPE address space
	if isRIPEPlaceholder(match.OrgName) {
		return nil
	}

	// Convert RIPE bulk match to RDAPOrg format
	return &model.RDAPOrg{
		OrgName:     match.OrgName,
		RIR:         "RIPE",
		SourceRole:  "ripe_bulk",
		StatusLabel: match.Status,
	}
}

// isRIPEPlaceholder checks if an organization name is a RIPE placeholder
// for non-RIPE address space (ARIN, APNIC, etc.)
func isRIPEPlaceholder(orgName string) bool {
	placeholders := []string{
		"NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK",
		"UNALLOCATED",
		"RESERVED",
	}

	for _, placeholder := range placeholders {
		if orgName == placeholder {
			return true
		}
	}

	return false
}

// tryARINBulkLookup attempts to fetch org info from ARIN bulk database
// Returns nil if ARIN bulk is not configured or IP not found
func (b *Builder) tryARINBulkLookup(ip netip.Addr) *model.RDAPOrg {
	if b.arinBulkDB == nil {
		return nil
	}

	// IPv6 not supported in ARIN bulk yet
	if !ip.Is4() {
		return nil
	}

	match, err := b.arinBulkDB.LookupIP(ip)
	if err != nil || match == nil {
		// Not found in ARIN region or error
		return nil
	}

	// Convert ARIN bulk match to RDAPOrg format
	return &model.RDAPOrg{
		OrgName:     match.OrgName,
		RIR:         "ARIN",
		SourceRole:  "arin_bulk",
		StatusLabel: match.NetType,
	}
}

// tryARINBulkLookupPrefix attempts to fetch org info from ARIN bulk database for a prefix
// Returns nil if ARIN bulk is not configured or prefix not found
func (b *Builder) tryARINBulkLookupPrefix(prefix netip.Prefix) *model.RDAPOrg {
	if b.arinBulkDB == nil {
		return nil
	}

	// IPv6 not supported in ARIN bulk yet
	if !prefix.Addr().Is4() {
		return nil
	}

	match, err := b.arinBulkDB.LookupPrefix(prefix)
	if err != nil || match == nil {
		// Not found in ARIN region or error
		return nil
	}

	// Convert ARIN bulk match to RDAPOrg format
	return &model.RDAPOrg{
		OrgName:     match.OrgName,
		RIR:         "ARIN",
		SourceRole:  "arin_bulk",
		StatusLabel: match.NetType,
	}
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
	if b.ripeBulkDB != nil {
		fmt.Printf("RIPE bulk hits:         %d\n", b.stats.RIPEBulkHits)
	}
	if b.arinBulkDB != nil {
		fmt.Printf("ARIN bulk hits:         %d\n", b.stats.ARINBulkHits)
	}
	fmt.Printf("RDAP cache hits:        %d\n", b.stats.RDAPCacheHits)
	fmt.Printf("RDAP cache misses:      %d\n", b.stats.RDAPCacheMisses)
	fmt.Printf("Errors:                 %d\n", b.stats.Errors)

	// Print timing breakdown
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("TIMING BREAKDOWN")
	fmt.Println(strings.Repeat("=", 60))

	// Convert atomic nanoseconds to Duration for reporting
	timeMaxMindASN := time.Duration(atomic.LoadInt64(&b.stats.TimeMaxMindASNNanos))
	timeMaxMindGeo := time.Duration(atomic.LoadInt64(&b.stats.TimeMaxMindGeoNanos))
	timeRIPEBulk := time.Duration(atomic.LoadInt64(&b.stats.TimeRIPEBulkNanos))
	timeARINBulk := time.Duration(atomic.LoadInt64(&b.stats.TimeARINBulkNanos))
	timeRDAP := time.Duration(atomic.LoadInt64(&b.stats.TimeRDAPNanos))
	timeDBWrite := time.Duration(atomic.LoadInt64(&b.stats.TimeDBWriteNanos))

	callsMaxMindASN := atomic.LoadInt64(&b.stats.CallsMaxMindASN)
	callsMaxMindGeo := atomic.LoadInt64(&b.stats.CallsMaxMindGeo)
	callsRIPEBulk := atomic.LoadInt64(&b.stats.CallsRIPEBulk)
	callsARINBulk := atomic.LoadInt64(&b.stats.CallsARINBulk)
	callsRDAP := atomic.LoadInt64(&b.stats.CallsRDAP)
	callsDBWrite := atomic.LoadInt64(&b.stats.CallsDBWrite)

	// Calculate total accumulated work time (across all parallel workers)
	totalWork := timeMaxMindASN + timeMaxMindGeo + timeRIPEBulk + timeARINBulk + timeRDAP + timeDBWrite
	parallelismFactor := totalWork.Seconds() / elapsed.Seconds()

	fmt.Printf("MaxMind ASN lookups:    %s (%.1f%% of work) - %d calls, %.2fms avg\n",
		timeMaxMindASN.Round(time.Millisecond),
		100*timeMaxMindASN.Seconds()/totalWork.Seconds(),
		callsMaxMindASN,
		float64(timeMaxMindASN.Microseconds())/float64(callsMaxMindASN)/1000.0)
	fmt.Printf("MaxMind Geo lookups:    %s (%.1f%% of work) - %d calls, %.2fms avg\n",
		timeMaxMindGeo.Round(time.Millisecond),
		100*timeMaxMindGeo.Seconds()/totalWork.Seconds(),
		callsMaxMindGeo,
		float64(timeMaxMindGeo.Microseconds())/float64(callsMaxMindGeo)/1000.0)
	fmt.Printf("RIPE bulk lookups:      %s (%.1f%% of work) - %d calls, %.2fms avg\n",
		timeRIPEBulk.Round(time.Millisecond),
		100*timeRIPEBulk.Seconds()/totalWork.Seconds(),
		callsRIPEBulk,
		float64(timeRIPEBulk.Microseconds())/float64(callsRIPEBulk)/1000.0)
	fmt.Printf("ARIN bulk lookups:      %s (%.1f%% of work) - %d calls, %.2fms avg\n",
		timeARINBulk.Round(time.Millisecond),
		100*timeARINBulk.Seconds()/totalWork.Seconds(),
		callsARINBulk,
		float64(timeARINBulk.Microseconds())/float64(callsARINBulk)/1000.0)
	fmt.Printf("RDAP lookups:           %s (%.1f%% of work) - %d calls, %.2fms avg\n",
		timeRDAP.Round(time.Millisecond),
		100*timeRDAP.Seconds()/totalWork.Seconds(),
		callsRDAP,
		float64(timeRDAP.Microseconds())/float64(callsRDAP)/1000.0)
	fmt.Printf("Database writes:        %s (%.1f%% of work) - %d calls, %.2fms avg\n",
		timeDBWrite.Round(time.Millisecond),
		100*timeDBWrite.Seconds()/totalWork.Seconds(),
		callsDBWrite,
		float64(timeDBWrite.Microseconds())/float64(callsDBWrite)/1000.0)

	fmt.Printf("\nTotal work time:        %s (%.1fx parallelism)\n",
		totalWork.Round(time.Millisecond),
		parallelismFactor)
	fmt.Println(strings.Repeat("=", 60))

	if b.stats.Errors > 0 {
		fmt.Printf("\nWARN: Build completed with %d errors (see log above)\n", b.stats.Errors)
	}
}
