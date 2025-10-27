package arinbulk

import (
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"net/netip"
	"strings"
)

// XML structures matching ARIN bulk schema

type ARINDatabase struct {
	XMLName xml.Name `xml:"arin"`
	Nets    []NetXML `xml:"net"`
	Orgs    []OrgXML `xml:"org"`
	ASNs    []ASNXML `xml:"asn"`
	POCs    []POCXML `xml:"poc"`
}

type NetXML struct {
	Handle       string       `xml:"handle"`
	Name         string       `xml:"name"`
	OrgHandle    string       `xml:"orgHandle"`
	NetBlocks    NetBlocksXML `xml:"netBlocks"`
	ParentHandle string       `xml:"parentNetHandle"`
	RegDate      string       `xml:"registrationDate"`
	UpdateDate   string       `xml:"updateDate"`
	Version      string       `xml:"version"` // "4" or "6"
	Comments     []string     `xml:"comment>line"`
}

type NetBlocksXML struct {
	Blocks []NetBlockXML `xml:"netBlock"`
}

type NetBlockXML struct {
	StartAddress string `xml:"startAddress"`
	EndAddress   string `xml:"endAddress"`
	CIDRLength   string `xml:"cidrLength"`
	Type         string `xml:"type"`
	Description  string `xml:"description"`
}

type OrgXML struct {
	Handle     string `xml:"handle"`
	Name       string `xml:"name"`
	ISO3166_1  string `xml:"iso3166-1>code2"`  // Country
	ISO3166_2  string `xml:"iso3166-2>code3"`  // State/Province
	RegDate    string `xml:"registrationDate"`
	UpdateDate string `xml:"updateDate"`
}

type ASNXML struct {
	Handle       string `xml:"handle"`
	Name         string `xml:"name"`
	StartASN     string `xml:"startAsNumber"`
	EndASN       string `xml:"endAsNumber"`
	OrgHandle    string `xml:"orgHandle"`
	RegDate      string `xml:"registrationDate"`
	UpdateDate   string `xml:"updateDate"`
}

type POCXML struct {
	Handle  string `xml:"handle"`
	Name    string `xml:"contactName"`
	Company string `xml:"companyName"`
}

// ParseXML parses the ARIN bulk XML file
func ParseXML(r io.Reader) ([]NetBlock, map[string]Organization, error) {
	decoder := xml.NewDecoder(r)

	var nets []NetBlock
	var orgs = make(map[string]Organization)

	// Parse incrementally to handle large files
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("XML decode error: %w", err)
		}

		switch se := token.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "net":
				var netXML NetXML
				if err := decoder.DecodeElement(&netXML, &se); err != nil {
					return nil, nil, fmt.Errorf("failed to decode net: %w", err)
				}

				// Only process IPv4 for now
				if netXML.Version != "4" {
					continue
				}

				// Process each netBlock
				for _, block := range netXML.NetBlocks.Blocks {
					nb, err := parseNetBlock(netXML, block)
					if err != nil {
						// Skip invalid blocks silently
						continue
					}
					nets = append(nets, nb)
				}

			case "org":
				var orgXML OrgXML
				if err := decoder.DecodeElement(&orgXML, &se); err != nil {
					return nil, nil, fmt.Errorf("failed to decode org: %w", err)
				}

				orgs[orgXML.Handle] = Organization{
					OrgID:      orgXML.Handle,
					OrgName:    strings.TrimSpace(orgXML.Name),
					Country:    orgXML.ISO3166_1,
					StateProv:  orgXML.ISO3166_2,
					UpdateDate: orgXML.UpdateDate,
				}
			}
		}
	}

	return nets, orgs, nil
}

func parseNetBlock(net NetXML, block NetBlockXML) (NetBlock, error) {
	// Parse IP addresses (strip leading zeros - ARIN uses "001.002.003.004" format)
	startIP, err := netip.ParseAddr(stripLeadingZeros(block.StartAddress))
	if err != nil {
		return NetBlock{}, fmt.Errorf("invalid start address %s: %w", block.StartAddress, err)
	}

	endIP, err := netip.ParseAddr(stripLeadingZeros(block.EndAddress))
	if err != nil {
		return NetBlock{}, fmt.Errorf("invalid end address %s: %w", block.EndAddress, err)
	}

	// Only IPv4 for now
	if !startIP.Is4() || !endIP.Is4() {
		return NetBlock{}, fmt.Errorf("IPv6 not supported yet")
	}

	// Build CIDR notation
	var cidrs []string
	if block.CIDRLength != "" {
		cidrs = []string{fmt.Sprintf("%s/%s", block.StartAddress, block.CIDRLength)}
	}

	return NetBlock{
		Start:      AddrToUint32(startIP),
		End:        AddrToUint32(endIP),
		NetName:    net.Name,
		NetHandle:  net.Handle,
		OrgID:      net.OrgHandle,
		NetType:    block.Type,
		ParentNet:  net.ParentHandle,
		CIDR:       cidrs,
		Comments:   net.Comments,
		UpdateDate: net.UpdateDate,
	}, nil
}

