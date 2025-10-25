package maxmind

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/oschwald/geoip2-golang"
)

// Readers contains MaxMind database readers
type Readers struct {
	ASN  *geoip2.Reader
	City *geoip2.Reader
}

// Open opens the MaxMind database readers
func Open(asnPath, cityPath string) (*Readers, error) {
	asnDB, err := geoip2.Open(asnPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open ASN database: %w", err)
	}

	cityDB, err := geoip2.Open(cityPath)
	if err != nil {
		asnDB.Close()
		return nil, fmt.Errorf("failed to open City database: %w", err)
	}

	return &Readers{
		ASN:  asnDB,
		City: cityDB,
	}, nil
}

// Close closes both database readers
func (r *Readers) Close() error {
	var err error
	if r.ASN != nil {
		if e := r.ASN.Close(); e != nil {
			err = e
		}
	}
	if r.City != nil {
		if e := r.City.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// ASNInfo returns the ASN number and organization name for an IP
func (r *Readers) ASNInfo(ip netip.Addr) (number int, name string, err error) {
	netIP := net.IP(ip.AsSlice())
	record, err := r.ASN.ASN(netIP)
	if err != nil {
		return 0, "", fmt.Errorf("ASN lookup failed: %w", err)
	}

	return int(record.AutonomousSystemNumber), record.AutonomousSystemOrganization, nil
}

// GeoInfo represents geographic information for an IP
type GeoInfo struct {
	Country string
	Region  string
	City    string
	Lat     float64
	Lon     float64
}

// Equals checks if two GeoInfo structs represent the same geographic location
func (g *GeoInfo) Equals(other *GeoInfo) bool {
	if g == nil || other == nil {
		return g == other
	}
	return g.Country == other.Country &&
		g.Region == other.Region &&
		g.City == other.City
	// Note: We intentionally ignore Lat/Lon for equality
	// because we care about semantic location (Country/Region/City)
}

// Geo returns geographic information for an IP
func (r *Readers) Geo(ip netip.Addr) (*GeoInfo, error) {
	netIP := net.IP(ip.AsSlice())
	record, err := r.City.City(netIP)
	if err != nil {
		return nil, fmt.Errorf("geo lookup failed: %w", err)
	}

	info := &GeoInfo{
		Country: record.Country.IsoCode,
		Lat:     record.Location.Latitude,
		Lon:     record.Location.Longitude,
	}

	// Get region name (subdivision)
	if len(record.Subdivisions) > 0 {
		info.Region = record.Subdivisions[0].Names["en"]
	}

	// Get city name
	info.City = record.City.Names["en"]

	return info, nil
}

// Network returns the network range that contains the IP from the City database
// This uses binary search to approximate MaxMind's network boundaries
// by finding where geographic information changes
func (r *Readers) Network(ip netip.Addr) (*netip.Prefix, error) {
	// Get the geo for this IP
	baseGeo, err := r.Geo(ip)
	if err != nil {
		// If we can't get geo, return single IP
		if ip.Is4() {
			prefix := netip.PrefixFrom(ip, 32)
			return &prefix, nil
		}
		prefix := netip.PrefixFrom(ip, 128)
		return &prefix, nil
	}

	// Binary search to find the largest prefix length where geo remains constant
	// Start with the IP itself (max prefix length)
	minBits := 0
	maxBits := 32
	if ip.Is6() {
		maxBits = 128
	}

	// Find the largest network (smallest prefix length) where edges have same geo
	bestPrefix := maxBits
	for bits := minBits; bits <= maxBits; bits++ {
		testPrefix := netip.PrefixFrom(ip, bits)

		// Check if start and end of this prefix have the same geo as our IP
		startGeo, err1 := r.Geo(testPrefix.Addr())

		// Get the last IP in this prefix
		lastIP := lastAddrInPrefix(testPrefix)
		endGeo, err2 := r.Geo(lastIP)

		if err1 == nil && err2 == nil && baseGeo.Equals(startGeo) && baseGeo.Equals(endGeo) {
			// This prefix length works, remember it and try a larger network (smaller bits)
			bestPrefix = bits
			// For efficiency, jump by larger increments early on
			if bits < 16 {
				bits += 3  // Will be incremented by loop
			}
		} else {
			// Geo changed, our best guess is the previous one
			break
		}
	}

	prefix := netip.PrefixFrom(ip, bestPrefix)
	return &prefix, nil
}

// lastAddrInPrefix returns the last (broadcast) address in a prefix
func lastAddrInPrefix(prefix netip.Prefix) netip.Addr {
	addr := prefix.Addr()
	bits := prefix.Bits()

	addrBytes := addr.AsSlice()
	resultBytes := make([]byte, len(addrBytes))
	copy(resultBytes, addrBytes)

	// Set all host bits to 1
	hostBits := len(addrBytes)*8 - bits
	for i := 0; i < hostBits; i++ {
		bitPos := bits + i
		byteIdx := bitPos / 8
		bitIdx := 7 - (bitPos % 8)
		resultBytes[byteIdx] |= 1 << bitIdx
	}

	result, _ := netip.AddrFromSlice(resultBytes)
	return result
}

// GetAllNetworks returns all MaxMind networks that intersect with a given prefix
// This is used for Mode B to split a large prefix into smaller geo-accurate blocks
func (r *Readers) GetAllNetworks(prefix netip.Prefix) ([]NetworkBlock, error) {
	// This is a simplified implementation
	// In a real implementation, you'd need to walk through the MaxMind database
	// For now, we'll just return the prefix itself as a single block
	// A more complete implementation would require parsing the MaxMind CSV files

	// Get a representative IP from the prefix
	ip := prefix.Addr()

	// Look up the network
	network, err := r.Network(ip)
	if err != nil {
		// If lookup fails, just return the original prefix
		return []NetworkBlock{{Prefix: prefix}}, nil
	}

	// Check if the MaxMind network is contained within our prefix
	if !prefixContains(prefix, *network) {
		// The MaxMind block extends beyond our prefix, so just use our prefix
		return []NetworkBlock{{Prefix: prefix}}, nil
	}

	// Get geo info for this block
	geo, _ := r.Geo(ip)

	block := NetworkBlock{
		Prefix: *network,
	}
	if geo != nil {
		block.Country = geo.Country
		block.Region = geo.Region
		block.City = geo.City
		block.Lat = geo.Lat
		block.Lon = geo.Lon
	}

	return []NetworkBlock{block}, nil
}

// NetworkBlock represents a network block with geographic information
type NetworkBlock struct {
	Prefix  netip.Prefix
	Country string
	Region  string
	City    string
	Lat     float64
	Lon     float64
}

// prefixContains checks if prefix a contains prefix b
func prefixContains(a, b netip.Prefix) bool {
	// a contains b if:
	// 1. They're the same IP version
	// 2. a's prefix length is <= b's prefix length
	// 3. b's network address starts with a's network address

	if a.Addr().Is4() != b.Addr().Is4() {
		return false
	}

	if a.Bits() > b.Bits() {
		return false
	}

	// Check if b's address is within a's range
	bAddr := b.Addr()
	return a.Contains(bAddr)
}

// SplitPrefixByGeo splits a large prefix into smaller geo-located blocks
// This is used for Mode B to improve accuracy
func (r *Readers) SplitPrefixByGeo(prefix netip.Prefix, minPrefixLen int) ([]NetworkBlock, error) {
	prefixLen := prefix.Bits()
	if prefixLen >= minPrefixLen {
		// Prefix is already small enough, just return it
		geo, _ := r.Geo(prefix.Addr())
		block := NetworkBlock{Prefix: prefix}
		if geo != nil {
			block.Country = geo.Country
			block.Region = geo.Region
			block.City = geo.City
			block.Lat = geo.Lat
			block.Lon = geo.Lon
		}
		return []NetworkBlock{block}, nil
	}

	// Get the two halves
	half1, half2 := splitPrefix(prefix)

	// Recursively process each half
	blocks1, err := r.SplitPrefixByGeo(half1, minPrefixLen)
	if err != nil {
		return nil, err
	}

	blocks2, err := r.SplitPrefixByGeo(half2, minPrefixLen)
	if err != nil {
		return nil, err
	}

	// Optimization #4: Merge adjacent blocks with identical geo
	// This is safer than early stopping - we still recurse fully,
	// but collapse identical neighbors after the fact
	var blocks []NetworkBlock
	blocks = append(blocks, blocks1...)
	blocks = append(blocks, blocks2...)

	// Try to merge contiguous blocks with same geo
	return mergeAdjacentBlocks(blocks), nil
}

// mergeAdjacentBlocks merges contiguous blocks with identical geo
// This safely reduces the number of blocks without missing any boundaries
func mergeAdjacentBlocks(blocks []NetworkBlock) []NetworkBlock {
	if len(blocks) <= 1 {
		return blocks
	}

	// Blocks must be sorted by start IP and be contiguous for merging
	// The recursive split produces them in order, but let's be explicit
	var merged []NetworkBlock
	current := blocks[0]

	for i := 1; i < len(blocks); i++ {
		next := blocks[i]

		// Check if current and next are adjacent and have same geo
		currentEnd := lastAddrInPrefix(current.Prefix)
		nextStart := next.Prefix.Addr()

		// Are they contiguous? (current.end + 1 == next.start)
		areContiguous := areAdjacentIPs(currentEnd, nextStart)

		// Do they have same geo?
		sameGeo := current.Country == next.Country &&
			current.Region == next.Region &&
			current.City == next.City

		// Can we merge them into a larger prefix?
		if areContiguous && sameGeo {
			// Try to create a merged prefix
			// Check if they form a valid CIDR block (power of 2 alignment)
			if merged := tryMergePrefixes(current.Prefix, next.Prefix); merged.IsValid() {
				current.Prefix = merged
				// Keep current's geo (same as next's)
				continue
			}
		}

		// Can't merge, save current and move to next
		merged = append(merged, current)
		current = next
	}

	// Don't forget the last block
	merged = append(merged, current)
	return merged
}

// areAdjacentIPs checks if two IPs are consecutive (b = a + 1)
func areAdjacentIPs(a, b netip.Addr) bool {
	if a.Is4() != b.Is4() {
		return false
	}

	aBytes := a.AsSlice()
	bBytes := b.AsSlice()

	// Add 1 to 'a' and check if it equals 'b'
	carry := uint(1)
	for i := len(aBytes) - 1; i >= 0; i-- {
		sum := uint(aBytes[i]) + carry
		if byte(sum&0xFF) != bBytes[i] {
			return false
		}
		carry = sum >> 8
	}

	return carry == 0 // No overflow
}

// tryMergePrefixes attempts to merge two adjacent prefixes into a single larger prefix
// Returns an invalid prefix if they can't be merged
func tryMergePrefixes(a, b netip.Prefix) netip.Prefix {
	// Can only merge if they're the same size
	if a.Bits() != b.Bits() {
		return netip.Prefix{}
	}

	// And if they'd form a valid parent (differ only in the last bit of the prefix)
	parentBits := a.Bits() - 1
	if parentBits < 0 {
		return netip.Prefix{}
	}

	// Determine which prefix comes first
	var first, second netip.Prefix
	if a.Addr().Compare(b.Addr()) < 0 {
		first, second = a, b
	} else {
		first, second = b, a
	}

	// Check if they are the two halves of a parent prefix
	parentPrefix := netip.PrefixFrom(first.Addr(), parentBits)
	half1, half2 := splitPrefix(parentPrefix)

	if first == half1 && second == half2 {
		return parentPrefix
	}

	return netip.Prefix{} // Can't merge
}

// splitPrefix splits a prefix into two halves
func splitPrefix(prefix netip.Prefix) (netip.Prefix, netip.Prefix) {
	addr := prefix.Addr()
	bits := prefix.Bits()
	newBits := bits + 1

	// First half is just the original prefix with one more bit
	half1 := netip.PrefixFrom(addr, newBits)

	// Second half has the next bit set
	addrBytes := addr.AsSlice()
	byteIndex := bits / 8
	bitIndex := bits % 8

	// Make a copy
	half2Bytes := make([]byte, len(addrBytes))
	copy(half2Bytes, addrBytes)

	// Set the bit
	half2Bytes[byteIndex] |= 1 << (7 - bitIndex)

	half2Addr, _ := netip.AddrFromSlice(half2Bytes)
	half2 := netip.PrefixFrom(half2Addr, newBits)

	return half1, half2
}
