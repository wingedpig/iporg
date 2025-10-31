// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package ripebulk

import (
	"net/netip"
	"time"
)

// Inetnum represents an IPv4 range from RIPE inetnum object
type Inetnum struct {
	Start   uint32   // Start IP (big-endian uint32)
	End     uint32   // End IP (big-endian uint32, inclusive)
	OrgID   string   // Organisation ID (e.g., "ORG-EA123-RIPE")
	Status  string   // Status (e.g., ASSIGNED-PA, SUB-ALLOCATED-PA, ALLOCATED-PA, LEGACY)
	Country string   // Country code (2-letter ISO, may be empty)
	Netname string   // Network name
	Descr   string   // Description (often contains organization name)
	Remarks []string // Remarks (for extracting organization info when OrgID is missing)
}

// Organisation represents a RIPE organisation object
type Organisation struct {
	OrgID   string // Primary key (e.g., "ORG-EA123-RIPE")
	OrgName string // Human-readable name
	OrgType string // RIR, LIR, OTHER, etc.
}

// Match represents the result of a prefix lookup
type Match struct {
	Start        netip.Addr   // Start IP of the matching inetnum
	End          netip.Addr   // End IP of the matching inetnum
	Prefix       netip.Prefix // Original query prefix
	OrgID        string       // Organisation ID
	OrgName      string       // Organisation name (resolved from OrgID)
	OrgType      string       // Organisation type
	Status       string       // RIPE status
	Country      string       // Country code
	Netname      string       // Network name
	MatchedAt    time.Time    // When this lookup was performed
	FullyCovered bool         // True if inetnum fully covers the query prefix
}

// Metadata stores build information
type Metadata struct {
	SchemaVersion      int       // Database schema version
	BuildTime          time.Time // When the database was built
	InetnumCount       int64     // Number of inetnum objects indexed
	OrgCount           int64     // Number of organisation objects indexed
	InetnumSerial      string    // RIPE serial/version of inetnum dump
	OrganisationSerial string    // RIPE serial/version of organisation dump
	SourceURL          string    // Base URL where dumps were fetched from
}

// Error types for RIPE bulk operations
type Error string

const (
	ErrNotFound       Error = "no matching inetnum found"
	ErrInvalidIP      Error = "invalid IP address"
	ErrInvalidRange   Error = "invalid IP range"
	ErrParseError     Error = "RPSL parse error"
	ErrDatabaseClosed Error = "database is closed"
	ErrFetchFailed    Error = "failed to fetch RIPE dump"
)

func (e Error) Error() string {
	return string(e)
}
