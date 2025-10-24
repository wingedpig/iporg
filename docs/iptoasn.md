# IPtoASN Utility

A fast, offline AS-to-prefix lookup database built from iptoasn.com's free dataset.

## Overview

The `iptoasn` utility provides:
- **Complete ASN prefix database**: Downloaded from iptoasn.com (updated daily upstream)
- **Fast ASN lookups**: Query all prefixes for any ASN in <1ms
- **Prefix aggregation**: Optional CIDR collapse for more compact storage
- **Full iteration**: Walk all prefixes in deterministic order
- **Incremental updates**: ETag-based caching for efficient rebuilds

## Quick Start

### Build the Tools

```bash
make build
```

This creates:
- `./bin/iptoasn-build` - Database builder
- `./bin/iptoasn-query` - Query tool

### Download and Build Database

```bash
# Download data + build database (first run)
./bin/iptoasn-build all --db=./iptoasndb

# Subsequent updates (only re-downloads if changed)
./bin/iptoasn-build all --db=./iptoasndb
```

Build time: ~2-5 minutes (depends on dataset size)

### Query ASN Prefixes

```bash
# List all prefixes for AS2856
./bin/iptoasn-query asn 2856 --db=./iptoasndb

# List collapsed (aggregated) prefixes
./bin/iptoasn-query asn 2856 --collapsed --db=./iptoasndb

# List all ASNs in database
./bin/iptoasn-query list-asns --db=./iptoasndb
```

## Commands

### iptoasn-build

Build and maintain the iptoasn database.

**Subcommands:**
```
fetch   - Download iptoasn data if changed (ETag/Last-Modified)
build   - Parse and build database from cached data
all     - Fetch + build (recommended workflow)
stats   - Show database statistics
```

**Options:**
```
--db=<path>           Database path (default: ./iptoasndb)
--url=<url>           Source URL (default: https://iptoasn.com/data/ip2asn-v4.tsv.gz)
--cache-dir=<path>    Cache directory (default: ./cache/iptoasn)
--skip-download       Skip download, use cached file
--collapse            Collapse adjacent prefixes per ASN (default: true)
--workers=<n>         Concurrent workers (default: 4)
```

**Examples:**
```bash
# Full workflow (fetch + build)
./bin/iptoasn-build all --db=./iptoasndb

# Fetch only (useful for cron jobs)
./bin/iptoasn-build fetch --cache-dir=./cache

# Build from existing cache (fast rebuild)
./bin/iptoasn-build build --db=./iptoasndb

# Disable prefix collapse (larger DB, more granular data)
./bin/iptoasn-build all --db=./iptoasndb --collapse=false

# Show statistics
./bin/iptoasn-build stats --db=./iptoasndb
```

### iptoasn-query

Query the iptoasn database.

**Subcommands:**
```
asn <number>   - List all prefixes for an ASN
walk           - Iterate all prefixes in order
list-asns      - List all ASNs in database
```

**Options:**
```
--db=<path>         Database path (default: ./iptoasndb)
--collapsed         Show collapsed prefixes (for asn command)
--json              Output as JSON (default: true)
--limit=<n>         Limit number of results
--offset-key=<key>  Resume walk from this key
```

**Examples:**
```bash
# List all prefixes for AS2856
./bin/iptoasn-query asn 2856

# List collapsed prefixes (aggregated)
./bin/iptoasn-query asn 2856 --collapsed

# Human-readable output
./bin/iptoasn-query asn 2856 --json=false

# Walk first 100 prefixes
./bin/iptoasn-query walk --limit=100

# List all ASNs with counts
./bin/iptoasn-query list-asns --json=false
```

## Data Source

**Dataset:** https://iptoasn.com/data/ip2asn-v4.tsv.gz

This dataset provides:
- IP range → ASN mappings
- Country codes
- RIR (Regional Internet Registry) information
- Updated daily by upstream

**Format:**
```
<start_ip> \t <end_ip> \t <asn> \t <country> \t <registry>
```

**License:** Check iptoasn.com for current license terms

## Architecture

### Storage Design

**LevelDB with ordered keys:**

- **Global prefix list**: `P4:<start-ip-uint32>` → `CanonicalPrefix` (msgpack)
- **Per-ASN raw prefixes**: `A:<asn>:v4:<n>` → `CanonicalPrefix`
- **Per-ASN collapsed**: `Ac:<asn>:v4:<n>` → `CanonicalPrefix`
- **ASN index**: `AIDX:<asn>` → `ASNIndexEntry` (counts, last modified)
- **Metadata**: `meta:*` keys (source URL, built time, ETag)
- **Statistics**: `stats:totals` → global stats

