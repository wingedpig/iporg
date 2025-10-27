package arinbulk

import (
	"encoding/binary"
	"fmt"
	"log"
	"net/netip"
	"sort"
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

// OpenDatabase opens an existing ARIN bulk database
func OpenDatabase(path string) (*Database, error) {
	db, err := leveldb.OpenFile(path, &opt.Options{
		Compression: opt.SnappyCompression,
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

	iter := d.db.NewIterator(util.BytesPrefix([]byte(prefixRange)), nil)
	defer iter.Release()

	for iter.Next() {
		var net NetBlock
		if err := msgpack.Unmarshal(iter.Value(), &net); err != nil {
			continue
		}

		// Check if IP is within range
		if ipInt >= net.Start && ipInt <= net.End {
			candidates = append(candidates, net)
		}

		// Stop if we've passed the IP
		if net.Start > ipInt {
			break
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

// LookupPrefix finds the most specific network containing the prefix
func (d *Database) LookupPrefix(prefix netip.Prefix) (*Match, error) {
	if !prefix.Addr().Is4() {
		return nil, fmt.Errorf("IPv6 not supported yet")
	}

	// Use the start IP of the prefix for lookup
	return d.LookupIP(prefix.Addr())
}

func (d *Database) buildMatch(net NetBlock, queryPrefix netip.Prefix) (*Match, error) {
	// Resolve organization
	orgName := "(no org)"
	if net.OrgID != "" {
		if org, err := d.GetOrganization(net.OrgID); err == nil && IsValidOrgName(org.OrgName) {
			orgName = org.OrgName
		}
	}

	// Fall back to network name if no valid org
	if orgName == "(no org)" && net.NetName != "" {
		orgName = net.NetName
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
