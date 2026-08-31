package main

// The operator's manual and the engine index (docs/MANUAL_OPERACION.md,
// docs/ESTADO_DEL_MOTOR.md — MANUAL-OPERACION-S1) are written by hand; this
// test is what keeps them from rotting: every `appximo drill <x>` they name
// must be a real subcommand, every docs/*.md they link must exist, and every
// APPXIMO_* variable they mention must be read somewhere in the source tree.
// A row can still be WRONG in prose — but it can no longer point at nothing.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoRootForDocs(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "MANUAL_OPERACION.md")); err != nil {
		t.Skip("docs/ not next to this package (a module consumer's build) — nothing to check")
	}
	return root
}

func TestOperatorDocsDoNotRot(t *testing.T) {
	root := repoRootForDocs(t)
	docs := []string{"docs/MANUAL_OPERACION.md", "docs/ESTADO_DEL_MOTOR.md"}

	// the drill subcommands this binary actually has
	have := map[string]bool{}
	for _, c := range drillCmd.Commands() {
		have[strings.Fields(c.Use)[0]] = true
	}

	// every APPXIMO_* / RATE_LIMIT_* / BACKUP_* / DB_MAX_CONNS the source tree reads
	source := map[string]bool{}
	envRe := regexp.MustCompile(`\b(APPXIMO_[A-Z0-9_]+|RATE_LIMIT_[A-Z_]+|BACKUP_[A-Z_]+|DB_MAX_CONNS|SLACK_WEBHOOK_URL|GOMEMLIMIT|GOMAXPROCS|OBS_DB_PATH)\b`)
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "docs", "site", "benchmarks", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if !(strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".sh")) || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		for _, m := range envRe.FindAllString(string(b), -1) {
			source[m] = true
		}
		return nil
	})

	drillRe := regexp.MustCompile("`?appximo drill ([a-z]+)")
	linkRe := regexp.MustCompile(`\]\(([^)#\s]+\.md)(#[^)]*)?\)`)
	for _, doc := range docs {
		b, err := os.ReadFile(filepath.Join(root, doc))
		if err != nil {
			t.Fatalf("%s: %v", doc, err)
		}
		text := string(b)
		for _, m := range drillRe.FindAllStringSubmatch(text, -1) {
			if !have[m[1]] {
				t.Errorf("%s names `appximo drill %s`, which is not a subcommand (have: %v)", doc, m[1], keys(have))
			}
		}
		for _, m := range linkRe.FindAllStringSubmatch(text, -1) {
			target := m[1]
			if strings.HasPrefix(target, "http") {
				continue
			}
			p := filepath.Join(root, "docs", target)
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s links %s, which does not exist", doc, target)
			}
		}
		// a variable in a `code span` that the source never reads is a typo or a
		// removed knob — either way the reader would set it and nothing would happen
		codeRe := regexp.MustCompile("`(APPXIMO_[A-Z0-9_]+|RATE_LIMIT_[A-Z_]+|BACKUP_[A-Z_]+|DB_MAX_CONNS|SLACK_WEBHOOK_URL|GOMEMLIMIT|OBS_DB_PATH)`")
		for _, m := range codeRe.FindAllStringSubmatch(text, -1) {
			v := m[1]
			if strings.HasSuffix(v, "_*") || strings.Contains(v, "*") {
				continue
			}
			if !source[v] {
				t.Errorf("%s mentions `%s`, which no .go/.sh in the tree reads", doc, v)
			}
		}
		for _, img := range regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`).FindAllStringSubmatch(text, -1) {
			if _, err := os.Stat(filepath.Join(root, "docs", img[1])); err != nil {
				t.Errorf("%s embeds %s, which does not exist", doc, img[1])
			}
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
