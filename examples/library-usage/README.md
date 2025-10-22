# Library Usage Examples

These examples demonstrate how to use the `iporgdb` package as a library in your own Go projects.

## Prerequisites

```bash
# Build the iporg database first
cd ../..
make build

# Build database with your ASNs
./bin/iporg-build build \
  --asn-file=examples/asns.txt \
  --mmdb-asn=GeoLite2-ASN.mmdb \
  --mmdb-city=GeoLite2-City.mmdb \
  --db=/var/groupsio/data/iporgdb
```

## Examples

### 1. Simple Lookup

Look up a single IP address:

```bash
cd examples/library-usage
go run simple-lookup.go
```

**Output:**
```
IP: 86.150.233.24
Organization: BT CENTRAL PLUS
ASN: AS2856 (British Telecommunications PLC)
Country: GB
Region: England
City: Lewisham
Prefix: 86.128.0.0/10
Source: network_name
```

### 2. JSON Output

Look up an IP and output as JSON:

```bash
go run json-output.go 31.112.0.10
```

**Output:**
```json
{
  "ip": "31.112.0.10",
  "asn": 2856,
  "asn_name": "British Telecommunications PLC",
  "org_name": "EE Limited",
  "rir": "RIPE",
  "country": "GB",
  "region": "England",
  "city": "Camden",
  "lat": 51.5321,
  "lon": -0.1233,
  "prefix": "31.112.0.0/14",
  "source_role": "registrant"
}
```

### 3. Bulk Lookup (Concurrent)

Process multiple IPs efficiently:

```bash
cat > ips.txt <<EOF
86.150.233.24
31.112.0.10
217.42.22.152
8.8.8.8
EOF

cat ips.txt | go run bulk-lookup.go > results.jsonl
```

**Features:**
- Concurrent processing with worker pool
- JSONL output (one JSON object per line)
- Handles errors gracefully

### 4. HTTP API Server

Run an HTTP API for IP lookups:

```bash
# Start the server
go run http-api.go

# In another terminal:
curl "http://localhost:8080/lookup?ip=86.150.233.24"
curl "http://localhost:8080/health"
```

**Environment Variables:**
- `IPORG_DB` - Database path (default: `/var/groupsio/data/iporgdb`)
- `PORT` - HTTP port (default: `8080`)

### 5. Filter by Country

Filter IPs by country code:

```bash
cat ips.txt | go run filter-by-country.go GB
```

**Output:**
```
86.150.233.24    BT CENTRAL PLUS           Lewisham
31.112.0.10      EE Limited                Camden
217.42.22.152    BT-CENTRAL-PLUS          Nottingham
```

## Using in Your Project

### Add Dependency

If this repo is at `github.com/yourusername/iporg`:

```bash
go get github.com/yourusername/iporg/pkg/iporgdb
go get github.com/yourusername/iporg/pkg/model
```

### Import in Code

```go
package main

import (
    "fmt"
    "log"

    "github.com/yourusername/iporg/pkg/iporgdb"
    "github.com/yourusername/iporg/pkg/model"
)

func main() {
    db, err := iporgdb.Open("/var/groupsio/data/iporgdb")
    if err != nil {
        log.Fatalf("Failed to open database: %v", err)
    }
    defer db.Close()

    rec, err := db.LookupString("86.150.233.24")
    if err == model.ErrNotFound {
        fmt.Println("IP not found")
        return
    }
    if err != nil {
        log.Fatalf("Lookup failed: %v", err)
    }

    fmt.Printf("Organization: %s\n", rec.OrgName)
}
```

## Key Functions

### Open Database
```go
db, err := iporgdb.Open("/path/to/iporgdb")
defer db.Close()
```

### Look Up IP (String)
```go
rec, err := db.LookupString("86.150.233.24")
if err == model.ErrNotFound {
    // IP not in database
}
```

### Look Up IP (netip.Addr)
```go
import "net/netip"

ip := netip.MustParseAddr("86.150.233.24")
rec, err := db.GetByIP(ip)
```

### Convert to JSON-Friendly Format
```go
result := iporgdb.ToLookupResult("86.150.233.24", rec)
// result is *model.LookupResult with JSON tags
```

### Check Database Status
```go
if db.IsClosed() {
    // Database has been closed
}
```

## Record Fields

```go
type Record struct {
    Start       netip.Addr // Start IP of range
    End         netip.Addr // End IP of range
    ASN         int        // Autonomous System Number
    ASNName     string     // ASN organization name
    OrgName     string     // Organization name
    RIR         string     // ARIN/RIPE/APNIC/LACNIC/AFRINIC
    Country     string     // ISO 3166-1 alpha-2
    Region      string     // State/province
    City        string     // City name
    Lat         float64    // Latitude
    Lon         float64    // Longitude
    SourceRole  string     // customer/registrant/network_name/etc
    StatusLabel string     // RIPE status
    Prefix      string     // CIDR prefix
    LastChecked time.Time  // Last update
    Schema      int        // Schema version
}
```

## Performance

- **Lookup speed**: < 1ms per IP
- **Concurrency**: Thread-safe, share one `*DB` instance
- **Memory**: ~10-50MB for database with 100k-500k ranges
- **Bulk processing**: Use worker pools for optimal performance

## Error Handling

```go
rec, err := db.LookupString(ip)
switch err {
case nil:
    // Success
case model.ErrNotFound:
    // IP not in database
case model.ErrInvalidIP:
    // Invalid IP address format
case model.ErrDatabaseClosed:
    // Database has been closed
default:
    // Other error
}
```

## Best Practices

1. **Reuse database handle**: Open once, use many times
2. **Always close**: Use `defer db.Close()`
3. **Handle not found**: `ErrNotFound` is normal for IPs outside your ASN list
4. **Use worker pools**: For bulk processing, limit concurrency
5. **Check IsoClosed()**: Before long-running operations

## Building Your Own Tool

See the source code of these examples for patterns you can use:
- HTTP servers
- CLI tools
- Batch processors
- Stream processors

All examples are self-contained and can be adapted to your needs!
