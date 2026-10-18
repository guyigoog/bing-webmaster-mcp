# Fixture provenance

The column names, order, encoding (UTF-8 BOM), quoting and CRLF line endings
match real Bing `AIPageStatsReport` and `AISearchQueriesReport` files downloaded
on 2026-09-30 and inspected locally on 2026-10-18. The exports have no period
metadata or per-day rows. Their selected date ranges are unknown.

These fixtures contain three representative rows per format. Domains, paths
and query text are replaced with synthetic values. Query text additionally
exercises embedded quotes and commas. The representative counts, percentage
format, and blank intent/topic fields preserve observed data types. No private
source exports are committed. These are parser fixtures, not complete reports
or evidence of a site's performance.
