package main

import (
	"context"
	"fmt"
	"log"
	"net/netip"

	"github.com/wingedpig/iporg/pkg/iporgdb"
	"github.com/wingedpig/iporg/pkg/model"
)

// RunVerify performs consistency checks on the database
func RunVerify(ctx context.Context, dbPath string) error {
	log.Printf("INFO: Opening database at %s", dbPath)
	db, err := iporgdb.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	var issues int

	// Check 1: Verify no overlapping ranges
	log.Println("INFO: Checking for overlapping ranges...")
	overlaps, err := checkOverlaps(db)
	if err != nil {
		return fmt.Errorf("overlap check failed: %w", err)
	}
	if overlaps > 0 {
		log.Printf("ERROR: Found %d overlapping ranges", overlaps)
		issues += overlaps
	} else {
		log.Println("OK: No overlapping ranges found")
	}

	// Check 2: Verify all records have required fields
	log.Println("INFO: Checking for missing required fields...")
	missing, err := checkMissingFields(db)
	if err != nil {
		return fmt.Errorf("missing fields check failed: %w", err)
	}
	if missing > 0 {
		log.Printf("ERROR: Found %d records with missing fields", missing)
		issues += missing
	} else {
		log.Println("OK: All records have required fields")
	}

	// Check 3: Verify IP range validity
	log.Println("INFO: Checking IP range validity...")
	invalid, err := checkRangeValidity(db)
	if err != nil {
		return fmt.Errorf("range validity check failed: %w", err)
	}
	if invalid > 0 {
		log.Printf("ERROR: Found %d invalid ranges", invalid)
		issues += invalid
	} else {
		log.Println("OK: All ranges are valid")
	}

	// Check 4: Verify metadata
	log.Println("INFO: Checking metadata...")
	if err := checkMetadata(db); err != nil {
		log.Printf("WARN: Metadata issues: %v", err)
	} else {
		log.Println("OK: Metadata is valid")
	}

	if issues > 0 {
		return fmt.Errorf("verification found %d issues", issues)
	}

	log.Println("INFO: All verification checks passed")
	return nil
}

// checkOverlaps checks for overlapping IP ranges
func checkOverlaps(db *iporgdb.DB) (int, error) {
	overlaps := 0

	// Check IPv4
	var prevRec *model.Record
	err := db.IterateRanges(true, func(rec *model.Record) error {
		if prevRec != nil {
			// Check if current range overlaps with previous
			if rec.Start.Compare(prevRec.End) <= 0 {
				log.Printf("ERROR: Overlap detected: %s (%s-%s) overlaps with %s (%s-%s)",
					rec.Prefix, rec.Start, rec.End,
					prevRec.Prefix, prevRec.Start, prevRec.End)
				overlaps++
			}
		}
		prevRec = rec
		return nil
	})
	if err != nil {
		return 0, err
	}

	// Check IPv6
	prevRec = nil
	err = db.IterateRanges(false, func(rec *model.Record) error {
		if prevRec != nil {
			if rec.Start.Compare(prevRec.End) <= 0 {
				log.Printf("ERROR: Overlap detected: %s (%s-%s) overlaps with %s (%s-%s)",
					rec.Prefix, rec.Start, rec.End,
					prevRec.Prefix, prevRec.Start, prevRec.End)
				overlaps++
			}
		}
		prevRec = rec
		return nil
	})
	if err != nil {
		return 0, err
	}

	return overlaps, nil
}

// checkMissingFields checks for records with missing required fields
func checkMissingFields(db *iporgdb.DB) (int, error) {
	missing := 0

	check := func(rec *model.Record) error {
		if rec.OrgName == "" {
			log.Printf("ERROR: Record %s has no org name", rec.Prefix)
			missing++
		}
		if rec.Country == "" {
			log.Printf("WARN: Record %s has no country", rec.Prefix)
		}
		if rec.ASN == 0 {
			log.Printf("WARN: Record %s has no ASN", rec.Prefix)
		}
		if rec.SourceRole == "" {
			log.Printf("WARN: Record %s has no source role", rec.Prefix)
		}
		return nil
	}

	if err := db.IterateRanges(true, check); err != nil {
		return 0, err
	}
	if err := db.IterateRanges(false, check); err != nil {
		return 0, err
	}

	return missing, nil
}

// checkRangeValidity checks if IP ranges are valid
func checkRangeValidity(db *iporgdb.DB) (int, error) {
	invalid := 0

	check := func(rec *model.Record) error {
		// Check that start <= end
		if rec.Start.Compare(rec.End) > 0 {
			log.Printf("ERROR: Invalid range %s: start %s > end %s",
				rec.Prefix, rec.Start, rec.End)
			invalid++
		}

		// Check that start and end have the same IP version
		if rec.Start.Is4() != rec.End.Is4() {
			log.Printf("ERROR: Invalid range %s: mixed IP versions", rec.Prefix)
			invalid++
		}

		// Verify prefix is valid
		if _, err := netip.ParsePrefix(rec.Prefix); err != nil {
			log.Printf("ERROR: Invalid prefix %s: %v", rec.Prefix, err)
			invalid++
		}

		return nil
	}

	if err := db.IterateRanges(true, check); err != nil {
		return 0, err
	}
	if err := db.IterateRanges(false, check); err != nil {
		return 0, err
	}

	return invalid, nil
}

// checkMetadata verifies database metadata
func checkMetadata(db *iporgdb.DB) error {
	schema, err := db.GetSchemaVersion()
	if err != nil {
		return fmt.Errorf("failed to get schema version: %w", err)
	}
	if schema == 0 {
		return fmt.Errorf("schema version not set")
	}
	log.Printf("INFO: Schema version: %d", schema)

	builtAt, err := db.GetBuiltAt()
	if err != nil {
		return fmt.Errorf("failed to get built_at: %w", err)
	}
	if builtAt.IsZero() {
		return fmt.Errorf("built_at not set")
	}
	log.Printf("INFO: Built at: %s", builtAt.Format("2006-01-02 15:04:05"))

	builderVer, err := db.GetBuilderVersion()
	if err != nil {
		return fmt.Errorf("failed to get builder version: %w", err)
	}
	if builderVer == "" {
		return fmt.Errorf("builder version not set")
	}
	log.Printf("INFO: Builder version: %s", builderVer)

	return nil
}
