package ipcodec

import (
	"net/netip"
	"testing"
)

func TestEncodeDecodeRangeKey(t *testing.T) {
	tests := []struct {
		name string
		ip   string
	}{
		{"IPv4 start", "192.168.0.0"},
		{"IPv4 end", "192.168.255.255"},
		{"IPv4 single", "8.8.8.8"},
		{"IPv6 start", "2001:db8::"},
		{"IPv6 end", "2001:db8::ffff"},
		{"IPv6 single", "2001:4860:4860::8888"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := netip.MustParseAddr(tt.ip)
			key := EncodeRangeKey(ip)
			decoded, err := DecodeRangeKey(key)
			if err != nil {
				t.Fatalf("DecodeRangeKey failed: %v", err)
			}
			if decoded != ip {
				t.Errorf("got %v, want %v", decoded, ip)
			}
		})
	}
}

func TestCIDRToRange(t *testing.T) {
	tests := []struct {
		name      string
		cidr      string
		wantStart string
		wantEnd   string
	}{
		{
			name:      "IPv4 /24",
			cidr:      "192.168.1.0/24",
			wantStart: "192.168.1.0",
			wantEnd:   "192.168.1.255",
		},
		{
			name:      "IPv4 /32",
			cidr:      "8.8.8.8/32",
			wantStart: "8.8.8.8",
			wantEnd:   "8.8.8.8",
		},
		{
			name:      "IPv4 /16",
			cidr:      "10.0.0.0/16",
			wantStart: "10.0.0.0",
			wantEnd:   "10.0.255.255",
		},
		{
			name:      "IPv6 /64",
			cidr:      "2001:db8::/64",
			wantStart: "2001:db8::",
			wantEnd:   "2001:db8::ffff:ffff:ffff:ffff",
		},
		{
			name:      "IPv6 /128",
			cidr:      "2001:4860:4860::8888/128",
			wantStart: "2001:4860:4860::8888",
			wantEnd:   "2001:4860:4860::8888",
		},
		{
			name:      "IPv6 /32 (regression: large host bit count)",
			cidr:      "2001:db8::/32",
			wantStart: "2001:db8::",
			wantEnd:   "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff",
		},
		{
			name:      "IPv6 /48",
			cidr:      "2001:db8:1234::/48",
			wantStart: "2001:db8:1234::",
			wantEnd:   "2001:db8:1234:ffff:ffff:ffff:ffff:ffff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, err := CIDRToRange(tt.cidr)
			if err != nil {
				t.Fatalf("CIDRToRange failed: %v", err)
			}
			wantStart := netip.MustParseAddr(tt.wantStart)
			wantEnd := netip.MustParseAddr(tt.wantEnd)
			if start != wantStart {
				t.Errorf("start: got %v, want %v", start, wantStart)
			}
			if end != wantEnd {
				t.Errorf("end: got %v, want %v", end, wantEnd)
			}
		})
	}
}

func TestIsInRange(t *testing.T) {
	tests := []struct {
		name  string
		ip    string
		start string
		end   string
		want  bool
	}{
		{
			name:  "IPv4 in range",
			ip:    "192.168.1.100",
			start: "192.168.1.0",
			end:   "192.168.1.255",
			want:  true,
		},
		{
			name:  "IPv4 before range",
			ip:    "192.168.0.255",
			start: "192.168.1.0",
			end:   "192.168.1.255",
			want:  false,
		},
		{
			name:  "IPv4 after range",
			ip:    "192.168.2.0",
			start: "192.168.1.0",
			end:   "192.168.1.255",
			want:  false,
		},
		{
			name:  "IPv4 at start",
			ip:    "192.168.1.0",
			start: "192.168.1.0",
			end:   "192.168.1.255",
			want:  true,
		},
		{
			name:  "IPv4 at end",
			ip:    "192.168.1.255",
			start: "192.168.1.0",
			end:   "192.168.1.255",
			want:  true,
		},
		{
			name:  "IPv6 in range",
			ip:    "2001:db8::100",
			start: "2001:db8::",
			end:   "2001:db8::ffff",
			want:  true,
		},
		{
			name:  "IPv6 before range",
			ip:    "2001:db7::ffff",
			start: "2001:db8::",
			end:   "2001:db8::ffff",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := netip.MustParseAddr(tt.ip)
			start := netip.MustParseAddr(tt.start)
			end := netip.MustParseAddr(tt.end)
			got := IsInRange(ip, start, end)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizePrefix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Already normalized",
			input: "192.168.0.0/24",
			want:  "192.168.0.0/24",
		},
		{
			name:  "Not normalized",
			input: "192.168.1.100/24",
			want:  "192.168.1.0/24",
		},
		{
			name:  "IPv6 normalized",
			input: "2001:db8::1/64",
			want:  "2001:db8::/64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePrefix(tt.input)
			if err != nil {
				t.Fatalf("NormalizePrefix failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIPv4ToInt32(t *testing.T) {
	tests := []struct {
		ip   string
		want uint32
	}{
		{"0.0.0.0", 0},
		{"0.0.0.1", 1},
		{"8.8.8.8", 0x08080808},
		{"192.168.1.1", 0xC0A80101},
		{"255.255.255.255", 0xFFFFFFFF},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := netip.MustParseAddr(tt.ip)
			got := IPv4ToInt32(ip)
			if got != tt.want {
				t.Errorf("got 0x%08X, want 0x%08X", got, tt.want)
			}
			// Test round-trip
			roundtrip := Int32ToIPv4(got)
			if roundtrip != ip {
				t.Errorf("round-trip failed: got %v, want %v", roundtrip, ip)
			}
		})
	}
}
