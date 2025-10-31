package ripebulk

import (
	"encoding/binary"
	"fmt"
	"log"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	// Key prefixes for different record types
	prefixRange    = "R4:"   // IPv4 ranges (followed by 4-byte start IP + 4-byte end IP)
	prefixOrg      = "ORG:"  // Organisations (followed by OrgID string)
	prefixMetadata = "META:" // Metadata

	// Schema version
	currentSchemaVersion = 2 // v2: Changed key format to include both start and end IP
)

// Database manages the RIPE bulk data index
type Database struct {
	db   *leveldb.DB
	path string
}

// OpenDatabase opens an existing RIPE bulk database with default cache size
func OpenDatabase(path string) (*Database, error) {
	return OpenDatabaseWithCache(path, 128*1024*1024) // 128MB default
}

// OpenDatabaseWithCache opens with custom cache size (in bytes)
func OpenDatabaseWithCache(path string, cacheSize int) (*Database, error) {
	db, err := leveldb.OpenFile(path, &opt.Options{
		Compression:        opt.SnappyCompression,
		BlockCacheCapacity: cacheSize,
		OpenFilesCacheCapacity: 500,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	return &Database{
		db:   db,
		path: path,
	}, nil
}

// Close closes the database
func (d *Database) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// isValidOrgRemark checks if a remark is valid as an organization name
// Filters out separator lines, URLs, and other noise
func isValidOrgRemark(remark string) bool {
	if len(remark) < 3 {
		return false
	}

	// Convert to lowercase for case-insensitive checks
	lower := strings.ToLower(remark)

	// Skip URLs
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return false
	}

	// Skip email addresses and mailto links
	if strings.Contains(lower, "mailto:") || strings.Contains(remark, "@") {
		return false
	}

	// Skip RIPE administrative comments (lines starting with *)
	trimmed := strings.TrimSpace(remark)
	if strings.HasPrefix(trimmed, "*") {
		return false
	}

	// Skip lines starting with dashes (separators, certificates, PEM blocks, etc.)
	if strings.HasPrefix(trimmed, "-") {
		return false
	}

	// Skip instructional/informational text
	instructionalPrefixes := []string{
		"please ", "for registration", "you can consult", "this network",
		"abuse", "contact", "send ", "see ", "visit ", "refer to",
	}
	for _, prefix := range instructionalPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}

	// Skip separator lines (lines made mostly of dashes, asterisks, equals, etc.)
	separatorChars := 0
	for _, ch := range remark {
		if ch == '-' || ch == '*' || ch == '=' || ch == '_' || ch == '#' {
			separatorChars++
		}
	}
	// If more than 80% of the line is separator characters, skip it
	if separatorChars > len(remark)*4/5 {
		return false
	}

	return true
}

// BuildDatabase creates a new RIPE bulk database from parsed data
func BuildDatabase(path string, inetnums []Inetnum, orgs map[string]Organisation) (*Database, error) {
	log.Printf("INFO: Building RIPE bulk database at %s", path)
	log.Printf("INFO: Indexing %d inetnums, %d organisations", len(inetnums), len(orgs))

	// Open database
	db, err := leveldb.OpenFile(path, &opt.Options{
		Compression:        opt.SnappyCompression,
		WriteBuffer:        64 * 1024 * 1024, // 64MB write buffer
		BlockCacheCapacity: 32 * 1024 * 1024, // 32MB block cache
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create database: %w", err)
	}

	defer func() {
		if err != nil {
			db.Close()
		}
	}()

	// Sort inetnums: Start ascending, End descending (parents before children)
	log.Printf("INFO: Sorting inetnums...")
	sort.Slice(inetnums, func(i, j int) bool {
		if inetnums[i].Start != inetnums[j].Start {
			return inetnums[i].Start < inetnums[j].Start
		}
		// For same Start, larger ranges first (End descending)
		return inetnums[i].End > inetnums[j].End
	})

	// Write ranges
	log.Printf("INFO: Writing ranges to database...")
	batch := new(leveldb.Batch)
	batchCount := 0

	for _, inet := range inetnums {
		key := makeRangeKey(inet.Start, inet.End)
		value, err := msgpack.Marshal(&inet)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal inetnum: %w", err)
		}

		batch.Put(key, value)
		batchCount++

		// Flush batch every 10k records
		if batchCount >= 10000 {
			if err := db.Write(batch, nil); err != nil {
				return nil, fmt.Errorf("failed to write batch: %w", err)
			}
			batch.Reset()
			batchCount = 0
		}
	}

	// Flush remaining ranges
	if batchCount > 0 {
		if err := db.Write(batch, nil); err != nil {
			return nil, fmt.Errorf("failed to write final batch: %w", err)
		}
	}

	// Write organisations
	log.Printf("INFO: Writing organisations to database...")
	batch.Reset()
	batchCount = 0

	for _, org := range orgs {
		key := makeOrgKey(org.OrgID)
		value, err := msgpack.Marshal(&org)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal organisation: %w", err)
		}

		batch.Put(key, value)
		batchCount++

		if batchCount >= 1000 {
			if err := db.Write(batch, nil); err != nil {
				return nil, fmt.Errorf("failed to write org batch: %w", err)
			}
			batch.Reset()
			batchCount = 0
		}
	}

	// Flush remaining orgs
	if batchCount > 0 {
		if err := db.Write(batch, nil); err != nil {
			return nil, fmt.Errorf("failed to write final org batch: %w", err)
		}
	}

	// Write metadata
	log.Printf("INFO: Writing metadata...")
	metadata := Metadata{
		SchemaVersion: currentSchemaVersion,
		BuildTime:     time.Now(),
		InetnumCount:  int64(len(inetnums)),
		OrgCount:      int64(len(orgs)),
		SourceURL:     DefaultBaseURL,
	}

	metaValue, err := msgpack.Marshal(&metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := db.Put([]byte(prefixMetadata+"build"), metaValue, nil); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	log.Printf("INFO: Database build complete - %d ranges, %d orgs indexed", len(inetnums), len(orgs))

	return &Database{
		db:   db,
		path: path,
	}, nil
}

