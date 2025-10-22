package iporgdb

import (
	"fmt"
	"log"
	"net/netip"

	"github.com/syndtr/goleveldb/leveldb/util"

	"iporg/pkg/model"
	"iporg/pkg/util/ipcodec"
)

// PutRange stores an IP range record in the database
// It performs conflict detection for overlapping ranges
func (d *DB) PutRange(rec *model.Record) error {
	if !rec.Start.IsValid() || !rec.End.IsValid() {
		return model.ErrInvalidRange
	}

	// Validate that start <= end
	if rec.Start.Compare(rec.End) > 0 {
		return fmt.Errorf("%w: start %v > end %v", model.ErrInvalidRange, rec.Start, rec.End)
	}

	// Check for overlaps
	if err := d.checkOverlap(rec); err != nil {
		return err
	}

	// Encode the record
	value, err := encodeRecord(rec)
	if err != nil {
		return fmt.Errorf("failed to encode record: %w", err)
	}

	// Store with start IP as key
	key := ipcodec.EncodeRangeKey(rec.Start)
	if err := d.Put(key, value); err != nil {
		return fmt.Errorf("failed to store range: %w", err)
	}

	return nil
}

// checkOverlap checks if a new range overlaps with existing ranges
// Returns an error if an unresolvable overlap is detected
func (d *DB) checkOverlap(newRec *model.Record) error {
	// Find ranges that might overlap
	// We need to check:
	// 1. Any range that starts before newRec.End
	// 2. Any range that ends after newRec.Start

	var prefix string
	if newRec.Start.Is4() {
		prefix = ipcodec.PrefixRangeV4
	} else {
		prefix = ipcodec.PrefixRangeV6
	}

	// Create iterator for the same IP version
	slice := &util.Range{
		Start: []byte(prefix),
		Limit: []byte(prefix + "\xFF"), // End of this prefix range
	}

	iter := d.NewIterator(slice)
	defer iter.Release()

	// Seek to the key just before or at newRec.Start
	searchKey := ipcodec.EncodeRangeKey(newRec.Start)
	iter.Seek(searchKey)

	// Also check the previous record
	if iter.Valid() {
		iter.Prev()
	} else {
		iter.Last()
	}

	// Check up to 2 records: the one before and at/after start
	for checked := 0; checked < 2 && iter.Valid(); checked++ {
		key := make([]byte, len(iter.Key()))
		copy(key, iter.Key())

		value := make([]byte, len(iter.Value()))
		copy(value, iter.Value())

		// Decode existing record
		startIP, err := ipcodec.DecodeRangeKey(key)
		if err != nil {
			iter.Next()
			continue
		}

		existingRec, err := decodeRecord(ipcodec.IPToBytes(startIP), value)
		if err != nil {
			iter.Next()
			continue
		}

		// Check for overlap: ranges overlap if:
		// newStart <= existingEnd && existingStart <= newEnd
		if newRec.Start.Compare(existingRec.End) <= 0 &&
			existingRec.Start.Compare(newRec.End) <= 0 {

			// Ranges overlap - determine if it's acceptable
			if existingRec.Start == newRec.Start && existingRec.End == newRec.End {
				// Exact match - this is an update, which is OK
				log.Printf("INFO: Updating existing range %v-%v", newRec.Start, newRec.End)
				return nil
			}

			// Check if new range is more specific (longer prefix)
			newPrefixLen := getPrefixLen(newRec.Prefix)
			existingPrefixLen := getPrefixLen(existingRec.Prefix)

			if newPrefixLen > existingPrefixLen {
				// New range is more specific than existing - skip it
				// (We process least specific first, so the existing one has broader coverage)
				log.Printf("INFO: Skipping more specific range %s (covered by %s)",
					newRec.Prefix, existingRec.Prefix)
				return fmt.Errorf("%w: %s is covered by less specific %s",
					model.ErrOverlap, newRec.Prefix, existingRec.Prefix)
			} else if newPrefixLen < existingPrefixLen {
				// New range is less specific - this shouldn't happen if we sorted correctly
				// but if it does, replace the more specific with less specific
				log.Printf("WARN: New range %s is less specific than existing %s (replacing)",
					newRec.Prefix, existingRec.Prefix)
				// Delete the old range
				if err := d.Delete(key); err != nil {
					return fmt.Errorf("failed to delete overlapping range: %w", err)
				}
				return nil
			} else {
				// Same specificity but different ranges - conflict
				return fmt.Errorf("%w: %s overlaps with %s",
					model.ErrOverlap, newRec.Prefix, existingRec.Prefix)
			}
		}

		iter.Next()
	}

	return nil
}

// getPrefixLen extracts the prefix length from a CIDR string
func getPrefixLen(cidr string) int {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return 0
	}
	return prefix.Bits()
}

// DeleteRange removes a range record by start IP
func (d *DB) DeleteRange(start netip.Addr) error {
	key := ipcodec.EncodeRangeKey(start)
	return d.Delete(key)
}

// IterateRanges iterates over all range records
func (d *DB) IterateRanges(v4 bool, fn func(*model.Record) error) error {
	var prefix string
	if v4 {
		prefix = ipcodec.PrefixRangeV4
	} else {
		prefix = ipcodec.PrefixRangeV6
	}

	slice := &util.Range{
		Start: []byte(prefix),
		Limit: []byte(prefix + "\xFF"),
	}

	iter := d.NewIterator(slice)
	defer iter.Release()

	for iter.Next() {
		key := iter.Key()
		value := iter.Value()

		startIP, err := ipcodec.DecodeRangeKey(key)
		if err != nil {
			log.Printf("WARN: Failed to decode key: %v", err)
			continue
		}

		rec, err := decodeRecord(ipcodec.IPToBytes(startIP), value)
		if err != nil {
			log.Printf("WARN: Failed to decode record for %v: %v", startIP, err)
			continue
		}

		if err := fn(rec); err != nil {
			return err
		}
	}

	return iter.Error()
}

// CountRanges counts the total number of range records
func (d *DB) CountRanges() (ipv4, ipv6 int64, err error) {
	// Count IPv4
	v4Slice := &util.Range{
		Start: []byte(ipcodec.PrefixRangeV4),
		Limit: []byte(ipcodec.PrefixRangeV4 + "\xFF"),
	}
	v4Iter := d.NewIterator(v4Slice)
	for v4Iter.Next() {
		ipv4++
	}
	v4Iter.Release()
	if err := v4Iter.Error(); err != nil {
		return 0, 0, err
	}

	// Count IPv6
	v6Slice := &util.Range{
		Start: []byte(ipcodec.PrefixRangeV6),
		Limit: []byte(ipcodec.PrefixRangeV6 + "\xFF"),
	}
	v6Iter := d.NewIterator(v6Slice)
	for v6Iter.Next() {
		ipv6++
	}
	v6Iter.Release()
	if err := v6Iter.Error(); err != nil {
		return 0, 0, err
	}

	return ipv4, ipv6, nil
}
