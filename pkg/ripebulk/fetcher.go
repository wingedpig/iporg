package ripebulk

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultBaseURL is the RIPE FTP base URL for split dumps
	DefaultBaseURL = "https://ftp.ripe.net/ripe/dbase/split"

	// Dump file names
	InetnumFile      = "ripe.db.inetnum.gz"
	OrganisationFile = "ripe.db.organisation.gz"

	// HTTP client timeout
	fetchTimeout = 5 * time.Minute
)

// Fetcher handles downloading RIPE split dumps
type Fetcher struct {
	baseURL    string
	httpClient *http.Client
	cacheDir   string
}

// NewFetcher creates a new RIPE dump fetcher
func NewFetcher(baseURL, cacheDir string) *Fetcher {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	return &Fetcher{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: fetchTimeout,
		},
		cacheDir: cacheDir,
	}
}

// FetchResult contains the result of a fetch operation
type FetchResult struct {
	FilePath     string    // Local path to the downloaded file
	LastModified time.Time // Last-Modified header from server
	Size         int64     // File size in bytes
	Cached       bool      // True if file was already cached (not downloaded)
}

// Fetch downloads a RIPE dump file with conditional request support
func (f *Fetcher) Fetch(ctx context.Context, filename string) (*FetchResult, error) {
	url := fmt.Sprintf("%s/%s", f.baseURL, filename)

	// Create cache directory if needed
	if err := os.MkdirAll(f.cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache dir: %w", err)
	}

	cachePath := filepath.Join(f.cacheDir, filename)

	// Check if we have a cached version
	var ifModifiedSince time.Time
	if stat, err := os.Stat(cachePath); err == nil {
		ifModifiedSince = stat.ModTime()
		log.Printf("INFO: Found cached %s (modified: %s)", filename, ifModifiedSince.Format(time.RFC3339))
	}

	// Build request with conditional headers
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if !ifModifiedSince.IsZero() {
		req.Header.Set("If-Modified-Since", ifModifiedSince.UTC().Format(http.TimeFormat))
	}

	// Execute request
	log.Printf("INFO: Fetching %s", url)
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFetchFailed, err)
	}
	defer resp.Body.Close()

	// Handle 304 Not Modified
	if resp.StatusCode == http.StatusNotModified {
		stat, _ := os.Stat(cachePath)
		log.Printf("INFO: %s not modified (using cached version)", filename)
		return &FetchResult{
			FilePath:     cachePath,
			LastModified: ifModifiedSince,
			Size:         stat.Size(),
			Cached:       true,
		}, nil
	}

	// Check for success
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: unexpected status %d for %s", ErrFetchFailed, resp.StatusCode, url)
	}

	// Parse Last-Modified header
	var lastModified time.Time
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		lastModified, _ = time.Parse(http.TimeFormat, lm)
	}

	// Write to temporary file first
	tmpPath := cachePath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	// Copy response body
	size, err := io.Copy(tmpFile, resp.Body)
	tmpFile.Close()
	if err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	// Set modification time from Last-Modified header
	if !lastModified.IsZero() {
		os.Chtimes(tmpPath, lastModified, lastModified)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, cachePath); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to rename file: %w", err)
	}

	log.Printf("INFO: Downloaded %s (%d bytes, last-modified: %s)", filename, size, lastModified.Format(time.RFC3339))

	return &FetchResult{
		FilePath:     cachePath,
		LastModified: lastModified,
		Size:         size,
		Cached:       false,
	}, nil
}

// FetchAll downloads both inetnum and organisation dumps
func (f *Fetcher) FetchAll(ctx context.Context) (inetnumPath, orgPath string, err error) {
	// Fetch inetnum dump
	inetnumResult, err := f.Fetch(ctx, InetnumFile)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch inetnum: %w", err)
	}

	// Fetch organisation dump
	orgResult, err := f.Fetch(ctx, OrganisationFile)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch organisation: %w", err)
	}

	log.Printf("INFO: Fetch complete - inetnum: %s (cached: %v), org: %s (cached: %v)",
		filepath.Base(inetnumResult.FilePath), inetnumResult.Cached,
		filepath.Base(orgResult.FilePath), orgResult.Cached)

	return inetnumResult.FilePath, orgResult.FilePath, nil
}

// OpenGzipFile opens a gzipped file and returns a reader
func OpenGzipFile(path string) (io.ReadCloser, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	gzr, err := gzip.NewReader(file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}

	// Return a combined closer that closes both the gzip reader and file
	return &gzipFileReader{gzr: gzr, file: file}, nil
}

// gzipFileReader wraps both gzip.Reader and os.File for proper cleanup
type gzipFileReader struct {
	gzr  *gzip.Reader
	file *os.File
}

func (g *gzipFileReader) Read(p []byte) (int, error) {
	return g.gzr.Read(p)
}

func (g *gzipFileReader) Close() error {
	err1 := g.gzr.Close()
	err2 := g.file.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
