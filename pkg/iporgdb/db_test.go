// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package iporgdb

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/wingedpig/iporg/pkg/model"
)

func TestOpenClose(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iporgdb-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	if db.Path() != tmpDir {
		t.Errorf("got path %s, want %s", db.Path(), tmpDir)
	}

	if db.IsClosed() {
		t.Error("database should not be closed")
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Failed to close database: %v", err)
	}

	if !db.IsClosed() {
		t.Error("database should be closed")
	}
}

func TestPutGetRange(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iporgdb-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create a test record
	rec := &model.Record{
		Start:       netip.MustParseAddr("192.168.1.0"),
		End:         netip.MustParseAddr("192.168.1.255"),
		ASN:         64512,
		ASNName:     "TEST-AS",
		OrgName:     "Test Organization",
		RIR:         "TEST",
		Country:     "US",
		Region:      "California",
		City:        "San Francisco",
		Lat:         37.7749,
		Lon:         -122.4194,
		SourceRole:  "customer",
		StatusLabel: "ASSIGNED",
		Prefix:      "192.168.1.0/24",
		LastChecked: time.Now(),
		Schema:      1,
	}

	// Store the record
	if err := db.PutRange(rec); err != nil {
		t.Fatalf("Failed to put range: %v", err)
	}

	// Look up an IP in the range
	ip := netip.MustParseAddr("192.168.1.100")
	found, err := db.GetByIP(ip)
	if err != nil {
		t.Fatalf("Failed to get by IP: %v", err)
	}

	if found.ASN != rec.ASN {
		t.Errorf("got ASN %d, want %d", found.ASN, rec.ASN)
	}
	if found.OrgName != rec.OrgName {
		t.Errorf("got OrgName %s, want %s", found.OrgName, rec.OrgName)
	}
	if found.Country != rec.Country {
		t.Errorf("got Country %s, want %s", found.Country, rec.Country)
	}
}

func TestLookupNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iporgdb-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Look up an IP that doesn't exist
	ip := netip.MustParseAddr("10.0.0.1")
	_, err = db.GetByIP(ip)
	if err != model.ErrNotFound {
		t.Errorf("got error %v, want %v", err, model.ErrNotFound)
	}
}

func TestMultipleRanges(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iporgdb-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Add multiple ranges
	ranges := []struct {
		cidr    string
		asn     int
		orgName string
	}{
		{"10.0.0.0/24", 100, "Org A"},
		{"10.0.1.0/24", 101, "Org B"},
		{"10.0.2.0/24", 102, "Org C"},
		{"192.168.0.0/16", 200, "Org D"},
	}

	for _, r := range ranges {
		start := netip.MustParsePrefix(r.cidr).Addr()
		// Calculate end IP
		prefix := netip.MustParsePrefix(r.cidr)
		bits := prefix.Bits()
		hostBits := 32 - bits
		endInt := uint32(1<<hostBits - 1)

		startBytes := start.As4()
		var startInt uint32
		startInt = uint32(startBytes[0])<<24 | uint32(startBytes[1])<<16 |
			uint32(startBytes[2])<<8 | uint32(startBytes[3])
		endIntFull := startInt + endInt

		endBytes := [4]byte{
			byte(endIntFull >> 24),
			byte(endIntFull >> 16),
			byte(endIntFull >> 8),
			byte(endIntFull),
		}
		end := netip.AddrFrom4(endBytes)

		rec := &model.Record{
			Start:       start,
			End:         end,
			ASN:         r.asn,
			OrgName:     r.orgName,
			Prefix:      r.cidr,
			LastChecked: time.Now(),
			Schema:      1,
		}

		if err := db.PutRange(rec); err != nil {
			t.Fatalf("Failed to put range %s: %v", r.cidr, err)
		}
	}

	// Test lookups
	tests := []struct {
		ip      string
		wantASN int
		wantOrg string
	}{
		{"10.0.0.50", 100, "Org A"},
		{"10.0.1.100", 101, "Org B"},
		{"10.0.2.200", 102, "Org C"},
		{"192.168.50.50", 200, "Org D"},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := netip.MustParseAddr(tt.ip)
			rec, err := db.GetByIP(ip)
			if err != nil {
				t.Fatalf("Failed to lookup %s: %v", tt.ip, err)
			}
			if rec.ASN != tt.wantASN {
				t.Errorf("got ASN %d, want %d", rec.ASN, tt.wantASN)
			}
			if rec.OrgName != tt.wantOrg {
				t.Errorf("got OrgName %s, want %s", rec.OrgName, tt.wantOrg)
			}
		})
	}
}

func TestMetadata(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iporgdb-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Test schema version
	if err := db.SetSchemaVersion(1); err != nil {
		t.Fatalf("Failed to set schema version: %v", err)
	}
	version, err := db.GetSchemaVersion()
	if err != nil {
		t.Fatalf("Failed to get schema version: %v", err)
	}
	if version != 1 {
		t.Errorf("got version %d, want 1", version)
	}

	// Test built at
	now := time.Now()
	if err := db.SetBuiltAt(now); err != nil {
		t.Fatalf("Failed to set built_at: %v", err)
	}
	builtAt, err := db.GetBuiltAt()
	if err != nil {
		t.Fatalf("Failed to get built_at: %v", err)
	}
	// Compare with second precision (RFC3339 doesn't preserve nanoseconds)
	if builtAt.Unix() != now.Unix() {
		t.Errorf("got built_at %v, want %v", builtAt, now)
	}

	// Test builder version
	if err := db.SetBuilderVersion("test-123"); err != nil {
		t.Fatalf("Failed to set builder version: %v", err)
	}
	builderVer, err := db.GetBuilderVersion()
	if err != nil {
		t.Fatalf("Failed to get builder version: %v", err)
	}
	if builderVer != "test-123" {
		t.Errorf("got builder version %s, want test-123", builderVer)
	}
}

