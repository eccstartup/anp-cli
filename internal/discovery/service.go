// Package discovery crawls agent description documents (ad.json) and
// interface.json, and searches the local crawl index.
package discovery

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/eccstartup/anp-cli/internal/store"
	"github.com/eccstartup/anp-cli/internal/transport"
)

type CrawlResult struct {
	URL          string         `json:"url"`
	DID          string         `json:"did,omitempty"`
	Name         string         `json:"name,omitempty"`
	Description  string         `json:"description,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
	AD           map[string]any `json:"ad"`
	Interface    map[string]any `json:"interface,omitempty"`
	Interfaces   []string       `json:"interfaces,omitempty"`
}

// Crawl fetches an agent description from url, trying url itself and common
// ad.json/interface.json locations.
func Crawl(ctx context.Context, db *sql.DB, rawURL string) (*CrawlResult, error) {
	candidates := []string{rawURL}
	trimmed := strings.TrimRight(rawURL, "/")
	if !strings.HasSuffix(trimmed, "ad.json") {
		candidates = append(candidates, trimmed+"/ad.json")
	}
	adURL := rawURL
	var ad map[string]any
	var fetchErr error
	for _, candidate := range candidates {
		doc, err := transport.FetchJSON(ctx, candidate)
		if err != nil {
			fetchErr = err
			continue
		}
		ad = doc
		adURL = candidate
		fetchErr = nil
		break
	}
	if fetchErr != nil {
		return nil, fmt.Errorf("crawl %s: %w", rawURL, fetchErr)
	}

	result := &CrawlResult{URL: adURL, AD: ad}
	if did, ok := ad["did"].(string); ok {
		result.DID = did
	}
	if name, ok := ad["name"].(string); ok {
		result.Name = name
	}
	if description, ok := ad["description"].(string); ok {
		result.Description = description
	}
	if capabilities, ok := stringList(ad["capabilities"]); ok {
		result.Capabilities = capabilities
	}
	if interfaces, ok := ad["interfaces"].([]any); ok {
		for _, entry := range interfaces {
			if name, ok := entry.(map[string]any)["name"].(string); ok {
				result.Interfaces = append(result.Interfaces, name)
			}
		}
	}
	// interface.json, when present.
	interfaceCandidates := []string{trimmed + "/interface.json"}
	if base := strings.TrimSuffix(trimmed, "/ad.json"); base != trimmed {
		interfaceCandidates = append(interfaceCandidates, base+"/interface.json")
	}
	for _, candidate := range interfaceCandidates {
		if doc, err := transport.FetchJSON(ctx, candidate); err == nil {
			result.Interface = doc
			break
		}
	}
	agent := store.DiscoveredAgent{
		DID:          result.DID,
		URL:          result.URL,
		Name:         result.Name,
		Description:  result.Description,
		Capabilities: result.Capabilities,
		AD:           result.AD,
	}
	if err := store.UpsertDiscoveredAgent(db, agent); err != nil {
		return nil, err
	}
	return result, nil
}

func Search(ctx context.Context, db *sql.DB, query string, limit int) ([]store.DiscoveredAgent, error) {
	return store.SearchDiscoveredAgents(db, query, limit)
}

func stringList(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []any:
		list := []string{}
		for _, entry := range typed {
			if item, ok := entry.(string); ok {
				list = append(list, item)
			}
		}
		return list, true
	case []string:
		return typed, true
	default:
		return nil, false
	}
}
