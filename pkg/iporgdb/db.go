// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package iporgdb

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/wingedpig/iporg/pkg/model"
	"github.com/wingedpig/iporg/pkg/util/ipcodec"
)

// DB wraps a LevelDB instance for IP organization data
type DB struct {
	db     *leveldb.DB
	mu     sync.RWMutex
	path   string
	closed bool
}

// Open opens or creates a LevelDB database at the specified path
func Open(path string) (*DB, error) {
	opts := &opt.Options{
		// Use snappy compression for values
		Compression: opt.SnappyCompression,
		// Increase write buffer for faster builds
		WriteBuffer: 64 * 1024 * 1024, // 64MB
	}

	db, err := leveldb.OpenFile(path, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	return &DB{
		db:   db,
		path: path,
	}, nil
}

// Close closes the database
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return model.ErrDatabaseClosed
	}

	d.closed = true
	return d.db.Close()
}

// IsClosed returns true if the database is closed
func (d *DB) IsClosed() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.closed
}

// Path returns the database path
func (d *DB) Path() string {
	return d.path
}

// Get retrieves a value by key
func (d *DB) Get(key []byte) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return nil, model.ErrDatabaseClosed
	}

	value, err := d.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get failed: %w", err)
	}
	return value, nil
}

// Put stores a key-value pair
func (d *DB) Put(key, value []byte) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return model.ErrDatabaseClosed
	}

	return d.db.Put(key, value, nil)
}

// Delete removes a key-value pair
func (d *DB) Delete(key []byte) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return model.ErrDatabaseClosed
	}

	return d.db.Delete(key, nil)
}

// NewIterator creates a new iterator
func (d *DB) NewIterator(slice *util.Range) iterator.Iterator {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.db.NewIterator(slice, nil)
}

// WriteBatch writes multiple key-value pairs atomically
func (d *DB) WriteBatch(ops []BatchOp) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return model.ErrDatabaseClosed
	}

	batch := new(leveldb.Batch)
	for _, op := range ops {
		if op.Delete {
			batch.Delete(op.Key)
		} else {
			batch.Put(op.Key, op.Value)
		}
	}

	return d.db.Write(batch, nil)
}

// BatchOp represents a batch operation
type BatchOp struct {
	Key    []byte
	Value  []byte
	Delete bool
}

// encodeRecord serializes a Record to msgpack
func encodeRecord(rec *model.Record) ([]byte, error) {
	// Create a serializable struct
	data := struct {
		EndBytes    []byte
		ASN         int
		ASNName     string
		OrgName     string
		RIR         string
		Country     string
		Region      string
		City        string
		Lat         float64
		Lon         float64
		SourceRole  string
		StatusLabel string
		Prefix      string
		LastChecked int64 // Unix timestamp
		Schema      int
	}{
		EndBytes:    ipcodec.IPToBytes(rec.End),
		ASN:         rec.ASN,
		ASNName:     rec.ASNName,
		OrgName:     rec.OrgName,
		RIR:         rec.RIR,
		Country:     rec.Country,
		Region:      rec.Region,
		City:        rec.City,
		Lat:         rec.Lat,
		Lon:         rec.Lon,
		SourceRole:  rec.SourceRole,
		StatusLabel: rec.StatusLabel,
		Prefix:      rec.Prefix,
		LastChecked: rec.LastChecked.Unix(),
		Schema:      rec.Schema,
	}

	return msgpack.Marshal(data)
}

// decodeRecord deserializes a Record from msgpack
func decodeRecord(startIP []byte, data []byte) (*model.Record, error) {
	var stored struct {
		EndBytes    []byte
		ASN         int
		ASNName     string
		OrgName     string
		RIR         string
		Country     string
		Region      string
		City        string
		Lat         float64
		Lon         float64
		SourceRole  string
		StatusLabel string
		Prefix      string
		LastChecked int64
		Schema      int
	}

	if err := msgpack.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("failed to unmarshal record: %w", err)
	}

	start, err := ipcodec.BytesToIP(startIP)
	if err != nil {
		return nil, fmt.Errorf("invalid start IP: %w", err)
	}

	end, err := ipcodec.BytesToIP(stored.EndBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid end IP: %w", err)
	}

	return &model.Record{
		Start:       start,
		End:         end,
		ASN:         stored.ASN,
		ASNName:     stored.ASNName,
		OrgName:     stored.OrgName,
		RIR:         stored.RIR,
		Country:     stored.Country,
		Region:      stored.Region,
		City:        stored.City,
		Lat:         stored.Lat,
		Lon:         stored.Lon,
		SourceRole:  stored.SourceRole,
		StatusLabel: stored.StatusLabel,
		Prefix:      stored.Prefix,
		LastChecked: time.Unix(stored.LastChecked, 0),
		Schema:      stored.Schema,
	}, nil
}

// CompactDB forces compaction of the database
func (d *DB) CompactDB(ctx context.Context) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return model.ErrDatabaseClosed
	}

	// Compact the entire database
	return d.db.CompactRange(util.Range{Start: nil, Limit: nil})
}
