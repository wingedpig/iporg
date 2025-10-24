package iptoasn

import (
	"net"
	"net/netip"
	"sort"

	"iporg/pkg/model"
)

// Aggregator handles CIDR aggregation and collapse
type Aggregator struct{}

// NewAggregator creates a new aggregator
func NewAggregator() *Aggregator {
	return &Aggregator{}
}

// CollapseByASN collapses prefixes per ASN
// Returns a map of ASN -> collapsed prefixes
func (a *Aggregator) CollapseByASN(prefixes []*model.CanonicalPrefix) map[int][]*model.CanonicalPrefix {
	// Group by ASN
	byASN := make(map[int][]*model.CanonicalPrefix)
	for _, p := range prefixes {
		byASN[p.ASN] = append(byASN[p.ASN], p)
	}

	// Collapse each ASN's prefixes
	result := make(map[int][]*model.CanonicalPrefix)
	for asn, asnPrefixes := range byASN {
		result[asn] = a.Collapse(asnPrefixes)
	}

	return result
}

// Collapse aggregates adjacent/sibling prefixes into supernets
func (a *Aggregator) Collapse(prefixes []*model.CanonicalPrefix) []*model.CanonicalPrefix {
	if len(prefixes) == 0 {
		return prefixes
	}

	// Parse all prefixes into sortable structures
	type parsedPrefix struct {
		prefix *model.CanonicalPrefix
		ipnet  *net.IPNet
		start  uint32
		end    uint32
	}

	var parsed []parsedPrefix
	for _, p := range prefixes {
		_, ipnet, err := net.ParseCIDR(p.CIDR)
		if err != nil {
			// Skip invalid CIDRs
			continue
		}

		// Only handle IPv4 for now
		if len(ipnet.IP) == 16 && ipnet.IP.To4() == nil {
			// Skip IPv6
			continue
		}

		ip := ipnet.IP.To4()
		if ip == nil {
			continue
		}

		addr, _ := netip.ParseAddr(ip.String())
		start := ipToUint32(addr)

		// Calculate end IP
		ones, bits := ipnet.Mask.Size()
		hostBits := bits - ones
		end := start + (1 << hostBits) - 1

		parsed = append(parsed, parsedPrefix{
			prefix: p,
			ipnet:  ipnet,
			start:  start,
			end:    end,
		})
	}

	// Sort by start IP
	sort.Slice(parsed, func(i, j int) bool {
		return parsed[i].start < parsed[j].start
	})

	// Collapse adjacent ranges
	var collapsed []*model.CanonicalPrefix
	if len(parsed) == 0 {
		return collapsed
	}

	current := parsed[0]
	currentStart := current.start
	currentEnd := current.end

	for i := 1; i < len(parsed); i++ {
		next := parsed[i]

		// Check if we can merge
		if next.start <= currentEnd+1 {
			// Overlapping or adjacent - extend current range
			if next.end > currentEnd {
				currentEnd = next.end
			}
		} else {
			// Gap - emit current and start new range
			cidrs := a.rangeToCIDRList(currentStart, currentEnd, current.prefix)
			collapsed = append(collapsed, cidrs...)

			current = next
			currentStart = next.start
			currentEnd = next.end
		}
	}

	// Emit final range
	cidrs := a.rangeToCIDRList(currentStart, currentEnd, current.prefix)
	collapsed = append(collapsed, cidrs...)

	return collapsed
}

// rangeToCIDRList converts an IP range to optimal CIDR list
func (a *Aggregator) rangeToCIDRList(start, end uint32, template *model.CanonicalPrefix) []*model.CanonicalPrefix {
	var result []*model.CanonicalPrefix

	for start <= end {
		// Find the maximum number of trailing zero bits in start
		// This determines the largest possible aligned block
		maxTrailingZeros := 0
		if start != 0 {
			// Count trailing zeros - this is the max prefix we can align to
			temp := start
			for temp&1 == 0 {
				maxTrailingZeros++
				temp >>= 1
			}
		} else {
			// start is 0, so it's aligned to any prefix
			maxTrailingZeros = 32
		}

		// Now find the largest prefix length (smallest mask bits) that:
		// - Is aligned (prefix length >= 32 - maxTrailingZeros)
		// - Doesn't exceed end
		prefixLen := 32
		for pl := 32 - maxTrailingZeros; pl <= 32; pl++ {
			// Calculate the end of this potential CIDR block
			blockSize := uint32(1) << (32 - pl)
			blockEnd := start + blockSize - 1

			if blockEnd <= end {
				// This block fits
				prefixLen = pl
				break
			}
		}

		// Create CIDR
		ip := uint32ToIP(start)
		cidr := &net.IPNet{
			IP:   ip,
			Mask: net.CIDRMask(prefixLen, 32),
		}

		// Create canonical prefix with template's metadata
		cp := &model.CanonicalPrefix{
			CIDR:     cidr.String(),
			ASN:      template.ASN,
			Country:  template.Country,
			Registry: template.Registry,
			ASName:   template.ASName,
		}
		result = append(result, cp)

		// Move to next IP after this CIDR block
		blockSize := uint32(1) << (32 - prefixLen)
		start += blockSize

		// Check for overflow (wrapped around to 0)
		if start == 0 && end != 0xFFFFFFFF {
			break
		}
	}

	return result
}

// Deduplicate removes exact duplicate prefixes
func (a *Aggregator) Deduplicate(prefixes []*model.CanonicalPrefix) []*model.CanonicalPrefix {
	seen := make(map[string]bool)
	var result []*model.CanonicalPrefix

	for _, p := range prefixes {
		key := p.CIDR + "|" + string(rune(p.ASN))
		if !seen[key] {
			seen[key] = true
			result = append(result, p)
		}
	}

	return result
}

// SortByStartIP sorts prefixes by their start IP address
func (a *Aggregator) SortByStartIP(prefixes []*model.CanonicalPrefix) {
	sort.Slice(prefixes, func(i, j int) bool {
		_, ipnetI, _ := net.ParseCIDR(prefixes[i].CIDR)
		_, ipnetJ, _ := net.ParseCIDR(prefixes[j].CIDR)

		if ipnetI == nil || ipnetJ == nil {
			return false
		}

		ipI := ipnetI.IP.To4()
		ipJ := ipnetJ.IP.To4()

		if ipI == nil || ipJ == nil {
			return false
		}

		addrI, _ := netip.ParseAddr(ipI.String())
		addrJ, _ := netip.ParseAddr(ipJ.String())

		return ipToUint32(addrI) < ipToUint32(addrJ)
	})
}
