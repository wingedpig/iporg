package arinbulk

import (
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	// Key prefixes
	prefixRange    = "R4:"   // IPv4 ranges
	prefixOrg      = "ORG:"  // Organizations
	prefixMetadata = "META:" // Metadata

	// Schema version
	currentSchemaVersion = 1
)

// Database manages the ARIN bulk data index
type Database struct {
	db   *leveldb.DB
	path string
}

// OpenDatabase opens an existing ARIN bulk database with default cache size
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

// BuildDatabaseStreaming creates a new ARIN bulk database with low memory usage
// by writing organizations immediately and only keeping networks in memory for sorting
func BuildDatabaseStreaming(path string, r io.Reader) (*Database, error) {
	log.Printf("INFO: Building ARIN bulk database at %s", path)

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

	// Parse XML incrementally, writing orgs immediately
	decoder := xml.NewDecoder(r)

	var nets []NetBlock
	orgCount := int64(0)
	orgBatch := new(leveldb.Batch)
	orgBatchSize := 0

	log.Printf("INFO: Parsing XML and writing organizations...")

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML decode error: %w", err)
		}

		switch se := token.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "net":
				var netXML NetXML
				if err := decoder.DecodeElement(&netXML, &se); err != nil {
					return nil, fmt.Errorf("failed to decode net: %w", err)
				}

				// Only process IPv4 for now
				if netXML.Version != "4" {
					continue
				}

				// Process each netBlock (keep in memory for sorting)
				for _, block := range netXML.NetBlocks.Blocks {
					nb, err := parseNetBlock(netXML, block)
					if err != nil {
						continue
					}
					nets = append(nets, nb)
				}

			case "org":
				var orgXML OrgXML
				if err := decoder.DecodeElement(&orgXML, &se); err != nil {
					return nil, fmt.Errorf("failed to decode org: %w", err)
				}

				// Write organization immediately to database
				org := Organization{
					OrgID:      orgXML.Handle,
					OrgName:    strings.TrimSpace(orgXML.Name),
					Country:    orgXML.ISO3166_1,
					StateProv:  orgXML.ISO3166_2,
					UpdateDate: orgXML.UpdateDate,
				}

				key := []byte(prefixOrg + org.OrgID)
				value, err := msgpack.Marshal(org)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal org: %w", err)
				}

				orgBatch.Put(key, value)
				orgBatchSize++
				orgCount++

				// Write batch every 10000 orgs to keep memory low
				if orgBatchSize >= 10000 {
					if err := db.Write(orgBatch, nil); err != nil {
						return nil, fmt.Errorf("failed to write org batch: %w", err)
					}
					orgBatch.Reset()
					orgBatchSize = 0

					if orgCount%100000 == 0 {
						log.Printf("INFO: Wrote %d organizations...", orgCount)
					}
				}
			}
		}
	}

	// Write remaining organizations
	if orgBatchSize > 0 {
		if err := db.Write(orgBatch, nil); err != nil {
			return nil, fmt.Errorf("failed to write final org batch: %w", err)
		}
	}

	log.Printf("INFO: Parsed %d networks, wrote %d organizations", len(nets), orgCount)

	// Sort networks: Start ascending, End descending (parents before children)
	log.Printf("INFO: Sorting networks...")
	sort.Slice(nets, func(i, j int) bool {
		if nets[i].Start != nets[j].Start {
			return nets[i].Start < nets[j].Start
		}
		return nets[i].End > nets[j].End
	})

	// Write networks to database
	log.Printf("INFO: Writing networks to database...")
	netBatch := new(leveldb.Batch)
	batchSize := 0

	for _, net := range nets {
		key := make([]byte, 3+4+4)
		copy(key[0:3], []byte(prefixRange))
		binary.BigEndian.PutUint32(key[3:7], net.Start)
		binary.BigEndian.PutUint32(key[7:11], net.End)

		value, err := msgpack.Marshal(net)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal net: %w", err)
		}

		netBatch.Put(key, value)
		batchSize++

		if batchSize >= 10000 {
			if err := db.Write(netBatch, nil); err != nil {
				return nil, fmt.Errorf("failed to write net batch: %w", err)
			}
			netBatch.Reset()
			batchSize = 0
		}
	}

	// Write remaining networks
	if batchSize > 0 {
		if err := db.Write(netBatch, nil); err != nil {
			return nil, fmt.Errorf("failed to write final net batch: %w", err)
		}
	}

	// Write metadata
	log.Printf("INFO: Writing metadata...")
	metadata := Metadata{
		SchemaVersion: currentSchemaVersion,
		BuildTime:     time.Now(),
		NetBlockCount: int64(len(nets)),
		OrgCount:      orgCount,
	}
	metaValue, err := msgpack.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	if err := db.Put([]byte(prefixMetadata+"version"), metaValue, nil); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	log.Printf("INFO: Database build complete - %d networks, %d orgs indexed", len(nets), orgCount)

	return &Database{
		db:   db,
		path: path,
	}, nil
}

