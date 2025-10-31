// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/wingedpig/iporg/pkg/iptoasn"
	"github.com/wingedpig/iporg/pkg/model"
)

// Builder coordinates the build process
type Builder struct {
	cfg *Config
}

// NewBuilder creates a new builder
func NewBuilder(cfg *Config) *Builder {
	return &Builder{cfg: cfg}
}

// Build executes the build process
func (b *Builder) Build(ctx context.Context) error {
	startTime := time.Now()

	// 1. Load cached data
	meta, reader, err := b.loadCachedData()
	if err != nil {
		return fmt.Errorf("failed to load cached data: %w", err)
	}
	defer reader.Close()

	log.Printf("Parsing TSV from %s...", meta.CachePath)

	// 2. Parse TSV
	parser := iptoasn.NewParser(reader)
	rows, err := parser.ParseAll()
	if err != nil {
		return fmt.Errorf("failed to parse TSV: %w", err)
	}

	log.Printf("Parsed %d rows", len(rows))

	// 3. Convert to canonical prefixes
	log.Printf("Converting to canonical prefixes...")
	var prefixes []*model.CanonicalPrefix
	for _, row := range rows {
		// Skip if no CIDR
		if row.Prefix == nil {
			continue
		}

		cp := &model.CanonicalPrefix{
			CIDR:     row.Prefix.String(),
			ASN:      row.ASN,
			Country:  row.Country,
			Registry: row.Registry,
			ASName:   row.ASName,
		}
		prefixes = append(prefixes, cp)
	}

	log.Printf("Generated %d canonical prefixes", len(prefixes))

	// 4. Deduplicate
	log.Printf("Deduplicating...")
	aggregator := iptoasn.NewAggregator()
	prefixes = aggregator.Deduplicate(prefixes)
	log.Printf("After deduplication: %d prefixes", len(prefixes))

	// 5. Sort by start IP
	log.Printf("Sorting by start IP...")
	aggregator.SortByStartIP(prefixes)

	// 6. Collapse per ASN (if enabled)
	var collapsedByASN map[int][]*model.CanonicalPrefix
	if b.cfg.collapse {
		log.Printf("Collapsing adjacent prefixes per ASN...")
		collapsedByASN = aggregator.CollapseByASN(prefixes)

		totalCollapsed := 0
		for _, collapsed := range collapsedByASN {
			totalCollapsed += len(collapsed)
		}
		log.Printf("After collapse: %d prefixes (saved %d)", totalCollapsed, len(prefixes)-totalCollapsed)
	}

	// 7. Open/create database
	log.Printf("Opening database at %s...", b.cfg.dbPath)
	store, err := iptoasn.Open(b.cfg.dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer store.Close()

	// 8. Write to database
	log.Printf("Writing to database...")
	if err := store.WriteBatch(prefixes, collapsedByASN); err != nil {
		return fmt.Errorf("failed to write batch: %w", err)
	}

	// 9. Calculate statistics
	log.Printf("Calculating statistics...")
	stats := b.calculateStats(prefixes, collapsedByASN, meta)

	// 10. Write metadata and stats
	if err := store.SetMetadata("source_url", b.cfg.sourceURL); err != nil {
		log.Printf("Warning: failed to set source_url metadata: %v", err)
	}
	if err := store.SetMetadata("built_at", time.Now().Format(time.RFC3339)); err != nil {
		log.Printf("Warning: failed to set built_at metadata: %v", err)
	}
	if err := store.SetMetadata("version", version); err != nil {
		log.Printf("Warning: failed to set version metadata: %v", err)
	}
	if err := store.SetStats(stats); err != nil {
		log.Printf("Warning: failed to set stats: %v", err)
	}

	duration := time.Since(startTime)
	log.Printf("Build completed in %s", duration)

	// Print summary
	fmt.Printf("\nBuild Summary:\n")
	fmt.Printf("  Total prefixes:    %d\n", stats.TotalPrefixes)
	fmt.Printf("  IPv4 prefixes:     %d\n", stats.IPv4Prefixes)
	fmt.Printf("  Collapsed (IPv4):  %d\n", stats.CollapsedV4)
	fmt.Printf("  Unique ASNs:       %d\n", stats.UniqueASNs)
	fmt.Printf("  Build time:        %s\n", duration)

	return nil
}

// loadCachedData loads the most recent cached file
func (b *Builder) loadCachedData() (*model.FetchMetadata, io.ReadCloser, error) {
	// Load metadata
	metaPath := filepath.Join(b.cfg.cacheDir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read metadata (did you run fetch first?): %w", err)
	}

	var meta model.FetchMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	// Open cached file
	fetcher := iptoasn.NewFetcher(b.cfg.sourceURL, b.cfg.cacheDir)
	reader, err := fetcher.OpenCachedFile(&meta)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open cached file: %w", err)
	}

	return &meta, reader, nil
}

// calculateStats computes database statistics
func (b *Builder) calculateStats(prefixes []*model.CanonicalPrefix, collapsedByASN map[int][]*model.CanonicalPrefix, meta *model.FetchMetadata) *model.IPToASNStats {
	stats := &model.IPToASNStats{
		SourceURL:    b.cfg.sourceURL,
		LastModified: meta.LastModified,
		BuiltAt:      time.Now(),
		ETag:         meta.ETag,
		ByRegistry:   make(map[string]int64),
	}

	asnSet := make(map[int]bool)

	for _, p := range prefixes {
		stats.TotalPrefixes++

		// Check if IPv4
		_, ipnet, err := net.ParseCIDR(p.CIDR)
		if err == nil && ipnet.IP.To4() != nil {
			stats.IPv4Prefixes++
		}

		// Count by registry
		stats.ByRegistry[p.Registry]++

		// Track unique ASNs
		asnSet[p.ASN] = true
	}

	stats.UniqueASNs = len(asnSet)

	// Count collapsed
	for _, collapsed := range collapsedByASN {
		stats.CollapsedV4 += int64(len(collapsed))
	}

	if stats.CollapsedV4 == 0 {
		stats.CollapsedV4 = stats.IPv4Prefixes
	}

	return stats
}
