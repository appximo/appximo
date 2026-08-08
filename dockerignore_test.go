package appximo

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The image build has died FOUR times on the same mechanism: a new
// `//go:embed docs/X.md` lands, `.dockerignore` still excludes `docs/`, and
// the Docker build fails with
//
//	pattern docs/X.md: no matching files found
//
// …only on the publish workflow, long after the unit lane went green. This
// test moves that check into the fast lane: every docs/ path any Go file in
// this module embeds must carry a matching `!docs/...` re-include.
func TestDockerignoreKeepsEmbeddedDocs(t *testing.T) {
	embedded := embeddedDocPaths(t)
	if len(embedded) == 0 {
		t.Fatal("found no //go:embed docs/... directives — the scan is broken, not the repo")
	}
	reincluded := map[string]bool{}
	f, err := os.Open(".dockerignore")
	if err != nil {
		t.Fatalf("open .dockerignore: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "!") {
			reincluded[strings.TrimPrefix(line, "!")] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read .dockerignore: %v", err)
	}
	for _, p := range embedded {
		if !reincluded[p] {
			t.Errorf("%s is //go:embed'd but .dockerignore does not re-include it — "+
				"the Docker image build will fail with \"pattern %s: no matching files found\". "+
				"Add a line: !%s", p, p, p)
		}
	}
}

// Every embedded doc must also actually exist — a stale path fails `go build`
// anyway, but naming it here makes the cause obvious in the lane.
func TestEmbeddedDocsExist(t *testing.T) {
	for _, p := range embeddedDocPaths(t) {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("//go:embed %s: %v", p, err)
		}
	}
}

var embedDocRe = regexp.MustCompile(`//go:embed\s+(docs/[^\s]+)`)

func embeddedDocPaths(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read module root: %v", err)
	}
	var out []string
	seen := map[string]bool{}
	for _, e := range entries {
		// Test files are skipped: nothing they contain is embedded into the
		// shipped binary, and this file's own prose names the directive.
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range embedDocRe.FindAllSubmatch(src, -1) {
			p := string(m[1])
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}
