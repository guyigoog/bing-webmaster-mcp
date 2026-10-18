// Package aiperformance imports observed Bing AI Performance CSV exports.
package aiperformance

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxCSVBytes bounds a single import before CSV allocation.
const MaxCSVBytes = 8 * 1024 * 1024

const maxRows = 100000

// ReportType identifies an independently aggregated export.
type ReportType string

const (
	// Pages is the observed AIPageStatsReport format.
	Pages ReportType = "pages"
	// Queries is the observed AISearchQueriesReport format.
	Queries ReportType = "queries"
)

// Row preserves an exported page or query and its reported metrics.
type Row struct {
	Page          string   `json:"page,omitempty"`
	Query         string   `json:"grounding_query,omitempty"`
	Intent        string   `json:"intent,omitempty"`
	Topic         string   `json:"topic,omitempty"`
	Citations     int64    `json:"citations"`
	CitationShare *float64 `json:"citation_share_percent,omitempty"`
}

func (r Row) key() string {
	if r.Page != "" {
		return r.Page
	}
	return r.Query
}

// Parse accepts only the two header sequences verified against real exports.
// A UTF-8 BOM, CRLF, quoting and empty optional query metadata are supported.
func Parse(data string) (ReportType, []Row, error) {
	if len(data) > MaxCSVBytes {
		return "", nil, fmt.Errorf("CSV exceeds %d bytes", MaxCSVBytes)
	}
	if !utf8.ValidString(data) {
		return "", nil, fmt.Errorf("CSV must be UTF-8")
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(data, "\ufeff")))
	header, err := r.Read()
	if err != nil {
		return "", nil, fmt.Errorf("read CSV header: %w", err)
	}
	var kind ReportType
	switch {
	case slices.Equal(header, []string{"Page", "Citations"}):
		kind = Pages
	case slices.Equal(header, []string{"Grounding Query", "Intent", "Topic", "Citations", "Citation Share"}):
		kind = Queries
	default:
		return "", nil, fmt.Errorf("unsupported CSV header; expected Page,Citations or Grounding Query,Intent,Topic,Citations,Citation Share; provide a real export sample before adding other schemas")
	}
	rows := make([]Row, 0)
	seen := make(map[string]bool)
	var total int64
	for n := 2; ; n++ {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, fmt.Errorf("CSV record %d: %w", n, err)
		}
		if len(rows) >= maxRows {
			return "", nil, fmt.Errorf("CSV exceeds %d rows", maxRows)
		}
		if strings.TrimSpace(record[0]) == "" {
			return "", nil, fmt.Errorf("CSV record %d: empty page or query", n)
		}
		row := Row{}
		count := record[1]
		if kind == Pages {
			if _, err := httpURL(record[0]); err != nil {
				return "", nil, fmt.Errorf("CSV record %d: invalid page URL", n)
			}
			row.Page = record[0]
		} else {
			row.Query, row.Intent, row.Topic = record[0], record[1], record[2]
			count = record[3]
			if record[4] != "" {
				share, err := percentage(record[4])
				if err != nil {
					return "", nil, fmt.Errorf("CSV record %d: %w", n, err)
				}
				row.CitationShare = &share
			}
		}
		if count == "" || strings.IndexFunc(count, func(c rune) bool { return c < '0' || c > '9' }) >= 0 {
			return "", nil, fmt.Errorf("CSV record %d: citations must be a nonnegative integer without separators", n)
		}
		row.Citations, err = strconv.ParseInt(count, 10, 64)
		if err != nil || row.Citations > math.MaxInt64-total {
			return "", nil, fmt.Errorf("CSV record %d: citation count overflow", n)
		}
		total += row.Citations
		if seen[row.key()] {
			return "", nil, fmt.Errorf("CSV record %d: duplicate page or query", n)
		}
		seen[row.key()] = true
		rows = append(rows, row)
	}
	// Canonical ordering makes reordered copies idempotent.
	slices.SortFunc(rows, func(a, b Row) int { return strings.Compare(a.key(), b.key()) })
	return kind, rows, nil
}

func percentage(s string) (float64, error) {
	if !strings.HasSuffix(s, "%") {
		return 0, fmt.Errorf("citation share must be a percentage ending in %%")
	}
	n := strings.TrimSuffix(s, "%")
	if n == "" || strings.IndexFunc(n, func(c rune) bool { return (c < '0' || c > '9') && c != '.' }) >= 0 {
		return 0, fmt.Errorf("invalid citation share")
	}
	f, err := strconv.ParseFloat(n, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 100 {
		return 0, fmt.Errorf("citation share must be between 0%% and 100%%")
	}
	return f, nil
}

func httpURL(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(s, " \t\r\n") {
		return nil, fmt.Errorf("expected an absolute HTTP(S) URL without credentials or fragment")
	}
	return u, nil
}
