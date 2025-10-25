package maxmind

import (
	"net/netip"
	"testing"
)

func TestGeoInfoEquals(t *testing.T) {
	tests := []struct {
		name     string
		geo1     *GeoInfo
		geo2     *GeoInfo
		expected bool
	}{
		{
			name:     "identical geo",
			geo1:     &GeoInfo{Country: "US", Region: "CA", City: "Los Angeles", Lat: 34.05, Lon: -118.24},
			geo2:     &GeoInfo{Country: "US", Region: "CA", City: "Los Angeles", Lat: 34.05, Lon: -118.24},
			expected: true,
		},
		{
			name:     "same location, different lat/lon (should still be equal)",
			geo1:     &GeoInfo{Country: "US", Region: "CA", City: "Los Angeles", Lat: 34.05, Lon: -118.24},
			geo2:     &GeoInfo{Country: "US", Region: "CA", City: "Los Angeles", Lat: 34.06, Lon: -118.25},
			expected: true,
		},
		{
			name:     "different city",
			geo1:     &GeoInfo{Country: "US", Region: "CA", City: "Los Angeles"},
			geo2:     &GeoInfo{Country: "US", Region: "CA", City: "San Francisco"},
			expected: false,
		},
		{
			name:     "different region",
			geo1:     &GeoInfo{Country: "US", Region: "CA", City: "Los Angeles"},
			geo2:     &GeoInfo{Country: "US", Region: "NY", City: "New York"},
			expected: false,
		},
		{
			name:     "different country",
			geo1:     &GeoInfo{Country: "US", Region: "CA", City: "Los Angeles"},
			geo2:     &GeoInfo{Country: "CA", Region: "ON", City: "Toronto"},
			expected: false,
		},
		{
			name:     "both nil",
			geo1:     nil,
			geo2:     nil,
			expected: true,
		},
		{
			name:     "one nil",
			geo1:     &GeoInfo{Country: "US"},
			geo2:     nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.geo1.Equals(tt.geo2)
			if result != tt.expected {
				t.Errorf("Equals() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestAreAdjacentIPs(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		{
			name:     "consecutive IPv4",
			a:        "1.2.3.4",
			b:        "1.2.3.5",
			expected: true,
		},
		{
			name:     "consecutive at byte boundary",
			a:        "1.2.3.255",
			b:        "1.2.4.0",
			expected: true,
		},
		{
			name:     "not consecutive",
			a:        "1.2.3.4",
			b:        "1.2.3.6",
			expected: false,
		},
		{
			name:     "reversed order",
			a:        "1.2.3.5",
			b:        "1.2.3.4",
			expected: false,
		},
		{
			name:     "different IP families",
			a:        "1.2.3.4",
			b:        "::1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := netip.MustParseAddr(tt.a)
			b := netip.MustParseAddr(tt.b)
			result := areAdjacentIPs(a, b)
			if result != tt.expected {
				t.Errorf("areAdjacentIPs(%s, %s) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestTryMergePrefixes(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected string // empty string means merge should fail
	}{
		{
			name:     "merge two /25s into /24",
			a:        "1.2.3.0/25",
			b:        "1.2.3.128/25",
			expected: "1.2.3.0/24",
		},
		{
			name:     "merge two /24s into /23",
			a:        "1.2.2.0/24",
			b:        "1.2.3.0/24",
			expected: "1.2.2.0/23",
		},
		{
			name:     "cannot merge different sizes",
			a:        "1.2.3.0/24",
			b:        "1.2.4.0/25",
			expected: "",
		},
		{
			name:     "cannot merge non-adjacent",
			a:        "1.2.3.0/24",
			b:        "1.2.5.0/24",
			expected: "",
		},
		{
			name:     "merge in reverse order",
			a:        "1.2.3.128/25",
			b:        "1.2.3.0/25",
			expected: "1.2.3.0/24",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := netip.MustParsePrefix(tt.a)
			b := netip.MustParsePrefix(tt.b)
			result := tryMergePrefixes(a, b)

			if tt.expected == "" {
				if result.IsValid() {
					t.Errorf("tryMergePrefixes(%s, %s) should fail, got %s", tt.a, tt.b, result)
				}
			} else {
				expected := netip.MustParsePrefix(tt.expected)
				if !result.IsValid() {
					t.Errorf("tryMergePrefixes(%s, %s) failed, want %s", tt.a, tt.b, tt.expected)
				} else if result != expected {
					t.Errorf("tryMergePrefixes(%s, %s) = %s, want %s", tt.a, tt.b, result, expected)
				}
			}
		})
	}
}

func TestLastAddrInPrefix(t *testing.T) {
	tests := []struct {
		prefix   string
		expected string
	}{
		{"1.2.3.0/24", "1.2.3.255"},
		{"1.2.0.0/16", "1.2.255.255"},
		{"10.0.0.0/8", "10.255.255.255"},
		{"192.168.1.0/25", "192.168.1.127"},
		{"192.168.1.128/25", "192.168.1.255"},
		{"192.168.1.1/32", "192.168.1.1"},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			prefix := netip.MustParsePrefix(tt.prefix)
			expected := netip.MustParseAddr(tt.expected)
			result := lastAddrInPrefix(prefix)

			if result != expected {
				t.Errorf("lastAddrInPrefix(%s) = %s, want %s", tt.prefix, result, expected)
			}
		})
	}
}

func TestMergeAdjacentBlocks(t *testing.T) {
	tests := []struct {
		name     string
		blocks   []NetworkBlock
		expected int // expected number of blocks after merging
	}{
		{
			name: "merge two adjacent blocks with same geo",
			blocks: []NetworkBlock{
				{Prefix: netip.MustParsePrefix("1.2.3.0/25"), Country: "US", Region: "CA", City: "LA"},
				{Prefix: netip.MustParsePrefix("1.2.3.128/25"), Country: "US", Region: "CA", City: "LA"},
			},
			expected: 1, // Should merge into 1.2.3.0/24
		},
		{
			name: "don't merge blocks with different geo",
			blocks: []NetworkBlock{
				{Prefix: netip.MustParsePrefix("1.2.3.0/25"), Country: "US", Region: "CA", City: "LA"},
				{Prefix: netip.MustParsePrefix("1.2.3.128/25"), Country: "US", Region: "CA", City: "SF"},
			},
			expected: 2, // Should NOT merge (different cities)
		},
		{
			name: "merge multiple blocks",
			blocks: []NetworkBlock{
				{Prefix: netip.MustParsePrefix("1.2.2.0/24"), Country: "US", Region: "CA", City: "LA"},
				{Prefix: netip.MustParsePrefix("1.2.3.0/24"), Country: "US", Region: "CA", City: "LA"},
			},
			expected: 1, // Should merge into 1.2.2.0/23
		},
		{
			name: "no merging possible - different sizes",
			blocks: []NetworkBlock{
				{Prefix: netip.MustParsePrefix("1.2.3.0/24"), Country: "US", Region: "CA", City: "LA"},
				{Prefix: netip.MustParsePrefix("1.2.4.0/25"), Country: "US", Region: "CA", City: "LA"},
			},
			expected: 2, // Can't merge different sized blocks
		},
		{
			name: "single block",
			blocks: []NetworkBlock{
				{Prefix: netip.MustParsePrefix("1.2.3.0/24"), Country: "US", Region: "CA", City: "LA"},
			},
			expected: 1,
		},
		{
			name:     "empty blocks",
			blocks:   []NetworkBlock{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeAdjacentBlocks(tt.blocks)
			if len(result) != tt.expected {
				t.Errorf("mergeAdjacentBlocks() returned %d blocks, want %d", len(result), tt.expected)
				for i, block := range result {
					t.Logf("  Block %d: %s -> %s/%s/%s", i, block.Prefix, block.Country, block.Region, block.City)
				}
			}
		})
	}
}

func TestSplitPrefix(t *testing.T) {
	tests := []struct {
		prefix        string
		expectedHalf1 string
		expectedHalf2 string
	}{
		{
			prefix:        "1.2.0.0/16",
			expectedHalf1: "1.2.0.0/17",
			expectedHalf2: "1.2.128.0/17",
		},
		{
			prefix:        "10.0.0.0/8",
			expectedHalf1: "10.0.0.0/9",
			expectedHalf2: "10.128.0.0/9",
		},
		{
			prefix:        "192.168.1.0/24",
			expectedHalf1: "192.168.1.0/25",
			expectedHalf2: "192.168.1.128/25",
		},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			prefix := netip.MustParsePrefix(tt.prefix)
			half1, half2 := splitPrefix(prefix)

			expected1 := netip.MustParsePrefix(tt.expectedHalf1)
			expected2 := netip.MustParsePrefix(tt.expectedHalf2)

			if half1 != expected1 {
				t.Errorf("splitPrefix(%s) half1 = %s, want %s", tt.prefix, half1, expected1)
			}
			if half2 != expected2 {
				t.Errorf("splitPrefix(%s) half2 = %s, want %s", tt.prefix, half2, expected2)
			}

			// Verify the halves are contiguous
			lastInHalf1 := lastAddrInPrefix(half1)
			firstInHalf2 := half2.Addr()
			if !areAdjacentIPs(lastInHalf1, firstInHalf2) {
				t.Errorf("splitPrefix(%s) produced non-contiguous halves", tt.prefix)
			}
		})
	}
}
