package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/wingedpig/iporg/pkg/iporgdb"
	"github.com/wingedpig/iporg/pkg/model"
)

var db *iporgdb.DB

// Example: HTTP API server for IP lookups
func main() {
	dbPath := os.Getenv("IPORG_DB")
	if dbPath == "" {
		dbPath = "/var/groupsio/data/iporgdb"
	}

	var err error
	db, err = iporgdb.Open(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	http.HandleFunc("/lookup", lookupHandler)
	http.HandleFunc("/health", healthHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting IP organization lookup API on :%s", port)
	log.Printf("Database: %s", dbPath)
	log.Printf("Endpoints:")
	log.Printf("  GET /lookup?ip=<address>  - Look up an IP address")
	log.Printf("  GET /health               - Health check")
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func lookupHandler(w http.ResponseWriter, r *http.Request) {
	ip := r.URL.Query().Get("ip")
	if ip == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Missing 'ip' query parameter",
		})
		return
	}

	rec, err := db.LookupString(ip)
	if err == model.ErrNotFound {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "IP not found in database",
			"ip":    ip,
		})
		return
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("Lookup failed: %v", err),
		})
		return
	}

	result := iporgdb.ToLookupResult(ip, rec)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	// Check if database is accessible
	if db.IsClosed() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "unhealthy",
			"reason": "database is closed",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
	})
}
