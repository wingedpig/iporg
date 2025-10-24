package iptoasn

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/vmihailenco/msgpack/v5"

	"iporg/pkg/model"
)

// Store handles LevelDB storage for iptoasn data
type Store struct {
	db     *leveldb.DB
	mu     sync.RWMutex
	closed bool
}

// Key prefixes
const (
	prefixGlobalV4     = "P4:"    // Global ordered list for IPv4
	prefixASNRaw       = "A:"     // Per-ASN raw prefixes
	prefixASNCollapsed = "Ac:"    // Per-ASN collapsed prefixes
	prefixASNIndex     = "AIDX:"  // ASN index
	prefixMeta         = "meta:"  // Metadata
	prefixStats        = "stats:" // Statistics
)

// Open opens or creates the LevelDB database
func Open(path string) (*Store, error) {
	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	return &Store{
		db: db,
	}, nil
}

// Close closes the database
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true
	return s.db.Close()
}

// IsClosed returns whether the database is closed
func (s *Store) IsClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// WriteBatch writes a batch of prefixes to the database
func (s *Store) WriteBatch(prefixes []*model.CanonicalPrefix, collapsedByASN map[int][]*model.CanonicalPrefix) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return model.ErrDatabaseClosed
	}

	batch := new(leveldb.Batch)

	// Write global ordered list (P4)
	for _, p := range prefixes {
		_, ipnet, err := net.ParseCIDR(p.CIDR)
		if err != nil {
			continue
		}

		ip := ipnet.IP.To4()
		if ip == nil {
			continue // Skip non-IPv4
		}

		addr, _ := netip.ParseAddr(ip.String())
		start := ipToUint32(addr)

		key := makeGlobalKey(start)
		value, err := msgpack.Marshal(p)
		if err != nil {
			return fmt.Errorf("failed to marshal prefix: %w", err)
		}

		batch.Put(key, value)
	}

	// Group raw prefixes by ASN
	rawByASN := make(map[int][]*model.CanonicalPrefix)
	for _, p := range prefixes {
		rawByASN[p.ASN] = append(rawByASN[p.ASN], p)
	}

	// Write per-ASN raw and collapsed lists
	for asn, rawPrefixes := range rawByASN {
		// Write raw prefixes
		for i, p := range rawPrefixes {
			key := makeASNRawKey(asn, i)
			value, err := msgpack.Marshal(p)
			if err != nil {
				return fmt.Errorf("failed to marshal prefix: %w", err)
			}
			batch.Put(key, value)
		}

		// Write collapsed prefixes if available
		if collapsed, ok := collapsedByASN[asn]; ok {
			for i, p := range collapsed {
				key := makeASNCollapsedKey(asn, i)
				value, err := msgpack.Marshal(p)
				if err != nil {
					return fmt.Errorf("failed to marshal prefix: %w", err)
				}
				batch.Put(key, value)
			}

			// Write ASN index
			indexEntry := &model.ASNIndexEntry{
				ASN:          asn,
				V4Count:      len(rawPrefixes),
				V4Collapsed:  len(collapsed),
				LastModified: time.Now(),
			}
			indexValue, err := msgpack.Marshal(indexEntry)
			if err != nil {
				return fmt.Errorf("failed to marshal index: %w", err)
			}
			batch.Put(makeASNIndexKey(asn), indexValue)
		} else {
			// No collapsed version, just index the raw
			indexEntry := &model.ASNIndexEntry{
				ASN:          asn,
				V4Count:      len(rawPrefixes),
				V4Collapsed:  len(rawPrefixes),
				LastModified: time.Now(),
			}
			indexValue, err := msgpack.Marshal(indexEntry)
			if err != nil {
				return fmt.Errorf("failed to marshal index: %w", err)
			}
			batch.Put(makeASNIndexKey(asn), indexValue)
		}
	}

	// Write batch
	return s.db.Write(batch, nil)
}

// WalkV4 iterates over all IPv4 prefixes in order
func (s *Store) WalkV4(ctx context.Context, startKey []byte, fn func(k []byte, p *model.CanonicalPrefix) (cont bool, err error)) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return model.ErrDatabaseClosed
	}

	iter := s.db.NewIterator(util.BytesPrefix([]byte(prefixGlobalV4)), nil)
	defer iter.Release()

	if startKey != nil {
		iter.Seek(startKey)
	} else {
		iter.First()
	}

	for iter.Valid() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var p model.CanonicalPrefix
		if err := msgpack.Unmarshal(iter.Value(), &p); err != nil {
			return fmt.Errorf("failed to unmarshal prefix: %w", err)
		}

		cont, err := fn(iter.Key(), &p)
		if err != nil {
			return err
		}
		if !cont {
			break
		}

		iter.Next()
	}

	return iter.Error()
}

