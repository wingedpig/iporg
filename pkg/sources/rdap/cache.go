package rdap

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"time"

	"github.com/wingedpig/iporg/pkg/iporgdb"
	"github.com/wingedpig/iporg/pkg/model"
)

// CachedClient wraps an RDAP client with caching
type CachedClient struct {
	client   *Client
	db       *iporgdb.DB
	cacheTTL time.Duration
}

// NewCachedClient creates a new cached RDAP client
func NewCachedClient(client *Client, db *iporgdb.DB, cacheTTL time.Duration) *CachedClient {
	return &CachedClient{
		client:   client,
		db:       db,
		cacheTTL: cacheTTL,
	}
}

// CacheEntry represents a cached RDAP result
type CacheEntry struct {
	Org       *model.RDAPOrg
	FetchedAt time.Time
}

// OrgForPrefix retrieves organization info for a prefix, using cache if available
func (c *CachedClient) OrgForPrefix(ctx context.Context, prefix string) (*model.RDAPOrg, error) {
	// Normalize prefix for cache key
	parsedPrefix, err := netip.ParsePrefix(prefix)
	if err != nil {
		return nil, fmt.Errorf("invalid prefix: %w", err)
	}
	normalizedPrefix := parsedPrefix.Masked().String()

	// Try cache first
	var cached CacheEntry
	if err := c.db.GetCache("rdap", normalizedPrefix, &cached); err == nil && cached.Org != nil {
		// Check if cache is still valid
		if time.Since(cached.FetchedAt) < c.cacheTTL {
			log.Printf("INFO: Cache hit for prefix %s", normalizedPrefix)
			return cached.Org, nil
		}
		log.Printf("INFO: Cache expired for prefix %s", normalizedPrefix)
	}

	// Cache miss or expired - fetch from RDAP
	log.Printf("INFO: Fetching RDAP data for prefix %s", normalizedPrefix)
	org, err := c.client.OrgForPrefix(ctx, normalizedPrefix)
	if err != nil {
		// If it's a rate limit error, try to use expired cache
		if err == model.ErrRateLimited && cached.Org != nil {
			log.Printf("WARN: Rate limited, using expired cache for %s", normalizedPrefix)
			return cached.Org, nil
		}
		return nil, err
	}

	// Store in cache
	entry := CacheEntry{
		Org:       org,
		FetchedAt: time.Now(),
	}
	if err := c.db.SetCache("rdap", normalizedPrefix, entry); err != nil {
		log.Printf("WARN: Failed to cache RDAP result: %v", err)
	}

	return org, nil
}

// OrgForIP retrieves organization info for an IP, using cache if available
func (c *CachedClient) OrgForIP(ctx context.Context, ip netip.Addr) (*model.RDAPOrg, error) {
	// For caching, we use the IP as the key
	// In practice, you might want to cache by prefix instead
	ipStr := ip.String()

	var cached CacheEntry
	if err := c.db.GetCache("rdap", "ip:"+ipStr, &cached); err == nil && cached.Org != nil {
		if time.Since(cached.FetchedAt) < c.cacheTTL {
			log.Printf("INFO: Cache hit for IP %s", ipStr)
			return cached.Org, nil
		}
	}

	log.Printf("INFO: Fetching RDAP data for IP %s", ipStr)
	org, err := c.client.OrgForIP(ctx, ip)
	if err != nil {
		if err == model.ErrRateLimited && cached.Org != nil {
			log.Printf("WARN: Rate limited, using expired cache for %s", ipStr)
			return cached.Org, nil
		}
		return nil, err
	}

	entry := CacheEntry{
		Org:       org,
		FetchedAt: time.Now(),
	}
	if err := c.db.SetCache("rdap", "ip:"+ipStr, entry); err != nil {
		log.Printf("WARN: Failed to cache RDAP result: %v", err)
	}

	return org, nil
}

// ClearExpiredCache removes expired cache entries
func (c *CachedClient) ClearExpiredCache(ctx context.Context) error {
	// This is a simplified implementation
	// In a real implementation, you'd iterate through cache entries
	// and delete expired ones
	log.Printf("INFO: Cache cleanup not yet implemented")
	return nil
}

// Stats returns cache statistics
func (c *CachedClient) Stats() (hits, misses int64, err error) {
	// This would require tracking cache hits/misses
	// For now, return zeros
	return 0, 0, nil
}
