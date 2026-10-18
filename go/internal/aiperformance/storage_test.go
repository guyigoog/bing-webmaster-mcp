package aiperformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var january = Period{Start: "2026-01-01", End: "2026-01-31"}

const site = "https://example.test/"

func importPages(t *testing.T, s *Store, p Period, count int) {
	t.Helper()
	_, err := s.Import(context.Background(), site, p, fmt.Sprintf("Page,Citations\nhttps://example.test/a,%d\n", count))
	if err != nil {
		t.Fatal(err)
	}
}

func TestImportPersistenceReplacementAndIsolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := NewStore(dir)
	result, err := s.Import(ctx, "https://EXAMPLE.test", january, fixture(t, "pages"))
	if err != nil || result.Status != "imported" {
		t.Fatalf("%+v %v", result, err)
	}
	result, err = s.Import(ctx, site, january, fixture(t, "pages"))
	if err != nil || result.Status != "unchanged" {
		t.Fatalf("%+v %v", result, err)
	}
	// Reordering and newline differences do not create a new snapshot.
	result, err = s.Import(ctx, site, january, "Page,Citations\nhttps://example.test/c/,179\nhttps://example.test/b/,583\nhttps://example.test/a/,1039\n")
	if err != nil || result.Status != "unchanged" {
		t.Fatalf("%+v %v", result, err)
	}
	_, err = s.Import(ctx, site, january, fixture(t, "queries"))
	if err != nil {
		t.Fatal(err)
	}
	stats, err := NewStore(dir).CitationStats(ctx, site, january)
	if err != nil || stats.Pages.ExportedCitations != 1801 || stats.Queries.ExportedCitations != 701 {
		t.Fatalf("%+v %v", stats, err)
	}
	// A changed report replaces all old rows, including rows absent from the new export.
	result, err = s.Import(ctx, site, january, "Page,Citations\nhttps://example.test/new,5\n")
	if err != nil || result.Status != "replaced" {
		t.Fatalf("%+v %v", result, err)
	}
	stats, err = s.CitationStats(ctx, site, january)
	if err != nil || stats.Pages.ExportedCitations != 5 || stats.Pages.RowCount != 1 || stats.Queries.ExportedCitations != 701 {
		t.Fatalf("%+v %v", stats, err)
	}
	other := "https://other.test/"
	if _, err := s.Import(ctx, other, january, "Page,Citations\nhttps://other.test/a,99\n"); err != nil {
		t.Fatal(err)
	}
	otherStats, err := s.CitationStats(ctx, other, january)
	if err != nil || otherStats.Pages.ExportedCitations != 99 || otherStats.Queries != nil {
		t.Fatalf("%+v %v", otherStats, err)
	}
	for _, entry := range mustReadDir(t, dir) {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("snapshot permissions: %v", info.Mode())
		}
	}
}

func mustReadDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestInvalidImportDoesNotReplaceSnapshot(t *testing.T) {
	s := NewStore(t.TempDir())
	importPages(t, s, january, 10)
	for _, csvText := range []string{"Page,Citations\nhttps://example.test/a,2\nhttps://example.test/b,broken\n", "Page,Citations\nhttps://other.test/a,3\n"} {
		if _, err := s.Import(context.Background(), site, january, csvText); err == nil {
			t.Fatal("expected error")
		}
	}
	stats, err := s.CitationStats(context.Background(), site, january)
	if err != nil || stats.Pages.ExportedCitations != 10 {
		t.Fatalf("%+v %v", stats, err)
	}
	for _, p := range []Period{{}, {"2026-02-30", "2026-03-01"}, {"2026-2-01", "2026-03-01"}, {"2026-02-02", "2026-02-01"}} {
		if _, err := s.Import(context.Background(), site, p, "Page,Citations\n"); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
	for _, u := range []string{"../../escape", "https://example.test/?x=1", "https://example.test/#fragment"} {
		if _, err := s.Import(context.Background(), u, january, "Page,Citations\n"); err == nil {
			t.Fatalf("accepted %s", u)
		}
	}
}

func TestConcurrentStoresAndCanceledImport(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 1; i <= 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			date := fmt.Sprintf("2026-01-%02d", i)
			_, err := NewStore(dir).Import(context.Background(), site, Period{date, date}, "Page,Citations\nhttps://example.test/a,1\n")
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	trends, err := NewStore(dir).VisibilityTrends(context.Background(), site, Pages, january)
	if err != nil || len(trends.Points) != 12 {
		t.Fatalf("%+v %v", trends, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewStore(dir).Import(ctx, site, january, "Page,Citations\n"); err == nil {
		t.Fatal("canceled import succeeded")
	}
	if err := os.Mkdir(filepath.Join(dir, ".lock"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := NewStore(dir).CitationStats(ctx, site, january); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatal(err)
	}
}

func TestCorruptOrUnwritableStore(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	name := filepath.Join(dir, snapshotName(site, Pages, january))
	if err := os.WriteFile(name, []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CitationStats(context.Background(), site, january); err == nil {
		t.Fatal("ignored corruption")
	}
	if _, err := s.Import(context.Background(), site, january, "Page,Citations\n"); err == nil {
		t.Fatal("overwrote corruption")
	}
	if _, err := NewStore(name).Import(context.Background(), site, january, "Page,Citations\n"); err == nil {
		t.Fatal("accepted file as directory")
	}
}

func TestPagePropertyBoundaries(t *testing.T) {
	for _, tc := range []struct {
		site string
		page string
		ok   bool
	}{
		{"https://example.test/", "https://example.test", true},
		{"https://example.test/blog/", "https://example.test/blog/article", true},
		{"https://example.test/blog", "https://example.test/blog", true},
		{"https://example.test/blog/", "https://example.test/blogger/article", false},
		{"https://example.test/", "https://example.test.evil.test/article", false},
		{"https://example.test/", "https://sub.example.test/article", false},
		{"https://example.test/", "http://example.test/article", false},
	} {
		_, err := NewStore(t.TempDir()).Import(context.Background(), tc.site, january, "Page,Citations\n"+tc.page+",1\n")
		if (err == nil) != tc.ok {
			t.Fatalf("site=%s page=%s err=%v", tc.site, tc.page, err)
		}
	}
}
