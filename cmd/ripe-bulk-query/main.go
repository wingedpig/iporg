package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"

	"github.com/wingedpig/iporg/pkg/ripebulk"
)

const version = "0.1.0"

func main() {
	var (
		dbPath      = flag.String("db", "data/ripe-bulk.ldb", "Path to RIPE bulk LevelDB database")
		jsonOutput  = flag.Bool("json", false, "Output in JSON format")
		showVersion = flag.Bool("version", false, "Show version and exit")
	)

	flag.Parse()

	if *showVersion {
		fmt.Printf("ripe-bulk-query v%s\n", version)
		os.Exit(0)
	}

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <ip-or-prefix>\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(1)
	}

	query := flag.Arg(0)

	// Open database
	db, err := ripebulk.OpenDatabase(*dbPath)
	if err != nil {
		log.Fatalf("ERROR: Failed to open database: %v", err)
	}
	defer db.Close()

	// Parse query (IP or CIDR)
	var match *ripebulk.Match

	// Try parsing as CIDR first
	if prefix, err := netip.ParsePrefix(query); err == nil {
		match, err = db.LookupPrefix(prefix)
		if err != nil {
			log.Fatalf("ERROR: Lookup failed: %v", err)
		}
	} else {
		// Try parsing as IP
		ip, err := netip.ParseAddr(query)
		if err != nil {
			log.Fatalf("ERROR: Invalid IP or prefix: %s", query)
		}

		match, err = db.LookupIP(ip)
		if err != nil {
			log.Fatalf("ERROR: Lookup failed: %v", err)
		}
	}

	// Output result
	if match == nil {
		if *jsonOutput {
			fmt.Println("{}")
		} else {
			fmt.Println("No match found (not in RIPE database)")
		}
		return
	}

	if *jsonOutput {
		outputJSON(match)
	} else {
		outputHuman(match)
	}
}

func outputJSON(match *ripebulk.Match) {
	result := map[string]interface{}{
		"start":      match.Start.String(),
		"end":        match.End.String(),
		"org_id":     match.OrgID,
		"org_name":   match.OrgName,
		"org_type":   match.OrgType,
		"status":     match.Status,
		"country":    match.Country,
		"netname":    match.Netname,
		"matched_at": match.MatchedAt,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		log.Fatalf("ERROR: Failed to encode JSON: %v", err)
	}
}

func outputHuman(match *ripebulk.Match) {
	fmt.Printf("Range:       %s - %s\n", match.Start, match.End)
	fmt.Printf("Org Name:    %s\n", match.OrgName)
	if match.OrgID != "" {
		fmt.Printf("Org ID:      %s\n", match.OrgID)
	}
	if match.OrgType != "" {
		fmt.Printf("Org Type:    %s\n", match.OrgType)
	}
	if match.Status != "" {
		fmt.Printf("Status:      %s\n", match.Status)
	}
	if match.Country != "" {
		fmt.Printf("Country:     %s\n", match.Country)
	}
	if match.Netname != "" {
		fmt.Printf("Netname:     %s\n", match.Netname)
	}
}
