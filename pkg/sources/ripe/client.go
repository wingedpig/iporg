package ripe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	workers2 "github.com/wingedpig/iporg/pkg/util/workers"
)

const (
	defaultBaseURL = "https://stat.ripe.net"
	defaultTimeout = 30 * time.Second
)

// Client is a RIPEstat API client
type Client struct {
	baseURL    string
	httpClient *http.Client
	limiter    *rate.Limiter
	userAgent  string
}

// NewClient creates a new RIPEstat client
func NewClient(baseURL, userAgent string, rateLimit float64) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	var limiter *rate.Limiter
	if rateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(rateLimit), int(rateLimit)+1)
	}

	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		limiter:   limiter,
		userAgent: userAgent,
	}
}

// AnnouncedPrefixes fetches announced prefixes for an ASN
func (c *Client) AnnouncedPrefixes(ctx context.Context, asn int) ([]string, error) {
	url := fmt.Sprintf("%s/data/announced-prefixes/data.json?resource=AS%d", c.baseURL, asn)

	// Rate limit
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
	}

	// Retry with backoff
	var result announcedPrefixesResponse
	err := workers2.Retry(ctx, workers2.DefaultRetryConfig(), func() error {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		if c.userAgent != "" {
			req.Header.Set("User-Agent", c.userAgent)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("rate limited by server")
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		if err := json.Unmarshal(body, &result); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Extract prefixes
	var prefixes []string
	if result.Data != nil && result.Data.Prefixes != nil {
		for _, p := range result.Data.Prefixes {
			if p.Prefix != "" {
				prefixes = append(prefixes, p.Prefix)
			}
		}
	}

	log.Printf("INFO: Fetched %d prefixes for AS%d", len(prefixes), asn)
	return prefixes, nil
}

// FetchAnnouncedPrefixesForASNs fetches prefixes for multiple ASNs concurrently
func (c *Client) FetchAnnouncedPrefixesForASNs(ctx context.Context, asns []int, workers int) (map[int][]string, error) {
	if workers <= 0 {
		workers = 5
	}

	pool := workers2.NewPool(ctx, workers2.Config{
		Workers:   workers,
		RateLimit: 0, // Rate limiting handled by client
	})

	type result struct {
		asn      int
		prefixes []string
		err      error
	}

	results := make([]result, len(asns))

	for i, asn := range asns {
		idx := i
		currentASN := asn
		pool.Submit(idx, func(ctx context.Context) error {
			prefixes, err := c.AnnouncedPrefixes(ctx, currentASN)
			results[idx] = result{
				asn:      currentASN,
				prefixes: prefixes,
				err:      err,
			}
			return nil // Don't fail the pool on individual errors
		})
	}

	pool.Wait()

	// Collect results
	asnPrefixes := make(map[int][]string)
	var errors []error

	for _, r := range results {
		if r.err != nil {
			errors = append(errors, fmt.Errorf("AS%d: %w", r.asn, r.err))
			log.Printf("ERROR: Failed to fetch prefixes for AS%d: %v", r.asn, r.err)
		} else {
			asnPrefixes[r.asn] = r.prefixes
		}
	}

	if len(errors) > 0 {
		log.Printf("WARN: %d ASNs failed to fetch", len(errors))
	}

	return asnPrefixes, nil
}

// Response types for RIPEstat API

type announcedPrefixesResponse struct {
	Data *struct {
		Prefixes []struct {
			Prefix    string `json:"prefix"`
			Timelines []struct {
				StartTime string `json:"starttime"`
				EndTime   string `json:"endtime"`
			} `json:"timelines"`
		} `json:"prefixes"`
		Resource   string `json:"resource"`
		QueryTime  string `json:"query_time"`
		LatestTime string `json:"latest_time"`
	} `json:"data"`
	Messages   [][]interface{} `json:"messages"`
	Version    string          `json:"version"`
	Status     string          `json:"status"`
	StatusCode int             `json:"status_code"`
	Time       string          `json:"time"`
}