// LookupPrefix finds the most specific inetnum that fully covers a prefix
func (d *Database) LookupPrefix(prefix netip.Prefix) (*Match, error) {
	if !prefix.Addr().Is4() {
		return nil, fmt.Errorf("%w: prefix is not IPv4", ErrInvalidIP)
	}

	start, end, err := PrefixToRange(prefix)
	if err != nil {
		return nil, err
	}

	return d.lookupRange(start, end, prefix)
}

// LookupIP finds the most specific inetnum that contains an IP address
func (d *Database) LookupIP(ip netip.Addr) (*Match, error) {
	if !ip.Is4() {
		return nil, fmt.Errorf("%w: IP is not IPv4", ErrInvalidIP)
	}

	ipUint := AddrToUint32(ip)
	prefix := netip.PrefixFrom(ip, 32)

	return d.lookupRange(ipUint, ipUint, prefix)
}

// lookupRange implements the most-specific covering range algorithm
func (d *Database) lookupRange(queryStart, queryEnd uint32, prefix netip.Prefix) (*Match, error) {
	// Seek to the query start IP
	// Note: with the new key format (start+end), we seek to start and scan
	seekKey := makeRangeKey(queryStart, 0) // Use 0 for end to get first key with this start
	iter := d.db.NewIterator(&util.Range{Start: []byte(prefixRange)}, nil)
	defer iter.Release()

	// Seek to queryStart
	iter.Seek(seekKey)

	// Collect candidates: all ranges where Start <= queryStart
	var candidates []Inetnum

	// If we're past queryStart, go back one
	if iter.Valid() {
		currentStart := extractStartFromKey(iter.Key())
		if currentStart > queryStart {
			iter.Prev()
		}
	} else {
		// We're at the end, go to last record
		iter.Last()
	}

	// Scan backward to collect all ranges where Start <= queryStart
	// Limit scan to prevent O(N) worst case
	const maxBackwardScan = 10000 // Safety limit
	scannedCount := 0
	lastStart := uint32(0xFFFFFFFF) // Track for monotonicity check

	for iter.Valid() && scannedCount < maxBackwardScan {
		currentStart := extractStartFromKey(iter.Key())

		// Break if we've gone too far backward (start is way before queryStart)
		// Large allocations are typically < /8, so if we're more than 16 million IPs back, stop
		if currentStart < queryStart && queryStart-currentStart > 16777216 { // 2^24 = /8
			break
		}

		// Sanity check: if start is increasing (going forward), something is wrong
		if currentStart > lastStart {
			break
		}
		lastStart = currentStart

		var inet Inetnum
		if err := msgpack.Unmarshal(iter.Value(), &inet); err != nil {
			return nil, fmt.Errorf("failed to unmarshal inetnum: %w", err)
		}

		// Check if this range covers our query
		if inet.Start <= queryStart && inet.End >= queryEnd {
			candidates = append(candidates, inet)
		}

		// Continue scanning backward
		iter.Prev()
		scannedCount++
	}

	// Also scan forward a few steps to handle edge cases
	iter.Seek(seekKey)
	for i := 0; i < 5 && iter.Valid(); i++ {
		currentStart := extractStartFromKey(iter.Key())
		if currentStart > queryStart {
			break
		}

		var inet Inetnum
		if err := msgpack.Unmarshal(iter.Value(), &inet); err != nil {
			return nil, fmt.Errorf("failed to unmarshal inetnum: %w", err)
		}

		if inet.Start <= queryStart && inet.End >= queryEnd {
			// Check if already in candidates
			isDuplicate := false
			for _, c := range candidates {
				if c.Start == inet.Start && c.End == inet.End {
					isDuplicate = true
					break
				}
			}
			if !isDuplicate {
				candidates = append(candidates, inet)
			}
		}

		iter.Next()
	}

	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("iterator error: %w", err)
	}

	if len(candidates) == 0 {
		return nil, ErrNotFound
	}

	// Find the most specific (smallest range)
	mostSpecific := candidates[0]
	for _, c := range candidates[1:] {
		rangeSize := c.End - c.Start
		currentSize := mostSpecific.End - mostSpecific.Start
		if rangeSize < currentSize {
			mostSpecific = c
		}
	}

	// Skip non-RIPE managed address blocks (catch-all entries)
	if mostSpecific.Netname == "NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK" {
		return nil, nil // No match - caller should try other sources
	}

	// Resolve organisation with fallback hierarchy:
	// 1. OrgID → organisation name
	// 2. Descr (description field)
	// 3. Valid remarks
	// 4. Netname
	orgName := "(no org)"
	orgType := ""
	if mostSpecific.OrgID != "" {
		if org, err := d.GetOrganisation(mostSpecific.OrgID); err == nil {
			orgName = org.OrgName
			orgType = org.OrgType
		}
	}

	// Fall back to descr field (often contains organization name)
	if orgName == "(no org)" && mostSpecific.Descr != "" {
		descr := strings.TrimSpace(mostSpecific.Descr)
		if isValidOrgRemark(descr) {
			orgName = descr
		}
	}

	// Fall back to remarks (like RDAP does)
	if orgName == "(no org)" && len(mostSpecific.Remarks) > 0 {
		// Use first valid remark (filter out separators and URLs)
		for _, remark := range mostSpecific.Remarks {
			remark = strings.TrimSpace(remark)
			if isValidOrgRemark(remark) {
				orgName = remark
				break
			}
		}
	}

	// Fall back to Netname as last resort
	if orgName == "(no org)" && mostSpecific.Netname != "" {
		orgName = mostSpecific.Netname
	}

	return &Match{
		Start:        Uint32ToAddr(mostSpecific.Start),
		End:          Uint32ToAddr(mostSpecific.End),
		Prefix:       prefix,
		OrgID:        mostSpecific.OrgID,
		OrgName:      orgName,
		OrgType:      orgType,
		Status:       mostSpecific.Status,
		Country:      mostSpecific.Country,
		Netname:      mostSpecific.Netname,
		MatchedAt:    time.Now(),
		FullyCovered: true, // We only return fully covering ranges
	}, nil
}

