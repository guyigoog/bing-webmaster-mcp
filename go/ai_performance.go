package main

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ncosentino/bing-webmaster-mcp/go/internal/aiperformance"
)

type aiPeriodInput struct {
	SiteURL   string `json:"site_url" jsonschema:"Site selected for the export. Use the same site for all reports."`
	StartDate string `json:"start_date" jsonschema:"Inclusive report start date in YYYY-MM-DD; not the download date."`
	EndDate   string `json:"end_date" jsonschema:"Inclusive report end date in YYYY-MM-DD; not the download date."`
}

func (i aiPeriodInput) period() aiperformance.Period {
	return aiperformance.Period{Start: i.StartDate, End: i.EndDate}
}

type aiImportInput struct {
	aiPeriodInput
	CSVContent string `json:"csv_content" jsonschema:"Complete UTF-8 CSV text (maximum 8 MiB). Supported headers: Page,Citations or Grounding Query,Intent,Topic,Citations,Citation Share. Import whole unfiltered reports. Same site/type/period replaces the prior snapshot."`
}

type aiRankInput struct {
	aiPeriodInput
	Limit int `json:"limit,omitempty" jsonschema:"Maximum rows from 1 to 1000; defaults to 20."`
}

type aiTrendInput struct {
	aiPeriodInput
	ReportType string `json:"report_type" jsonschema:"pages or queries; never combine the two report types."`
}

func registerAIPerformanceTools(srv *mcp.Server, store *aiperformance.Store) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "import_ai_performance_csv",
		Description: "Import a real Bing AI Performance CSV as a local snapshot. Requires the dashboard date range because exports omit dates. Replaces the same site/type/period atomically; rejects unknown schemas. CSV text only; does not read arbitrary files or contact Bing.",
	}, aiToolHandler(func(ctx context.Context, i aiImportInput) (any, error) {
		return store.Import(ctx, i.SiteURL, i.period(), i.CSVContent)
	}))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_ai_citation_stats",
		Description: "Summarize exported citations for an exact imported period. Page and query totals are separate, not dashboard-wide totals. A missing report is null, not zero.",
	}, aiToolHandler(func(ctx context.Context, i aiPeriodInput) (any, error) {
		return store.CitationStats(ctx, i.SiteURL, i.period())
	}))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_top_cited_pages",
		Description: "Rank cited pages from an exact imported report period by exported citation count.",
	}, aiToolHandler(func(ctx context.Context, i aiRankInput) (any, error) {
		return store.Top(ctx, i.SiteURL, aiperformance.Pages, i.period(), i.Limit)
	}))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_ai_grounding_queries",
		Description: "Rank grounding queries from an exact imported period, preserving intent, topic and citation share percentages. Does not infer query-to-page relationships.",
	}, aiToolHandler(func(ctx context.Context, i aiRankInput) (any, error) {
		return store.Top(ctx, i.SiteURL, aiperformance.Queries, i.period(), i.Limit)
	}))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_ai_visibility_trends",
		Description: "Compare non-overlapping whole imported periods within a date window for pages or queries. Returns period averages and percent changes, not daily observations. Rejects overlapping or partially selected snapshots.",
	}, aiToolHandler(func(ctx context.Context, i aiTrendInput) (any, error) {
		return store.VisibilityTrends(ctx, i.SiteURL, aiperformance.ReportType(i.ReportType), i.period())
	}))
}

// Keep the existing API tools' response contract unchanged while marking local errors.
func aiToolHandler[T any](fn func(context.Context, T) (any, error)) func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input T) (*mcp.CallToolResult, any, error) {
		value, err := fn(ctx, input)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
	}
}
