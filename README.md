# iporg - IP Organization Lookup Tools

A fast, offline IP organization lookup database built from free data sources. Query any IP address to get its ASN, organization name, location, and more.

## Features

- **Offline lookups**: O(log N) range queries using LevelDB
- **Accurate organization data**: Combines RDAP (for exact org assignments) with MaxMind (for ASN and geo)
- **IPv4-first design**: IPv4-only mode enabled by default (reduces RDAP rate limiting)
- **Flexible accuracy modes**:
  - **Mode A (simple)**: One record per announced prefix (fast build, smaller DB)
  - **Mode B (accurate)**: Split large prefixes by MaxMind city blocks for better geo precision
- **Multiple data sources**: RIPEstat (announced prefixes), RDAP (org info), MaxMind GeoLite2 (ASN + geo)
- **IPv4 & IPv6 support**: Full support for both IP versions (IPv6 can be enabled with `--ipv4-only=false`)
- **Built-in caching**: RDAP responses cached to speed up rebuilds
- **Tools included**:
  - `iporg-build`: Build and maintain the database
  - `iporg-lookup`: Single IP lookup
  - `iporg-bulk`: Bulk IP processing from files/stdin

## Quick Start

### 1. Prerequisites

Download MaxMind GeoLite2 databases (requires free account at https://www.maxmind.com):

```bash
# Download these files:
# - GeoLite2-ASN.mmdb
# - GeoLite2-City.mmdb
```

### 2. Build the tools

```bash
make build
```

This creates binaries in `./bin/`:
- `iporg-build`
- `iporg-lookup`
- `iporg-bulk`

### 3. Create an ASN list

```bash
cat > asns.txt <<EOF
# British Telecom / EE
2856
# Lumen (formerly Level 3)
3356
# Amazon
16509
# Google
15169
# Cloudflare
13335
EOF
```

Or use the example file:
```bash
cp examples/asns.txt .
```

### 4. Build the database

**Mode A (simple, faster build, IPv4 only):**
```bash
./bin/iporg-build build \
  --asn-file=asns.txt \
  --mmdb-asn=GeoLite2-ASN.mmdb \
  --mmdb-city=GeoLite2-City.mmdb \
  --db=./data/iporgdb
```

Note: IPv4-only mode is enabled by default. To include IPv6 prefixes, add `--ipv4-only=false`

**Mode B (better geo accuracy):**
```bash
./bin/iporg-build build \
  --asn-file=asns.txt \
  --mmdb-asn=GeoLite2-ASN.mmdb \
  --mmdb-city=GeoLite2-City.mmdb \
  --db=./data/iporgdb \
  --split-by-maxmind \
  --min-prefix-v4=24
```

Build time depends on:
- Number of ASNs (hundreds of thousands of prefixes for large carriers)
- RDAP rate limits (default 5 req/s)
- Caching (subsequent builds much faster)

### 5. Lookup IPs

**Single lookup:**
```bash
./bin/iporg-lookup --db=./data/iporgdb 8.8.8.8
```

Output:
```json
{
  "ip": "8.8.8.8",
  "asn": 15169,
  "asn_name": "GOOGLE",
  "org_name": "Google LLC",
  "rir": "ARIN",
  "country": "US",
  "region": "California",
  "city": "Mountain View",
  "lat": 37.405,
  "lon": -122.077,
  "prefix": "8.8.8.0/24",
  "source_role": "customer"
}
```

**Bulk lookup:**
```bash
# From file
cat ips.txt | ./bin/iporg-bulk --db=./data/iporgdb > results.jsonl

# With input/output files
./bin/iporg-bulk --db=./data/iporgdb --input=ips.txt --output=results.jsonl
```

## Usage

### iporg-build

```
Usage: iporg-build <command> [options]

Commands:
  build    Build or update the database
  verify   Verify database consistency
  stats    Show database statistics

Build options:
  --asn-file string              Path to ASN list file (required)
  --mmdb-asn string              Path to GeoLite2-ASN.mmdb (required)
  --mmdb-city string             Path to GeoLite2-City.mmdb (required)
  --db string                    Path to database (default: ./iporgdb)
  --workers int                  Concurrent workers (default: 16)
  --cache-ttl duration           RDAP cache TTL (default: 168h)
  --ipv4-only                    Skip IPv6 prefixes (default: true)
  --split-by-maxmind             Enable Mode B
  --min-prefix-v4 int            Min IPv4 prefix len for Mode B (default: 20)
  --min-prefix-v6 int            Min IPv6 prefix len for Mode B (default: 32)
  --rdap-rate-limit float        RDAP req/s (default: 5.0)
```

**Examples:**

```bash
# Build database for specific carriers
./bin/iporg-build build --asn-file=asns.txt \
  --mmdb-asn=GeoLite2-ASN.mmdb --mmdb-city=GeoLite2-City.mmdb

# Verify database integrity
./bin/iporg-build verify --db=./data/iporgdb

# Show statistics
./bin/iporg-build stats --db=./data/iporgdb --verbose
```

### iporg-lookup

```
Usage: iporg-lookup [options] <ip-address>

Options:
  --db string       Path to database (default: ./iporgdb)
  --json            Output JSON (default: true)
  --version         Show version
```

**Examples:**

```bash
# Lookup IPv4
./bin/iporg-lookup 31.90.1.1

# Lookup IPv6
./bin/iporg-lookup 2a02:2770::21a:4aff:fef8:a207

# Human-readable output
./bin/iporg-lookup --json=false 8.8.8.8
```

### iporg-bulk

```
Usage: iporg-bulk [options]

Options:
  --db string           Path to database (default: ./iporgdb)
  --input string        Input file (default: stdin)
  --output string       Output file (default: stdout)
  --workers int         Concurrent workers (default: 10)
```

**Examples:**

```bash
# Process IPs from stdin
cat ips.txt | ./bin/iporg-bulk > results.jsonl

# Process from file
./bin/iporg-bulk --input=ips.txt --output=results.jsonl

# High performance with more workers
./bin/iporg-bulk --workers=50 --input=million_ips.txt --output=results.jsonl
```

## Architecture

### Database Design

**LevelDB with range-based keys:**
- **Keys**: `R4:<4-byte IP>` (IPv4) or `R6:<16-byte IP>` (IPv6)
- **Values**: msgpack-encoded records (ASN, org, geo, etc.)
- **Lookup**: O(log N) via iterator seek + prev check

**Metadata keys:**
- `meta:schema` - Schema version
- `meta:built_at` - Build timestamp
- `meta:builder_version` - Builder version
- `cache:rdap:<prefix>` - Cached RDAP responses

### Data Sources & Truth Order

1. **Organization**: RDAP (prefer `customer` > `registrant` > fallback to MaxMind ASN org)
2. **ASN**: MaxMind GeoLite2-ASN
3. **Location**: MaxMind GeoLite2-City
4. **Prefixes**: RIPEstat announced-prefixes API

### Accuracy Modes

**Mode A (default):**
- One record per announced prefix
- Geo from representative IP (first in range)
- Fast build, smaller database
- Good for most use cases

**Mode B (`--split-by-maxmind`):**
- Splits large prefixes by MaxMind city blocks
- More records, larger database
- Better geo accuracy for large allocations
- Use for applications requiring precise location

## Data Sources

### MaxMind GeoLite2

**License**: Creative Commons Attribution-ShareAlike 4.0 International License

Download from: https://dev.maxmind.com/geoip/geolite2-free-geolocation-data

You must comply with MaxMind's license terms:
- Attribute MaxMind in your application
- Share any improvements under the same license
- Free for internal use; check licensing for public services

**Required databases:**
- GeoLite2-ASN.mmdb (for ASN number and organization name)
- GeoLite2-City.mmdb (for city-level geolocation)

### RIPEstat

**API**: https://stat.ripe.net
**Rate limit**: Self-imposed 10 req/s (be polite)
**Usage**: Fetch announced prefixes per ASN

Free, no API key required. Used to discover which IP ranges are announced by each ASN.

### RDAP

**Protocol**: RFC 7480-7485
**Rate limit**: Self-imposed 5 req/s (configurable)
**Usage**: Fetch organization assignments (customer/registrant info)

Free, no API key. RDAP is the modern replacement for WHOIS. Regional Internet Registries (RIRs) provide RDAP endpoints:
- ARIN (North America): https://rdap.arin.net
- RIPE (Europe, Middle East): https://rdap.db.ripe.net
- APNIC (Asia-Pacific): https://rdap.apnic.net
- LACNIC (Latin America): https://rdap.lacnic.net
- AFRINIC (Africa): https://rdap.afrinic.net

## Performance

**Database size:**
- Mode A: ~10-50 MB per 100k prefixes
- Mode B: 2-5x larger (depends on split granularity)

**Build time (estimated):**
- 10 ASNs: 5-10 minutes (first build)
- 100 ASNs: 1-3 hours (first build)
- Subsequent builds: 10-30 minutes (with caching)

**Lookup performance:**
- Single lookup: <1ms
- Bulk (10k IPs): <1 second with 50 workers

## Development

```bash
# Build
make build

# Run tests
make test

# Test with coverage
make test-coverage

# Format code
make fmt

# Clean
make clean
```

## Troubleshooting

**"Rate limited by RDAP server"**
- Use `--ipv4-only` (default) to skip IPv6 prefixes and reduce RDAP queries
- Lower `--rdap-rate-limit` (try 2.0 or 3.0)
- Wait and retry - RDAP cache will be used
- IPv6 RDAP queries are often slower and more likely to be rate-limited

**"Failed to open MaxMind database"**
- Ensure paths are correct
- Download GeoLite2-ASN and GeoLite2-City from MaxMind

**"No organization name found"**
- Some prefixes lack RDAP data
- Fallback to MaxMind ASN org is automatic
- Check logs for specific issues

**Overlapping ranges**
- Run `iporg-build verify` to check
- More specific prefixes (longer masks) are preferred
- Should not occur with well-formed announced prefixes

## License

MIT License - see LICENSE file for details

**Data sources have their own licenses:**
- MaxMind GeoLite2: CC BY-SA 4.0
- RIPEstat: RIPE NCC Terms of Service
- RDAP: Free use (per RIR policies)

When using this tool, ensure you comply with all applicable data source licenses, especially MaxMind's attribution requirements.

## Using as a Library

You can import and use `iporgdb` in your own Go projects. See [examples/library-usage/](examples/library-usage/) for complete examples including:

- Simple IP lookups
- JSON output
- Bulk/concurrent processing
- HTTP API server
- Filtering by country

**Quick example:**

```go
import (
    "github.com/yourusername/iporg/pkg/iporgdb"
    "github.com/yourusername/iporg/pkg/model"
)

db, _ := iporgdb.Open("/var/groupsio/data/iporgdb")
defer db.Close()

rec, err := db.LookupString("86.150.233.24")
if err == model.ErrNotFound {
    // IP not found
}
// Use rec.OrgName, rec.ASN, rec.Country, etc.
```

See the [library usage examples](examples/library-usage/README.md) for more details.

## Contributing

Contributions welcome! Please:
1. Fork the repository
2. Create a feature branch
3. Add tests for new functionality
4. Submit a pull request

## Acknowledgments

- MaxMind for GeoLite2 databases
- RIPE NCC for RIPEstat API
- Regional Internet Registries for RDAP services
