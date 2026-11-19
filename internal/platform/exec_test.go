package platform

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Starting a child anywhere but Command is how the black console windows came
// back once: Command is the one place that decides a child's process group and,
// on Windows, that it gets no console of its own. A `go build` from a terminal
// hides the mistake — the child inherits that terminal's console — so only the
// built app showed it, and only on someone else's screen.
func TestProcessesAreSpawnedOnlyByCommand(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	skipDir := map[string]bool{
		".git":               true,
		"testdata":           true,
		"tailcat-tool-swift": true,
		"dist":               true,
	}
	// The file that builds them, and the tool that exists to prove the flags.
	allowed := map[string]bool{
		"internal/platform/exec.go": true,
	}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// The root's own name is "..", so it is compared by path.
			if path == root {
				return nil
			}
			if skipDir[entry.Name()] || strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if allowed[rel] {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "exec.Command(") || strings.Contains(string(data), "exec.CommandContext(") {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("these files start a process without platform.Command, so it gets a console window on Windows: %v", offenders)
	}
}
