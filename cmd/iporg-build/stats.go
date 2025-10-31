// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/wingedpig/iporg/pkg/iporgdb"
)

// RunStats displays database statistics
func RunStats(ctx context.Context, dbPath string, verbose bool) error {
	log.Printf("INFO: Opening database at %s", dbPath)
	db, err := iporgdb.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	// Get stats
	stats, err := db.Stats(ctx)
	if err != nil {
		return fmt.Errorf("failed to get stats: %w", err)
	}

	// Print summary
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("DATABASE STATISTICS")
	fmt.Println(strings.Repeat("=", 60))

	// Metadata
	if !stats.LastBuiltAt.IsZero() {
		fmt.Printf("Built at:               %s\n", stats.LastBuiltAt.Format("2006-01-02 15:04:05 MST"))
	}
	if stats.BuilderVersion != "" {
		fmt.Printf("Builder version:        %s\n", stats.BuilderVersion)
	}
	if stats.SchemaVersion > 0 {
		fmt.Printf("Schema version:         %d\n", stats.SchemaVersion)
	}

	fmt.Println()

	// Record counts
	fmt.Printf("Total records:          %d\n", stats.TotalRecords)
	fmt.Printf("  IPv4 records:         %d\n", stats.IPv4Records)
	fmt.Printf("  IPv6 records:         %d\n", stats.IPv6Records)

	// RIR breakdown
	if len(stats.RecordsByRIR) > 0 {
		fmt.Println("\nRecords by RIR:")
		printBreakdown(stats.RecordsByRIR)
	}

	// Source role breakdown
	if len(stats.RecordsByRole) > 0 {
		fmt.Println("\nRecords by source role:")
		printBreakdown(stats.RecordsByRole)
	}

	// Country breakdown (top 20)
	if len(stats.RecordsByCountry) > 0 {
		fmt.Println("\nRecords by country (top 20):")
		printBreakdownTop(stats.RecordsByCountry, 20)
	}

	// Verbose mode: show all countries
	if verbose && len(stats.RecordsByCountry) > 20 {
		fmt.Println("\nAll countries:")
		printBreakdown(stats.RecordsByCountry)
	}

	fmt.Println(strings.Repeat("=", 60))

	return nil
}

// printBreakdown prints a breakdown of counts by category
func printBreakdown(breakdown map[string]int64) {
	// Sort by count (descending)
	type kv struct {
		key   string
		value int64
	}

	var sorted []kv
	for k, v := range breakdown {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].value > sorted[j].value
	})

	for _, item := range sorted {
		fmt.Printf("  %-20s %d\n", item.key, item.value)
	}
}

// printBreakdownTop prints top N items from a breakdown
func printBreakdownTop(breakdown map[string]int64, topN int) {
	type kv struct {
		key   string
		value int64
	}

	var sorted []kv
	for k, v := range breakdown {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].value > sorted[j].value
	})

	for i, item := range sorted {
		if i >= topN {
			break
		}
		fmt.Printf("  %-20s %d\n", item.key, item.value)
	}

	if len(sorted) > topN {
		fmt.Printf("  ... and %d more\n", len(sorted)-topN)
	}
}