// BuildDatabase creates a new ARIN bulk database from parsed data
func BuildDatabase(path string, nets []NetBlock, orgs map[string]Organization) (*Database, error) {
	log.Printf("INFO: Building ARIN bulk database at %s", path)
	log.Printf("INFO: Indexing %d networks, %d organizations", len(nets), len(orgs))

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

	// Sort networks: Start ascending, End descending (parents before children)
	log.Printf("INFO: Sorting networks...")
	sort.Slice(nets, func(i, j int) bool {
		if nets[i].Start != nets[j].Start {
			return nets[i].Start < nets[j].Start
		}
		// Same start: larger range (smaller end) comes first
		return nets[i].End > nets[j].End
	})

	// Write networks to database
	log.Printf("INFO: Writing networks to database...")
	batch := new(leveldb.Batch)
	batchSize := 0

	for _, net := range nets {
		// Create key: "R4:" + start (4 bytes) + end (4 bytes)
		key := make([]byte, 3+4+4)
		copy(key[0:3], []byte(prefixRange))
		binary.BigEndian.PutUint32(key[3:7], net.Start)
		binary.BigEndian.PutUint32(key[7:11], net.End)

		// Serialize value
		value, err := msgpack.Marshal(net)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal net: %w", err)
		}

		batch.Put(key, value)
		batchSize++

		// Write batch every 10000 records
		if batchSize >= 10000 {
			if err := db.Write(batch, nil); err != nil {
				return nil, fmt.Errorf("failed to write batch: %w", err)
			}
			batch.Reset()
			batchSize = 0
		}
	}

	// Write remaining records
	if batchSize > 0 {
		if err := db.Write(batch, nil); err != nil {
			return nil, fmt.Errorf("failed to write final batch: %w", err)
		}
	}

	// Write organizations
	log.Printf("INFO: Writing organizations...")
	batch.Reset()
	for _, org := range orgs {
		key := []byte(prefixOrg + org.OrgID)
		value, err := msgpack.Marshal(org)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal org: %w", err)
		}
		batch.Put(key, value)
	}
	if err := db.Write(batch, nil); err != nil {
		return nil, fmt.Errorf("failed to write organizations: %w", err)
	}

	// Write metadata
	log.Printf("INFO: Writing metadata...")
	metadata := Metadata{
		SchemaVersion: currentSchemaVersion,
		BuildTime:     time.Now(),
		NetBlockCount: int64(len(nets)),
		OrgCount:      int64(len(orgs)),
	}
	metaValue, err := msgpack.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	if err := db.Put([]byte(prefixMetadata+"version"), metaValue, nil); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	log.Printf("INFO: Database build complete - %d networks, %d orgs indexed", len(nets), len(orgs))

	return &Database{
		db:   db,
		path: path,
	}, nil
}

