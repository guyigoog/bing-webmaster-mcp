package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ncosentino/bing-webmaster-mcp/go/internal/aiperformance"
)

func aiSession(t *testing.T, dir string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	srv := newServer(nil, nil)
	if dir != "" {
		registerAIPerformanceTools(srv, aiperformance.NewStore(dir))
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "ai-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func aiCall(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	result, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("%s: %+v", name, result.Content)
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAIPerformanceMCPRoundTrip(t *testing.T) {
	dir := t.TempDir()
	session := aiSession(t, dir)
	baseline := aiSession(t, "")
	originalTools, err := baseline.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(originalTools.Tools) != 55 || len(tools.Tools) != 60 {
		t.Fatalf("original=%d extended=%d", len(originalTools.Tools), len(tools.Tools))
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	for _, tool := range originalTools.Tools {
		before, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		after, err := json.Marshal(byName[tool.Name])
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatalf("existing tool changed: %s", tool.Name)
		}
	}
	args := map[string]any{"site_url": "https://example.test/", "start_date": "2026-01-01", "end_date": "2026-01-31"}
	for _, fixture := range []string{"pages", "queries"} {
		data, err := os.ReadFile("internal/aiperformance/testdata/" + fixture + ".csv")
		if err != nil {
			t.Fatal(err)
		}
		args["csv_content"] = string(data)
		result := aiCall(t, session, "import_ai_performance_csv", args)
		if result["status"] != "imported" || result["report_type"] != fixture {
			t.Fatal(result)
		}
		result = aiCall(t, session, "import_ai_performance_csv", args)
		if result["status"] != "unchanged" {
			t.Fatal(result)
		}
	}
	delete(args, "csv_content")
	result := aiCall(t, session, "get_ai_citation_stats", args)
	if result["pages"].(map[string]any)["exported_citations"] != float64(1801) || result["queries"].(map[string]any)["exported_citations"] != float64(701) {
		t.Fatal(result)
	}
	args["limit"] = 1
	for _, name := range []string{"get_top_cited_pages", "get_ai_grounding_queries"} {
		result = aiCall(t, session, name, args)
		if len(result["rows"].([]any)) != 1 {
			t.Fatal(result)
		}
	}
	delete(args, "limit")
	args["report_type"] = "pages"
	result = aiCall(t, session, "get_ai_visibility_trends", args)
	if len(result["points"].([]any)) != 1 {
		t.Fatal(result)
	}
	delete(args, "report_type")
	args["csv_content"] = "Unsupported,Schema\na,1\n"
	bad, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "import_ai_performance_csv", Arguments: args})
	if err != nil || !bad.IsError {
		t.Fatalf("expected tool error: %+v %v", bad, err)
	}
	delete(args, "csv_content")
	delete(args, "start_date")
	bad, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_ai_citation_stats", Arguments: args})
	if err == nil && !bad.IsError {
		t.Fatal("missing required date was accepted")
	}
	// A new server process/store can read the durable data.
	args["start_date"] = "2026-01-01"
	aiCall(t, aiSession(t, dir), "get_ai_citation_stats", args)
}
