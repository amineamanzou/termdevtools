package parser

import (
	"strings"
	"testing"
)

const sample = `# cheatsheet
GET _cat/indices?v

POST _search
{
  "query": { "match_all": {} }
}

# une requête sans corps
DELETE my-index
`

func TestParseAll(t *testing.T) {
	reqs := ParseAll(sample)
	if len(reqs) != 3 {
		t.Fatalf("expected 3 requests, got %d: %+v", len(reqs), reqs)
	}

	if reqs[0].Method != "GET" || reqs[0].Path != "_cat/indices?v" || reqs[0].Body != nil {
		t.Errorf("unexpected first request: %+v", reqs[0])
	}

	if reqs[1].Method != "POST" || reqs[1].Path != "_search" {
		t.Errorf("unexpected second request: %+v", reqs[1])
	}
	if err := ValidateBody(reqs[1].Body); err != nil {
		t.Errorf("expected valid JSON body, got error: %v (body=%q)", err, reqs[1].Body)
	}

	if reqs[2].Method != "DELETE" || reqs[2].Path != "my-index" || reqs[2].Body != nil {
		t.Errorf("unexpected third request: %+v", reqs[2])
	}
}

func TestRequestAtLine(t *testing.T) {
	// Line of the second request's JSON body (index 5 = `"query": ...`)
	r, err := RequestAtLine(sample, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Method != "POST" {
		t.Errorf("expected POST request at line 5, got %+v", r)
	}

	// Blank line between two requests (index 2) -> should fall back to the previous request (GET)
	r, err = RequestAtLine(sample, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Method != "GET" {
		t.Errorf("expected fallback to GET request at line 2, got %+v", r)
	}
}

func TestValidateBodyInvalid(t *testing.T) {
	if err := ValidateBody([]byte(`{"a":}`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestRequestAtLineRejectsInvalidCursorLine(t *testing.T) {
	for _, line := range []string{"GET", "GET /_cluster/health extra", "HEAD /index", "notes", "GET /index # comment"} {
		t.Run(line, func(t *testing.T) {
			if req, err := RequestAtLine("DELETE /index\n\n"+line, 2); err == nil {
				t.Fatalf("invalid cursor line selected another request: %+v", req)
			}
		})
	}
}

func TestRequestAtLineKeepsBlankAndCommentFallback(t *testing.T) {
	for _, text := range []string{"DELETE /index\n\n", "DELETE /index\n\n# notes", "# notes\n\nGET /_cluster/health"} {
		line := 2
		want := "DELETE"
		if strings.HasPrefix(text, "#") {
			line, want = 0, "GET"
		}
		req, err := RequestAtLine(text, line)
		if err != nil || req.Method != want {
			t.Fatalf("blank/comment fallback: req=%+v err=%v, want %s", req, err, want)
		}
	}
}
