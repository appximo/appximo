//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitProjectCompiles — PUBLIC-SURFACE-S1 Part A: `appximo init` must emit
// a COMPLETE project that compiles as generated (the second field report found
// a go.mod + schema.json orphan: "sin main.go"). The generated go.mod is
// pointed at THIS working tree with a replace directive, so the test exercises
// the current library surface; everything else is exactly what init wrote.
func TestInitProjectCompiles(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	oldwd, _ := os.Getwd()
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd) //nolint:errcheck

	if err := initCmd.RunE(initCmd, []string{"demoapp"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	proj := filepath.Join(work, "demoapp")
	for _, f := range []string{"main.go", "go.mod", "schema.json", filepath.Join("web", "index.html"), ".gitignore"} {
		if _, err := os.Stat(filepath.Join(proj, f)); err != nil {
			t.Fatalf("init did not write %s: %v", f, err)
		}
	}

	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = proj
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
	}
	run("go", "mod", "edit", "-replace", "github.com/appximo/appximo="+repoRoot)
	run("go", "mod", "tidy")
	run("go", "build", "./...")
}
