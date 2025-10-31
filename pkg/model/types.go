package model

import (
	"net/netip"
	"time"
)

// Record represents a single IP range with associated metadata
type Record struct {
	Start       netip.Addr // Start IP of range
	End         netip.Addr // End IP of range
	ASN         int        // Autonomous System Number
	ASNName     string     // ASN organization name from MaxMind
	OrgName     string     // Organization name from RDAP (preferred) or ASN fallback
	RIR         string     // Regional Internet Registry (ARIN/RIPE/APNIC/LACNIC/AFRINIC)
	Country     string     // ISO 3166-1 alpha-2 country code
	Region      string     // Region/state name (optional)
	City        string     // City name (optional)
	Lat         float64    // Latitude (optional)
	Lon         float64    // Longitude (optional)
	SourceRole  string     // customer/registrant/asn_fallback - source of org_name
	StatusLabel string     // RIPE status (e.g., ASSIGNED-PA, SUB-ALLOCATED-PA)
	Prefix      string     // Original announced prefix (CIDR notation)
	LastChecked time.Time  // Last time this record was validated
	Schema      int        // Schema version for future migrations
}

// Stats represents database statistics
type Stats struct {
	TotalRecords     int64
	IPv4Records      int64
	IPv6Records      int64
	RecordsByRIR     map[string]int64
	RecordsByRole    map[string]int64
	RecordsByCountry map[string]int64
	LastBuiltAt      time.Time
	SchemaVersion    int
	BuilderVersion   string
}

// BuildConfig contains configuration for the build process
type BuildConfig struct {
	// Input files
	ASNFile       string
	MMDBASNPath   string
	MMDBCityPath  string
	IPtoASNDBPath  string // Optional: use iptoasn database instead of RIPEstat API
	RIPEBulkDBPath string // Optional: use RIPE bulk database instead of RDAP for RIPE region
	ARINBulkDBPath string // Optional: use ARIN bulk database instead of RDAP for ARIN region

	// Output
	DBPath string

	// Processing options
	Workers        int
	CacheTTL       time.Duration
	SplitByMaxMind bool // Mode B: split by MaxMind city blocks
	IPv4Only       bool // Skip IPv6 prefixes entirely
	AllASNs        bool // Build for all ASNs from iptoasn database
	BulkOnly       bool // Only process ASNs/prefixes with bulk database coverage

	// API configuration
	RIPEBaseURL      string
	RDAPBootstrapURL string
	UserAgent        string

	// Rate limiting
	RDAPRateLimit float64 // requests per second
}

// RDAPOrg represents organization information extracted from RDAP
type RDAPOrg struct {
	OrgName     string // Organization name
	RIR         string // Regional Internet Registry
	SourceRole  string // customer/registrant/asn_fallback
	StatusLabel string // Status from RDAP (e.g., ASSIGNED-PA)
	Country     string // Country code from RIR (preferred over MaxMind for RIR-managed space)
}

// ASNPrefixes represents announced prefixes for an ASN
type ASNPrefixes struct {
	ASN       int
	Prefixes  []string // CIDR notation
	FetchedAt time.Time
}

// LookupResult is the output format for IP lookups
type LookupResult struct {
	IP         string  `json:"ip"`
	ASN        int     `json:"asn"`
	ASNName    string  `json:"asn_name"`
	OrgName    string  `json:"org_name"`
	RIR        string  `json:"rir"`
	Country    string  `json:"country"`
	Region     string  `json:"region,omitempty"`
	City       string  `json:"city,omitempty"`
	Lat        float64 `json:"lat,omitempty"`
	Lon        float64 `json:"lon,omitempty"`
	Prefix     string  `json:"prefix"`
	SourceRole string  `json:"source_role"`
}

// Error types
type Error string

const (
	ErrNotFound       Error = "IP not found in database"
	ErrInvalidIP      Error = "invalid IP address"
	ErrDatabaseClosed Error = "database is closed"
	ErrOverlap        Error = "overlapping range detected"
	ErrInvalidRange   Error = "invalid IP range"
	ErrRateLimited    Error = "rate limited by upstream service"
	ErrRDAPFailed     Error = "RDAP query failed"
)

func (e Error) Error() string {
	return string(e)
}
