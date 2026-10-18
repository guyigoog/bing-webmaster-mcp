package aiperformance

import (
	"context"
	"testing"
)

func TestRankingsMissingAndEmptyReports(t *testing.T) {
	ctx := context.Background()
	s := NewStore(t.TempDir())
	if _, err := s.CitationStats(ctx, site, january); err == nil {
		t.Fatal("missing report returned data")
	}
	if _, err := s.Top(ctx, site, Queries, january, 10); err == nil {
		t.Fatal("missing queries returned data")
	}
	_, err := s.Import(ctx, site, january, "Page,Citations\nhttps://example.test/b,10\nhttps://example.test/a,10\nhttps://example.test/c,20\n")
	if err != nil {
		t.Fatal(err)
	}
	top, err := s.Top(ctx, site, Pages, january, 2)
	if err != nil || len(top.Rows) != 2 || top.Rows[0].Page != site+"c" || top.Rows[1].Page != site+"a" || top.Summary.ExportedCitations != 40 {
		t.Fatalf("%+v %v", top, err)
	}
	for _, limit := range []int{-1, 1001} {
		if _, err := s.Top(ctx, site, Pages, january, limit); err == nil {
			t.Fatal(limit)
		}
	}
	if _, err := s.Import(ctx, site, january, "Grounding Query,Intent,Topic,Citations,Citation Share\n"); err != nil {
		t.Fatal(err)
	}
	stats, err := s.CitationStats(ctx, site, january)
	if err != nil || stats.Queries == nil || stats.Queries.ExportedCitations != 0 {
		t.Fatalf("%+v %v", stats, err)
	}
	queries, err := s.Top(ctx, site, Queries, january, 0)
	if err != nil || queries.Rows == nil || len(queries.Rows) != 0 {
		t.Fatalf("%+v %v", queries, err)
	}
}

func TestTrendsUsePeriodRatesAndNeverDoubleCount(t *testing.T) {
	ctx := context.Background()
	s := NewStore(t.TempDir())
	importPages(t, s, Period{"2026-01-01", "2026-01-10"}, 100)
	importPages(t, s, Period{"2026-01-15", "2026-01-19"}, 100)
	trends, err := s.VisibilityTrends(ctx, site, Pages, january)
	if err != nil || len(trends.Points) != 2 {
		t.Fatalf("%+v %v", trends, err)
	}
	if trends.Points[0].CitationsPerDay != 10 || trends.Points[0].ChangePercent != nil || trends.Points[1].CitationsPerDay != 20 || trends.Points[1].ChangePercent == nil || *trends.Points[1].ChangePercent != 100 {
		t.Fatal(trends)
	}
	if _, err := s.VisibilityTrends(ctx, site, Pages, Period{"2026-01-02", "2026-01-31"}); err == nil {
		t.Fatal("accepted partial period")
	}
	if _, err := s.VisibilityTrends(ctx, site, Queries, january); err == nil {
		t.Fatal("invented query series")
	}
	if _, err := s.VisibilityTrends(ctx, site, "daily", january); err == nil {
		t.Fatal("invented daily series")
	}
	importPages(t, s, Period{"2026-01-10", "2026-01-15"}, 60)
	if _, err := s.VisibilityTrends(ctx, site, Pages, january); err == nil {
		t.Fatal("accepted inclusive overlap")
	}
	// Exact snapshot queries remain available even when other snapshots overlap.
	stats, err := s.CitationStats(ctx, site, Period{"2026-01-10", "2026-01-15"})
	if err != nil || stats.Pages.ExportedCitations != 60 {
		t.Fatalf("%+v %v", stats, err)
	}
}

func TestTrendZeroBaseline(t *testing.T) {
	s := NewStore(t.TempDir())
	importPages(t, s, Period{"2026-01-01", "2026-01-01"}, 0)
	importPages(t, s, Period{"2026-01-02", "2026-01-02"}, 10)
	result, err := s.VisibilityTrends(context.Background(), site, Pages, january)
	if err != nil || result.Points[1].ChangePercent != nil {
		t.Fatalf("%+v %v", result, err)
	}
}
