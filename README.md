# iporg - IP Organization Lookup Tools

A fast, offline IP organization lookup database built from free data sources. Query any IP address to get its ASN, organization name, location, and more.

## Recent Updates

### Bug Fixes (October 2024)

**iptoasn: Multi-CIDR Range Bug** - Fixed critical parser bug where IP ranges spanning multiple CIDRs only stored the first CIDR, causing incomplete database coverage. Example: `204.110.219.0 - 204.110.221.255` previously only stored `204.110.219.0/24`, dropping `204.110.220.0/23`. Now correctly expands to all covering CIDRs. **Action required:** Rebuild iptoasn databases with `iptoasn-build all`.

**RIPE Bulk: Placeholder Filtering** - Fixed issue where RIPE bulk database contained placeholder entries like "NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK" for non-RIPE address space (ARIN, APNIC, etc.). These placeholders are now filtered out, allowing proper fallback to RDAP for accurate organization names. **Action required:** Rebuild iporg databases with `iporg-build` to get correct organization names for non-RIPE IPs.

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
  --iptoasn-db string            Use iptoasn DB for prefixes (optional)
  --ripe-bulk-db string          Use RIPE bulk DB for RIPE region (optional)
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

# Build with RIPE bulk (faster, no RDAP rate limits for RIPE region)
./bin/iporg-build build --asn-file=asns.txt \
  --mmdb-asn=GeoLite2-ASN.mmdb --mmdb-city=GeoLite2-City.mmdb \
  --ripe-bulk-db=./data/ripe-bulk.ldb

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

**"IP not found but should be in database"**
- If using iptoasn database: Rebuild with `iptoasn-build all` to fix multi-CIDR range bug
- Check that the ASN is in your asns.txt file
- Verify the prefix is announced (use debug mode: `iporg-build debug --ip=X.X.X.X --asn=N`)
- iptoasn.com data may be incomplete for some ASNs (shows ~15k missing prefixes for AS16509)

**"Wrong organization name (shows NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK)"**
- Rebuild iporg database to apply RIPE placeholder filtering
- This placeholder is now automatically filtered out with fallback to RDAP

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

## IPtoASN Utility

The `iptoasn` utility provides a complete global ASN→prefix database using data from iptoasn.com:

```bash
# Build database (downloads latest data)
./bin/iptoasn-build all --db=./iptoasndb

# Query all prefixes for an ASN
./bin/iptoasn-query asn 2856

# List all ASNs in database
./bin/iptoasn-query list-asns
```

**Key features:**
- Complete global prefix database (all ASNs, 600k+ prefixes)
- Fast ASN lookups (<1ms)
- Optional CIDR collapse/aggregation
- Incremental updates with ETag caching
- Deterministic prefix iteration
- **Fixed:** Multi-CIDR ranges now properly expand (e.g., `204.110.219.0-204.110.221.255` → `204.110.219.0/24` + `204.110.220.0/23`)

See [docs/iptoasn.md](docs/iptoasn.md) for full documentation.

**iptoasn vs iporg:**
- **iptoasn**: Global prefix→ASN mapping for all ASNs
- **iporg**: Detailed organization info (RDAP + MaxMind) for specific ASNs

**Integration:** Use iptoasn as a prefix source for iporg-build (faster, no API rate limits):

```bash
# First, build iptoasn database
./bin/iptoasn-build all --db=./iptoasndb

# Then use it with iporg-build
./bin/iporg-build build \
  --asn-file=asns.txt \
  --mmdb-asn=GeoLite2-ASN.mmdb \
  --mmdb-city=GeoLite2-City.mmdb \
  --db=./data/iporgdb \
  --iptoasn-db=./iptoasndb
```

This replaces RIPEstat API calls with fast local lookups from the iptoasn database.

## RIPE Bulk Data (Stage B)

The `ripe-bulk` tools provide **authoritative organization data** for the RIPE region by parsing RIPE's official database dumps. This eliminates RDAP calls for RIPE-region IPs and provides more complete metadata including RIPE status labels.

### Features

- **Direct RIPE database parsing**: Fetches and parses official RIPE inetnum/organisation dumps
- **IPv4 support**: Full coverage of RIPE IPv4 allocations
- **Most-specific lookup**: Finds the smallest inetnum that fully covers a query prefix
- **Efficient indexing**: LevelDB-based sorted range index with O(log N) lookups
- **Conditional fetching**: HTTP If-Modified-Since support to skip unchanged dumps
- **Metadata-rich**: Includes org-name, status (ASSIGNED-PA, ALLOCATED-PA, etc.), country, netname

