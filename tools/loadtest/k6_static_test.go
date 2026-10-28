// Package loadtest holds the k6 scenario scripts (D-37, GF-337). k6 is not
// installed on every host, so these tests validate the scripts statically: the
// thresholds, the balance of brackets, and, when node is present, the syntax.
package loadtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// k6Dir is the script directory, relative to this package.
const k6Dir = "k6"

// scenarioScript pins one script's threshold expressions to its roadmap §5 and
// README §9 row (GF-337). Each expression must appear verbatim in the file.
type scenarioScript struct {
	file string
	want []string
}

var scripts = []scenarioScript{
	{file: "ws-fanout.js", want: []string{
		"freshness_ms: ['p(95)<2000']",
		"ws_connecting: ['p(95)<1000']",
		"checks: ['rate>0.99']",
	}},
	{file: "assign.js", want: []string{
		"'http_req_duration{name:assign}': ['p(95)<300']",
		"'http_req_failed': ['rate<0.01']",
	}},
	{file: "report.js", want: []string{
		"'http_req_duration{name:report}': ['p(95)<300']",
		"'http_req_failed': ['rate<0.01']",
	}},
	{file: "ingest-steady.js", want: []string{
		"executor: 'constant-arrival-rate'",
		"rate: 100,",
		"http_req_failed: ['rate<0.001']",
		"http_req_duration: ['p(95)<300']",
		"dropped_iterations: ['count==0']",
	}},
	{file: "ingest-burst.js", want: []string{
		"executor: 'ramping-arrival-rate'",
		"{ target: 2000, duration: '30s' }",
		"http_req_failed: ['rate<0.01']",
		"heap_inuse_bytes: ['max>0']",
		"heap_within_2x: ['rate>=1']",
		"go_memstats_heap_inuse_bytes",
	}},
}

// readScript returns one script's text, failing the test when it is missing.
func readScript(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(k6Dir, rel)) //nolint:gosec // fixed script paths in this repo, not user input
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(body)
}

// TestK6ScriptsHaveThresholds: every scenario script has its own options and a
// thresholds block with the exact expressions of its §5 row (GF-337, check j).
func TestK6ScriptsHaveThresholds(t *testing.T) {
	for _, s := range scripts {
		body := readScript(t, s.file)
		if !strings.Contains(body, "export const options") {
			t.Errorf("%s: no `export const options`", s.file)
		}
		if !strings.Contains(body, "thresholds: {") {
			t.Errorf("%s: no thresholds block", s.file)
		}
		for _, want := range s.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing threshold %q", s.file, want)
			}
		}
	}
}

// TestK6ScriptsBalanced: brackets, braces and parens nest correctly in every
// script and in lib/config.js.
func TestK6ScriptsBalanced(t *testing.T) {
	files := []string{"lib/config.js"}
	for _, s := range scripts {
		files = append(files, s.file)
	}
	pairs := map[rune]rune{')': '(', ']': '[', '}': '{'}
	for _, f := range files {
		var stack []rune
		line := 1
		for _, r := range readScript(t, f) {
			switch r {
			case '\n':
				line++
			case '(', '[', '{':
				stack = append(stack, r)
			case ')', ']', '}':
				if len(stack) == 0 || stack[len(stack)-1] != pairs[r] {
					t.Fatalf("%s line %d: unbalanced %q", f, line, r)
				}
				stack = stack[:len(stack)-1]
			}
		}
		if len(stack) != 0 {
			t.Fatalf("%s: %d unclosed brackets, last %q", f, len(stack), stack[len(stack)-1])
		}
	}
}

// TestK6ScriptsParseWithNode: each script parses as an ES module. The check is
// skipped with a logged reason when node is not on PATH (GF-346).
func TestK6ScriptsParseWithNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not on PATH, syntax check skipped (GF-346): %v", err)
	}
	files := []string{"lib/config.js"}
	for _, s := range scripts {
		files = append(files, s.file)
	}
	for _, f := range files {
		// node --check reads the file; .mjs makes it an ES module, as k6 treats it.
		tmp := filepath.Join(t.TempDir(), strings.ReplaceAll(filepath.Base(f), ".js", ".mjs"))
		if err := os.WriteFile(tmp, []byte(readScript(t, f)), 0o600); err != nil {
			t.Fatalf("write temp copy of %s: %v", f, err)
		}
		//nolint:gosec // node from PATH checks a temp copy this test wrote
		if out, err := exec.Command(node, "--check", tmp).CombinedOutput(); err != nil {
			t.Errorf("%s: node --check: %v\n%s", f, err, out)
		}
	}
}