// GetOrganisation retrieves an organisation by ID
func (d *Database) GetOrganisation(orgID string) (*Organisation, error) {
	key := makeOrgKey(orgID)
	value, err := d.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return nil, fmt.Errorf("%w: organisation %s", ErrNotFound, orgID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get organisation: %w", err)
	}

	var org Organisation
	if err := msgpack.Unmarshal(value, &org); err != nil {
		return nil, fmt.Errorf("failed to unmarshal organisation: %w", err)
	}

	return &org, nil
}

// GetMetadata retrieves database metadata
func (d *Database) GetMetadata() (*Metadata, error) {
	value, err := d.db.Get([]byte(prefixMetadata+"build"), nil)
	if err == leveldb.ErrNotFound {
		return nil, fmt.Errorf("%w: metadata", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	var meta Metadata
	if err := msgpack.Unmarshal(value, &meta); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &meta, nil
}

// IterateRanges iterates over all inetnum ranges
func (d *Database) IterateRanges(fn func(Inetnum) error) error {
	iter := d.db.NewIterator(util.BytesPrefix([]byte(prefixRange)), nil)
	defer iter.Release()

	for iter.Next() {
		var inet Inetnum
		if err := msgpack.Unmarshal(iter.Value(), &inet); err != nil {
			return fmt.Errorf("failed to unmarshal inetnum: %w", err)
		}

		if err := fn(inet); err != nil {
			return err
		}
	}

	return iter.Error()
}

// Helper functions for key encoding

func makeRangeKey(startIP, endIP uint32) []byte {
	// Key format: "R4:" + 4-byte start + 4-byte end
	// This makes keys unique even when multiple ranges have the same start IP
	key := make([]byte, len(prefixRange)+8)
	copy(key, prefixRange)
	binary.BigEndian.PutUint32(key[len(prefixRange):], startIP)
	binary.BigEndian.PutUint32(key[len(prefixRange)+4:], endIP)
	return key
}

func makeOrgKey(orgID string) []byte {
	return []byte(prefixOrg + orgID)
}

func extractStartFromKey(key []byte) uint32 {
	if len(key) < len(prefixRange)+4 {
		return 0
	}
	return binary.BigEndian.Uint32(key[len(prefixRange):])
}

func extractEndFromKey(key []byte) uint32 {
	if len(key) < len(prefixRange)+8 {
		return 0
	}
	return binary.BigEndian.Uint32(key[len(prefixRange)+4:])
}

// NewIterator creates a new range iterator for advanced use cases
func (d *Database) NewIterator() iterator.Iterator {
	return d.db.NewIterator(util.BytesPrefix([]byte(prefixRange)), nil)
}
