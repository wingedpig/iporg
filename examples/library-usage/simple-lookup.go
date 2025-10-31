package main

import (
	"fmt"
	"log"

	"github.com/wingedpig/iporg/pkg/iporgdb"
	"github.com/wingedpig/iporg/pkg/model"
)

// Simple example: Look up a single IP address
func main() {
	// Open the database
	db, err := iporgdb.Open("/var/groupsio/data/iporgdb")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Look up an IP
	ip := "86.150.233.24"
	rec, err := db.LookupString(ip)
	if err != nil {
		if err == model.ErrNotFound {
			fmt.Printf("IP %s not found\n", ip)
			return
		}
		log.Fatalf("Lookup failed: %v", err)
	}

	// Print the results
	fmt.Printf("IP: %s\n", ip)
	fmt.Printf("Organization: %s\n", rec.OrgName)
	fmt.Printf("ASN: AS%d (%s)\n", rec.ASN, rec.ASNName)
	fmt.Printf("Country: %s\n", rec.Country)
	if rec.Region != "" {
		fmt.Printf("Region: %s\n", rec.Region)
	}
	if rec.City != "" {
		fmt.Printf("City: %s\n", rec.City)
	}
	fmt.Printf("Prefix: %s\n", rec.Prefix)
	fmt.Printf("Source: %s\n", rec.SourceRole)
}
