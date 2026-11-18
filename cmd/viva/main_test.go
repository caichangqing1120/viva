package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestAddCopiesSelectedComponentSource(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "ui")
	var output bytes.Buffer
	if err := run([]string{"add", "--out", destination, "--package", "design", "button", "card"}, &output); err != nil {
		t.Fatal(err)
	}
	button, err := os.ReadFile(filepath.Join(destination, "button.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(button), "package design") || !strings.Contains(string(button), "func BeginButton") {
		t.Fatalf("unexpected generated button:\n%s", button)
	}
	if _, err := os.Stat(filepath.Join(destination, "card.go")); err != nil {
		t.Fatal(err)
	}
}

func TestListCoversEveryReferenceComponent(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"list"}, &output); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join("..", "..", "components", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, file := range files {
		base := filepath.Base(file)
		if strings.HasSuffix(base, "_test.go") || base == "doc.go" {
			continue
		}
		want = append(want, strings.ReplaceAll(strings.TrimSuffix(base, ".go"), "_", "-"))
	}
	sort.Strings(want)
	got := strings.Fields(output.String())
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("component list = %v, want every component file: %v", got, want)
	}
}

// The installable sources and the table of what each needs are generated from
// the components package. A change to a component that is not followed by
// `go generate ./cmd/viva` leaves them stale.
func TestGeneratedRecipesAreCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("type checks the components package")
	}
	command := exec.Command("go", "run", "../../tools/componentgen", "-root", "../..", "-check")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

func TestInstalledRecipesMatchReferenceSource(t *testing.T) {
	for name, template := range componentFiles {
		sourcePath := filepath.Join("..", "..", "components", strings.TrimSuffix(template, ".txt"))
		source, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		installed, err := componentTemplates.ReadFile("templates/" + template)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(string(source), "package components", "package {{PACKAGE}}", 1)
		if string(installed) != want {
			t.Errorf("%s installer template differs from its reference component", name)
		}
	}
}

func TestDocumentedRecipesCompileAfterInstallation(t *testing.T) {
	destination := t.TempDir()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module recipe-test\n\ngo 1.26\n\nrequire github.com/vivaui/viva v0.0.0\nreplace github.com/vivaui/viva => %q\n", filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(destination, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"add", "--out", destination, "--package", "recipes", "button", "badge", "checkbox", "switch", "input", "dialog"}, ioDiscard{}); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-trimpath", "-mod=mod", "./...")
	command.Dir = destination
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("installed documentation recipes do not compile: %v\n%s", err, output)
	}
}

// Each recipe must compile with only what viva add installs for it, so a helper
// shared with another file shows up as a requirement instead of a build error in
// somebody's project.
func TestEveryRecipeCompilesInstalledOnItsOwn(t *testing.T) {
	if testing.Short() {
		t.Skip("builds every recipe")
	}
	destination := t.TempDir()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module recipe-test\n\ngo 1.26\n\nrequire github.com/vivaui/viva v0.0.0\nreplace github.com/vivaui/viva => %q\n", filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(destination, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range componentNames() {
		directory := filepath.Join(destination, strings.ReplaceAll(name, "-", "_"))
		if err := run([]string{"add", "--out", directory, "--package", "recipe", name}, ioDiscard{}); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	command := exec.Command("go", "build", "-trimpath", "-mod=mod", "./...")
	command.Dir = destination
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("a recipe does not compile on its own: %v\n%s", err, output)
	}
}

func TestAddRefusesOverwriteWithoutForce(t *testing.T) {
	destination := t.TempDir()
	arguments := []string{"add", "--out", destination, "button"}
	if err := run(arguments, ioDiscard{}); err != nil {
		t.Fatal(err)
	}
	if err := run(arguments, ioDiscard{}); err == nil {
		t.Fatal("second add unexpectedly replaced existing source")
	}
	if err := run([]string{"add", "--force", "--out", destination, "button"}, ioDiscard{}); err != nil {
		t.Fatal(err)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(value []byte) (int, error) { return len(value), nil }

func TestAddingARecipeAlsoInstallsWhatItCallsInto(t *testing.T) {
	destination := t.TempDir()
	var output bytes.Buffer
	if err := run([]string{"add", "--out", destination, "--package", "recipes", "dialog"}, &output); err != nil {
		t.Fatal(err)
	}
	files := []string{"button.go", "alert_dialog.go", "dialog.go"}
	for _, file := range files {
		if _, err := os.Stat(filepath.Join(destination, file)); err != nil {
			t.Errorf("adding dialog did not install %s: %v", file, err)
		}
	}
	lines := strings.Fields(output.String())
	for index, line := range lines {
		lines[index] = filepath.Base(line)
	}
	if strings.Join(lines, ",") != strings.Join(files, ",") {
		t.Errorf("installed files = %q, want %q", lines, files)
	}
	// The installed recipes compile on their own.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module recipe-test\n\ngo 1.26\n\nrequire github.com/vivaui/viva v0.0.0\nreplace github.com/vivaui/viva => %q\n", filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(destination, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "build", "-trimpath", "-mod=mod", "./...")
	command.Dir = destination
	command.Env = append(os.Environ(), "GOWORK=off")
	if built, err := command.CombinedOutput(); err != nil {
		t.Fatalf("dialog installed alone does not compile: %v\n%s", err, built)
	}
}

func TestARequiredRecipeThatIsAlreadyInstalledIsLeftAlone(t *testing.T) {
	destination := t.TempDir()
	custom := filepath.Join(destination, "button.go")
	if err := os.WriteFile(custom, []byte("package recipes // customized\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"add", "--out", destination, "--package", "recipes", "dialog"}, ioDiscard{}); err != nil {
		t.Fatalf("a customized button.go made adding dialog fail: %v", err)
	}
	if kept, _ := os.ReadFile(custom); string(kept) != "package recipes // customized\n" {
		t.Fatalf("the customized button.go was replaced:\n%s", kept)
	}
	if _, err := os.Stat(filepath.Join(destination, "dialog.go")); err != nil {
		t.Fatal(err)
	}
	// Asking for it by name still refuses to overwrite.
	if err := run([]string{"add", "--out", destination, "--package", "recipes", "button"}, ioDiscard{}); err == nil {
		t.Fatal("an explicitly requested recipe overwrote existing source")
	}
}

func TestRequirementsComeFirstAndAreListedOnce(t *testing.T) {
	got := withRequirements([]string{"card", "dialog", "button", "dialog"})
	if strings.Join(got, ",") != "card,button,alert-dialog,dialog" {
		t.Fatalf("order = %v", got)
	}
}