// ListByASN returns all prefixes for a given ASN
func (s *Store) ListByASN(ctx context.Context, asn int, collapsed bool) ([]*model.CanonicalPrefix, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, model.ErrDatabaseClosed
	}

	var prefix string
	if collapsed {
		prefix = makeASNCollapsedPrefix(asn)
	} else {
		prefix = makeASNRawPrefix(asn)
	}

	iter := s.db.NewIterator(util.BytesPrefix([]byte(prefix)), nil)
	defer iter.Release()

	var prefixes []*model.CanonicalPrefix
	for iter.Next() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		var p model.CanonicalPrefix
		if err := msgpack.Unmarshal(iter.Value(), &p); err != nil {
			return nil, fmt.Errorf("failed to unmarshal prefix: %w", err)
		}

		prefixes = append(prefixes, &p)
	}

	return prefixes, iter.Error()
}

// GetASNIndex returns the index entry for an ASN
func (s *Store) GetASNIndex(asn int) (*model.ASNIndexEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, model.ErrDatabaseClosed
	}

	data, err := s.db.Get(makeASNIndexKey(asn), nil)
	if err == leveldb.ErrNotFound {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var entry model.ASNIndexEntry
	if err := msgpack.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("failed to unmarshal index: %w", err)
	}

	return &entry, nil
}

// ListASNs returns all ASNs in the database
func (s *Store) ListASNs(ctx context.Context) ([]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, model.ErrDatabaseClosed
	}

	iter := s.db.NewIterator(util.BytesPrefix([]byte(prefixASNIndex)), nil)
	defer iter.Release()

	var asns []int
	for iter.Next() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Parse ASN from key
		key := string(iter.Key())
		asnStr := strings.TrimPrefix(key, prefixASNIndex)
		asn, err := strconv.Atoi(asnStr)
		if err != nil {
			continue
		}

		asns = append(asns, asn)
	}

	return asns, iter.Error()
}

// SetMetadata sets a metadata value
func (s *Store) SetMetadata(key, value string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return model.ErrDatabaseClosed
	}

	return s.db.Put([]byte(prefixMeta+key), []byte(value), nil)
}

// GetMetadata gets a metadata value
func (s *Store) GetMetadata(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return "", model.ErrDatabaseClosed
	}

	data, err := s.db.Get([]byte(prefixMeta+key), nil)
	if err == leveldb.ErrNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// SetStats saves statistics
func (s *Store) SetStats(stats *model.IPToASNStats) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return model.ErrDatabaseClosed
	}

	data, err := msgpack.Marshal(stats)
	if err != nil {
		return fmt.Errorf("failed to marshal stats: %w", err)
	}

	return s.db.Put([]byte(prefixStats+"totals"), data, nil)
}

// GetStats retrieves statistics
func (s *Store) GetStats() (*model.IPToASNStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, model.ErrDatabaseClosed
	}

	data, err := s.db.Get([]byte(prefixStats+"totals"), nil)
	if err == leveldb.ErrNotFound {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var stats model.IPToASNStats
	if err := msgpack.Unmarshal(data, &stats); err != nil {
		return nil, fmt.Errorf("failed to unmarshal stats: %w", err)
	}

	return &stats, nil
}

// Key construction helpers

func makeGlobalKey(start uint32) []byte {
	key := make([]byte, len(prefixGlobalV4)+4)
	copy(key, prefixGlobalV4)
	binary.BigEndian.PutUint32(key[len(prefixGlobalV4):], start)
	return key
}

func makeASNRawKey(asn, index int) []byte {
	return []byte(fmt.Sprintf("%s%d:v4:%d", prefixASNRaw, asn, index))
}

func makeASNCollapsedKey(asn, index int) []byte {
	return []byte(fmt.Sprintf("%s%d:v4:%d", prefixASNCollapsed, asn, index))
}

func makeASNRawPrefix(asn int) string {
	return fmt.Sprintf("%s%d:v4:", prefixASNRaw, asn)
}

func makeASNCollapsedPrefix(asn int) string {
	return fmt.Sprintf("%s%d:v4:", prefixASNCollapsed, asn)
}

func makeASNIndexKey(asn int) []byte {
	return []byte(fmt.Sprintf("%s%d", prefixASNIndex, asn))
}