### Build Pipeline

1. **Fetch**: HTTP GET with ETag/If-None-Match (incremental updates)
2. **Parse**: Stream-parse TSV, convert IP ranges to CIDRs
3. **Normalize**: Deduplicate exact duplicates
4. **Sort**: Order by start IP (deterministic)
5. **Collapse**: Per-ASN CIDR aggregation (optional, enabled by default)
6. **Write**: Batch write to LevelDB (global + per-ASN indexes)

### CIDR Collapse Algorithm

When `--collapse=true` (default):

1. Group prefixes by ASN
2. For each ASN:
   - Merge overlapping/adjacent ranges
   - Convert to optimal CIDR blocks
   - Store both raw and collapsed versions

**Example:**
```
Raw (4 prefixes):
  1.0.0.0/24
  1.0.1.0/24
  1.0.2.0/24
  1.0.3.0/24

Collapsed (1 prefix):
  1.0.0.0/22
```

## Performance

**Database size:**
- ~600k prefixes (as of 2025): ~50-80 MB
- Collapsed: ~30-40% reduction

**Build time:**
- First build: 2-5 minutes
- Incremental (with cache): 1-3 minutes
- No-change rebuild (ETag hit): <1 second

**Query performance:**
- Single ASN lookup: <1ms
- Walk 100k prefixes: ~100ms
- List all ASNs: ~10ms

## Use Cases

### 1. ASN Prefix Discovery

Find all IP ranges announced by an organization:

```bash
# Get all Cloudflare (AS13335) prefixes
./bin/iptoasn-query asn 13335 --collapsed
```

### 2. BGP Analysis

Export prefix data for analysis:

```bash
# Export Google's IPv4 space
./bin/iptoasn-query asn 15169 > google-prefixes.json
```

### 3. Network Planning

Identify potential conflicts or overlaps:

```bash
# Walk all prefixes, filter by country
./bin/iptoasn-query walk | jq 'select(.country == "US")'
```

### 4. Automation

Daily updates via cron:

```bash
# /etc/cron.daily/iptoasn-update
0 2 * * * /usr/local/bin/iptoasn-build all --db=/var/lib/iptoasn/db
```

## Integration with iporg

The `iptoasn` utility is complementary to `iporg`:

- **iporg**: Detailed organization info via RDAP + MaxMind (per ASN list)
- **iptoasn**: Complete global prefix→ASN mapping (all ASNs)

**Workflow:**
1. Use `iptoasn` to discover ASNs of interest
2. Build `iporg` database for those specific ASNs
3. Get detailed org/geo data from `iporg` lookups

## Troubleshooting

**"failed to read metadata (did you run fetch first?)"**
- Run `iptoasn-build fetch` or `iptoasn-build all` first
- Ensure `--cache-dir` is writable

**"ASN not found in database"**
- Check ASN number (must be numeric)
- Rebuild database to get latest data
- ASN may not have any announced prefixes

**Slow builds**
- Normal for first build (~2-5 min for 600k prefixes)
- Subsequent builds use cache (much faster)
- Use `--workers=8` for faster parsing (if CPU available)

**Database too large**
- Use `--collapse=true` (default) for smaller DB
- Consider filtering specific RIRs or countries if needed

## Advanced Usage

### Custom Source URL

```bash
# Use alternative mirror or local file
./bin/iptoasn-build all \
  --url=https://mirror.example.com/ip2asn-v4.tsv.gz \
  --db=./iptoasndb
```

### Batch Processing

```bash
# Export all ASNs to separate files
./bin/iptoasn-query list-asns --json | jq -r '.asns[]' | while read asn; do
  ./bin/iptoasn-query asn $asn > prefixes/as${asn}.json
done
```

### Integration with jq

```bash
# Find ASNs with >1000 prefixes
./bin/iptoasn-query list-asns | jq '.asns[] | select(.count > 1000)'

# Get total IP count for an ASN
./bin/iptoasn-query asn 15169 | jq '[.prefixes[].cidr | split("/")[1] | tonumber | pow(2; 32 - .)] | add'
```

## Future Enhancements

- IPv6 support (currently IPv4 only)
- Prefix→ASN LPM (longest prefix match) lookup
- Country/RIR filtering during build
- CSV/JSONL export formats
- SQLite backend option for SQL queries

## References

- iptoasn.com dataset: https://iptoasn.com
- BGP prefix data: https://www.cidr-report.org
- LevelDB: https://github.com/google/leveldb
- CIDR aggregation: https://tools.ietf.org/html/rfc4632
