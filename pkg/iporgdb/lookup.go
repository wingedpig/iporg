package iporgdb

import (
	"fmt"
	"net/netip"

	"github.com/wingedpig/iporg/pkg/model"
	"github.com/wingedpig/iporg/pkg/util/ipcodec"
)

// GetByIP performs an IP lookup using the seek/prev algorithm
// Returns the record containing the IP, or ErrNotFound if not found
func (d *DB) GetByIP(ip netip.Addr) (*model.Record, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return nil, model.ErrDatabaseClosed
	}

	if !ip.IsValid() {
		return nil, model.ErrInvalidIP
	}

	// Determine the key prefix based on IP version
	var prefix string
	if ip.Is4() {
		prefix = ipcodec.PrefixRangeV4
	} else {
		prefix = ipcodec.PrefixRangeV6
	}

	// Create the search key
	searchKey := ipcodec.EncodeRangeKey(ip)

	// Create an iterator
	iter := d.db.NewIterator(nil, nil)
	defer iter.Release()

	// Seek to the key >= searchKey
	if !iter.Seek(searchKey) {
		// No key >= searchKey, so we need to check the last record
		if !iter.Last() {
			// Database is empty
			return nil, model.ErrNotFound
		}
		// Check if this record's key has the right prefix
		key := iter.Key()
		if len(key) < len(prefix) || string(key[:len(prefix)]) != prefix {
			// Not the same IP version
			return nil, model.ErrNotFound
		}
	} else {
		// We found a key >= searchKey
		// Check if it's an exact match
		key := iter.Key()
		if len(key) < len(prefix) || string(key[:len(prefix)]) != prefix {
			// Different IP version - need to go back
			if !iter.Prev() {
				return nil, model.ErrNotFound
			}
			key = iter.Key()
			if len(key) < len(prefix) || string(key[:len(prefix)]) != prefix {
				return nil, model.ErrNotFound
			}
		} else {
			// Same IP version
			startIP, err := ipcodec.DecodeRangeKey(key)
			if err != nil {
				return nil, fmt.Errorf("invalid key: %w", err)
			}

			if startIP.Compare(ip) == 0 {
				// Exact match on start IP
				value := iter.Value()
				rec, err := decodeRecord(ipcodec.IPToBytes(startIP), value)
				if err != nil {
					return nil, fmt.Errorf("failed to decode record: %w", err)
				}

				// Verify IP is in range
				if ipcodec.IsInRange(ip, rec.Start, rec.End) {
					return rec, nil
				}
				return nil, model.ErrNotFound
			} else if startIP.Compare(ip) > 0 {
				// The found key is greater than our IP
				// We need to check the previous record
				if !iter.Prev() {
					return nil, model.ErrNotFound
				}
				key = iter.Key()
				if len(key) < len(prefix) || string(key[:len(prefix)]) != prefix {
					return nil, model.ErrNotFound
				}
			}
			// If startIP < ip, we stay at this record
		}
	}

	// At this point, iter is positioned at a record where start <= ip
	// Decode and check if ip <= end
	key := iter.Key()
	value := iter.Value()

	startIP, err := ipcodec.DecodeRangeKey(key)
	if err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}

	rec, err := decodeRecord(ipcodec.IPToBytes(startIP), value)
	if err != nil {
		return nil, fmt.Errorf("failed to decode record: %w", err)
	}

	// Check if IP is in range [start, end]
	if ipcodec.IsInRange(ip, rec.Start, rec.End) {
		return rec, nil
	}

	return nil, model.ErrNotFound
}

// LookupString is a convenience method that parses an IP string and performs lookup
func (d *DB) LookupString(ipStr string) (*model.Record, error) {
	ip, err := ipcodec.ParseIP(ipStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", model.ErrInvalidIP, err)
	}
	return d.GetByIP(ip)
}

// ToLookupResult converts a Record to a LookupResult
func ToLookupResult(ip string, rec *model.Record) *model.LookupResult {
	result := &model.LookupResult{
		IP:         ip,
		ASN:        rec.ASN,
		ASNName:    rec.ASNName,
		OrgName:    rec.OrgName,
		RIR:        rec.RIR,
		Country:    rec.Country,
		Prefix:     rec.Prefix,
		SourceRole: rec.SourceRole,
	}

	// Optional fields
	if rec.Region != "" {
		result.Region = rec.Region
	}
	if rec.City != "" {
		result.City = rec.City
	}
	if rec.Lat != 0 || rec.Lon != 0 {
		result.Lat = rec.Lat
		result.Lon = rec.Lon
	}

	return result
}