// LookupIP finds the most specific network containing the IP
func (d *Database) LookupIP(ip netip.Addr) (*Match, error) {
	if !ip.Is4() {
		return nil, fmt.Errorf("IPv6 not supported yet")
	}

	ipInt := AddrToUint32(ip)

	// Find all ranges containing this IP
	var candidates []NetBlock

	// OPTIMIZATION: Seek to approximately where the IP should be
	// Key format: "R4:" + start (4 bytes) + end (4 bytes)
	// We seek to "R4:" + ipInt + 0x00000000 to position near the IP
	seekKey := make([]byte, 3+4+4)
	copy(seekKey[0:3], []byte(prefixRange))
	binary.BigEndian.PutUint32(seekKey[3:7], ipInt)
	binary.BigEndian.PutUint32(seekKey[7:11], 0) // Smallest end for this start

	// Use range with prefix to constrain search
	iter := d.db.NewIterator(&util.Range{
		Start: []byte(prefixRange),
		Limit: nil,
	}, nil)
	defer iter.Release()

	// Seek to approximately the right location
	if !iter.Seek(seekKey) {
		// If seek fails, we might be past the end, so rewind to beginning
		iter.First()
	}

	// Search backwards to find ranges that might contain this IP
	// (ranges with start <= ipInt)
	for iter.Prev() {
		var net NetBlock
		if err := msgpack.Unmarshal(iter.Value(), &net); err != nil {
			continue
		}

		// If this range's start is way before the IP, we can stop
		// (assuming reasonable network sizes, nothing spans > /8)
		if net.Start < ipInt && (ipInt-net.Start) > 0x01000000 { // More than /8 away
			break
		}

		// Check if IP is within this range
		if ipInt >= net.Start && ipInt <= net.End {
			candidates = append(candidates, net)
		}
	}

	// Also search forward from seek point to catch ranges starting at exactly ipInt
	iter.Seek(seekKey)
	for iter.Next() {
		var net NetBlock
		if err := msgpack.Unmarshal(iter.Value(), &net); err != nil {
			continue
		}

		// If start is past our IP, ranges can't contain it
		if net.Start > ipInt {
			break
		}

		// Check if IP is within range
		if ipInt >= net.Start && ipInt <= net.End {
			candidates = append(candidates, net)
		}
	}

	if len(candidates) == 0 {
		return nil, ErrNotFound
	}

	// Find most specific (smallest range)
	mostSpecific := candidates[0]
	for _, c := range candidates[1:] {
		rangeSize := c.End - c.Start
		currentSize := mostSpecific.End - mostSpecific.Start
		if rangeSize < currentSize {
			mostSpecific = c
		}
	}

	return d.buildMatch(mostSpecific, netip.Prefix{})
}

