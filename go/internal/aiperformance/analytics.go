package aiperformance

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"
)

const coverageNote = "Metrics cover exported rows only; exports may be truncated or filtered. Page and query totals are independent and must not be added. No daily series or query-to-page mapping is available."

// Summary describes the rows of a single exported report, not a dashboard-wide total.
type Summary struct {
	RowCount          int    `json:"row_count"`
	ExportedCitations int64  `json:"exported_citations"`
	ImportedAt        string `json:"imported_at"`
}

func summary(s Snapshot) (Summary, error) {
	result := Summary{RowCount: len(s.Rows), ImportedAt: s.ImportedAt}
	for _, r := range s.Rows {
		if r.Citations < 0 || r.Citations > math.MaxInt64-result.ExportedCitations {
			return Summary{}, fmt.Errorf("invalid stored citation count or total overflow")
		}
		result.ExportedCitations += r.Citations
	}
	return result, nil
}

// Stats keeps page and query aggregates separate, with null for missing reports.
type Stats struct {
	SiteURL      string   `json:"site_url"`
	Period       Period   `json:"period"`
	Pages        *Summary `json:"pages"`
	Queries      *Summary `json:"queries"`
	CoverageNote string   `json:"coverage_note"`
}

// CitationStats reads exact-period snapshots; an absent report is never treated as zero.
func (s *Store) CitationStats(ctx context.Context, site string, p Period) (Stats, error) {
	site, err := normalizeSite(site)
	if err != nil {
		return Stats{}, err
	}
	if err := p.validate(); err != nil {
		return Stats{}, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return Stats{}, err
	}
	defer unlock()
	result := Stats{SiteURL: site, Period: p, CoverageNote: coverageNote}
	for _, kind := range []ReportType{Pages, Queries} {
		snapshot, err := s.read(snapshotName(site, kind, p))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Stats{}, err
		}
		sum, err := summary(snapshot)
		if err != nil {
			return Stats{}, err
		}
		if kind == Pages {
			result.Pages = &sum
		} else {
			result.Queries = &sum
		}
	}
	if result.Pages == nil && result.Queries == nil {
		return Stats{}, fmt.Errorf("no reports imported for this site and exact date range")
	}
	return result, nil
}

// RankedRows contains the highest-cited exported rows for an exact period.
type RankedRows struct {
	SiteURL      string     `json:"site_url"`
	ReportType   ReportType `json:"report_type"`
	Period       Period     `json:"period"`
	Summary      Summary    `json:"summary"`
	Rows         []Row      `json:"rows"`
	CoverageNote string     `json:"coverage_note"`
}

// Top returns deterministic descending rankings (ties sorted by page/query).
func (s *Store) Top(ctx context.Context, site string, kind ReportType, p Period, limit int) (RankedRows, error) {
	site, err := normalizeSite(site)
	if err != nil {
		return RankedRows{}, err
	}
	if err := p.validate(); err != nil {
		return RankedRows{}, err
	}
	if kind != Pages && kind != Queries {
		return RankedRows{}, fmt.Errorf("report_type must be pages or queries")
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 1000 {
		return RankedRows{}, fmt.Errorf("limit must be between 1 and 1000")
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return RankedRows{}, err
	}
	defer unlock()
	snapshot, err := s.read(snapshotName(site, kind, p))
	if errors.Is(err, os.ErrNotExist) {
		return RankedRows{}, fmt.Errorf("no %s report imported for this site and exact date range", kind)
	}
	if err != nil {
		return RankedRows{}, err
	}
	sum, err := summary(snapshot)
	if err != nil {
		return RankedRows{}, err
	}
	slices.SortFunc(snapshot.Rows, func(a, b Row) int {
		if a.Citations > b.Citations {
			return -1
		}
		if a.Citations < b.Citations {
			return 1
		}
		return strings.Compare(a.key(), b.key())
	})
	return RankedRows{SiteURL: site, ReportType: kind, Period: p, Summary: sum, Rows: snapshot.Rows[:min(limit, len(snapshot.Rows))], CoverageNote: coverageNote}, nil
}

// TrendPoint represents an exported period average, never an inferred daily observation.
type TrendPoint struct {
	Period          Period   `json:"period"`
	Summary         Summary  `json:"summary"`
	Days            int      `json:"days"`
	CitationsPerDay float64  `json:"exported_citations_per_day"`
	ChangePercent   *float64 `json:"per_day_change_percent"`
}

// Trends describes independent period snapshots and their average citation rates.
type Trends struct {
	SiteURL      string       `json:"site_url"`
	ReportType   ReportType   `json:"report_type"`
	Window       Period       `json:"window"`
	Points       []TrendPoint `json:"points"`
	CoverageNote string       `json:"coverage_note"`
}

// VisibilityTrends compares non-overlapping whole periods contained in the window.
// Partial periods and overlaps are rejected because aggregate rows cannot be deduplicated by day.
func (s *Store) VisibilityTrends(ctx context.Context, site string, kind ReportType, window Period) (Trends, error) {
	site, err := normalizeSite(site)
	if err != nil {
		return Trends{}, err
	}
	if err := window.validate(); err != nil {
		return Trends{}, err
	}
	if kind != Pages && kind != Queries {
		return Trends{}, fmt.Errorf("report_type must be pages or queries")
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return Trends{}, err
	}
	defer unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return Trends{}, err
	}
	snapshots := []Snapshot{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return Trends{}, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		snapshot, err := s.read(entry.Name())
		if err != nil {
			return Trends{}, err
		}
		if snapshot.SiteURL != site || snapshot.ReportType != kind || snapshot.Period.End < window.Start || snapshot.Period.Start > window.End {
			continue
		}
		if snapshot.Period.Start < window.Start || snapshot.Period.End > window.End {
			return Trends{}, fmt.Errorf("window cuts through an imported period; use whole snapshot periods")
		}
		snapshots = append(snapshots, snapshot)
	}
	if len(snapshots) == 0 {
		return Trends{}, fmt.Errorf("no %s reports imported in this window", kind)
	}
	slices.SortFunc(snapshots, func(a, b Snapshot) int { return strings.Compare(a.Period.Start, b.Period.Start) })
	result := Trends{SiteURL: site, ReportType: kind, Window: window, Points: []TrendPoint{}, CoverageNote: coverageNote + " Points are period averages, not daily observations. Gaps are not zero-filled. Compare only exports with the same filters and coverage."}
	for i, snapshot := range snapshots {
		if i > 0 && snapshot.Period.Start <= snapshots[i-1].Period.End {
			return Trends{}, fmt.Errorf("overlapping %s snapshots cannot be combined; select a window containing only non-overlapping periods", kind)
		}
		sum, err := summary(snapshot)
		if err != nil {
			return Trends{}, err
		}
		start, _ := time.Parse(time.DateOnly, snapshot.Period.Start)
		end, _ := time.Parse(time.DateOnly, snapshot.Period.End)
		days := int((end.Unix()-start.Unix())/86400) + 1
		point := TrendPoint{Period: snapshot.Period, Summary: sum, Days: days, CitationsPerDay: float64(sum.ExportedCitations) / float64(days)}
		if i > 0 && result.Points[i-1].CitationsPerDay != 0 {
			change := (point.CitationsPerDay/result.Points[i-1].CitationsPerDay - 1) * 100
			point.ChangePercent = &change
		}
		result.Points = append(result.Points, point)
	}
	return result, nil
}
