package rdap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"iporg/pkg/model"
	"iporg/pkg/util/workers"
)

const (
	defaultBootstrapURL = "https://rdap.db.ripe.net"
	defaultTimeout      = 30 * time.Second
)

// Client is an RDAP client
type Client struct {
	bootstrapURL string
	httpClient   *http.Client
	limiter      *rate.Limiter
	userAgent    string
}

// NewClient creates a new RDAP client
func NewClient(bootstrapURL, userAgent string, rateLimit float64) *Client {
	if bootstrapURL == "" {
		bootstrapURL = defaultBootstrapURL
	}

	var limiter *rate.Limiter
	if rateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(rateLimit), int(rateLimit)+1)
	}

	return &Client{
		bootstrapURL: bootstrapURL,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Follow redirects automatically
				return nil
			},
		},
		limiter:   limiter,
		userAgent: userAgent,
	}
}

// QueryIP performs an RDAP query for an IP address
func (c *Client) QueryIP(ctx context.Context, ip netip.Addr) (*Response, error) {
	// Convert to string
	ipStr := ip.String()

	// Build URL - RDAP uses /ip/{address} path
	url := fmt.Sprintf("%s/ip/%s", c.bootstrapURL, ipStr)

	// Rate limit
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
	}

	// Retry with backoff
	var response Response
	err := workers.Retry(ctx, workers.DefaultRetryConfig(), func() error {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		if c.userAgent != "" {
			req.Header.Set("User-Agent", c.userAgent)
		}
		req.Header.Set("Accept", "application/rdap+json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			log.Printf("WARN: Rate limited by RDAP server for %s", ipStr)
			return model.ErrRateLimited
		}

		if resp.StatusCode == http.StatusNotFound {
			// Not found is not an error for RDAP - it just means no data
			return nil
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		if err := json.Unmarshal(body, &response); err != nil {
			return fmt.Errorf("failed to parse RDAP response: %w", err)
		}

		return nil
	})

	if err != nil {
		if err == model.ErrRateLimited {
			return nil, err
		}
		return nil, fmt.Errorf("RDAP query failed for %s: %w", ipStr, err)
	}

	return &response, nil
}

// QueryPrefix performs an RDAP query for an IP prefix
func (c *Client) QueryPrefix(ctx context.Context, prefix string) (*Response, error) {
	// Normalize the prefix
	parsedPrefix, err := netip.ParsePrefix(prefix)
	if err != nil {
		return nil, fmt.Errorf("invalid prefix: %w", err)
	}

	// Query using a representative IP (first IP in the range)
	return c.QueryIP(ctx, parsedPrefix.Addr())
}

// OrgForIP extracts organization information from an RDAP response
func (c *Client) OrgForIP(ctx context.Context, ip netip.Addr) (*model.RDAPOrg, error) {
	response, err := c.QueryIP(ctx, ip)
	if err != nil {
		return nil, err
	}

	if response == nil {
		return nil, fmt.Errorf("no RDAP data for IP")
	}

	return ParseOrg(response)
}

// OrgForPrefix extracts organization information for a prefix
func (c *Client) OrgForPrefix(ctx context.Context, prefix string) (*model.RDAPOrg, error) {
	response, err := c.QueryPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}

	if response == nil {
		return nil, fmt.Errorf("no RDAP data for prefix")
	}

	return ParseOrg(response)
}

// Response represents an RDAP IP network response
type Response struct {
	ObjectClassName string   `json:"objectClassName"`
	Handle          string   `json:"handle"`
	StartAddress    string   `json:"startAddress"`
	EndAddress      string   `json:"endAddress"`
	IPVersion       string   `json:"ipVersion"`
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Country         string   `json:"country"`
	ParentHandle    string   `json:"parentHandle"`
	Status          []string `json:"status"`
	Entities        []Entity `json:"entities"`
	Remarks         []Remark `json:"remarks"`
	Links           []Link   `json:"links"`
	Events          []Event  `json:"events"`
	Port43          string   `json:"port43"`
}

