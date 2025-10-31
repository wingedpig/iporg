// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package iptoasn

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/wingedpig/iporg/pkg/model"
)

// Parser handles parsing iptoasn TSV format
type Parser struct {
	scanner *bufio.Scanner
	lineNum int
}

// NewParser creates a new parser for the given reader
func NewParser(r io.Reader) *Parser {
	scanner := bufio.NewScanner(r)
	// Increase buffer size for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	return &Parser{
		scanner: scanner,
	}
}

// ParseAll parses all rows from the input
func (p *Parser) ParseAll() ([]*model.RawRow, error) {
	var rows []*model.RawRow
	for {
		rowSet, err := p.ParseNext()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if rowSet != nil {
			rows = append(rows, rowSet...)
		}
	}
	return rows, nil
}

// ParseNext parses the next row from the input and returns one or more RawRow objects
// (a single TSV line may expand to multiple CIDRs)
func (p *Parser) ParseNext() ([]*model.RawRow, error) {
	if !p.scanner.Scan() {
		if err := p.scanner.Err(); err != nil {
			return nil, fmt.Errorf("scanner error at line %d: %w", p.lineNum, err)
		}
		return nil, io.EOF
	}

	p.lineNum++
	line := strings.TrimSpace(p.scanner.Text())

	// Skip empty lines and comments
	if line == "" || strings.HasPrefix(line, "#") {
		return nil, nil
	}

	// Parse TSV format: start_ip \t end_ip \t asn \t country \t registry
	fields := strings.Split(line, "\t")
	if len(fields) < 5 {
		return nil, fmt.Errorf("line %d: expected 5 fields, got %d", p.lineNum, len(fields))
	}

	// Parse start IP
	startIP, err := netip.ParseAddr(strings.TrimSpace(fields[0]))
	if err != nil {
		return nil, fmt.Errorf("line %d: invalid start IP: %w", p.lineNum, err)
	}

	// Parse end IP
	endIP, err := netip.ParseAddr(strings.TrimSpace(fields[1]))
	if err != nil {
		return nil, fmt.Errorf("line %d: invalid end IP: %w", p.lineNum, err)
	}

	// Ensure same IP family
	if startIP.Is4() != endIP.Is4() {
		return nil, fmt.Errorf("line %d: start and end IPs have different families", p.lineNum)
	}

	// Parse ASN
	asnStr := strings.TrimSpace(fields[2])
	asn, err := strconv.Atoi(asnStr)
	if err != nil {
		return nil, fmt.Errorf("line %d: invalid ASN: %w", p.lineNum, err)
	}

	// Parse country
	country := strings.TrimSpace(fields[3])
	if len(country) != 2 && country != "ZZ" {
		// Lenient: accept it anyway but log
		if country != "" {
			country = "ZZ"
		}
	}

	// Parse registry
	registry := strings.TrimSpace(fields[4])

	// Optional: AS name (some datasets have this as 6th field)
	asName := ""
	if len(fields) > 5 {
		asName = strings.TrimSpace(fields[5])
	}

	// Convert IP range to CIDR(s)
	cidrs, err := rangeToCIDRs(startIP, endIP)
	if err != nil {
		return nil, fmt.Errorf("line %d: failed to convert range to CIDRs: %w", p.lineNum, err)
	}

	if len(cidrs) == 0 {
		return nil, fmt.Errorf("line %d: no CIDRs generated from range", p.lineNum)
	}

	// Create one RawRow for each CIDR that the range expands to
	var rows []*model.RawRow
	for _, cidr := range cidrs {
		// Calculate start/end for this specific CIDR
		cidrStart, cidrEnd := cidrToRange(cidr)

		row := &model.RawRow{
			Prefix:   cidr,
			Start:    cidrStart,
			End:      cidrEnd,
			ASN:      asn,
			Country:  country,
			Registry: registry,
			ASName:   asName,
		}
		rows = append(rows, row)
	}

	return rows, nil
}

