package main

import (
	"archive/zip"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wingedpig/iporg/pkg/arinbulk"
)

const version = "1.0.0"

func main() {
	dbPath := flag.String("db", "./arinbulk.ldb", "Path to LevelDB database")
	xmlFile := flag.String("xml", "", "Path to arin_db.xml or .zip file (if already downloaded)")
	apiKey := flag.String("apikey", "", "ARIN API key for bulk download")
	downloadURL := flag.String("url", "https://account.arin.net/public/secure/downloads/bulkwhois", "ARIN bulk download URL")
	cacheDir := flag.String("cache-dir", "", "Cache directory for downloaded files (default: no caching)")
	forceDownload := flag.Bool("force-download", false, "Force re-download even if cached file exists")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("arin-bulk-build version %s\n", version)
		return
	}

	if *dbPath == "" {
		log.Fatal("ERROR: --db is required")
	}

	var xmlReader io.Reader
	var cleanup func()

	if *xmlFile != "" {
		// Use provided XML file
		log.Printf("INFO: Reading from %s", *xmlFile)

		// Check if it's a zip file
		if strings.HasSuffix(*xmlFile, ".zip") {
			zipReader, err := zip.OpenReader(*xmlFile)
			if err != nil {
				log.Fatalf("ERROR: Failed to open zip file: %v", err)
			}
			defer zipReader.Close()

			var xmlZipFile *zip.File
			for _, f := range zipReader.File {
				// Skip directories
				if f.FileInfo().IsDir() {
					continue
				}
				if strings.HasSuffix(f.Name, ".xml") {
					xmlZipFile = f
					break
				}
			}

			if xmlZipFile == nil {
				log.Fatalf("ERROR: No XML file found in zip archive")
			}

			log.Printf("INFO: Found %s in zip", xmlZipFile.Name)
			rc, err := xmlZipFile.Open()
			if err != nil {
				log.Fatalf("ERROR: Failed to open XML from zip: %v", err)
			}
			defer rc.Close()

			// Extract to temp file for reading
			tmpFile, err := os.CreateTemp("", "arin_db_*.xml")
			if err != nil {
				log.Fatalf("ERROR: Failed to create temp file: %v", err)
			}
			tmpPath := tmpFile.Name()
			cleanup = func() { os.Remove(tmpPath) }
			defer cleanup()

			_, err = io.Copy(tmpFile, rc)
			if err != nil {
				log.Fatalf("ERROR: Failed to extract XML: %v", err)
			}
			tmpFile.Close()

			f, err := os.Open(tmpPath)
			if err != nil {
				log.Fatalf("ERROR: Failed to open extracted XML: %v", err)
			}
			defer f.Close()
			xmlReader = f
		} else {
			// Plain XML or gzipped
			f, err := os.Open(*xmlFile)
			if err != nil {
				log.Fatalf("ERROR: Failed to open file: %v", err)
			}
			defer f.Close()

			// Check if gzipped
			if strings.HasSuffix(*xmlFile, ".gz") {
				gr, err := gzip.NewReader(f)
				if err != nil {
					log.Fatalf("ERROR: Failed to create gzip reader: %v", err)
				}
				defer gr.Close()
				xmlReader = gr
			} else {
				xmlReader = f
			}
		}

	} else if *apiKey != "" {
		var downloadPath string

		// Check cache first
		if *cacheDir != "" && !*forceDownload {
			cachedFile := filepath.Join(*cacheDir, "arin_db.zip")
			if stat, err := os.Stat(cachedFile); err == nil {
				log.Printf("INFO: Using cached file: %s (%.1f MB, downloaded %s)",
					cachedFile, float64(stat.Size())/1024/1024, stat.ModTime().Format("2006-01-02 15:04"))
				downloadPath = cachedFile
			}
		}

		// Download if not cached
		if downloadPath == "" {
			log.Printf("INFO: Downloading ARIN bulk data...")
			url := fmt.Sprintf("%s?apikey=%s", *downloadURL, *apiKey)

			resp, err := http.Get(url)
			if err != nil {
				log.Fatalf("ERROR: Failed to download: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				log.Fatalf("ERROR: Download failed with status %d", resp.StatusCode)
			}

			// Determine download path
			if *cacheDir != "" {
				// Save to cache directory
				if err := os.MkdirAll(*cacheDir, 0755); err != nil {
					log.Fatalf("ERROR: Failed to create cache directory: %v", err)
				}
				downloadPath = filepath.Join(*cacheDir, "arin_db.zip")
				log.Printf("INFO: Downloading to cache: %s", downloadPath)
			} else {
				// Save to temp file
				tmpFile, err := os.CreateTemp("", "arin_db_*.zip")
				if err != nil {
					log.Fatalf("ERROR: Failed to create temp file: %v", err)
				}
				downloadPath = tmpFile.Name()
				tmpFile.Close()
				cleanup = func() { os.Remove(downloadPath) }
				defer cleanup()
				log.Printf("INFO: Downloading to temp file: %s", downloadPath)
			}

			// Download file
			outFile, err := os.Create(downloadPath)
			if err != nil {
				log.Fatalf("ERROR: Failed to create output file: %v", err)
			}

			written, err := io.Copy(outFile, resp.Body)
			outFile.Close()
			if err != nil {
				log.Fatalf("ERROR: Failed to save download: %v", err)
			}
			log.Printf("INFO: Downloaded %.1f MB", float64(written)/1024/1024)
		}

		// Check if it's a zip file
		zipReader, err := zip.OpenReader(downloadPath)
		if err == nil {
			// It's a zip file - extract the XML
			defer zipReader.Close()
			log.Printf("INFO: Extracting XML from zip...")

			var xmlFile *zip.File
			for _, f := range zipReader.File {
				// Skip directories
				if f.FileInfo().IsDir() {
					continue
				}
				if strings.HasSuffix(f.Name, ".xml") {
					xmlFile = f
					break
				}
			}

			if xmlFile == nil {
				log.Fatalf("ERROR: No XML file found in zip archive")
			}

			log.Printf("INFO: Found %s in zip", xmlFile.Name)
			rc, err := xmlFile.Open()
			if err != nil {
				log.Fatalf("ERROR: Failed to open XML from zip: %v", err)
			}
			defer rc.Close()

			// Extract to temp file
			xmlTmpFile, err := os.CreateTemp("", "arin_db_*.xml")
			if err != nil {
				log.Fatalf("ERROR: Failed to create temp XML file: %v", err)
			}
			xmlTmpPath := xmlTmpFile.Name()
			if cleanup != nil {
				oldCleanup := cleanup
				cleanup = func() {
					os.Remove(xmlTmpPath)
					oldCleanup()
				}
			} else {
				cleanup = func() { os.Remove(xmlTmpPath) }
				defer cleanup()
			}

			_, err = io.Copy(xmlTmpFile, rc)
			if err != nil {
				log.Fatalf("ERROR: Failed to extract XML: %v", err)
			}
			xmlTmpFile.Close()

			// Open extracted XML
			f, err := os.Open(xmlTmpPath)
			if err != nil {
				log.Fatalf("ERROR: Failed to open extracted XML: %v", err)
			}
			defer f.Close()
			xmlReader = f
		} else {
			// Not a zip, assume it's plain XML or gzipped
			f, err := os.Open(downloadPath)
			if err != nil {
				log.Fatalf("ERROR: Failed to open file: %v", err)
			}
			defer f.Close()
			xmlReader = f
		}

	} else {
		log.Fatal("ERROR: Either --xml or --apikey must be provided")
	}

	// Remove existing database
	if err := os.RemoveAll(*dbPath); err != nil && !os.IsNotExist(err) {
		log.Fatalf("ERROR: Failed to remove existing database: %v", err)
	}

	// Create parent directory if needed
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0755); err != nil {
		log.Fatalf("ERROR: Failed to create directory: %v", err)
	}

	// Build database using streaming (low memory usage)
	db, err := arinbulk.BuildDatabaseStreaming(*dbPath, xmlReader)
	if err != nil {
		log.Fatalf("ERROR: Failed to build database: %v", err)
	}
	defer db.Close()

	log.Printf("INFO: Database built successfully at %s", *dbPath)

	// Show stats
	netCount, orgCount, err := db.Stats()
	if err != nil {
		log.Printf("WARN: Failed to get stats: %v", err)
	} else {
		log.Printf("INFO: Statistics:")
		log.Printf("INFO:   Networks: %d", netCount)
		log.Printf("INFO:   Organizations: %d", orgCount)
	}
}