// Entity represents an RDAP entity
type Entity struct {
	ObjectClassName string   `json:"objectClassName"`
	Handle          string   `json:"handle"`
	Roles           []string `json:"roles"`
	VCardArray      []interface{} `json:"vcardArray"`
	Entities        []Entity `json:"entities"`
	Remarks         []Remark `json:"remarks"`
	Links           []Link   `json:"links"`
	Events          []Event  `json:"events"`
	Status          []string `json:"status"`
	Port43          string   `json:"port43"`
}

// Remark represents an RDAP remark
type Remark struct {
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Description []string `json:"description"`
	Links       []Link   `json:"links"`
}

// Link represents an RDAP link
type Link struct {
	Value string `json:"value"`
	Rel   string `json:"rel"`
	Href  string `json:"href"`
	Type  string `json:"type"`
}

// Event represents an RDAP event
type Event struct {
	EventAction string `json:"eventAction"`
	EventDate   string `json:"eventDate"`
}

// GetEntityName extracts a name from an entity's vCard
func GetEntityName(entity *Entity) string {
	if entity.VCardArray == nil || len(entity.VCardArray) < 2 {
		return ""
	}

	// vCard format: ["vcard", [["version", {}, "text", "4.0"], ["fn", {}, "text", "Name"], ...]]
	vcard, ok := entity.VCardArray[1].([]interface{})
	if !ok {
		return ""
	}

	for _, field := range vcard {
		fieldArray, ok := field.([]interface{})
		if !ok || len(fieldArray) < 4 {
			continue
		}

		fieldName, ok := fieldArray[0].(string)
		if !ok {
			continue
		}

		// Look for "fn" (formatted name) or "org" (organization)
		if fieldName == "fn" || fieldName == "org" {
			if name, ok := fieldArray[3].(string); ok && name != "" {
				return name
			}
		}
	}

	return ""
}

// DetermineRIR determines the RIR from an RDAP response
func DetermineRIR(response *Response) string {
	// Check port43
	if response.Port43 != "" {
		port43 := strings.ToLower(response.Port43)
		if strings.Contains(port43, "ripe") {
			return "RIPE"
		}
		if strings.Contains(port43, "arin") {
			return "ARIN"
		}
		if strings.Contains(port43, "apnic") {
			return "APNIC"
		}
		if strings.Contains(port43, "lacnic") {
			return "LACNIC"
		}
		if strings.Contains(port43, "afrinic") {
			return "AFRINIC"
		}
	}

	// Check links for RIR URLs
	for _, link := range response.Links {
		href := strings.ToLower(link.Href)
		if strings.Contains(href, "ripe.net") {
			return "RIPE"
		}
		if strings.Contains(href, "arin.net") {
			return "ARIN"
		}
		if strings.Contains(href, "apnic.net") {
			return "APNIC"
		}
		if strings.Contains(href, "lacnic.net") {
			return "LACNIC"
		}
		if strings.Contains(href, "afrinic.net") {
			return "AFRINIC"
		}
	}

	// Try to determine from IP range
	if response.StartAddress != "" {
		ip := net.ParseIP(response.StartAddress)
		if ip != nil {
			return guessRIRFromIP(ip)
		}
	}

	return "UNKNOWN"
}

// guessRIRFromIP guesses the RIR based on IP address ranges
func guessRIRFromIP(ip net.IP) string {
	// This is a simplified heuristic
	// In practice, you'd want a more comprehensive mapping
	if ip.To4() != nil {
		// IPv4
		b := ip.To4()[0]
		// Very rough approximation
		if b >= 80 && b <= 95 {
			return "RIPE"
		}
		if b >= 1 && b <= 24 || (b >= 96 && b <= 126) {
			return "ARIN"
		}
	}
	return "UNKNOWN"
}