// rangeToCIDRs converts an IP range to a list of CIDRs that cover it
func rangeToCIDRs(start, end netip.Addr) ([]*net.IPNet, error) {
	// Ensure both are same family
	if start.Is4() != end.Is4() {
		return nil, fmt.Errorf("IP address family mismatch")
	}

	// For IPv4
	if start.Is4() {
		return rangeToCIDRsV4(start, end)
	}

	// IPv6 not implemented (user said ignore IPv6)
	return nil, fmt.Errorf("IPv6 not supported")
}

// rangeToCIDRsV4 converts an IPv4 range to CIDRs
func rangeToCIDRsV4(start, end netip.Addr) ([]*net.IPNet, error) {
	startInt := ipToUint32(start)
	endInt := ipToUint32(end)

	if startInt > endInt {
		return nil, fmt.Errorf("start IP is greater than end IP")
	}

	var cidrs []*net.IPNet

	for startInt <= endInt {
		// Find the largest CIDR block that:
		// 1. Starts at startInt (must be aligned for that prefix length)
		// 2. Doesn't extend past endInt

		// Find the maximum number of trailing zero bits in startInt
		// This determines the largest possible aligned block
		maxTrailingZeros := 0
		if startInt != 0 {
			// Count trailing zeros - this is the max prefix we can align to
			temp := startInt
			for temp&1 == 0 {
				maxTrailingZeros++
				temp >>= 1
			}
		} else {
			// startInt is 0, so it's aligned to any prefix
			maxTrailingZeros = 32
		}

		// Now find the largest prefix length (smallest mask bits) that:
		// - Is aligned (prefix length >= 32 - maxTrailingZeros)
		// - Doesn't exceed endInt
		prefixLen := 32
		for pl := 32 - maxTrailingZeros; pl <= 32; pl++ {
			// Calculate the end of this potential CIDR block
			blockSize := uint32(1) << (32 - pl)
			blockEnd := startInt + blockSize - 1

			if blockEnd <= endInt {
				// This block fits
				prefixLen = pl
				break
			}
		}

		// Create CIDR
		ip := uint32ToIP(startInt)
		cidr := &net.IPNet{
			IP:   ip,
			Mask: net.CIDRMask(prefixLen, 32),
		}
		cidrs = append(cidrs, cidr)

		// Move to next IP after this CIDR block
		blockSize := uint32(1) << (32 - prefixLen)
		startInt += blockSize

		// Check for overflow (wrapped around to 0)
		if startInt == 0 && endInt != 0xFFFFFFFF {
			break
		}
	}

	return cidrs, nil
}

// ipToUint32 converts netip.Addr to uint32 (IPv4 only)
func ipToUint32(ip netip.Addr) uint32 {
	b := ip.As4()
	return binary.BigEndian.Uint32(b[:])
}

// uint32ToIP converts uint32 to net.IP (IPv4)
func uint32ToIP(n uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, n)
	return ip
}

// NormalizeCIDR converts a net.IPNet to canonical CIDR string
func NormalizeCIDR(ipnet *net.IPNet) string {
	// Ensure the IP is the network address
	ip := ipnet.IP.Mask(ipnet.Mask)
	ones, _ := ipnet.Mask.Size()
	// ones is already the prefix length - no complex formula needed!
	return fmt.Sprintf("%s/%d", ip.String(), ones)
}

// cidrToRange converts a CIDR to start and end IP addresses
func cidrToRange(ipnet *net.IPNet) (start, end netip.Addr) {
	// Start is the network address
	startBytes := ipnet.IP.To4()
	if startBytes == nil {
		// IPv6 not supported
		return netip.Addr{}, netip.Addr{}
	}

	startInt := binary.BigEndian.Uint32(startBytes)

	// Calculate end IP
	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones
	endInt := startInt + (uint32(1) << hostBits) - 1

	start, _ = netip.AddrFromSlice(startBytes)
	endBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(endBytes, endInt)
	end, _ = netip.AddrFromSlice(endBytes)

	return start, end
}
