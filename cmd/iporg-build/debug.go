package main

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"sort"

	"iporg/pkg/iporgdb"
	"iporg/pkg/model"
	"iporg/pkg/sources/maxmind"
	"iporg/pkg/sources/rdap"
	"iporg/pkg/sources/ripe"
	"iporg/pkg/util/ipcodec"
)

// RunDebug performs debugging checks
func RunDebug(ctx context.Context, ip, asnPath, cityPath, dbPath string, asn int, ripeBase string) error {
	fmt.Println("=== IP Organization Debug Tool ===\n")

	parsedIP, err := netip.ParseAddr(ip)
	if err != nil {
		return fmt.Errorf("invalid IP address: %w", err)
	}

	fmt.Printf("IP Address: %s\n", ip)
	fmt.Printf("IP Version: IPv%d\n\n", func() int {
		if parsedIP.Is4() {
			return 4
		}
		return 6
	}())

	// Step 1: Check MaxMind
	if asnPath != "" && cityPath != "" {
		fmt.Println("--- MaxMind Lookup ---")
		mm, err := maxmind.Open(asnPath, cityPath)
		if err != nil {
			log.Printf("ERROR: Failed to open MaxMind: %v", err)
		} else {
			defer mm.Close()

			// ASN lookup
			asn, asnName, err := mm.ASNInfo(parsedIP)
			if err != nil {
				log.Printf("ERROR: MaxMind ASN lookup failed: %v", err)
			} else {
				fmt.Printf("ASN: AS%d (%s)\n", asn, asnName)
			}

			// Geo lookup
			geo, err := mm.Geo(parsedIP)
			if err != nil {
				log.Printf("ERROR: MaxMind Geo lookup failed: %v", err)
			} else {
				fmt.Printf("Country: %s\n", geo.Country)
				if geo.City != "" {
					fmt.Printf("City: %s\n", geo.City)
				}
			}
		}
		fmt.Println()
	}

	// Step 1.5: Check RDAP
	fmt.Println("--- RDAP Lookup ---")
	rdapClient := rdap.NewClient("https://rdap.db.ripe.net", "iporg-debug/1.0", 5.0)
	rdapResp, err := rdapClient.QueryIP(ctx, parsedIP)
	if err != nil {
		log.Printf("ERROR: RDAP query failed: %v", err)
	} else if rdapResp != nil {
		fmt.Printf("Network Name: %s\n", rdapResp.Name)
		fmt.Printf("Type: %s\n", rdapResp.Type)
		fmt.Printf("Status: %v\n", rdapResp.Status)
		fmt.Printf("Country: %s\n", rdapResp.Country)

		if len(rdapResp.Entities) > 0 {
			fmt.Printf("\nEntities (%d):\n", len(rdapResp.Entities))
			for i, entity := range rdapResp.Entities {
				fmt.Printf("  [%d] Handle: %s\n", i, entity.Handle)
				fmt.Printf("      Roles: %v\n", entity.Roles)
				name := rdap.GetEntityName(&entity)
				if name != "" {
					fmt.Printf("      Name: %s\n", name)
				}

				// Show nested entities
				if len(entity.Entities) > 0 {
					fmt.Printf("      Nested Entities (%d):\n", len(entity.Entities))
					for j, nested := range entity.Entities {
						nestedName := rdap.GetEntityName(&nested)
						fmt.Printf("        [%d] Handle: %s, Roles: %v", j, nested.Handle, nested.Roles)
						if nestedName != "" {
							fmt.Printf(", Name: %s", nestedName)
						}
						fmt.Println()
					}
				}
			}
		}

		if len(rdapResp.Remarks) > 0 {
			fmt.Printf("\nRemarks:\n")
			for _, remark := range rdapResp.Remarks {
				if remark.Title != "" {
					fmt.Printf("  Title: %s\n", remark.Title)
				}
				for _, desc := range remark.Description {
					fmt.Printf("  - %s\n", desc)
				}
			}
		}

		// Show what our parser extracts
		org, err := rdap.ParseOrg(rdapResp)
		if err != nil {
			fmt.Printf("\nParsed Organization: ERROR - %v\n", err)
		} else {
			fmt.Printf("\nParsed Organization:\n")
			fmt.Printf("  Name: %s\n", org.OrgName)
			fmt.Printf("  RIR: %s\n", org.RIR)
			fmt.Printf("  Role: %s\n", org.SourceRole)
			fmt.Printf("  Status: %s\n", org.StatusLabel)
		}
	}
	fmt.Println()

	// Step 2: Check RIPEstat announced prefixes
	if asn > 0 {
		fmt.Printf("--- RIPEstat Announced Prefixes for AS%d ---\n", asn)
		client := ripe.NewClient(ripeBase, "iporg-debug/1.0", 10.0)
		prefixes, err := client.AnnouncedPrefixes(ctx, asn)
		if err != nil {
			log.Printf("ERROR: Failed to fetch announced prefixes: %v", err)
		} else {
			fmt.Printf("Total prefixes: %d\n", len(prefixes))

			// Find prefixes that contain our IP
			var matching []string
			for _, prefix := range prefixes {
				start, end, err := ipcodec.CIDRToRange(prefix)
				if err != nil {
					continue
				}

				// Skip IPv6 if our IP is IPv4
				if start.Is4() != parsedIP.Is4() {
					continue
				}

				if ipcodec.IsInRange(parsedIP, start, end) {
					matching = append(matching, prefix)
				}
			}

			if len(matching) > 0 {
				fmt.Printf("Prefixes containing %s:\n", ip)
				for _, p := range matching {
					fmt.Printf("  - %s\n", p)
				}
			} else {
				fmt.Printf("WARNING: No announced prefixes contain %s\n", ip)
				fmt.Printf("\nShowing first 10 prefixes for reference:\n")
				for i, p := range prefixes {
					if i >= 10 {
						break
					}
					fmt.Printf("  - %s\n", p)
				}
			}
		}
		fmt.Println()
	}

	// Step 3: Check database
	if dbPath != "" {
		fmt.Println("--- Database Lookup ---")
		db, err := iporgdb.Open(dbPath)
		if err != nil {
			log.Printf("ERROR: Failed to open database: %v", err)
		} else {
			defer db.Close()

			rec, err := db.GetByIP(parsedIP)
			if err != nil {
				fmt.Printf("Result: NOT FOUND\n")
				fmt.Printf("Error: %v\n", err)

				// Show nearby ranges
				fmt.Println("\nNearby ranges in database:")
				showNearbyRanges(db, parsedIP)
			} else {
				fmt.Printf("Result: FOUND\n")
				fmt.Printf("Organization: %s\n", rec.OrgName)
				fmt.Printf("ASN: AS%d (%s)\n", rec.ASN, rec.ASNName)
				fmt.Printf("Prefix: %s\n", rec.Prefix)
				fmt.Printf("Range: %s - %s\n", rec.Start, rec.End)
				fmt.Printf("Country: %s\n", rec.Country)
				if rec.City != "" {
					fmt.Printf("City: %s\n", rec.City)
				}
				fmt.Printf("Source: %s\n", rec.SourceRole)
			}

			// Database stats
			fmt.Println("\nDatabase statistics:")
			ipv4, ipv6, err := db.CountRanges()
			if err != nil {
				log.Printf("ERROR: Failed to count ranges: %v", err)
			} else {
				fmt.Printf("IPv4 ranges: %d\n", ipv4)
				fmt.Printf("IPv6 ranges: %d\n", ipv6)
			}
		}
		fmt.Println()
	}

	return nil
}

