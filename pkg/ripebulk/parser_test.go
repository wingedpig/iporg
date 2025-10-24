package ripebulk

import (
	"net/netip"
	"strings"
	"testing"
)

func TestParseOrganisations(t *testing.T) {
	input := `
# Comment line
% Another comment
organisation:   ORG-EA123-RIPE
org-name:       Example Ltd
org-type:       LIR
address:        123 Main St

organisation:   ORG-TEST1-RIPE
org-name:       Test Organization
org-name:       with continuation
org-type:       OTHER

`

	orgs, err := ParseOrganisations(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseOrganisations failed: %v", err)
	}

	if len(orgs) != 2 {
		t.Fatalf("Expected 2 organisations, got %d", len(orgs))
	}

	// Check first org
	org1, ok := orgs["ORG-EA123-RIPE"]
	if !ok {
		t.Fatal("ORG-EA123-RIPE not found")
	}
	if org1.OrgName != "Example Ltd" {
		t.Errorf("Expected 'Example Ltd', got '%s'", org1.OrgName)
	}
	if org1.OrgType != "LIR" {
		t.Errorf("Expected 'LIR', got '%s'", org1.OrgType)
	}

	// Check second org (with continuation)
	org2, ok := orgs["ORG-TEST1-RIPE"]
	if !ok {
		t.Fatal("ORG-TEST1-RIPE not found")
	}
	if !strings.Contains(org2.OrgName, "Test Organization") {
		t.Errorf("Expected org name to contain 'Test Organization', got '%s'", org2.OrgName)
	}
	if org2.OrgType != "OTHER" {
		t.Errorf("Expected 'OTHER', got '%s'", org2.OrgType)
	}
}

func TestParseInetnums(t *testing.T) {
	input := `
inetnum:        192.0.2.0 - 192.0.2.255
netname:        TEST-NET
org:            ORG-TEST1-RIPE
status:         ASSIGNED-PA
country:        GB

inetnum:        198.51.100.0 - 198.51.100.255
netname:        EXAMPLE-BLOCK
status:         ALLOCATED-PA
country:        US

`

	inetnums, err := ParseInetnums(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseInetnums failed: %v", err)
	}

	if len(inetnums) != 2 {
		t.Fatalf("Expected 2 inetnums, got %d", len(inetnums))
	}

	// Check first inetnum
	inet1 := inetnums[0]
	expectedStart := AddrToUint32(netip.MustParseAddr("192.0.2.0"))
	expectedEnd := AddrToUint32(netip.MustParseAddr("192.0.2.255"))

	if inet1.Start != expectedStart {
		t.Errorf("Expected start %d, got %d", expectedStart, inet1.Start)
	}
	if inet1.End != expectedEnd {
		t.Errorf("Expected end %d, got %d", expectedEnd, inet1.End)
	}
	if inet1.OrgID != "ORG-TEST1-RIPE" {
		t.Errorf("Expected 'ORG-TEST1-RIPE', got '%s'", inet1.OrgID)
	}
	if inet1.Status != "ASSIGNED-PA" {
		t.Errorf("Expected 'ASSIGNED-PA', got '%s'", inet1.Status)
	}
	if inet1.Country != "GB" {
		t.Errorf("Expected 'GB', got '%s'", inet1.Country)
	}
	if inet1.Netname != "TEST-NET" {
		t.Errorf("Expected 'TEST-NET', got '%s'", inet1.Netname)
	}
}

func TestParseInetnumRange(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantStart string
		wantEnd   string
		wantErr   bool
	}{
		{
			name:      "valid range",
			input:     "192.0.2.0 - 192.0.2.255",
			wantStart: "192.0.2.0",
			wantEnd:   "192.0.2.255",
			wantErr:   false,
		},
		{
			name:      "single IP",
			input:     "192.0.2.1 - 192.0.2.1",
			wantStart: "192.0.2.1",
			wantEnd:   "192.0.2.1",
			wantErr:   false,
		},
		{
			name:      "large range",
			input:     "10.0.0.0 - 10.255.255.255",
			wantStart: "10.0.0.0",
			wantEnd:   "10.255.255.255",
			wantErr:   false,
		},
		{
			name:    "invalid format",
			input:   "192.0.2.0",
			wantErr: true,
		},
		{
			name:    "start > end",
			input:   "192.0.2.255 - 192.0.2.0",
			wantErr: true,
		},
		{
			name:    "invalid IP",
			input:   "999.0.0.0 - 999.0.0.255",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, err := parseInetnumRange(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseInetnumRange() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				wantStart := AddrToUint32(netip.MustParseAddr(tt.wantStart))
				wantEnd := AddrToUint32(netip.MustParseAddr(tt.wantEnd))

				if start != wantStart {
					t.Errorf("start = %d, want %d", start, wantStart)
				}
				if end != wantEnd {
					t.Errorf("end = %d, want %d", end, wantEnd)
				}
			}
		})
	}
}

func TestPrefixToRange(t *testing.T) {
	tests := []struct {
		prefix    string
		wantStart string
		wantEnd   string
		wantErr   bool
	}{
		{
			prefix:    "192.0.2.0/24",
			wantStart: "192.0.2.0",
			wantEnd:   "192.0.2.255",
		},
		{
			prefix:    "10.0.0.0/8",
			wantStart: "10.0.0.0",
			wantEnd:   "10.255.255.255",
		},
		{
			prefix:    "192.0.2.128/25",
			wantStart: "192.0.2.128",
			wantEnd:   "192.0.2.255",
		},
		{
			prefix:    "192.0.2.1/32",
			wantStart: "192.0.2.1",
			wantEnd:   "192.0.2.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			prefix := netip.MustParsePrefix(tt.prefix)
			start, end, err := PrefixToRange(prefix)

			if (err != nil) != tt.wantErr {
				t.Errorf("PrefixToRange() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				wantStart := AddrToUint32(netip.MustParseAddr(tt.wantStart))
				wantEnd := AddrToUint32(netip.MustParseAddr(tt.wantEnd))

				if start != wantStart {
					t.Errorf("start = %d (%s), want %d (%s)",
						start, Uint32ToAddr(start), wantStart, tt.wantStart)
				}
				if end != wantEnd {
					t.Errorf("end = %d (%s), want %d (%s)",
						end, Uint32ToAddr(end), wantEnd, tt.wantEnd)
				}
			}
		})
	}
}

func TestUint32AddrConversion(t *testing.T) {
	tests := []string{
		"0.0.0.0",
		"192.0.2.1",
		"10.0.0.0",
		"172.16.0.0",
		"255.255.255.255",
	}

	for _, ipStr := range tests {
		t.Run(ipStr, func(t *testing.T) {
			addr := netip.MustParseAddr(ipStr)
			uint32Val := AddrToUint32(addr)
			result := Uint32ToAddr(uint32Val)

			if result != addr {
				t.Errorf("Round-trip failed: %s -> %d -> %s", addr, uint32Val, result)
			}
		})
	}
}
