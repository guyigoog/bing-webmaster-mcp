package aiperformance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// Period is the inclusive date range selected in the dashboard, not the download date.
type Period struct {
	Start string `json:"start_date"`
	End   string `json:"end_date"`
}

func (p Period) validate() error {
	for _, s := range []string{p.Start, p.End} {
		d, err := time.Parse(time.DateOnly, s)
		if err != nil || d.Year() < 1 || d.Format(time.DateOnly) != s {
			return fmt.Errorf("start_date and end_date must be YYYY-MM-DD")
		}
	}
	if p.Start > p.End {
		return fmt.Errorf("start_date must not be after end_date")
	}
	return nil
}

// Snapshot contains one whole report; overlapping snapshots are never added together.
type Snapshot struct {
	Version    int        `json:"version"`
	SiteURL    string     `json:"site_url"`
	ReportType ReportType `json:"report_type"`
	Period     Period     `json:"period"`
	ImportedAt string     `json:"imported_at"`
	Rows       []Row      `json:"rows"`
}

// ImportResult describes the durable import operation.
type ImportResult struct {
	Status     string     `json:"status"`
	SiteURL    string     `json:"site_url"`
	ReportType ReportType `json:"report_type"`
	Period     Period     `json:"period"`
	RowCount   int        `json:"row_count"`
}

// Store holds snapshots in a dedicated local directory. It is safe for concurrent processes.
// Configure one directory per trusted user; this is not a multi-tenant database.
type Store struct{ dir string }

// NewStore constructs a store without touching disk until the first operation.
func NewStore(dir string) *Store { return &Store{dir: dir} }

func normalizeSite(site string) (string, error) {
	u, err := httpURL(site)
	if err != nil {
		return "", fmt.Errorf("invalid site_url: %w", err)
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("site_url must not contain a query")
	}
	u.Host = strings.ToLower(u.Host)
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

func snapshotName(site string, kind ReportType, p Period) string {
	h := sha256.Sum256([]byte(site + "\n" + string(kind) + "\n" + p.Start + "\n" + p.End))
	return hex.EncodeToString(h[:]) + ".json"
}

func (s *Store) lock(ctx context.Context) (func(), error) {
	if s.dir == "" {
		return nil, fmt.Errorf("AI Performance storage directory is not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lockPath := filepath.Join(s.dir, ".lock")
	for {
		err := os.Mkdir(lockPath, 0700)
		if err == nil {
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("AI Performance store is locked; if a process crashed, stop all users of the store before removing .lock: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Import validates the entire CSV before atomically replacing one site/type/period snapshot.
func (s *Store) Import(ctx context.Context, site string, period Period, csvText string) (ImportResult, error) {
	var result ImportResult
	site, err := normalizeSite(site)
	if err != nil {
		return result, err
	}
	if err := period.validate(); err != nil {
		return result, err
	}
	kind, rows, err := Parse(csvText)
	if err != nil {
		return result, err
	}
	siteURL, _ := httpURL(site)
	for _, row := range rows {
		if kind == Pages {
			u, _ := httpURL(row.Page)
			if u.Path == "" {
				u.Path = "/"
			}
			prefix := strings.TrimSuffix(siteURL.EscapedPath(), "/") + "/"
			if !strings.EqualFold(u.Host, siteURL.Host) || u.Scheme != siteURL.Scheme || (u.EscapedPath() != siteURL.EscapedPath() && !strings.HasPrefix(u.EscapedPath(), prefix)) {
				return result, fmt.Errorf("page URL is outside site_url; select the correct site for this export")
			}
		}
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return result, err
	}
	defer unlock()
	name := snapshotName(site, kind, period)
	result = ImportResult{Status: "imported", SiteURL: site, ReportType: kind, Period: period, RowCount: len(rows)}
	old, err := s.read(name)
	if err == nil {
		if reflect.DeepEqual(old.Rows, rows) {
			result.Status = "unchanged"
			return result, nil
		}
		result.Status = "replaced"
	} else if !errors.Is(err, os.ErrNotExist) {
		return ImportResult{}, err
	}
	snapshot := Snapshot{Version: 1, SiteURL: site, ReportType: kind, Period: period, ImportedAt: time.Now().UTC().Format(time.RFC3339Nano), Rows: rows}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return ImportResult{}, err
	}
	if err := s.write(ctx, name, data); err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func (s *Store) write(ctx context.Context, name string, data []byte) error {
	f, err := os.CreateTemp(s.dir, ".import-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(s.dir, name))
}

func (s *Store) read(name string) (Snapshot, error) {
	var snapshot Snapshot
	data, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		return snapshot, err
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return snapshot, fmt.Errorf("invalid stored snapshot: %w", err)
	}
	if snapshot.Version != 1 || snapshot.Period.validate() != nil || (snapshot.ReportType != Pages && snapshot.ReportType != Queries) || snapshotName(snapshot.SiteURL, snapshot.ReportType, snapshot.Period) != name {
		return snapshot, fmt.Errorf("invalid or unsupported stored snapshot")
	}
	if _, err := summary(snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}
