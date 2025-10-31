// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/wingedpig/iporg/pkg/iporgdb"
	"github.com/wingedpig/iporg/pkg/model"
)

// Example: Output lookup results as JSON
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <ip-address>\n", os.Args[0])
		os.Exit(1)
	}

	db, err := iporgdb.Open("/var/groupsio/data/iporgdb")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ip := os.Args[1]
	rec, err := db.LookupString(ip)
	if err != nil {
		if err == model.ErrNotFound {
			errorJSON := map[string]string{
				"error": "IP not found",
				"ip":    ip,
			}
			json.NewEncoder(os.Stdout).Encode(errorJSON)
			os.Exit(1)
		}
		log.Fatalf("Lookup failed: %v", err)
	}

	// Convert to LookupResult for JSON output
	result := iporgdb.ToLookupResult(ip, rec)

	// Output as pretty-printed JSON
	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Fatalf("JSON marshal failed: %v", err)
	}
	fmt.Println(string(jsonData))
}
