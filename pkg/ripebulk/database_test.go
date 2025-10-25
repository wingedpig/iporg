package ripebulk

import (
	"net/netip"
	"path/filepath"
	"testing"
)

func TestBuildAndLookup(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.ldb")

	// Create test data
	orgs := map[string]Organisation{
		"ORG-TEST1-RIPE": {
			OrgID:   "ORG-TEST1-RIPE",
			OrgName: "Test Organization 1",
			OrgType: "LIR",
		},
		"ORG-TEST2-RIPE": {
			OrgID:   "ORG-TEST2-RIPE",
			OrgName: "Test Organization 2",
			OrgType: "OTHER",
		},
	}

	// Create nested inetnums:
	// 10.0.0.0/8 (parent)
	//   10.1.0.0/16 (child)
	//     10.1.1.0/24 (grandchild)
	inetnums := []Inetnum{
		{
			Start:   AddrToUint32(netip.MustParseAddr("10.0.0.0")),
			End:     AddrToUint32(netip.MustParseAddr("10.255.255.255")),
			OrgID:   "ORG-TEST1-RIPE",
			Status:  "ALLOCATED-PA",
			Country: "GB",
			Netname: "TEST-PARENT",
		},
		{
			Start:   AddrToUint32(netip.MustParseAddr("10.1.0.0")),
			End:     AddrToUint32(netip.MustParseAddr("10.1.255.255")),
			OrgID:   "ORG-TEST2-RIPE",
			Status:  "ASSIGNED-PA",
			Country: "US",
			Netname: "TEST-CHILD",
		},
		{
			Start:   AddrToUint32(netip.MustParseAddr("10.1.1.0")),
			End:     AddrToUint32(netip.MustParseAddr("10.1.1.255")),
			OrgID:   "ORG-TEST2-RIPE",
			Status:  "SUB-ALLOCATED-PA",
			Country: "US",
			Netname: "TEST-GRANDCHILD",
		},
	}

	// Build database
	db, err := BuildDatabase(dbPath, inetnums, orgs)
	if err != nil {
		t.Fatalf("BuildDatabase failed: %v", err)
	}
	defer db.Close()

	// Verify metadata
	meta, err := db.GetMetadata()
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.InetnumCount != 3 {
		t.Errorf("Expected 3 inetnums, got %d", meta.InetnumCount)
	}
	if meta.OrgCount != 2 {
		t.Errorf("Expected 2 orgs, got %d", meta.OrgCount)
	}

	// Test lookups
	t.Run("lookup grandchild IP", func(t *testing.T) {
		// 10.1.1.1 should match the most specific range (10.1.1.0/24)
		ip := netip.MustParseAddr("10.1.1.1")
		match, err := db.LookupIP(ip)
		if err != nil {
			t.Fatalf("LookupIP failed: %v", err)
		}

		expectedStart := netip.MustParseAddr("10.1.1.0")
		expectedEnd := netip.MustParseAddr("10.1.1.255")

		if match.Start != expectedStart {
			t.Errorf("Expected start %s, got %s", expectedStart, match.Start)
		}
		if match.End != expectedEnd {
			t.Errorf("Expected end %s, got %s", expectedEnd, match.End)
		}
		if match.Netname != "TEST-GRANDCHILD" {
			t.Errorf("Expected 'TEST-GRANDCHILD', got '%s'", match.Netname)
		}
		if match.OrgName != "Test Organization 2" {
			t.Errorf("Expected 'Test Organization 2', got '%s'", match.OrgName)
		}
	})

	t.Run("lookup child IP", func(t *testing.T) {
		// 10.1.2.1 should match the child range (10.1.0.0/16)
		ip := netip.MustParseAddr("10.1.2.1")
		match, err := db.LookupIP(ip)
		if err != nil {
			t.Fatalf("LookupIP failed: %v", err)
		}

		expectedStart := netip.MustParseAddr("10.1.0.0")
		expectedEnd := netip.MustParseAddr("10.1.255.255")

		if match.Start != expectedStart {
			t.Errorf("Expected start %s, got %s", expectedStart, match.Start)
		}
		if match.End != expectedEnd {
			t.Errorf("Expected end %s, got %s", expectedEnd, match.End)
		}
		if match.Netname != "TEST-CHILD" {
			t.Errorf("Expected 'TEST-CHILD', got '%s'", match.Netname)
		}
	})

	t.Run("lookup parent IP", func(t *testing.T) {
		// 10.2.0.1 should match the parent range (10.0.0.0/8)
		ip := netip.MustParseAddr("10.2.0.1")
		match, err := db.LookupIP(ip)
		if err != nil {
			t.Fatalf("LookupIP failed: %v", err)
		}

		expectedStart := netip.MustParseAddr("10.0.0.0")
		expectedEnd := netip.MustParseAddr("10.255.255.255")

		if match.Start != expectedStart {
			t.Errorf("Expected start %s, got %s", expectedStart, match.Start)
		}
		if match.End != expectedEnd {
			t.Errorf("Expected end %s, got %s", expectedEnd, match.End)
		}
		if match.Netname != "TEST-PARENT" {
			t.Errorf("Expected 'TEST-PARENT', got '%s'", match.Netname)
		}
		if match.OrgName != "Test Organization 1" {
			t.Errorf("Expected 'Test Organization 1', got '%s'", match.OrgName)
		}
	})

	t.Run("lookup prefix", func(t *testing.T) {
		// 10.1.1.0/25 should match 10.1.1.0/24 (fully covered)
		prefix := netip.MustParsePrefix("10.1.1.0/25")
		match, err := db.LookupPrefix(prefix)
		if err != nil {
			t.Fatalf("LookupPrefix failed: %v", err)
		}

		if match.Netname != "TEST-GRANDCHILD" {
			t.Errorf("Expected 'TEST-GRANDCHILD', got '%s'", match.Netname)
		}
		if !match.FullyCovered {
			t.Error("Expected prefix to be fully covered")
		}
	})

	t.Run("lookup not found", func(t *testing.T) {
		// 192.0.2.1 should not match any range
		ip := netip.MustParseAddr("192.0.2.1")
		_, err := db.LookupIP(ip)
		if err != ErrNotFound {
			t.Errorf("Expected ErrNotFound, got %v", err)
		}
	})

	t.Run("get organisation", func(t *testing.T) {
		org, err := db.GetOrganisation("ORG-TEST1-RIPE")
		if err != nil {
			t.Fatalf("GetOrganisation failed: %v", err)
		}

		if org.OrgName != "Test Organization 1" {
			t.Errorf("Expected 'Test Organization 1', got '%s'", org.OrgName)
		}
		if org.OrgType != "LIR" {
			t.Errorf("Expected 'LIR', got '%s'", org.OrgType)
		}
	})

	t.Run("iterate ranges", func(t *testing.T) {
		count := 0
		err := db.IterateRanges(func(inet Inetnum) error {
			count++
			return nil
		})
		if err != nil {
			t.Fatalf("IterateRanges failed: %v", err)
		}

		if count != 3 {
			t.Errorf("Expected 3 ranges, iterated %d", count)
		}
	})
}

