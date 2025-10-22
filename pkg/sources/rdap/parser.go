package rdap

import (
	"fmt"
	"log"
	"strings"

	"iporg/pkg/model"
)

// ParseOrg extracts organization information from an RDAP response
func ParseOrg(response *Response) (*model.RDAPOrg, error) {
	if response == nil {
		return nil, fmt.Errorf("nil RDAP response")
	}

	org := &model.RDAPOrg{
		RIR: DetermineRIR(response),
	}

	// Try to extract status
	if len(response.Status) > 0 {
		org.StatusLabel = response.Status[0]
	}

	// Look for entities with customer or registrant roles
	// Prioritize: customer > org registrant > non-mnt registrant > administrative > technical > abuse
	var customerEntity *Entity
	var orgRegistrantEntity *Entity
	var registrantEntity *Entity
	var adminEntity *Entity
	var technicalEntity *Entity
	var abuseEntity *Entity

	for i := range response.Entities {
		entity := &response.Entities[i]

		// Skip maintainer references (handles ending in -MNT)
		if strings.HasSuffix(entity.Handle, "-MNT") {
			continue
		}

		// Check if this is an organization entity (handle starts with ORG-)
		isOrgEntity := strings.HasPrefix(entity.Handle, "ORG-")

		for _, role := range entity.Roles {
			roleLower := strings.ToLower(role)
			if roleLower == "customer" {
				customerEntity = entity
			} else if roleLower == "registrant" {
				if isOrgEntity && orgRegistrantEntity == nil {
					// Prefer ORG- entities for registrant role
					orgRegistrantEntity = entity
				} else if registrantEntity == nil {
					registrantEntity = entity
				}
			} else if roleLower == "administrative" {
				if adminEntity == nil {
					adminEntity = entity
				}
			} else if roleLower == "technical" {
				if technicalEntity == nil {
					technicalEntity = entity
				}
			} else if roleLower == "abuse" {
				abuseEntity = entity
			}
		}
	}

	// Check if network name looks like a good organization name
	// (not a technical code, has meaningful content)
	hasGoodNetworkName := response.Name != "" &&
		!strings.HasSuffix(response.Name, "-MNT") &&
		len(response.Name) > 3 &&
		!strings.HasPrefix(response.Name, "UK-")

	// Prefer customer, then org registrant, then registrant, then network name (if good),
	// then administrative, then technical, then abuse
	var selectedEntity *Entity
	if customerEntity != nil {
		selectedEntity = customerEntity
		org.SourceRole = "customer"
	} else if orgRegistrantEntity != nil {
		selectedEntity = orgRegistrantEntity
		org.SourceRole = "registrant"
	} else if registrantEntity != nil {
		selectedEntity = registrantEntity
		org.SourceRole = "registrant"
	} else if hasGoodNetworkName {
		// Use network name directly
		org.OrgName = response.Name
		org.SourceRole = "network_name"
		log.Printf("INFO: Using network name as organization: %s", response.Name)
		return org, nil
	} else if adminEntity != nil {
		selectedEntity = adminEntity
		org.SourceRole = "administrative"
	} else if technicalEntity != nil {
		selectedEntity = technicalEntity
		org.SourceRole = "technical"
	} else if abuseEntity != nil {
		selectedEntity = abuseEntity
		org.SourceRole = "abuse"
	}

	if selectedEntity != nil {
		// Extract name from vCard
		org.OrgName = GetEntityName(selectedEntity)
		if org.OrgName == "" {
			// Try nested entities
			for i := range selectedEntity.Entities {
				name := GetEntityName(&selectedEntity.Entities[i])
				if name != "" {
					org.OrgName = name
					break
				}
			}
		}
	}

	// Fallback: try to get any entity name
	if org.OrgName == "" {
		for i := range response.Entities {
			name := GetEntityName(&response.Entities[i])
			if name != "" {
				org.OrgName = name
				org.SourceRole = "entity"
				log.Printf("WARN: Using entity name as fallback: %s", name)
				break
			}
		}
	}

	// Fallback chain if we still don't have an org name
	if org.OrgName == "" {
		// Try network name first (like "BT-Central-Plus")
		if response.Name != "" && !strings.HasSuffix(response.Name, "-MNT") {
			org.OrgName = response.Name
			org.SourceRole = "network_name"
			log.Printf("INFO: Using network name: %s", response.Name)
		}
	}

	// If still no org name, check remarks for hints
	if org.OrgName == "" {
		org.OrgName = extractOrgFromRemarks(response)
		if org.OrgName != "" {
			org.SourceRole = "remark"
		}
	}

	if org.OrgName == "" {
		return nil, fmt.Errorf("no organization name found in RDAP response")
	}

	return org, nil
}

// extractOrgFromRemarks tries to extract organization info from remarks
func extractOrgFromRemarks(response *Response) string {
	for _, remark := range response.Remarks {
		// Look for descriptions that might contain org info
		for _, desc := range remark.Description {
			// Common patterns in RIPE remarks
			if strings.Contains(strings.ToLower(desc), "org-name:") {
				parts := strings.SplitN(desc, ":", 2)
				if len(parts) == 2 {
					return strings.TrimSpace(parts[1])
				}
			}
			if strings.Contains(strings.ToLower(desc), "organisation:") {
				parts := strings.SplitN(desc, ":", 2)
				if len(parts) == 2 {
					return strings.TrimSpace(parts[1])
				}
			}
		}
	}
	return ""
}

// CleanOrgName cleans up organization names
func CleanOrgName(name string) string {
	// Remove common prefixes/suffixes
	name = strings.TrimSpace(name)

	// Remove quotes
	name = strings.Trim(name, "\"'")

	// Collapse multiple spaces
	name = strings.Join(strings.Fields(name), " ")

	return name
}

// IsLikelyISP checks if an organization name looks like an ISP/carrier
func IsLikelyISP(name string) bool {
	nameLower := strings.ToLower(name)
	ispKeywords := []string{
		"telecom", "communications", "internet", "broadband",
		"isp", "cable", "fiber", "network", "telecommunications",
	}

	for _, keyword := range ispKeywords {
		if strings.Contains(nameLower, keyword) {
			return true
		}
	}

	return false
}

// ShouldPreferMaxMindASN determines if we should use MaxMind ASN org instead of RDAP
// This is useful when RDAP returns the ISP but we want the actual customer
func ShouldPreferMaxMindASN(rdapOrg, maxmindOrg string) bool {
	// If RDAP org looks like an ISP and MaxMind org doesn't, prefer MaxMind
	if IsLikelyISP(rdapOrg) && !IsLikelyISP(maxmindOrg) {
		return true
	}

	// If they're the same (case-insensitive), use RDAP (more specific)
	if strings.EqualFold(rdapOrg, maxmindOrg) {
		return false
	}

	return false
}