### Quick Start

```bash
# Build RIPE bulk database (downloads ~200MB dumps)
./bin/ripe-bulk-build --db=./data/ripe-bulk.ldb --cache=./cache/ripe

# Query an IP or prefix
./bin/ripe-bulk-query --db=./data/ripe-bulk.ldb 31.90.1.1

# JSON output
./bin/ripe-bulk-query --db=./data/ripe-bulk.ldb --json 31.90.0.0/15
```

### Usage

#### ripe-bulk-build

Fetches RIPE dumps, parses inetnum/organisation objects, and builds the LevelDB index.

```
Usage: ripe-bulk-build [options]

Options:
  --db string         Path to output LevelDB database (default: data/ripe-bulk.ldb)
  --cache string      Cache directory for RIPE dumps (default: cache/ripe)
  --url string        RIPE FTP base URL (default: https://ftp.ripe.net/ripe/dbase/split)
  --skip-fetch        Skip fetching, use cached files only
  --version           Show version
```

**Examples:**

```bash
# Initial build (downloads dumps)
./bin/ripe-bulk-build --db=./data/ripe-bulk.ldb

# Incremental rebuild (uses cached dumps if unmodified)
./bin/ripe-bulk-build --db=./data/ripe-bulk.ldb --cache=./cache/ripe

# Use cached files without fetching
./bin/ripe-bulk-build --skip-fetch --cache=./cache/ripe
```

**Build process:**

1. Fetches `ripe.db.inetnum.gz` (~200MB) and `ripe.db.organisation.gz` (~6MB)
2. Parses RPSL objects (handles continuation lines, comments)
3. Sorts inetnums by (Start ascending, End descending) for efficient lookups
4. Builds LevelDB index with msgpack-encoded values
5. Stores metadata (build time, serial, counts)

**Build time:** ~2-5 minutes (depending on network/disk speed)

#### ripe-bulk-query

Queries the RIPE bulk database for an IP address or CIDR prefix.

```
Usage: ripe-bulk-query [options] <ip-or-prefix>

Options:
  --db string    Path to RIPE bulk database (default: data/ripe-bulk.ldb)
  --json         Output in JSON format
  --version      Show version
```

**Examples:**

```bash
# Lookup single IP
./bin/ripe-bulk-query 31.90.1.1

# Output:
# Range:       31.90.0.0 - 31.91.255.255
# Org Name:    EE Limited
# Org ID:      ORG-EL122-RIPE
# Org Type:    LIR
# Status:      ALLOCATED-PA
# Country:     GB
# Netname:     EE-LEGACY-RANGE

# Lookup CIDR prefix (finds most-specific covering inetnum)
./bin/ripe-bulk-query 31.90.0.0/24

# JSON output
./bin/ripe-bulk-query --json 31.90.1.1 | jq .
```

### Data Model

**Inetnum (IPv4 range):**
- `inetnum: 31.90.0.0 - 31.91.255.255` → Start/End IPs
- `org: ORG-EA123-RIPE` → Links to organisation object
- `status: ASSIGNED-PA` → RIPE status label
- `country: GB` → Country code (informational, not authoritative)
- `netname: EE-BB` → Network name

**Organisation:**
- `organisation: ORG-EA123-RIPE` → Primary key
- `org-name: EE Limited` → Human-readable name
- `org-type: LIR` → RIR, LIR, OTHER, etc.

**Lookup algorithm:**
1. Convert query prefix to inclusive [start, end] uint32 range
2. Seek to start IP in sorted index
3. Scan backward to collect all inetnums where Start ≤ query_start
4. Filter to inetnums that fully cover [query_start, query_end]
5. Return the **most specific** (smallest) covering inetnum
6. Resolve org-name via organisation table

### Integration with iporg-build

The RIPE bulk database can be used with iporg-build to replace RDAP lookups for RIPE region IPs:

```bash
# First, build the RIPE bulk database
./bin/ripe-bulk-build --db=./data/ripe-bulk.ldb --cache=./cache/ripe

# Then use it with iporg-build
./bin/iporg-build build \
  --asn-file=asns.txt \
  --mmdb-asn=GeoLite2-ASN.mmdb \
  --mmdb-city=GeoLite2-City.mmdb \
  --db=./data/iporgdb \
  --ripe-bulk-db=./data/ripe-bulk.ldb
```

