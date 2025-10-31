// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package ripebulk

import (
	"net/netip"
	"path/filepath"
	"testing"
)

// BenchmarkLookupIP benchmarks single IP lookups
func BenchmarkLookupIP(b *testing.B) {
	// Create a test database with overlapping ranges
	tmpDir := b.TempDir()
	dbPath := filepath.Join(tmpDir, "bench.ldb")

	// Create realistic test data
	orgs := map[string]Organisation{
		"ORG-TEST1-RIPE": {OrgID: "ORG-TEST1-RIPE", OrgName: "Test Org 1", OrgType: "LIR"},
		"ORG-TEST2-RIPE": {OrgID: "ORG-TEST2-RIPE", OrgName: "Test Org 2", OrgType: "OTHER"},
	}

	// Create nested ranges like real RIPE data
	var inetnums []Inetnum
	for i := 0; i < 100; i++ {
		// Parent range
		start := uint32(167772160 + i*65536) // 10.0.0.0 + i*64k
		inetnums = append(inetnums, Inetnum{
			Start:   start,
			End:     start + 65535,
			OrgID:   "ORG-TEST1-RIPE",
			Status:  "ALLOCATED-PA",
			Country: "GB",
			Netname: "PARENT",
		})
		// Child range (same start)
		inetnums = append(inetnums, Inetnum{
			Start:   start,
			End:     start + 255,
			OrgID:   "ORG-TEST2-RIPE",
			Status:  "ASSIGNED-PA",
			Country: "GB",
			Netname: "CHILD",
		})
	}

	db, err := BuildDatabase(dbPath, inetnums, orgs)
	if err != nil {
		b.Fatalf("BuildDatabase failed: %v", err)
	}
	defer db.Close()

	// Benchmark lookup
	testIP := netip.MustParseAddr("10.0.1.1")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.LookupIP(testIP)
		if err != nil {
			b.Fatalf("LookupIP failed: %v", err)
		}
	}
}

// BenchmarkLookupPrefix benchmarks prefix lookups
func BenchmarkLookupPrefix(b *testing.B) {
	tmpDir := b.TempDir()
	dbPath := filepath.Join(tmpDir, "bench.ldb")

	orgs := map[string]Organisation{
		"ORG-TEST1-RIPE": {OrgID: "ORG-TEST1-RIPE", OrgName: "Test Org", OrgType: "LIR"},
	}

	var inetnums []Inetnum
	for i := 0; i < 100; i++ {
		start := uint32(167772160 + i*65536)
		inetnums = append(inetnums, Inetnum{
			Start:   start,
			End:     start + 65535,
			OrgID:   "ORG-TEST1-RIPE",
			Status:  "ALLOCATED-PA",
			Country: "GB",
			Netname: "TEST",
		})
	}

	db, err := BuildDatabase(dbPath, inetnums, orgs)
	if err != nil {
		b.Fatalf("BuildDatabase failed: %v", err)
	}
	defer db.Close()

	testPrefix := netip.MustParsePrefix("10.0.1.0/24")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.LookupPrefix(testPrefix)
		if err != nil {
			b.Fatalf("LookupPrefix failed: %v", err)
		}
	}
}