// showNearbyRanges shows ranges near the query IP
func showNearbyRanges(db *iporgdb.DB, ip netip.Addr) {
	isIPv4 := ip.Is4()

	type rangeInfo struct {
		start  netip.Addr
		end    netip.Addr
		prefix string
		org    string
	}

	var ranges []rangeInfo
	err := db.IterateRanges(isIPv4, func(rec *model.Record) error {
		ranges = append(ranges, rangeInfo{
			start:  rec.Start,
			end:    rec.End,
			prefix: rec.Prefix,
			org:    rec.OrgName,
		})
		return nil
	})

	if err != nil {
		log.Printf("ERROR: Failed to iterate ranges: %v", err)
		return
	}

	if len(ranges) == 0 {
		fmt.Println("  (database is empty)")
		return
	}

	// Sort by start IP
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].start.Compare(ranges[j].start) < 0
	})

	// Find position where our IP would fit
	pos := sort.Search(len(ranges), func(i int) bool {
		return ranges[i].start.Compare(ip) > 0
	})

	// Show ranges around this position
	start := pos - 3
	if start < 0 {
		start = 0
	}
	end := pos + 3
	if end > len(ranges) {
		end = len(ranges)
	}

	for i := start; i < end; i++ {
		marker := "  "
		if i == pos {
			marker = "→ "
		}
		fmt.Printf("%s%s - %s (%s) [%s]\n",
			marker,
			ranges[i].start,
			ranges[i].end,
			ranges[i].prefix,
			truncate(ranges[i].org, 40))
	}

	if pos < len(ranges) {
		fmt.Printf("\nYour IP %s would be between:\n", ip)
		if pos > 0 {
			fmt.Printf("  Previous: %s (%s)\n", ranges[pos-1].prefix, ranges[pos-1].start)
		}
		if pos < len(ranges) {
			fmt.Printf("  Next:     %s (%s)\n", ranges[pos].prefix, ranges[pos].start)
		}
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
