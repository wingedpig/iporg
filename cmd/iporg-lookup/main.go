// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/wingedpig/iporg/pkg/iporgdb"
	"github.com/wingedpig/iporg/pkg/model"
)

const version = "1.0.0"

func main() {
	// Parse flags
	dbPath := flag.String("db", "./iporgdb", "Path to LevelDB database")
	jsonOutput := flag.Bool("json", true, "Output as JSON")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("iporg-lookup version %s\n", version)
		return
	}

	// Get IP address from args
	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: iporg-lookup [options] <ip-address>\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  iporg-lookup 8.8.8.8\n")
		fmt.Fprintf(os.Stderr, "  iporg-lookup --db=/data/iporgdb 2001:4860:4860::8888\n")
		os.Exit(1)
	}

	ipStr := flag.Arg(0)

	// Open database
	db, err := iporgdb.Open(*dbPath)
	if err != nil {
		log.Fatalf("ERROR: Failed to open database: %v", err)
	}
	defer db.Close()

	// Lookup IP
	rec, err := db.LookupString(ipStr)
	if err != nil {
		if err == model.ErrNotFound {
			if *jsonOutput {
				fmt.Printf("{\"error\":\"IP not found in database\",\"ip\":\"%s\"}\n", ipStr)
			} else {
				fmt.Printf("IP %s not found in database\n", ipStr)
			}
			os.Exit(1)
		}
		log.Fatalf("ERROR: Lookup failed: %v", err)
	}

	// Convert to result
	result := iporgdb.ToLookupResult(ipStr, rec)

	// Output
	if *jsonOutput {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			log.Fatalf("ERROR: Failed to marshal JSON: %v", err)
		}
		fmt.Println(string(data))
	} else {
		printHumanReadable(ipStr, result)
	}
}

func printHumanReadable(ip string, result *model.LookupResult) {
	fmt.Printf("IP Address:         %s\n", ip)
	fmt.Printf("Organization:       %s\n", result.OrgName)
	fmt.Printf("ASN:                AS%d (%s)\n", result.ASN, result.ASNName)
	fmt.Printf("Prefix:             %s\n", result.Prefix)
	fmt.Printf("RIR:                %s\n", result.RIR)
	fmt.Printf("Country:            %s\n", result.Country)
	if result.Region != "" {
		fmt.Printf("Region:             %s\n", result.Region)
	}
	if result.City != "" {
		fmt.Printf("City:               %s\n", result.City)
	}
	if result.Lat != 0 || result.Lon != 0 {
		fmt.Printf("Location:           %.4f, %.4f\n", result.Lat, result.Lon)
	}
	fmt.Printf("Source:             %s\n", result.SourceRole)
}