// LookupPrefix finds the most specific network that exactly matches or contains the prefix
func (d *Database) LookupPrefix(prefix netip.Prefix) (*Match, error) {
	if !prefix.Addr().Is4() {
		return nil, fmt.Errorf("IPv6 not supported yet")
	}

	// Calculate start and end IPs of the prefix
	prefixStart := AddrToUint32(prefix.Addr())
	// For a /N prefix, calculate the end IP
	bits := prefix.Bits()
	hostBits := 32 - bits
	prefixEnd := prefixStart + (1 << hostBits) - 1

	// Find all ranges containing this IP
	var candidates []NetBlock

	// Seek to approximately where the IP should be
	seekKey := make([]byte, 3+4+4)
	copy(seekKey[0:3], []byte(prefixRange))
	binary.BigEndian.PutUint32(seekKey[3:7], prefixStart)
	binary.BigEndian.PutUint32(seekKey[7:11], 0)

	iter := d.db.NewIterator(&util.Range{
		Start: []byte(prefixRange),
		Limit: nil,
	}, nil)
	defer iter.Release()

	if !iter.Seek(seekKey) {
		iter.First()
	}

	// Check current position first (exact match or just after)
	if iter.Valid() {
		var net NetBlock
		if err := msgpack.Unmarshal(iter.Value(), &net); err == nil {
			if prefixStart >= net.Start && prefixEnd <= net.End {
				candidates = append(candidates, net)
			}
		}
	}

	// Search backwards
	iter.Seek(seekKey)
	for iter.Prev() {
		var net NetBlock
		if err := msgpack.Unmarshal(iter.Value(), &net); err != nil {
			continue
		}

		if net.Start < prefixStart && (prefixStart-net.Start) > 0x01000000 {
			break
		}

		// Check if this range fully covers the prefix
		if prefixStart >= net.Start && prefixEnd <= net.End {
			candidates = append(candidates, net)
		}
	}

	// Search forward (skip current since we already checked it)
	iter.Seek(seekKey)
	for iter.Next() {
		var net NetBlock
		if err := msgpack.Unmarshal(iter.Value(), &net); err != nil {
			continue
		}

		if net.Start > prefixStart {
			break
		}

		// Check if this range fully covers the prefix
		if prefixStart >= net.Start && prefixEnd <= net.End {
			candidates = append(candidates, net)
		}
	}

	if len(candidates) == 0 {
		return nil, ErrNotFound
	}

	// Find most specific (smallest range) that covers the entire prefix
	mostSpecific := candidates[0]
	for _, c := range candidates[1:] {
		rangeSize := c.End - c.Start
		currentSize := mostSpecific.End - mostSpecific.Start
		if rangeSize < currentSize {
			mostSpecific = c
		}
	}

	return d.buildMatch(mostSpecific, prefix)
}

func (d *Database) buildMatch(net NetBlock, queryPrefix netip.Prefix) (*Match, error) {
	// Resolve organization
	orgName := "(no org)"
	if net.OrgID != "" {
		if org, err := d.GetOrganization(net.OrgID); err == nil && IsValidOrgName(org.OrgName) {
			orgName = org.OrgName
		}
	}

	// Don't fall back to NetName - let caller use ASN organization instead
	// NetName is often just an internal label, not the actual organization
	// (NetName is still available in Match.NetName for reference)
	if orgName == "(no org)" {
		orgName = "" // Return empty so iporg-build can use ASN org
	}

	return &Match{
		Start:        Uint32ToAddr(net.Start),
		End:          Uint32ToAddr(net.End),
		Prefix:       queryPrefix,
		NetHandle:    net.NetHandle,
		OrgID:        net.OrgID,
		OrgName:      orgName,
		NetType:      net.NetType,
		NetName:      net.NetName,
		MatchedAt:    time.Now(),
		FullyCovered: true,
	}, nil
}

// GetOrganization retrieves an organization by ID
func (d *Database) GetOrganization(orgID string) (Organization, error) {
	key := []byte(prefixOrg + orgID)
	value, err := d.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, err
	}

	var org Organization
	if err := msgpack.Unmarshal(value, &org); err != nil {
		return Organization{}, fmt.Errorf("failed to unmarshal org: %w", err)
	}

	return org, nil
}

// IterateRanges iterates over all network ranges
func (d *Database) IterateRanges(callback func(NetBlock) error) error {
	iter := d.db.NewIterator(util.BytesPrefix([]byte(prefixRange)), nil)
	defer iter.Release()

	for iter.Next() {
		var net NetBlock
		if err := msgpack.Unmarshal(iter.Value(), &net); err != nil {
			return fmt.Errorf("failed to unmarshal net: %w", err)
		}

		if err := callback(net); err != nil {
			return err
		}
	}

	return iter.Error()
}

// GetMetadata retrieves database metadata
func (d *Database) GetMetadata() (*Metadata, error) {
	value, err := d.db.Get([]byte(prefixMetadata+"version"), nil)
	if err == leveldb.ErrNotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var meta Metadata
	if err := msgpack.Unmarshal(value, &meta); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &meta, nil
}

// Stats returns database statistics
func (d *Database) Stats() (netCount, orgCount int64, err error) {
	meta, err := d.GetMetadata()
	if err != nil {
		return 0, 0, err
	}

	return meta.NetBlockCount, meta.OrgCount, nil
}