func TestStats(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iporgdb-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Initialize metadata
	if err := db.InitializeMetadata("test-v1.0"); err != nil {
		t.Fatalf("Failed to initialize metadata: %v", err)
	}

	// Add some records
	for i := 0; i < 3; i++ {
		start := netip.MustParseAddr("10.0.0.0")
		end := netip.MustParseAddr("10.0.0.255")
		rec := &model.Record{
			Start:       start,
			End:         end,
			ASN:         100 + i,
			OrgName:     "Test Org",
			RIR:         "TEST",
			Country:     "US",
			SourceRole:  "customer",
			Prefix:      "10.0.0.0/24",
			LastChecked: time.Now(),
			Schema:      1,
		}
		// Modify the start IP for each record
		bytes := start.As4()
		bytes[2] = byte(i)
		rec.Start = netip.AddrFrom4(bytes)
		bytes[3] = 255
		rec.End = netip.AddrFrom4(bytes)

		if err := db.PutRange(rec); err != nil {
			t.Fatalf("Failed to put range: %v", err)
		}
	}

	// Get stats
	ctx := context.Background()
	stats, err := db.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	if stats.TotalRecords != 3 {
		t.Errorf("got total records %d, want 3", stats.TotalRecords)
	}
	if stats.IPv4Records != 3 {
		t.Errorf("got IPv4 records %d, want 3", stats.IPv4Records)
	}
	if stats.RecordsByRIR["TEST"] != 3 {
		t.Errorf("got RIR TEST count %d, want 3", stats.RecordsByRIR["TEST"])
	}
}

// TestOverlapMultipleChildren tests the regression where inserting a less-specific range
// only deleted the first overlapping child instead of all children
func TestOverlapMultipleChildren(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iporgdb-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// First, insert three specific /24 prefixes (10.0.0.0/24, 10.0.1.0/24, 10.0.2.0/24)
	specificPrefixes := []string{"10.0.0.0/24", "10.0.1.0/24", "10.0.2.0/24"}
	for _, cidr := range specificPrefixes {
		prefix := netip.MustParsePrefix(cidr)
		start := prefix.Addr()
		// Calculate end
		bits := prefix.Bits()
		hostBits := 32 - bits
		endOffset := uint32(1<<hostBits - 1)
		startInt := uint32(start.As4()[0])<<24 | uint32(start.As4()[1])<<16 |
			uint32(start.As4()[2])<<8 | uint32(start.As4()[3])
		endInt := startInt + endOffset
		end := netip.AddrFrom4([4]byte{
			byte(endInt >> 24), byte(endInt >> 16),
			byte(endInt >> 8), byte(endInt),
		})

		rec := &model.Record{
			Start:       start,
			End:         end,
			ASN:         100,
			OrgName:     "Specific Org",
			Prefix:      cidr,
			LastChecked: time.Now(),
			Schema:      1,
		}
		if err := db.PutRange(rec); err != nil {
			t.Fatalf("Failed to put specific prefix %s: %v", cidr, err)
		}
	}

	// Now insert a less-specific /22 that covers all three /24s (10.0.0.0/22)
	broadPrefix := netip.MustParsePrefix("10.0.0.0/22")
	broadStart := broadPrefix.Addr()
	broadBits := broadPrefix.Bits()
	broadHostBits := 32 - broadBits
	broadEndOffset := uint32(1<<broadHostBits - 1)
	broadStartInt := uint32(broadStart.As4()[0])<<24 | uint32(broadStart.As4()[1])<<16 |
		uint32(broadStart.As4()[2])<<8 | uint32(broadStart.As4()[3])
	broadEndInt := broadStartInt + broadEndOffset
	broadEnd := netip.AddrFrom4([4]byte{
		byte(broadEndInt >> 24), byte(broadEndInt >> 16),
		byte(broadEndInt >> 8), byte(broadEndInt),
	})

	broadRec := &model.Record{
		Start:       broadStart,
		End:         broadEnd,
		ASN:         200,
		OrgName:     "Broad Org",
		Prefix:      "10.0.0.0/22",
		LastChecked: time.Now(),
		Schema:      1,
	}

	if err := db.PutRange(broadRec); err != nil {
		t.Fatalf("Failed to put broad prefix: %v", err)
	}

	// Now verify: lookups in the /22 range should ALL return "Broad Org", not "Specific Org"
	// This tests that all three /24s were deleted, not just the first one
	testIPs := []string{
		"10.0.0.100", // In first /24
		"10.0.1.100", // In second /24
		"10.0.2.100", // In third /24
		"10.0.3.100", // In fourth /24 of the /22
	}

	for _, ipStr := range testIPs {
		ip := netip.MustParseAddr(ipStr)
		rec, err := db.GetByIP(ip)
		if err != nil {
			t.Fatalf("Failed to lookup %s: %v", ipStr, err)
		}
		if rec.OrgName != "Broad Org" {
			t.Errorf("IP %s: got org '%s', want 'Broad Org' (children not fully deleted)",
				ipStr, rec.OrgName)
		}
		if rec.ASN != 200 {
			t.Errorf("IP %s: got ASN %d, want 200", ipStr, rec.ASN)
		}
	}
}
