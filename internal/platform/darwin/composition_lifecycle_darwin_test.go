package darwin

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativeCompositionLifecycleState(t *testing.T) {
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	replacements := make(map[string]string)
	for _, name := range []string{"composition_lifecycle_bridge_darwin.go", "composition_lifecycle_bridge_darwin_test.go"} {
		replacements[filepath.Join(directory, name)] = filepath.Join(directory, "testdata", name)
	}
	data, err := json.Marshal(struct {
		Replace map[string]string
	}{Replace: replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-race", "-overlay="+overlay,
		"-run", "^TestCompositionLifecycleProbe", "-count=1", "-v", ".")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native composition lifecycle: %v\n%s", err, output)
	}
	t.Logf("%s", output)
}
