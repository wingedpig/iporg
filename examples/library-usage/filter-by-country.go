package main

import (
	"bufio"
	"fmt"
	"log"
	"os"

	"iporg/pkg/iporgdb"
	"iporg/pkg/model"
)

// Example: Filter IPs by country code
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <country-code>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Reads IPs from stdin, outputs only those in the specified country\n")
		fmt.Fprintf(os.Stderr, "Example: cat ips.txt | %s GB\n", os.Args[0])
		os.Exit(1)
	}

	targetCountry := os.Args[1]

	db, err := iporgdb.Open("/var/groupsio/data/iporgdb")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	scanner := bufio.NewScanner(os.Stdin)
	matchCount := 0
	totalCount := 0

	for scanner.Scan() {
		ip := scanner.Text()
		if ip == "" {
			continue
		}

		totalCount++

		rec, err := db.LookupString(ip)
		if err == model.ErrNotFound {
			continue
		}
		if err != nil {
			log.Printf("Error looking up %s: %v", ip, err)
			continue
		}

		if rec.Country == targetCountry {
			fmt.Printf("%s\t%s\t%s\n", ip, rec.OrgName, rec.City)
			matchCount++
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("Error reading input: %v", err)
	}

	fmt.Fprintf(os.Stderr, "Matched %d of %d IPs in country %s\n", matchCount, totalCount, targetCountry)
}
