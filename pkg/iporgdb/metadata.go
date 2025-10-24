package iporgdb

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"iporg/pkg/model"
	"iporg/pkg/util/ipcodec"
)

// Metadata keys
const (
	metaKeySchema         = "schema"
	metaKeyBuiltAt        = "built_at"
	metaKeyBuilderVersion = "builder_version"
)

// SetMetadata sets a metadata key-value pair
func (d *DB) SetMetadata(key, value string) error {
	return d.Put(ipcodec.MetaKey(key), []byte(value))
}

// GetMetadata retrieves a metadata value
func (d *DB) GetMetadata(key string) (string, error) {
	value, err := d.Get(ipcodec.MetaKey(key))
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return string(value), nil
}

// SetSchemaVersion sets the database schema version
func (d *DB) SetSchemaVersion(version int) error {
	return d.SetMetadata(metaKeySchema, fmt.Sprintf("%d", version))
}

// GetSchemaVersion retrieves the database schema version
func (d *DB) GetSchemaVersion() (int, error) {
	value, err := d.GetMetadata(metaKeySchema)
	if err != nil {
		return 0, err
	}
	if value == "" {
		return 0, nil
	}
	var version int
	if _, err := fmt.Sscanf(value, "%d", &version); err != nil {
		return 0, fmt.Errorf("invalid schema version: %w", err)
	}
	return version, nil
}

// SetBuiltAt sets the database build timestamp
func (d *DB) SetBuiltAt(t time.Time) error {
	return d.SetMetadata(metaKeyBuiltAt, t.Format(time.RFC3339))
}

// GetBuiltAt retrieves the database build timestamp
func (d *DB) GetBuiltAt() (time.Time, error) {
	value, err := d.GetMetadata(metaKeyBuiltAt)
	if err != nil {
		return time.Time{}, err
	}
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

// SetBuilderVersion sets the builder version (e.g., git SHA)
func (d *DB) SetBuilderVersion(version string) error {
	return d.SetMetadata(metaKeyBuilderVersion, version)
}

// GetBuilderVersion retrieves the builder version
func (d *DB) GetBuilderVersion() (string, error) {
	return d.GetMetadata(metaKeyBuilderVersion)
}

// SetCache stores a cached value with a category and key
func (d *DB) SetCache(category, key string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal cache value: %w", err)
	}
	return d.Put(ipcodec.CacheKey(category, key), data)
}

// GetCache retrieves a cached value
func (d *DB) GetCache(category, key string, result interface{}) error {
	data, err := d.Get(ipcodec.CacheKey(category, key))
	if err != nil {
		return err
	}
	if data == nil {
		return nil
	}
	return json.Unmarshal(data, result)
}

// DeleteCache removes a cached value
func (d *DB) DeleteCache(category, key string) error {
	return d.Delete(ipcodec.CacheKey(category, key))
}

// Stats computes database statistics
func (d *DB) Stats(ctx context.Context) (*model.Stats, error) {
	stats := &model.Stats{
		RecordsByRIR:     make(map[string]int64),
		RecordsByRole:    make(map[string]int64),
		RecordsByCountry: make(map[string]int64),
	}

	// Get schema version
	version, err := d.GetSchemaVersion()
	if err != nil {
		log.Printf("WARN: Failed to get schema version: %v", err)
	}
	stats.SchemaVersion = version

	// Get built at
	builtAt, err := d.GetBuiltAt()
	if err != nil {
		log.Printf("WARN: Failed to get built_at: %v", err)
	}
	stats.LastBuiltAt = builtAt

	// Get builder version
	builderVersion, err := d.GetBuilderVersion()
	if err != nil {
		log.Printf("WARN: Failed to get builder version: %v", err)
	}
	stats.BuilderVersion = builderVersion

	// Count records
	ipv4, ipv6, err := d.CountRanges()
	if err != nil {
		return nil, fmt.Errorf("failed to count ranges: %w", err)
	}
	stats.IPv4Records = ipv4
	stats.IPv6Records = ipv6
	stats.TotalRecords = ipv4 + ipv6

	// Iterate and collect stats (IPv4)
	err = d.IterateRanges(true, func(rec *model.Record) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stats.RecordsByRIR[rec.RIR]++
		stats.RecordsByRole[rec.SourceRole]++
		stats.RecordsByCountry[rec.Country]++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate IPv4 ranges: %w", err)
	}

	// Iterate and collect stats (IPv6)
	err = d.IterateRanges(false, func(rec *model.Record) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stats.RecordsByRIR[rec.RIR]++
		stats.RecordsByRole[rec.SourceRole]++
		stats.RecordsByCountry[rec.Country]++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate IPv6 ranges: %w", err)
	}

	return stats, nil
}

// InitializeMetadata sets initial metadata when creating a new database
func (d *DB) InitializeMetadata(builderVersion string) error {
	if err := d.SetSchemaVersion(1); err != nil {
		return err
	}
	if err := d.SetBuiltAt(time.Now()); err != nil {
		return err
	}
	if err := d.SetBuilderVersion(builderVersion); err != nil {
		return err
	}
	return nil
}
