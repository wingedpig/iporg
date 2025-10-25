package ipcodec

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
)

const (
	// Key prefixes for LevelDB
	PrefixRangeV4 = "R4:"
	PrefixRangeV6 = "R6:"
	PrefixMeta    = "meta:"
	PrefixCache   = "cache:"
)

// EncodeRangeKey creates a LevelDB key for an IP range start
// Format: "R4:" + 4-byte big-endian IP (IPv4) or "R6:" + 16-byte big-endian IP (IPv6)
func EncodeRangeKey(ip netip.Addr) []byte {
	if ip.Is4() {
		key := make([]byte, len(PrefixRangeV4)+4)
		copy(key, PrefixRangeV4)
		copy(key[len(PrefixRangeV4):], ip.AsSlice())
		return key
	}
	// IPv6
	key := make([]byte, len(PrefixRangeV6)+16)
	copy(key, PrefixRangeV6)
	copy(key[len(PrefixRangeV6):], ip.AsSlice())
	return key
}

// DecodeRangeKey extracts the IP address from a range key
func DecodeRangeKey(key []byte) (netip.Addr, error) {
	if len(key) >= len(PrefixRangeV4)+4 && string(key[:len(PrefixRangeV4)]) == PrefixRangeV4 {
		// IPv4
		ipBytes := key[len(PrefixRangeV4):]
		if len(ipBytes) != 4 {
			return netip.Addr{}, fmt.Errorf("invalid IPv4 key length: %d", len(ipBytes))
		}
		addr, ok := netip.AddrFromSlice(ipBytes)
		if !ok {
			return netip.Addr{}, fmt.Errorf("invalid IPv4 address bytes")
		}
		return addr, nil
	}
	if len(key) >= len(PrefixRangeV6)+16 && string(key[:len(PrefixRangeV6)]) == PrefixRangeV6 {
		// IPv6
		ipBytes := key[len(PrefixRangeV6):]
		if len(ipBytes) != 16 {
			return netip.Addr{}, fmt.Errorf("invalid IPv6 key length: %d", len(ipBytes))
		}
		addr, ok := netip.AddrFromSlice(ipBytes)
		if !ok {
			return netip.Addr{}, fmt.Errorf("invalid IPv6 address bytes")
		}
		return addr, nil
	}
	return netip.Addr{}, fmt.Errorf("invalid range key prefix")
}

// CIDRToRange converts a CIDR string to start and end IP addresses
func CIDRToRange(cidr string) (start, end netip.Addr, err error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("invalid CIDR: %w", err)
	}

	start = prefix.Addr()

	// Calculate end IP
	bits := prefix.Bits()
	hostBits := prefix.Addr().BitLen() - bits

	if hostBits == 0 {
		// Single IP (/32 or /128)
		end = start
		return start, end, nil
	}

	// Get the last IP in the range
	// For IPv4: add (2^hostBits - 1) to start
	// For IPv6: same logic, but we can't use uint64 for large host bit counts (>64)
	startBytes := start.AsSlice()
	endBytes := make([]byte, len(startBytes))
	copy(endBytes, startBytes)

	// Set all host bits to 1 by working byte-by-byte
	// This avoids uint64 overflow for IPv6 prefixes with >64 host bits
	if hostBits > 0 {
		// Calculate how many full bytes and remaining bits
		fullBytes := hostBits / 8
		remainingBits := hostBits % 8

		// Set the last fullBytes to 0xFF
		for i := len(endBytes) - 1; i >= len(endBytes)-fullBytes && i >= 0; i-- {
			endBytes[i] = 0xFF
		}

		// Set the remaining bits in the partial byte
		if remainingBits > 0 {
			byteIdx := len(endBytes) - fullBytes - 1
			if byteIdx >= 0 {
				mask := byte((1 << remainingBits) - 1)
				endBytes[byteIdx] |= mask
			}
		}
	}

	end, ok := netip.AddrFromSlice(endBytes)
	if !ok {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("failed to create end IP")
	}

	return start, end, nil
}

// IPToBytes converts an IP address to big-endian bytes
func IPToBytes(ip netip.Addr) []byte {
	return ip.AsSlice()
}

// BytesToIP converts big-endian bytes to an IP address
func BytesToIP(b []byte) (netip.Addr, error) {
	addr, ok := netip.AddrFromSlice(b)
	if !ok {
		return netip.Addr{}, fmt.Errorf("invalid IP bytes")
	}
	return addr, nil
}

// CompareIPs compares two IP addresses (-1 if a < b, 0 if a == b, 1 if a > b)
func CompareIPs(a, b netip.Addr) int {
	return a.Compare(b)
}

// IsInRange checks if an IP is within a range [start, end] inclusive
func IsInRange(ip, start, end netip.Addr) bool {
	return ip.Compare(start) >= 0 && ip.Compare(end) <= 0
}

// MetaKey creates a metadata key
func MetaKey(suffix string) []byte {
	return []byte(PrefixMeta + suffix)
}

// CacheKey creates a cache key
func CacheKey(category, key string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s", PrefixCache, category, key))
}

// NormalizePrefix normalizes a CIDR prefix string
func NormalizePrefix(cidr string) (string, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", err
	}
	return prefix.Masked().String(), nil
}

// RepresentativeIP returns the first usable IP in a CIDR range
// For use with RDAP queries (typically the first IP)
func RepresentativeIP(cidr string) (netip.Addr, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid CIDR: %w", err)
	}
	return prefix.Addr(), nil
}

// ParseIP parses an IP address string
func ParseIP(s string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid IP address: %w", err)
	}
	return addr, nil
}

// IPNetToPrefix converts net.IPNet to netip.Prefix
func IPNetToPrefix(ipnet *net.IPNet) (netip.Prefix, error) {
	addr, ok := netip.AddrFromSlice(ipnet.IP)
	if !ok {
		return netip.Prefix{}, fmt.Errorf("invalid IP in IPNet")
	}
	ones, _ := ipnet.Mask.Size()
	return netip.PrefixFrom(addr, ones), nil
}

// PrefixToIPNet converts netip.Prefix to net.IPNet
func PrefixToIPNet(prefix netip.Prefix) *net.IPNet {
	addr := prefix.Addr()
	bits := prefix.Bits()

	var mask net.IPMask
	if addr.Is4() {
		mask = net.CIDRMask(bits, 32)
	} else {
		mask = net.CIDRMask(bits, 128)
	}

	return &net.IPNet{
		IP:   addr.AsSlice(),
		Mask: mask,
	}
}

// Int32ToIPv4 converts a uint32 to an IPv4 address
func Int32ToIPv4(n uint32) netip.Addr {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	addr, _ := netip.AddrFromSlice(b)
	return addr
}

// IPv4ToInt32 converts an IPv4 address to uint32
func IPv4ToInt32(ip netip.Addr) uint32 {
	if !ip.Is4() {
		return 0
	}
	b := ip.AsSlice()
	return binary.BigEndian.Uint32(b)
}