**Benefits**:
- **Faster builds**: No RDAP rate limiting for RIPE region (~400k inetnums)
- **More accurate org names**: Direct from RIPE DB (authoritative `org-name`)
- **RIPE metadata**: Includes status labels (ASSIGNED-PA, ALLOCATED-PA, etc.)
- **Zero API calls**: All RIPE lookups are local

**How it works**:
1. For each prefix/IP, iporg-build first checks RIPE bulk database
2. If found (IPv4 RIPE region), uses RIPE org name & status
3. **Placeholder filtering**: Entries like "NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK" are ignored (non-RIPE space)
4. If not found or filtered, falls back to standard RDAP lookup
5. MaxMind still provides ASN and geolocation data

**Filtered placeholders**:
- `NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK` - Address space managed by other RIRs (ARIN, APNIC, etc.)
- `UNALLOCATED` - Unallocated ranges
- `RESERVED` - Reserved ranges

**Statistics**:
Build summary will show `RIPE bulk hits: N` to track usage

### Database Schema

**LevelDB keys:**
- `R4:<4-byte IP>` → msgpack(Inetnum) - IPv4 ranges sorted by start IP
- `ORG:<org-id>` → msgpack(Organisation) - Organisation metadata
- `META:build` → msgpack(Metadata) - Build metadata

**Metadata fields:**
- SchemaVersion (int) - Database schema version
- BuildTime (time.Time) - When database was built
- InetnumCount (int64) - Number of indexed inetnums
- OrgCount (int64) - Number of indexed organisations
- SourceURL (string) - RIPE FTP base URL

### Performance

**Database size:** ~60-80MB (compressed with Snappy)

**Lookup speed:**
- Single IP: <100µs (median)
- Prefix: <200µs (median)
- Memory usage: ~10-20MB (LevelDB block cache)

**Build performance:**
- Parse inetnums: ~10-15 seconds (~400k objects)
- Parse organisations: ~1-2 seconds (~30k objects)
- Sort & index: ~30-60 seconds
- Total: ~2-5 minutes (including download)

### Data Source

**RIPE NCC Database:**
- URL: https://ftp.ripe.net/ripe/dbase/split/
- Files: `ripe.db.inetnum.gz`, `ripe.db.organisation.gz`
- Format: RPSL (Routing Policy Specification Language)
- Update frequency: Daily snapshots
- License: RIPE Database Terms and Conditions

**Personal data:** RIPE sanitizes ("dummifies") personal data in public dumps, but inetnum/organisation content is included.

### Limitations (IPv4 only)

- **IPv6 not supported** in current implementation (Stage B)
- RIPE also has `inet6num` objects; these are ignored
- For IPv6, continue using RDAP or wait for Stage B.5 (IPv6 support)

### Testing

```bash
# Run unit tests
go test ./pkg/ripebulk/

# Test with known ranges
./bin/ripe-bulk-query 31.90.1.1     # EE Limited (UK)
./bin/ripe-bulk-query 193.0.0.1     # RIPE NCC (NL)
./bin/ripe-bulk-query 85.115.0.1    # British Telecom (UK)
```

### Troubleshooting

**"Failed to fetch RIPE dump"**
- Check network connectivity to ftp.ripe.net
- Try using `--skip-fetch` with cached files
- Verify cache directory permissions

**"No matching inetnum found"**
- IP may not be in RIPE region (try ARIN/APNIC/etc.)
- IP may be in a reserved/unallocated range
- Check that database was built successfully

**"NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK" organization name**
- This is a placeholder for non-RIPE address space (ARIN, APNIC, etc.)
- iporg-build now filters these out automatically (requires rebuild)
- Rebuild your database to get correct organization names via RDAP fallback

**"Parse error"**
- RIPE dump format may have changed (report issue)
- File may be corrupted (delete cache and re-fetch)

### RIPE vs RDAP

| Feature | RIPE Bulk | RDAP |
|---------|-----------|------|
| Coverage | RIPE region only | All RIRs |
| Rate limits | None (local) | 5-10 req/s typical |
| Org names | Authoritative (org-name) | Customer/registrant |
| Metadata | Status, netname, country | Less structured |
| Build time | 2-5 min (one-time) | Variable (per-prefix) |
| Update frequency | Daily (manual rebuild) | Real-time (per query) |
| IPv6 | Not yet (Stage B.5) | Full support |

**Recommendation:** Use RIPE bulk for RIPE region IPv4, RDAP for other RIRs and IPv6.

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
