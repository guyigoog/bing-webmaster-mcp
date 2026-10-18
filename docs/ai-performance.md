# AI Performance CSV analytics (Go fork)

The Go server can import Bing AI Performance CSV exports and analyze them locally.
This is an opt-in extension: the original 55 tools remain unchanged, and enabling
it adds five tools for a total of 60. The C# server is unchanged. No AI Performance
API endpoint, browser session, or new dependency is required.

## Verified export formats

The importer was developed against two actual Bing exports obtained on
September 30, 2026, inspected locally during development:

| Report | Exact header order |
| --- | --- |
| `AIPageStatsReport` | `Page,Citations` |
| `AISearchQueriesReport` | `Grounding Query,Intent,Topic,Citations,Citation Share` |

Both samples use UTF-8 with a BOM, quoted fields, CRLF line endings and integer
citation counts. Query rows can have blank intent and topic fields. Citation
share is a percentage such as `28.48%`; the output represents that as `28.48`,
not `0.2848`. Blank share fields are preserved as unavailable, never as zero.
Tests use anonymized, representative rows preserving these headers and formats.
Private exports are not included in the repository.

Neither sample contains report dates, daily observations, or a query-to-page
mapping. The download date in a filename does **not** establish the selected
reporting window. Supply the inclusive start and end dates selected in Bing.
Unknown, additional, reordered, or localized headers are rejected explicitly.
Support for another format requires a real sample and corresponding tests.
This release does not accept XLSX, ZIP, multi-section exports or a daily CSV schema.

Microsoft describes the report's metrics in its
[AI Performance announcement](https://blogs.bing.com/webmaster/2026/2/Introducing-AI-Performance-in-Bing-Webmaster-Tools-Public-Preview/).
Parser compatibility is based on the observed files above, not inferred from
dashboard labels or that article.

## Enable the tools

Build this fork's Go server and give it a dedicated, persistent local directory:

```bash
cd go
go build -o bin/bwt-mcp-go .
./bin/bwt-mcp-go --ai-performance-dir /absolute/path/to/bing-ai-data
```

The existing Bing API key configuration is still required at startup.
Alternatively, set `BING_AI_PERFORMANCE_DIR` in the server process environment.
The CLI flag takes precedence; `--ai-performance-dir ""` disables the extension
even if the environment variable is set. This setting is not read from `.env`.
Paths are interpreted on the machine running the server. Prefer an absolute path
to avoid different stores when the working directory changes.

When no directory is configured, there are no AI tools or storage side effects.
Both stdio and HTTP support the extension. An HTTP deployment shares its store
among all callers; enable it only for a single trusted user/account boundary.
The `site_url` argument partitions stored data but is not an authorization check.

## Tools and examples

All tools require `site_url`, `start_date` and `end_date`. Dates use `YYYY-MM-DD`.
Use a consistent site property and import whole **unfiltered** exports with
consistent dashboard settings; filters cannot be recovered from these CSVs.

| Tool | Additional arguments | Result |
| --- | --- | --- |
| `import_ai_performance_csv` | `csv_content` | Auto-detected report type, period, row count and `imported`, `replaced`, or `unchanged` status |
| `get_ai_citation_stats` | None | Separate page/query summaries for the exact period; missing reports are `null` |
| `get_top_cited_pages` | Optional `limit` (default 20, maximum 1000) | Pages ranked by citation count |
| `get_ai_grounding_queries` | Optional `limit` | Queries ranked by count, retaining intent, topic and citation share |
| `get_ai_visibility_trends` | `report_type`: `pages` or `queries` | Whole, non-overlapping snapshots inside the selected window, period averages and rate changes |

Synthetic example arguments for `import_ai_performance_csv`:

```json
{
  "site_url": "https://example.test/",
  "start_date": "2026-09-01",
  "end_date": "2026-09-30",
  "csv_content": "Page,Citations\nhttps://example.test/article/,120\n"
}
```

The importer takes CSV **text**, not a server-side file path. An MCP client can
read a user-selected file and pass its contents. This also works with HTTP
without exposing arbitrary server file reads. The maximum CSV size is 8 MiB
and the maximum row count is 100,000. There is no network access during import.

Then call `get_ai_citation_stats` with:

```json
{
  "site_url": "https://example.test/",
  "start_date": "2026-09-01",
  "end_date": "2026-09-30"
}
```

This returns `pages.exported_citations: 120`, `pages.row_count: 1`, and
`queries: null` until the corresponding query export is imported.

For a trend across independently imported September and October reports:

```json
{
  "site_url": "https://example.test/",
  "start_date": "2026-09-01",
  "end_date": "2026-10-31",
  "report_type": "pages"
}
```

## Correct interpretation and overlap handling

- Totals sum **exported rows**, which may be truncated by Bing. They are not
  asserted to equal the dashboard total. Page and query totals are independent;
  never add them together. Citation shares are neither summed nor averaged.
- Re-importing the same site, report type and period replaces the entire
  snapshot, including removal of rows missing from the new file. An identical
  parsed report (including reordered rows) is a no-op. An empty header-only
  report is a valid explicit empty snapshot and replaces prior rows.
- Different periods are retained independently, including overlapping periods.
  Exact-period stats and rankings do not merge them. The trend tool rejects
  overlaps and partially selected periods rather than pretending it can subtract
  duplicate days from aggregate counts. Use a window containing only disjoint
  snapshots, or a separate data directory for an alternative reporting cadence.
- Trend points expose inclusive period lengths and exported citations per day
  as a **period average**, not daily measured counts. Percentage changes compare
  these averages so different month lengths are explicit. The first change and
  any change from a zero baseline are `null`; gaps are not filled with zeroes.
- Page rows must belong to the supplied site's scheme, host and path prefix.
  Query exports contain no site identity, so the caller must supply the correct
  property. Host case and an omitted root slash are normalized; distinct schemes,
  subdomains and property paths remain separate.

## Persistence and recovery

Each snapshot is a versioned JSON file named using a hash of its site, report
type and dates. Files are created with mode `0600`; new storage directories use
`0700`. Use a private directory (existing directory permissions are not changed).
Keep it outside the source checkout and back it up as needed.

Validation completes before mutation. Imports use a temporary file, sync and
atomic rename, so readers do not see partially written reports. A directory
lock serializes imports and analytics across processes. Calls wait up to five
seconds for a lock or return early on cancellation. A crashed process may leave
`.lock`; stop every process using the store before removing that directory.
The server does not automatically break a potentially live lock. Files named
`.import-*` left by a crash are ignored and can be removed while stopped.

Invalid CSVs, dates, duplicate keys, negative/fractional/overflowing citation
counts, invalid shares and out-of-site pages return MCP tool errors without
replacing existing data. Corrupted/unknown-version snapshots produce errors;
they are not silently overwritten or interpreted as empty reports.

## Validation

```bash
cd go
go test -race ./...
go vet ./...
go test ./internal/aiperformance -run '^$' -fuzz FuzzParse -fuzztime 10s
go build -o bwt-mcp-go .
cd ..
python -m pytest tests/e2e -v
```

Go tests cover the observed schemas, malformed inputs, replacement/idempotency,
site isolation, concurrent stores, corruption, cancellation, rankings, overlap
rejection, period-rate comparisons and all five tools through a real MCP session.
The session test also compares the original 55 tool definitions with those in
the extended server. The stdio suite covers activation and restart persistence.
The C# binary is optional for local Go validation; its existing tests are skipped
when it has not been built.
