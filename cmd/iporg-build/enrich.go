package main

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"strings"
	"sync"
	"time"

	"iporg/pkg/model"
	"iporg/pkg/sources/maxmind"
	"iporg/pkg/sources/rdap"
	"iporg/pkg/util/ipcodec"
	"iporg/pkg/util/workers"
)

// enrichAndWriteModeA processes prefixes in Mode A (one record per prefix)
func (b *Builder) enrichAndWriteModeA(ctx context.Context, prefixes []string) error {
	// Create worker pool
	pool := workers.NewPool(ctx, workers.Config{
		Workers:   b.cfg.Workers,
		RateLimit: 0, // Rate limiting handled by individual clients
	})

	var mu sync.Mutex
	totalPrefixes := len(prefixes)

	for i, prefix := range prefixes {
		idx := i
		currentPrefix := prefix

		pool.Submit(idx, func(ctx context.Context) error {
			// Normalize prefix
			normalized, err := ipcodec.NormalizePrefix(currentPrefix)
			if err != nil {
				log.Printf("ERROR: Invalid prefix %s: %v", currentPrefix, err)
				mu.Lock()
				b.stats.Errors++
				mu.Unlock()
				return nil
			}

			// Get start and end IPs
			start, end, err := ipcodec.CIDRToRange(normalized)
			if err != nil {
				log.Printf("ERROR: Failed to parse prefix %s: %v", normalized, err)
				mu.Lock()
				b.stats.Errors++
				mu.Unlock()
				return nil
			}

			// Get representative IP for lookups
			repIP, err := ipcodec.RepresentativeIP(normalized)
			if err != nil {
				log.Printf("ERROR: Failed to get representative IP for %s: %v", normalized, err)
				mu.Lock()
				b.stats.Errors++
				mu.Unlock()
				return nil
			}

			// Create record
			rec := &model.Record{
				Start:       start,
				End:         end,
				Prefix:      normalized,
				LastChecked: time.Now(),
				Schema:      1,
			}

			// Enrich with MaxMind ASN
			asn, asnName, err := b.maxmind.ASNInfo(repIP)
			if err != nil {
				log.Printf("WARN: MaxMind ASN lookup failed for %s: %v", normalized, err)
				// Continue without ASN info
			} else {
				rec.ASN = asn
				rec.ASNName = asnName
			}

			// Enrich with MaxMind Geo
			geo, err := b.maxmind.Geo(repIP)
			if err != nil {
				log.Printf("WARN: MaxMind geo lookup failed for %s: %v", normalized, err)
			} else {
				rec.Country = geo.Country
				rec.Region = geo.Region
				rec.City = geo.City
				rec.Lat = geo.Lat
				rec.Lon = geo.Lon
			}

			// Try RIPE bulk first, then fall back to RDAP
			var rdapOrg *model.RDAPOrg

			// Parse prefix for RIPE bulk lookup
			parsedPrefix, parseErr := netip.ParsePrefix(normalized)
			if parseErr == nil {
				rdapOrg = b.tryRIPEBulkLookupPrefix(parsedPrefix)
				if rdapOrg != nil {
					mu.Lock()
					b.stats.RIPEBulkHits++
					// Log first few hits, then every 100th
					if b.stats.RIPEBulkHits <= 5 || b.stats.RIPEBulkHits%100 == 0 {
						log.Printf("INFO: RIPE bulk hit #%d for %s -> %s", b.stats.RIPEBulkHits, normalized, rdapOrg.OrgName)
					}
					mu.Unlock()
				}
			}

			// Fall back to RDAP if RIPE bulk didn't find it
			if rdapOrg == nil {
				var rdapErr error
				rdapOrg, rdapErr = b.rdapClient.OrgForPrefix(ctx, normalized)
				if rdapErr != nil {
					log.Printf("WARN: RDAP lookup failed for %s: %v", normalized, rdapErr)
					// Fallback to MaxMind ASN org
					rec.OrgName = asnName
					rec.SourceRole = "asn_fallback"
					rec.RIR = "UNKNOWN"
					mu.Lock()
					b.stats.RDAPCacheMisses++
					b.stats.Errors++
					mu.Unlock()
				} else {
					rec.OrgName = rdap.CleanOrgName(rdapOrg.OrgName)
					rec.SourceRole = rdapOrg.SourceRole
					rec.StatusLabel = rdapOrg.StatusLabel
					rec.RIR = rdapOrg.RIR
					mu.Lock()
					b.stats.RDAPCacheHits++
					mu.Unlock()
				}
			} else {
				// Use RIPE bulk data
				rec.OrgName = rdap.CleanOrgName(rdapOrg.OrgName)
				rec.SourceRole = rdapOrg.SourceRole
				rec.StatusLabel = rdapOrg.StatusLabel
				rec.RIR = rdapOrg.RIR
			}

			// Ensure we have at least some org name
			if rec.OrgName == "" {
				rec.OrgName = asnName
				rec.SourceRole = "asn_fallback"
			}

			// Write to database
			if err := b.db.PutRange(rec); err != nil {
				// Check if this is just an overlap with a less specific range (expected)
				if strings.Contains(err.Error(), "is covered by less specific") {
					mu.Lock()
					b.stats.RecordsSkipped++
					b.stats.PrefixesProcessed++
					mu.Unlock()
					return nil
				}
				log.Printf("ERROR: Failed to write record for %s: %v", normalized, err)
				mu.Lock()
				b.stats.Errors++
				mu.Unlock()
				return nil
			}

			mu.Lock()
			b.stats.RecordsWritten++
			b.stats.PrefixesProcessed++
			if (idx+1)%100 == 0 || idx+1 == totalPrefixes {
				progress := float64(idx+1) / float64(totalPrefixes) * 100
				log.Printf("INFO: Progress: %d/%d (%.1f%%) - Last: %s -> %s",
					idx+1, totalPrefixes, progress, normalized, rec.OrgName)
			}
			mu.Unlock()

			return nil
		})
	}

	// Wait for all workers to complete
	results := pool.Wait()

	// Check for errors
	for _, result := range results {
		if result.Error != nil {
			log.Printf("WARN: Worker error: %v", result.Error)
		}
	}

	return nil
}

