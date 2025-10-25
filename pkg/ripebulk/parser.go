package ripebulk

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"strings"
)

// ParseOrganisations parses RIPE organisation objects from a reader
func ParseOrganisations(r io.Reader) (map[string]Organisation, error) {
	orgs := make(map[string]Organisation)
	scanner := bufio.NewScanner(r)

	var current *Organisation
	var currentKey string

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line signals end of object
		if line == "" {
			if current != nil && current.OrgID != "" {
				orgs[current.OrgID] = *current
			}
			current = nil
			currentKey = ""
			continue
		}

		// Skip comments
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "%") {
			continue
		}

		// Continuation line (starts with whitespace)
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if current != nil && currentKey != "" {
				appendValue(current, currentKey, strings.TrimSpace(line))
			}
			continue
		}

		// Parse attribute: key colon value
		key, value, ok := parseAttribute(line)
		if !ok {
			continue
		}

		currentKey = key

		// Start new object
		if key == "organisation" {
			current = &Organisation{OrgID: value}
			continue
		}

		// Add attribute to current object
		if current != nil {
			appendValue(current, key, value)
		}
	}

	// Handle final object if file doesn't end with blank line
	if current != nil && current.OrgID != "" {
		orgs[current.OrgID] = *current
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseError, err)
	}

	return orgs, nil
}

// ParseInetnums parses RIPE inetnum objects from a reader
func ParseInetnums(r io.Reader) ([]Inetnum, error) {
	var inetnums []Inetnum
	scanner := bufio.NewScanner(r)

	var current *Inetnum
	var currentKey string

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line signals end of object
		if line == "" {
			// Accept ranges starting at 0.0.0.0 (e.g., 0.0.0.0/8 placeholders)
			if current != nil && current.End >= current.Start {
				inetnums = append(inetnums, *current)
			}
			current = nil
			currentKey = ""
			continue
		}

		// Skip comments
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "%") {
			continue
		}

		// Continuation line (starts with whitespace)
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if current != nil && currentKey != "" {
				appendInetnumValue(current, currentKey, strings.TrimSpace(line))
			}
			continue
		}

		// Parse attribute: key colon value
		key, value, ok := parseAttribute(line)
		if !ok {
			continue
		}

		currentKey = key

		// Start new object
		if key == "inetnum" {
			start, end, err := parseInetnumRange(value)
			if err != nil {
				// Skip invalid ranges
				continue
			}
			current = &Inetnum{Start: start, End: end}
			continue
		}

		// Add attribute to current object
		if current != nil {
			appendInetnumValue(current, key, value)
		}
	}

	// Handle final object if file doesn't end with blank line
	if current != nil && current.End >= current.Start {
		inetnums = append(inetnums, *current)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseError, err)
	}

	return inetnums, nil
}

// parseAttribute parses a line into key and value
// Format: "key:    value" or "key: value"
func parseAttribute(line string) (key, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx == -1 {
		return "", "", false
	}

	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])

	return key, value, true
}

// parseInetnumRange parses a range like "31.90.0.0 - 31.91.255.255"
func parseInetnumRange(s string) (start, end uint32, err error) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%w: invalid inetnum range format: %s", ErrInvalidRange, s)
	}

	startIP := strings.TrimSpace(parts[0])
	endIP := strings.TrimSpace(parts[1])

	startAddr, err := netip.ParseAddr(startIP)
	if err != nil || !startAddr.Is4() {
		return 0, 0, fmt.Errorf("%w: invalid start IP: %s", ErrInvalidIP, startIP)
	}

	endAddr, err := netip.ParseAddr(endIP)
	if err != nil || !endAddr.Is4() {
		return 0, 0, fmt.Errorf("%w: invalid end IP: %s", ErrInvalidIP, endIP)
	}

	// Convert to uint32 (big-endian)
	startBytes := startAddr.As4()
	endBytes := endAddr.As4()

	start = binary.BigEndian.Uint32(startBytes[:])
	end = binary.BigEndian.Uint32(endBytes[:])

	if start > end {
		return 0, 0, fmt.Errorf("%w: start > end: %s", ErrInvalidRange, s)
	}

	return start, end, nil
}

// appendValue appends a value to an Organisation field
func appendValue(org *Organisation, key, value string) {
	switch key {
	case "org-name":
		// Take first org-name if multiple
		if org.OrgName == "" {
			org.OrgName = value
		} else {
			org.OrgName += " " + value // Handle continuation lines
		}
	case "org-type":
		if org.OrgType == "" {
			org.OrgType = value
		}
	}
}

// appendInetnumValue appends a value to an Inetnum field
func appendInetnumValue(inet *Inetnum, key, value string) {
	switch key {
	case "org":
		// Take first org value if multiple (RIPE rules make this single-valued usually)
		if inet.OrgID == "" {
			inet.OrgID = value
		}
	case "status":
		if inet.Status == "" {
			inet.Status = value
		}
	case "country":
		if inet.Country == "" {
			inet.Country = value
		}
	case "netname":
		if inet.Netname == "" {
			inet.Netname = value
		} else {
			inet.Netname += " " + value // Handle continuation lines
		}
	case "descr":
		// Use first non-empty descr as the description
		if inet.Descr == "" && value != "" {
			inet.Descr = value
		}
	case "remarks":
		// Collect all remarks (there can be multiple)
		if value != "" {
			inet.Remarks = append(inet.Remarks, value)
		}
	}
}

// Uint32ToAddr converts a uint32 to netip.Addr (big-endian)
func Uint32ToAddr(ip uint32) netip.Addr {
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], ip)
	return netip.AddrFrom4(bytes)
}

// AddrToUint32 converts netip.Addr to uint32 (big-endian)
func AddrToUint32(addr netip.Addr) uint32 {
	bytes := addr.As4()
	return binary.BigEndian.Uint32(bytes[:])
}

// PrefixToRange converts a CIDR prefix to inclusive [start, end] uint32 range
func PrefixToRange(prefix netip.Prefix) (start, end uint32, err error) {
	if !prefix.Addr().Is4() {
		return 0, 0, fmt.Errorf("%w: prefix is not IPv4", ErrInvalidIP)
	}

	addr := prefix.Addr()
	bits := prefix.Bits()

	// Start is the network address
	start = AddrToUint32(addr)

	// End is start + (2^(32-bits) - 1)
	hostBits := 32 - bits
	if hostBits >= 32 {
		// Special case: /0
		end = 0xFFFFFFFF
	} else {
		end = start + (1<<hostBits - 1)
	}

	return start, end, nil
}
