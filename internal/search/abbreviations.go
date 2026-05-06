package search

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var tokenRe = regexp.MustCompile(`[A-Za-z0-9&]+(?:[-_][A-Za-z0-9&]+)*`)

func LoadAbbreviations(dataDir string) map[string]string {
	abbr := map[string]string{
		"ADB":    "Autonomous Database",
		"OCI":    "Oracle Cloud Infrastructure",
		"POC":    "Proof of Concept",
		"RAG":    "retrieval augmented generation",
		"S&C":    "Signal and Communication",
		"SIT":    "system integration testing staging",
		"SMRT":   "Singapore Mass Rapid Transit",
		"UAT":    "user acceptance testing",
		"UC1A":   "Use Case 1a Maintenance Engineering Centre",
		"UC1B":   "Use Case 1b Rolling Stock work instructions",
		"UC1C":   "Use Case 1c Signal and Communication",
		"WI":     "work instruction",
		"MEC":    "Maintenance Engineering Centre",
		"QDRANT": "vector database retrieval store",
	}
	path := filepath.Join(dataDir, "abbreviations.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return abbr
	}
	var extra map[string]string
	if json.Unmarshal(data, &extra) == nil {
		for k, v := range extra {
			k = strings.ToUpper(strings.TrimSpace(k))
			v = strings.TrimSpace(v)
			if k != "" && v != "" {
				abbr[k] = v
			}
		}
	}
	return abbr
}

func ExpandAbbreviations(query string, abbr map[string]string) (string, []string) {
	if len(abbr) == 0 {
		return query, nil
	}
	seen := map[string]bool{}
	var expansions []string
	for _, token := range tokenRe.FindAllString(query, -1) {
		key := strings.ToUpper(strings.Trim(token, " .,:;()[]{}"))
		if val, ok := abbr[key]; ok && !seen[key] {
			seen[key] = true
			expansions = append(expansions, key+"="+val)
		}
	}
	if len(expansions) == 0 {
		return query, nil
	}
	sort.Strings(expansions)
	return strings.TrimSpace(query + " " + strings.Join(expansions, " ")), expansions
}
