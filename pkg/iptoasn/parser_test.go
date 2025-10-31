// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package iptoasn

import (
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestParser_ParseNext(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantCIDR    string
		wantASN     int
		wantCountry string
		wantErr     bool
	}{
		{
			name:        "valid IPv4 line",
			input:       "1.0.0.0\t1.0.0.255\t13335\tUS\tarinic\tCloudflare",
			wantCIDR:    "1.0.0.0/24",
			wantASN:     13335,
			wantCountry: "US",
			wantErr:     false,
		},
		{
			name:        "valid line without AS name",
			input:       "8.8.8.0\t8.8.8.255\t15169\tUS\tarin",
			wantCIDR:    "8.8.8.0/24",
			wantASN:     15169,
			wantCountry: "US",
			wantErr:     false,
		},
		{
			name:        "empty line returns EOF",
			input:       "",
			wantCIDR:    "",
			wantASN:     0,
			wantCountry: "",
			wantErr:     true, // EOF is expected for empty input
		},
		{
			name:        "comment line",
			input:       "# This is a comment",
			wantCIDR:    "",
			wantASN:     0,
			wantCountry: "",
			wantErr:     false,
		},
		{
			name:    "invalid ASN",
			input:   "1.0.0.0\t1.0.0.255\tabc\tUS\tarin",
			wantErr: true,
		},
		{
			name:    "invalid IP",
			input:   "999.999.999.999\t1.0.0.255\t13335\tUS\tarin",
			wantErr: true,
		},
		{
			name:    "too few fields",
			input:   "1.0.0.0\t1.0.0.255\t13335",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewParser(strings.NewReader(tt.input))
			rows, err := parser.ParseNext()

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Nil result for empty/comment lines
			if tt.wantCIDR == "" {
				if rows != nil {
					t.Errorf("expected nil rows for empty/comment line, got %+v", rows)
				}
				return
			}

			if rows == nil || len(rows) == 0 {
				t.Fatalf("expected at least one row, got nil or empty")
			}

			// Check first row (most tests only produce one CIDR per range)
			row := rows[0]

			if row.Prefix.String() != tt.wantCIDR {
				t.Errorf("CIDR = %s, want %s", row.Prefix.String(), tt.wantCIDR)
			}

			if row.ASN != tt.wantASN {
				t.Errorf("ASN = %d, want %d", row.ASN, tt.wantASN)
			}

			if row.Country != tt.wantCountry {
				t.Errorf("Country = %s, want %s", row.Country, tt.wantCountry)
			}
		})
	}
}

func TestRangeToCIDRs(t *testing.T) {
	tests := []struct {
		name      string
		startIP   string
		endIP     string
		wantCIDRs []string
		wantErr   bool
	}{
		{
			name:      "single /24",
			startIP:   "1.0.0.0",
			endIP:     "1.0.0.255",
			wantCIDRs: []string{"1.0.0.0/24"},
			wantErr:   false,
		},
		{
			name:      "single /16",
			startIP:   "1.0.0.0",
			endIP:     "1.0.255.255",
			wantCIDRs: []string{"1.0.0.0/16"},
			wantErr:   false,
		},
		{
			name:      "single IP",
			startIP:   "8.8.8.8",
			endIP:     "8.8.8.8",
			wantCIDRs: []string{"8.8.8.8/32"},
			wantErr:   false,
		},
		{
			name:    "non-aligned range (should produce multiple CIDRs)",
			startIP: "1.0.0.0",
			endIP:   "1.0.1.255",
			// Should produce: 1.0.0.0/23 or 1.0.0.0/24 + 1.0.1.0/24
			wantCIDRs: []string{"1.0.0.0/23"},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := mustParseAddr(t, tt.startIP)
			end := mustParseAddr(t, tt.endIP)

			cidrs, err := rangeToCIDRs(start, end)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(cidrs) != len(tt.wantCIDRs) {
				t.Errorf("got %d CIDRs, want %d", len(cidrs), len(tt.wantCIDRs))
				for i, cidr := range cidrs {
					t.Logf("  [%d] %s", i, cidr.String())
				}
				return
			}

			for i, cidr := range cidrs {
				if cidr.String() != tt.wantCIDRs[i] {
					t.Errorf("CIDR[%d] = %s, want %s", i, cidr.String(), tt.wantCIDRs[i])
				}
			}
		})
	}
}

func mustParseAddr(t *testing.T, s string) netip.Addr {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("failed to parse IP %s: %v", s, err)
	}
	return addr
}

// TestNormalizeCIDR tests that CIDR normalization correctly extracts prefix length
func TestNormalizeCIDR(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1.0.0.100/24", "1.0.0.0/24"},
		{"10.0.0.0/8", "10.0.0.0/8"},
		{"192.168.1.1/32", "192.168.1.1/32"},
		{"8.8.8.8/16", "8.8.0.0/16"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, ipnet, err := net.ParseCIDR(tt.input)
			if err != nil {
				t.Fatalf("ParseCIDR failed: %v", err)
			}

			got := NormalizeCIDR(ipnet)
			if got != tt.want {
				t.Errorf("NormalizeCIDR(%s) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}

// TestMultiCIDRRange tests that a single TSV line spanning multiple CIDRs
// expands correctly into multiple RawRow objects
func TestMultiCIDRRange(t *testing.T) {
	// This is the exact case from the bug:
	// 204.110.219.0 - 204.110.221.255 should produce:
	//   204.110.219.0/24
	//   204.110.220.0/23
	input := "204.110.219.0\t204.110.221.255\t16509\tUS\tARIN\tAMAZON-02"

	parser := NewParser(strings.NewReader(input))
	rows, err := parser.ParseNext()
	if err != nil {
		t.Fatalf("ParseNext failed: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (2 CIDRs), got %d", len(rows))
	}

	// Verify first CIDR: 204.110.219.0/24
	if rows[0].Prefix.String() != "204.110.219.0/24" {
		t.Errorf("rows[0] CIDR = %s, want 204.110.219.0/24", rows[0].Prefix.String())
	}
	if rows[0].Start.String() != "204.110.219.0" {
		t.Errorf("rows[0] Start = %s, want 204.110.219.0", rows[0].Start.String())
	}
	if rows[0].End.String() != "204.110.219.255" {
		t.Errorf("rows[0] End = %s, want 204.110.219.255", rows[0].End.String())
	}

	// Verify second CIDR: 204.110.220.0/23
	if rows[1].Prefix.String() != "204.110.220.0/23" {
		t.Errorf("rows[1] CIDR = %s, want 204.110.220.0/23", rows[1].Prefix.String())
	}
	if rows[1].Start.String() != "204.110.220.0" {
		t.Errorf("rows[1] Start = %s, want 204.110.220.0", rows[1].Start.String())
	}
	if rows[1].End.String() != "204.110.221.255" {
		t.Errorf("rows[1] End = %s, want 204.110.221.255", rows[1].End.String())
	}

	// Verify both rows have same metadata
	for i, row := range rows {
		if row.ASN != 16509 {
			t.Errorf("rows[%d] ASN = %d, want 16509", i, row.ASN)
		}
		if row.Country != "US" {
			t.Errorf("rows[%d] Country = %s, want US", i, row.Country)
		}
		if row.ASName != "AMAZON-02" {
			t.Errorf("rows[%d] ASName = %s, want AMAZON-02", i, row.ASName)
		}
	}
}