func TestOpenNonexistentDatabase(t *testing.T) {
	_, err := OpenDatabase("/nonexistent/path/db.ldb")
	if err == nil {
		t.Error("Expected error opening nonexistent database")
	}
}

func TestOverlappingRangesSameStart(t *testing.T) {
	// Test case for bug fix: multiple ranges with same start IP
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.ldb")

	orgs := map[string]Organisation{
		"ORG-PARENT-RIPE": {
			OrgID:   "ORG-PARENT-RIPE",
			OrgName: "Parent Organization",
			OrgType: "LIR",
		},
		"ORG-CHILD-RIPE": {
			OrgID:   "ORG-CHILD-RIPE",
			OrgName: "Child Organization",
			OrgType: "OTHER",
		},
	}

	// Create two ranges with same start IP (like BT parent and Plusnet child)
	// Parent: 147.147.0.0 - 147.152.255.255 (large range)
	// Child:  147.147.0.0 - 147.147.255.255 (smaller range, same start)
	inetnums := []Inetnum{
		{
			Start:   AddrToUint32(netip.MustParseAddr("147.147.0.0")),
			End:     AddrToUint32(netip.MustParseAddr("147.152.255.255")),
			OrgID:   "ORG-PARENT-RIPE",
			Status:  "ALLOCATED-PA",
			Country: "GB",
			Netname: "PARENT-BLOCK",
		},
		{
			Start:   AddrToUint32(netip.MustParseAddr("147.147.0.0")),
			End:     AddrToUint32(netip.MustParseAddr("147.147.255.255")),
			OrgID:   "ORG-CHILD-RIPE",
			Status:  "ASSIGNED-PA",
			Country: "GB",
			Netname: "CHILD-BLOCK",
		},
	}

	// Build database
	db, err := BuildDatabase(dbPath, inetnums, orgs)
	if err != nil {
		t.Fatalf("BuildDatabase failed: %v", err)
	}
	defer db.Close()

	// Verify both ranges are stored
	meta, _ := db.GetMetadata()
	if meta.InetnumCount != 2 {
		t.Errorf("Expected 2 inetnums, got %d", meta.InetnumCount)
	}

	t.Run("lookup in child range", func(t *testing.T) {
		// IP in first /16 should match the child (more specific)
		ip := netip.MustParseAddr("147.147.1.1")
		match, err := db.LookupIP(ip)
		if err != nil {
			t.Fatalf("LookupIP failed: %v", err)
		}

		if match.Netname != "CHILD-BLOCK" {
			t.Errorf("Expected CHILD-BLOCK, got %s", match.Netname)
		}
		if match.OrgName != "Child Organization" {
			t.Errorf("Expected 'Child Organization', got '%s'", match.OrgName)
		}
	})

	t.Run("lookup in parent-only range", func(t *testing.T) {
		// IP outside child range but in parent range (147.148.x.x)
		ip := netip.MustParseAddr("147.148.32.2")
		match, err := db.LookupIP(ip)
		if err != nil {
			t.Fatalf("LookupIP failed: %v", err)
		}

		if match.Netname != "PARENT-BLOCK" {
			t.Errorf("Expected PARENT-BLOCK, got %s", match.Netname)
		}
		if match.OrgName != "Parent Organization" {
			t.Errorf("Expected 'Parent Organization', got '%s'", match.OrgName)
		}

		// Verify range is the parent
		expectedStart := netip.MustParseAddr("147.147.0.0")
		expectedEnd := netip.MustParseAddr("147.152.255.255")
		if match.Start != expectedStart || match.End != expectedEnd {
			t.Errorf("Expected range %s - %s, got %s - %s",
				expectedStart, expectedEnd, match.Start, match.End)
		}
	})
}

