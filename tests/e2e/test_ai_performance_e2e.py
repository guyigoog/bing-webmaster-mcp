"""Optional Go AI tools through the compiled server's stdio transport."""

import json
import os

import pytest

from conftest import IMPLEMENTATIONS, REPO_ROOT
from mcp_stdio_client import McpClient


def go_client(directory, mode):
    binary = IMPLEMENTATIONS["go"]
    if not binary.exists():
        pytest.skip("Go binary not built")
    env = dict(os.environ)
    env["BING_WEBMASTER_API_KEY"] = "dummy-ai-test-key"
    env.pop("BING_AI_PERFORMANCE_DIR", None)
    command = [str(binary)]
    if mode == "env":
        env["BING_AI_PERFORMANCE_DIR"] = str(directory)
    elif mode == "flag":
        env["BING_AI_PERFORMANCE_DIR"] = str(directory / "unused")
        command += ["--ai-performance-dir", str(directory)]
    else:
        env["BING_AI_PERFORMANCE_DIR"] = str(directory)
        command += ["--ai-performance-dir", ""]
    client = McpClient(command, env)
    client.initialize()
    return client


def call(client, name, args):
    response = client.call_tool(name, args)
    assert "error" not in response, response
    assert not response["result"].get("isError"), response
    return json.loads(response["result"]["content"][0]["text"])


@pytest.mark.parametrize("mode", ["env", "flag"])
def test_ai_tools_and_restart_persistence(tmp_path, mode):
    args = {
        "site_url": "https://example.test/",
        "start_date": "2026-09-01",
        "end_date": "2026-09-30",
    }
    client = go_client(tmp_path, mode)
    try:
        assert len(client.list_tools()["result"]["tools"]) == 60
        for report in ("pages", "queries"):
            path = REPO_ROOT / "go/internal/aiperformance/testdata" / f"{report}.csv"
            result = call(client, "import_ai_performance_csv", {
                **args, "csv_content": path.read_bytes().decode("utf-8"),
            })
            assert result["status"] == "imported"
            assert result["report_type"] == report
        stats = call(client, "get_ai_citation_stats", args)
        assert stats["pages"]["exported_citations"] == 1801
        assert stats["queries"]["exported_citations"] == 701
        for tool in ("get_top_cited_pages", "get_ai_grounding_queries"):
            assert len(call(client, tool, {**args, "limit": 1})["rows"]) == 1
        trends = call(client, "get_ai_visibility_trends", {**args, "report_type": "pages"})
        assert trends["points"][0]["days"] == 30
        assert trends["points"][0]["per_day_change_percent"] is None
        bad = client.call_tool("import_ai_performance_csv", {**args, "csv_content": "unknown\n"})
        assert bad["result"]["isError"] is True
    finally:
        client.close()

    restarted = go_client(tmp_path, mode)
    try:
        assert call(restarted, "get_ai_citation_stats", args)["pages"]["exported_citations"] == 1801
    finally:
        restarted.close()
    assert not (tmp_path / "unused").exists()


def test_ai_extension_can_be_disabled_with_flag(tmp_path):
    directory = tmp_path / "disabled"
    client = go_client(directory, "disabled")
    try:
        assert len(client.list_tools()["result"]["tools"]) == 55
    finally:
        client.close()
    assert not directory.exists()
