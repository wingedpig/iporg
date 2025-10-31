package iptoasn

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wingedpig/iporg/pkg/model"
)

const (
	DefaultSourceURL = "https://iptoasn.com/data/ip2asn-v4.tsv.gz"
	DefaultUserAgent = "github.com/wingedpig/iporg/iptoasn-client"
	MaxRetries       = 3
	RetryDelay       = 5 * time.Second
)

// Fetcher handles downloading iptoasn data with ETag/Last-Modified support
type Fetcher struct {
	client    *http.Client
	sourceURL string
	userAgent string
	cacheDir  string
}

// NewFetcher creates a new fetcher instance
func NewFetcher(sourceURL, cacheDir string) *Fetcher {
	if sourceURL == "" {
		sourceURL = DefaultSourceURL
	}
	return &Fetcher{
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
		sourceURL: sourceURL,
		userAgent: DefaultUserAgent,
		cacheDir:  cacheDir,
	}
}

// Fetch downloads the iptoasn data if it has changed since last fetch
// Returns the path to the cached file and metadata
func (f *Fetcher) Fetch(ctx context.Context) (*model.FetchMetadata, error) {
	// Ensure cache directory exists
	if err := os.MkdirAll(f.cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Load existing metadata if available
	metaPath := filepath.Join(f.cacheDir, "metadata.json")
	var existingMeta model.FetchMetadata
	if data, err := os.ReadFile(metaPath); err == nil {
		_ = json.Unmarshal(data, &existingMeta)
	}

	// Prepare request with conditional headers
	req, err := http.NewRequestWithContext(ctx, "GET", f.sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", f.userAgent)
	if existingMeta.ETag != "" {
		req.Header.Set("If-None-Match", existingMeta.ETag)
	}
	if !existingMeta.LastModified.IsZero() {
		req.Header.Set("If-Modified-Since", existingMeta.LastModified.Format(http.TimeFormat))
	}

	// Execute request with retries
	var resp *http.Response
	var lastErr error
	for i := 0; i < MaxRetries; i++ {
		resp, lastErr = f.client.Do(req)
		if lastErr == nil {
			break
		}
		if i < MaxRetries-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(RetryDelay):
			}
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("failed after %d retries: %w", MaxRetries, lastErr)
	}
	defer resp.Body.Close()

	// Handle 304 Not Modified
	if resp.StatusCode == http.StatusNotModified {
		fmt.Printf("Data unchanged (304 Not Modified), using cached file: %s\n", existingMeta.CachePath)
		return &existingMeta, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Generate cache file path with timestamp
	timestamp := time.Now().Format("20060102-150405")
	cachePath := filepath.Join(f.cacheDir, fmt.Sprintf("iptoasn-%s.tsv.gz", timestamp))

	// Download to temp file first
	tempPath := cachePath + ".tmp"
	tempFile, err := os.Create(tempPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() {
		tempFile.Close()
		os.Remove(tempPath)
	}()

	// Copy with progress
	written, err := io.Copy(tempFile, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to download: %w", err)
	}
	tempFile.Close()

	// Atomic rename
	if err := os.Rename(tempPath, cachePath); err != nil {
		return nil, fmt.Errorf("failed to rename temp file: %w", err)
	}

	fmt.Printf("Downloaded %d bytes to %s\n", written, cachePath)

	// Parse Last-Modified header
	var lastModified time.Time
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if t, err := http.ParseTime(lm); err == nil {
			lastModified = t
		}
	}

	// Create metadata
	meta := &model.FetchMetadata{
		SourceURL:    f.sourceURL,
		ETag:         resp.Header.Get("ETag"),
		LastModified: lastModified,
		CachePath:    cachePath,
		FetchedAt:    time.Now(),
	}

	// Save metadata
	if metaData, err := json.MarshalIndent(meta, "", "  "); err == nil {
		_ = os.WriteFile(metaPath, metaData, 0644)
	}

	return meta, nil
}

// OpenCachedFile opens the cached gzip file for reading
func (f *Fetcher) OpenCachedFile(meta *model.FetchMetadata) (io.ReadCloser, error) {
	file, err := os.Open(meta.CachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open cached file: %w", err)
	}

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}

	// Return a combined closer
	return &CombinedCloser{gzReader, file}, nil
}

// CombinedCloser closes both the gzip reader and the underlying file
type CombinedCloser struct {
	gzReader io.ReadCloser
	file     io.Closer
}

func (c *CombinedCloser) Read(p []byte) (n int, err error) {
	return c.gzReader.Read(p)
}

func (c *CombinedCloser) Close() error {
	err1 := c.gzReader.Close()
	err2 := c.file.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