// AddrToUint32 converts netip.Addr to uint32 (big-endian)
func AddrToUint32(ip netip.Addr) uint32 {
	bytes := ip.As4()
	return binary.BigEndian.Uint32(bytes[:])
}

// Uint32ToAddr converts uint32 to netip.Addr (big-endian)
func Uint32ToAddr(ip uint32) netip.Addr {
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], ip)
	return netip.AddrFrom4(bytes)
}

// stripLeadingZeros removes leading zeros from IP address octets
// ARIN uses format like "001.002.003.004" which netip.ParseAddr() rejects
func stripLeadingZeros(ip string) string {
	parts := strings.Split(ip, ".")
	for i, part := range parts {
		// Remove leading zeros but keep "0" if the whole part is "000"
		if len(part) > 1 {
			parts[i] = strings.TrimLeft(part, "0")
			if parts[i] == "" {
				parts[i] = "0"
			}
		}
	}
	return strings.Join(parts, ".")
}

// PrefixToRange converts a CIDR prefix to start/end uint32
func PrefixToRange(prefix string) (uint32, uint32, error) {
	p, err := netip.ParsePrefix(prefix)
	if err != nil {
		return 0, 0, err
	}

	if !p.Addr().Is4() {
		return 0, 0, fmt.Errorf("IPv6 not supported")
	}

	start := p.Addr()
	startInt := AddrToUint32(start)

	// Calculate end IP
	bits := p.Bits()
	hostBits := 32 - bits
	offset := uint32(1<<hostBits - 1)
	endInt := startInt + offset

	return startInt, endInt, nil
}

// ExpandNetType returns a human-readable description of the net type
func ExpandNetType(netType string) string {
	types := map[string]string{
		"A":  "Reallocation",
		"AF": "Allocated to AFRINIC",
		"AP": "Allocated to APNIC",
		"AR": "Allocated to ARIN",
		"AV": "Early registration (ARIN)",
		"DA": "Direct Allocation",
		"DS": "Direct Assignment",
		"FX": "Transferred to AFRINIC",
		"IR": "IANA Reserved",
		"IU": "IANA Special Use",
		"LN": "Allocated to LACNIC",
		"LX": "Transferred to LACNIC",
		"PV": "Early registration (APNIC)",
		"PX": "Transferred to APNIC",
		"RN": "Allocated to RIPE NCC",
		"RV": "Early registration (RIPE)",
		"RX": "Transferred to RIPE",
		"S":  "Reassignment",
	}

	if desc, ok := types[netType]; ok {
		return desc
	}
	return netType
}

// ParseRange parses an IP range string like "8.0.0.0 - 8.255.255.255"
func ParseRange(rangeStr string) (uint32, uint32, error) {
	parts := strings.Split(rangeStr, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range format")
	}

	startAddr, err := netip.ParseAddr(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start IP: %w", err)
	}

	endAddr, err := netip.ParseAddr(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end IP: %w", err)
	}

	if !startAddr.Is4() || !endAddr.Is4() {
		return 0, 0, fmt.Errorf("IPv6 not supported")
	}

	return AddrToUint32(startAddr), AddrToUint32(endAddr), nil
}

// IsValidOrgName filters out placeholder or administrative org names
func IsValidOrgName(name string) bool {
	if len(name) < 3 {
		return false
	}

	lower := strings.ToLower(name)

	// Skip common placeholders
	placeholders := []string{
		"unallocated",
		"reserved",
		"legacy-",
		"arin-",
		"not disclosed",
		"none",
		"n/a",
	}

	for _, placeholder := range placeholders {
		if strings.Contains(lower, placeholder) {
			return false
		}
	}

	return true
}
