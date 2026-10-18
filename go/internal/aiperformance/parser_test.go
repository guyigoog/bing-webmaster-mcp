package aiperformance

import (
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".csv")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestObservedExportFormats(t *testing.T) {
	kind, rows, err := Parse(fixture(t, "pages"))
	if err != nil || kind != Pages || len(rows) != 3 {
		t.Fatalf("%s %+v %v", kind, rows, err)
	}
	if rows[0].Page != "https://example.test/a/" || rows[0].Citations != 1039 {
		t.Fatal(rows)
	}
	kind, rows, err = Parse(fixture(t, "queries"))
	if err != nil || kind != Queries || len(rows) != 3 {
		t.Fatalf("%s %+v %v", kind, rows, err)
	}
	if rows[0].Query != `a "quoted", query` || rows[0].Intent != "Commercial" || rows[0].Topic != "Retail Sales & Promotions" || rows[0].CitationShare == nil || *rows[0].CitationShare != 28.48 {
		t.Fatal(rows[0])
	}
	if rows[1].Intent != "" || rows[1].Topic != "" {
		t.Fatal(rows[1])
	}
}

func TestParserRejectsUnsupportedOrInvalidData(t *testing.T) {
	tests := map[string]string{
		"empty":            "",
		"unknown schema":   "URL,Total Citations\nhttps://example.test/a,1\n",
		"extra column":     "Page,Citations,Date\nhttps://example.test/a,1,2026-01-01\n",
		"reordered header": "Citations,Page\n1,https://example.test/a\n",
		"duplicate header": "Page,Page\na,a\n",
		"negative":         "Page,Citations\nhttps://example.test/a,-1\n",
		"fraction":         "Page,Citations\nhttps://example.test/a,1.2\n",
		"separator":        "Page,Citations\nhttps://example.test/a,\"1,000\"\n",
		"overflow":         "Page,Citations\nhttps://example.test/a,9223372036854775808\n",
		"total overflow":   "Page,Citations\nhttps://example.test/a,9223372036854775807\nhttps://example.test/b,1\n",
		"empty count":      "Page,Citations\nhttps://example.test/a,\n",
		"empty key":        "Page,Citations\n,1\n",
		"duplicate row":    "Page,Citations\nhttps://example.test/a,1\nhttps://example.test/a,1\n",
		"invalid URL":      "Page,Citations\nfile:///etc/passwd,1\n",
		"URL credentials":  "Page,Citations\nhttps://user:secret@example.test/a,1\n",
		"bad quoting":      "Page,Citations\n\"https://example.test/a,1\n",
		"ragged":           "Page,Citations\nhttps://example.test/a\n",
		"bad utf8":         "Page,Citations\nhttps://example.test/\xff,1\n",
		"oversize":         strings.Repeat("a", MaxCSVBytes+1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Parse(input); err == nil {
				t.Fatal("accepted invalid CSV")
			}
		})
	}
	for _, share := range []string{"101%", "-1%", "NaN%", "Inf%", "20", "2.3.4%", "%", "1e2%"} {
		if _, _, err := Parse("Grounding Query,Intent,Topic,Citations,Citation Share\nquery,,,1," + share + "\n"); err == nil {
			t.Fatalf("accepted %s", share)
		}
	}
}

func TestEmptyReportAndOptionalShare(t *testing.T) {
	_, rows, err := Parse("Page,Citations\r\n")
	if err != nil || len(rows) != 0 {
		t.Fatalf("%v %v", rows, err)
	}
	_, rows, err = Parse("Grounding Query,Intent,Topic,Citations,Citation Share\nquery,,,0,\n")
	if err != nil || rows[0].CitationShare != nil || rows[0].Citations != 0 {
		t.Fatalf("%v %v", rows, err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add("Page,Citations\nhttps://example.test/a,1\n")
	f.Add("Grounding Query,Intent,Topic,Citations,Citation Share\nquery,,,1,10%\n")
	f.Fuzz(func(t *testing.T, input string) {
		kind, rows, err := Parse(input)
		if err != nil {
			return
		}
		if kind != Pages && kind != Queries {
			t.Fatal(kind)
		}
		if _, err := summary(Snapshot{Rows: rows}); err != nil {
			t.Fatal(err)
		}
	})
}
