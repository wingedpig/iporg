// SPDX-License-Identifier: MIT
// Copyright (c) 2025 Mark Feghali

package arinbulk

import (
	"net/netip"
	"time"
)

// NetBlock represents an ARIN network block (NetRange)
type NetBlock struct {
	Start      uint32   // Start IP (big-endian uint32 for IPv4)
	End        uint32   // End IP (big-endian uint32 for IPv4)
	NetName    string   // Network name
	NetHandle  string   // ARIN net handle (e.g., NET-8-0-0-0-1)
	OrgID      string   // Organization ID (e.g., LPL-141)
	NetType    string   // Direct Allocation, Direct Assignment, etc.
	ParentNet  string   // Parent network handle
	CIDR       []string // CIDR blocks (can be multiple)
	Comments   []string // Comments
	UpdateDate string   // Last updated date
}

// Organization represents an ARIN organization
type Organization struct {
	OrgID      string // Organization ID (e.g., LPL-141)
	OrgName    string // Organization name
	Address    string // Street address
	City       string
	StateProv  string // State/Province
	PostalCode string
	Country    string
	UpdateDate string
}

// Match represents the result of a lookup
type Match struct {
	Start        netip.Addr   // Start IP
	End          netip.Addr   // End IP
	Prefix       netip.Prefix // Query prefix
	NetHandle    string       // ARIN net handle
	OrgID        string       // Organization ID
	OrgName      string       // Organization name
	NetType      string       // Network type
	NetName      string       // Network name
	Country      string       // Country code
	MatchedAt    time.Time    // When lookup was performed
	FullyCovered bool         // True if fully covers query
}

// Metadata stores build information
type Metadata struct {
	SchemaVersion int       // Database schema version
	BuildTime     time.Time // When built
	NetBlockCount int64     // Number of net blocks
	OrgCount      int64     // Number of organizations
	SourceDate    string    // Date of ARIN bulk data
	SourceURL     string    // Where data was fetched from
}

// Error types
type Error string

const (
	ErrNotFound       Error = "no matching network found"
	ErrInvalidIP      Error = "invalid IP address"
	ErrInvalidRange   Error = "invalid IP range"
	ErrParseError     Error = "parse error"
	ErrDatabaseClosed Error = "database is closed"
)

func (e Error) Error() string {
	return string(e)
}
