// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package model

import (
	"net"
	"net/netip"
	"time"
)

// RawRow represents a parsed line from the iptoasn TSV file
type RawRow struct {
	Prefix   *net.IPNet // Canonical CIDR
	Start    netip.Addr // Inclusive start
	End      netip.Addr // Inclusive end
	ASN      int        // Origin ASN
	Country  string     // 2-letter code (may be "ZZ" for unknown)
	Registry string     // e.g., "ripencc", "arin", "apnic"
	ASName   string     // Provider/organization name from dataset
}

// CanonicalPrefix represents a normalized prefix entry for storage
type CanonicalPrefix struct {
	CIDR     string `msgpack:"cidr"`     // Canonical CIDR (e.g., "31.90.0.0/15")
	ASN      int    `msgpack:"asn"`      // Origin ASN
	Country  string `msgpack:"country"`  // 2-letter code
	Registry string `msgpack:"registry"` // RIR name
	ASName   string `msgpack:"asname"`   // Organization name
}

// ASNIndexEntry stores metadata about an ASN's prefixes
type ASNIndexEntry struct {
	ASN          int       `msgpack:"asn"`
	V4Count      int       `msgpack:"v4_count"`     // Number of IPv4 prefixes
	V4Collapsed  int       `msgpack:"v4_collapsed"` // Number after collapse
	LastModified time.Time `msgpack:"last_modified"`
}

// IPToASNStats represents statistics about the iptoasn database
type IPToASNStats struct {
	TotalPrefixes int64            `json:"total_prefixes"`
	IPv4Prefixes  int64            `json:"ipv4_prefixes"`
	CollapsedV4   int64            `json:"collapsed_v4"`
	UniqueASNs    int              `json:"unique_asns"`
	ByRegistry    map[string]int64 `json:"by_registry"`
	SourceURL     string           `json:"source_url"`
	LastModified  time.Time        `json:"last_modified"`
	BuiltAt       time.Time        `json:"built_at"`
	ETag          string           `json:"etag,omitempty"`
}

// FetchMetadata stores HTTP fetch metadata for incremental updates
type FetchMetadata struct {
	SourceURL    string    `json:"source_url"`
	ETag         string    `json:"etag,omitempty"`
	LastModified time.Time `json:"last_modified,omitempty"`
	CachePath    string    `json:"cache_path"`
	FetchedAt    time.Time `json:"fetched_at"`
}
