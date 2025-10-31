// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"

	"github.com/wingedpig/iporg/pkg/arinbulk"
)

const version = "1.0.0"

func main() {
	dbPath := flag.String("db", "./arinbulk.ldb", "Path to ARIN bulk LevelDB database")
	jsonOutput := flag.Bool("json", false, "Output in JSON format")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("arin-bulk-query version %s\n", version)
		return
	}

	if len(flag.Args()) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <ip-address>\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Open database
	db, err := arinbulk.OpenDatabase(*dbPath)
	if err != nil {
		log.Fatalf("ERROR: Failed to open database: %v", err)
	}
	defer db.Close()

	// Parse IP
	ipStr := flag.Args()[0]
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		log.Fatalf("ERROR: Invalid IP address: %v", err)
	}

	// Lookup
	match, err := db.LookupIP(ip)
	if err != nil {
		if err == arinbulk.ErrNotFound {
			if *jsonOutput {
				fmt.Println("{}")
			} else {
				fmt.Println("No match found (not in ARIN database)")
			}
			return
		}
		log.Fatalf("ERROR: Lookup failed: %v", err)
	}

	// Output result
	if match == nil {
		if *jsonOutput {
			fmt.Println("{}")
		} else {
			fmt.Println("No match found")
		}
		return
	}

	if *jsonOutput {
		outputJSON(match)
	} else {
		outputHuman(match)
	}
}

func outputJSON(match *arinbulk.Match) {
	result := map[string]interface{}{
		"start":       match.Start.String(),
		"end":         match.End.String(),
		"net_handle":  match.NetHandle,
		"org_id":      match.OrgID,
		"org_name":    match.OrgName,
		"net_type":    match.NetType,
		"net_name":    match.NetName,
		"country":     match.Country,
		"matched_at":  match.MatchedAt,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		log.Fatalf("ERROR: Failed to encode JSON: %v", err)
	}
}

func outputHuman(match *arinbulk.Match) {
	fmt.Printf("Range:       %s - %s\n", match.Start, match.End)
	fmt.Printf("Org Name:    %s\n", match.OrgName)
	if match.OrgID != "" {
		fmt.Printf("Org ID:      %s\n", match.OrgID)
	}
	if match.NetHandle != "" {
		fmt.Printf("Net Handle:  %s\n", match.NetHandle)
	}
	if match.NetName != "" {
		fmt.Printf("Net Name:    %s\n", match.NetName)
	}
	if match.NetType != "" {
		fmt.Printf("Net Type:    %s (%s)\n", match.NetType, arinbulk.ExpandNetType(match.NetType))
	}
	if match.Country != "" {
		fmt.Printf("Country:     %s\n", match.Country)
	}
}