// enrichAndWriteModeB processes prefixes in Mode B (split by MaxMind city blocks)
func (b *Builder) enrichAndWriteModeB(ctx context.Context, prefixes []string) error {
	// Create worker pool
	pool := workers.NewPool(ctx, workers.Config{
		Workers:   b.cfg.Workers,
		RateLimit: 0,
	})

	var mu sync.Mutex
	totalPrefixes := len(prefixes)

	for i, prefix := range prefixes {
		idx := i
		currentPrefix := prefix

		pool.Submit(idx, func(ctx context.Context) error {
			// Normalize prefix
			normalized, err := ipcodec.NormalizePrefix(currentPrefix)
			if err != nil {
				log.Printf("ERROR: Invalid prefix %s: %v", currentPrefix, err)
				mu.Lock()
				b.stats.Errors++
				mu.Unlock()
				return nil
			}

			// Parse prefix
			parsedPrefix, err := netip.ParsePrefix(normalized)
			if err != nil {
				log.Printf("ERROR: Failed to parse prefix %s: %v", normalized, err)
				mu.Lock()
				b.stats.Errors++
				mu.Unlock()
				return nil
			}

			// Determine minimum prefix length based on IP version
			minPrefixLen := b.minPrefixV4
			if parsedPrefix.Addr().Is6() {
				minPrefixLen = b.minPrefixV6
			}

			// Split by geo
			blocks, err := b.maxmind.SplitPrefixByGeo(parsedPrefix, minPrefixLen)
			if err != nil {
				log.Printf("ERROR: Failed to split prefix %s: %v", normalized, err)
				mu.Lock()
				b.stats.Errors++
				mu.Unlock()
				return nil
			}

			log.Printf("INFO: Split %s into %d blocks", normalized, len(blocks))

			// Process each block
			for _, block := range blocks {
				if err := b.processBlock(ctx, &mu, block, normalized); err != nil {
					log.Printf("ERROR: Failed to process block %s: %v", block.Prefix.String(), err)
					mu.Lock()
					b.stats.Errors++
					mu.Unlock()
				}
			}

			mu.Lock()
			b.stats.PrefixesProcessed++
			if (idx+1)%50 == 0 || idx+1 == totalPrefixes {
				progress := float64(idx+1) / float64(totalPrefixes) * 100
				log.Printf("INFO: Progress: %d/%d (%.1f%%)", idx+1, totalPrefixes, progress)
			}
			mu.Unlock()

			return nil
		})
	}

	// Wait for completion
	results := pool.Wait()
	for _, result := range results {
		if result.Error != nil {
			log.Printf("WARN: Worker error: %v", result.Error)
		}
	}

	return nil
}