func TestInetnumWithoutOrg(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.ldb")

	// Create inetnum without org
	inetnums := []Inetnum{
		{
			Start:   AddrToUint32(netip.MustParseAddr("192.0.2.0")),
			End:     AddrToUint32(netip.MustParseAddr("192.0.2.255")),
			OrgID:   "", // No org
			Status:  "LEGACY",
			Country: "XX",
			Netname: "LEGACY-BLOCK",
		},
	}

	orgs := map[string]Organisation{}

	db, err := BuildDatabase(dbPath, inetnums, orgs)
	if err != nil {
		t.Fatalf("BuildDatabase failed: %v", err)
	}
	defer db.Close()

	// Lookup should succeed and use Netname as fallback
	ip := netip.MustParseAddr("192.0.2.1")
	match, err := db.LookupIP(ip)
	if err != nil {
		t.Fatalf("LookupIP failed: %v", err)
	}

	if match.OrgName != "LEGACY-BLOCK" {
		t.Errorf("Expected 'LEGACY-BLOCK' (netname fallback), got '%s'", match.OrgName)
	}
	if match.Status != "LEGACY" {
		t.Errorf("Expected 'LEGACY', got '%s'", match.Status)
	}
}

func TestIsValidOrgRemark(t *testing.T) {
	tests := []struct {
		remark   string
		expected bool
	}{
		// Valid org names
		{"Amazon.com, Inc.", true},
		{"CDN77.com", true},
		{"Google LLC", true},
		{"Example Corporation", true},
		{"BT-Central-Plus", true},

		// Invalid - separators
		{"------------------------------------------------------", false},
		{"****************************", false},
		{"==================================================", false},
		{"__________________________________________________", false},
		{"###################################################", false},

		// Invalid - URLs
		{"http://www.example.com", false},
		{"https://www.example.com", false},

		// Invalid - email addresses and mailto
		{"Please send abuse notification to abuse@bt.net<mailto:abuse@bt.net>", false},
		{"contact@example.com", false},
		{"mailto:info@example.com", false},

		// Invalid - instructional text
		{"Please send abuse to abuse@example.com", false},
		{"For registration information,", false},
		{"You can consult the following sources:", false},
		{"Abuse notifications should be sent to", false},
		{"Contact us at support@example.com", false},

		// Invalid - RIPE administrative comments
		{"* THIS OBJECT IS MODIFIED", false},
		{"* Please note that all data...", false},
		{"  * To view the original object...", false},

		// Invalid - lines starting with dashes (separators, certificates)
		{"-----BEGIN CERTIFICATE-----", false},
		{"-----END CERTIFICATE-----", false},
		{"----------------------------", false},
		{"- This is a note", false},

		// Invalid - too short
		{"ab", false},
		{"", false},

		// Edge cases
		{"ABC", true},                              // Exactly 3 chars - valid
		{"A-B-C", true},                            // Contains dashes but not 80% - valid
		{"---------------------- Note", false},      // Mostly dashes - invalid
	}

	for _, tt := range tests {
		t.Run(tt.remark, func(t *testing.T) {
			result := isValidOrgRemark(tt.remark)
			if result != tt.expected {
				t.Errorf("isValidOrgRemark(%q) = %v, want %v", tt.remark, result, tt.expected)
			}
		})
	}
}

func TestNonRIPEManagedBlock(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.ldb")

	// Create a NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK entry (should be filtered out)
	inetnums := []Inetnum{
		{
			Start:   AddrToUint32(netip.MustParseAddr("32.0.0.0")),
			End:     AddrToUint32(netip.MustParseAddr("36.255.91.255")),
			OrgID:   "",
			Status:  "ALLOCATED UNSPECIFIED",
			Country: "EU",
			Netname: "NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK",
			Remarks: []string{"------------------------------------------------------", "Not managed by RIPE"},
		},
	}

	// Build database
	db, err := BuildDatabase(dbPath, inetnums, map[string]Organisation{})
	if err != nil {
		t.Fatalf("BuildDatabase failed: %v", err)
	}
	defer db.Close()

	// Lookup IP in the NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK range
	match, err := db.LookupIP(netip.MustParseAddr("32.190.240.23"))
	if err != nil {
		t.Fatalf("LookupIP failed: %v", err)
	}

	// Should return nil (no match) for non-RIPE managed blocks
	if match != nil {
		t.Errorf("Expected nil for NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK, got org '%s'", match.OrgName)
	}
}
