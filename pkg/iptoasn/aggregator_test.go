package iptoasn

import (
	"testing"

	"github.com/wingedpig/iporg/pkg/model"
)

func TestAggregator_Collapse(t *testing.T) {
	tests := []struct {
		name          string
		input         []*model.CanonicalPrefix
		wantCollapsed int // Number of prefixes after collapse
	}{
		{
			name: "adjacent /24s collapse to /23",
			input: []*model.CanonicalPrefix{
				{CIDR: "1.0.0.0/24", ASN: 13335, Country: "US", Registry: "arin"},
				{CIDR: "1.0.1.0/24", ASN: 13335, Country: "US", Registry: "arin"},
			},
			wantCollapsed: 1,
		},
		{
			name: "non-adjacent prefixes don't collapse",
			input: []*model.CanonicalPrefix{
				{CIDR: "1.0.0.0/24", ASN: 13335, Country: "US", Registry: "arin"},
				{CIDR: "1.0.2.0/24", ASN: 13335, Country: "US", Registry: "arin"},
			},
			wantCollapsed: 2,
		},
		{
			name: "single prefix unchanged",
			input: []*model.CanonicalPrefix{
				{CIDR: "1.0.0.0/24", ASN: 13335, Country: "US", Registry: "arin"},
			},
			wantCollapsed: 1,
		},
		{
			name: "four /24s collapse to /22",
			input: []*model.CanonicalPrefix{
				{CIDR: "1.0.0.0/24", ASN: 13335, Country: "US", Registry: "arin"},
				{CIDR: "1.0.1.0/24", ASN: 13335, Country: "US", Registry: "arin"},
				{CIDR: "1.0.2.0/24", ASN: 13335, Country: "US", Registry: "arin"},
				{CIDR: "1.0.3.0/24", ASN: 13335, Country: "US", Registry: "arin"},
			},
			wantCollapsed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := NewAggregator()
			collapsed := agg.Collapse(tt.input)

			if len(collapsed) != tt.wantCollapsed {
				t.Errorf("got %d prefixes after collapse, want %d", len(collapsed), tt.wantCollapsed)
				for i, p := range collapsed {
					t.Logf("  [%d] %s", i, p.CIDR)
				}
			}

			// Verify all collapsed prefixes have same ASN
			for _, p := range collapsed {
				if p.ASN != tt.input[0].ASN {
					t.Errorf("collapsed prefix has ASN %d, want %d", p.ASN, tt.input[0].ASN)
				}
			}
		})
	}
}

func TestAggregator_Deduplicate(t *testing.T) {
	tests := []struct {
		name      string
		input     []*model.CanonicalPrefix
		wantCount int
	}{
		{
			name: "no duplicates",
			input: []*model.CanonicalPrefix{
				{CIDR: "1.0.0.0/24", ASN: 13335},
				{CIDR: "1.0.1.0/24", ASN: 13335},
			},
			wantCount: 2,
		},
		{
			name: "exact duplicates removed",
			input: []*model.CanonicalPrefix{
				{CIDR: "1.0.0.0/24", ASN: 13335},
				{CIDR: "1.0.0.0/24", ASN: 13335},
				{CIDR: "1.0.1.0/24", ASN: 13335},
			},
			wantCount: 2,
		},
		{
			name: "same CIDR different ASN kept",
			input: []*model.CanonicalPrefix{
				{CIDR: "1.0.0.0/24", ASN: 13335},
				{CIDR: "1.0.0.0/24", ASN: 15169},
			},
			wantCount: 2,
		},
		{
			name: "regression: large ASNs > 0x10FFFF (32-bit ASN space)",
			input: []*model.CanonicalPrefix{
				{CIDR: "1.0.0.0/24", ASN: 4200000000}, // Large 32-bit ASN
				{CIDR: "1.0.0.0/24", ASN: 4200000001}, // Different large ASN
				{CIDR: "1.0.0.0/24", ASN: 4200000000}, // Duplicate of first
			},
			wantCount: 2, // Should keep both distinct large ASNs, remove duplicate
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := NewAggregator()
			result := agg.Deduplicate(tt.input)

			if len(result) != tt.wantCount {
				t.Errorf("got %d prefixes after dedup, want %d", len(result), tt.wantCount)
			}
		})
	}
}

func TestAggregator_SortByStartIP(t *testing.T) {
	input := []*model.CanonicalPrefix{
		{CIDR: "10.0.0.0/8", ASN: 1},
		{CIDR: "1.0.0.0/8", ASN: 2},
		{CIDR: "5.0.0.0/8", ASN: 3},
	}

	agg := NewAggregator()
	agg.SortByStartIP(input)

	// Check sorted order
	want := []string{"1.0.0.0/8", "5.0.0.0/8", "10.0.0.0/8"}
	for i, p := range input {
		if p.CIDR != want[i] {
			t.Errorf("position %d: got %s, want %s", i, p.CIDR, want[i])
		}
	}
}