// processBlock processes a single MaxMind block
func (b *Builder) processBlock(ctx context.Context, mu *sync.Mutex, block maxmind.NetworkBlock, originalPrefix string) error {
	start := block.Prefix.Addr()

	// Calculate end IP from prefix
	bits := block.Prefix.Bits()
	hostBits := start.BitLen() - bits

	startBytes := start.AsSlice()
	endBytes := make([]byte, len(startBytes))
	copy(endBytes, startBytes)

	// Add offset for end IP
	carry := uint64(1<<hostBits - 1)
	for i := len(endBytes) - 1; i >= 0 && carry > 0; i-- {
		sum := uint64(endBytes[i]) + carry
		endBytes[i] = byte(sum & 0xFF)
		carry = sum >> 8
	}

	end, _ := netip.AddrFromSlice(endBytes)

	// Create record
	rec := &model.Record{
		Start:       start,
		End:         end,
		Prefix:      originalPrefix, // Keep original announced prefix
		LastChecked: time.Now(),
		Schema:      1,
		Country:     block.Country,
		Region:      block.Region,
		City:        block.City,
		Lat:         block.Lat,
		Lon:         block.Lon,
	}

	// Get representative IP
	repIP := start

	// Enrich with MaxMind ASN
	asn, asnName, err := b.maxmind.ASNInfo(repIP)
	if err == nil {
		rec.ASN = asn
		rec.ASNName = asnName
	}

	// Try RIPE bulk first, then fall back to RDAP
	rdapOrg := b.tryRIPEBulkLookup(repIP)
	if rdapOrg != nil {
		mu.Lock()
		b.stats.RIPEBulkHits++
		// Log first few hits, then every 100th
		if b.stats.RIPEBulkHits <= 5 || b.stats.RIPEBulkHits%100 == 0 {
			log.Printf("INFO: RIPE bulk hit #%d (Mode B block)", b.stats.RIPEBulkHits)
		}
		mu.Unlock()
	} else {
		// Fall back to RDAP
		var err error
		rdapOrg, err = b.rdapClient.OrgForIP(ctx, repIP)
		if err != nil {
			rec.OrgName = asnName
			rec.SourceRole = "asn_fallback"
			rec.RIR = "UNKNOWN"
			mu.Lock()
			b.stats.RDAPCacheMisses++
			mu.Unlock()
		} else {
			mu.Lock()
			b.stats.RDAPCacheHits++
			mu.Unlock()
		}
	}

	if rdapOrg != nil {
		rec.OrgName = rdap.CleanOrgName(rdapOrg.OrgName)
		rec.SourceRole = rdapOrg.SourceRole
		rec.StatusLabel = rdapOrg.StatusLabel
		rec.RIR = rdapOrg.RIR
	}

	if rec.OrgName == "" {
		rec.OrgName = asnName
	}

	// Write to database
	if err := b.db.PutRange(rec); err != nil {
		// Check if this is just an overlap with a less specific range (expected)
		if strings.Contains(err.Error(), "is covered by less specific") {
			mu.Lock()
			b.stats.RecordsSkipped++
			mu.Unlock()
			return nil
		}
		return fmt.Errorf("failed to write block: %w", err)
	}

	mu.Lock()
	b.stats.RecordsWritten++
	mu.Unlock()

	return nil
}
