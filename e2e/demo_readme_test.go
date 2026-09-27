package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestDemoReadmeCopyBackIncludesGeneratedFiles(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "docs", "demo", "README.md"))
	if err != nil {
		t.Fatalf("read demo README: %v", err)
	}

	text := string(readme)
	generated := make(map[string]struct{})
	redirectedOutput := regexp.MustCompile(`(?m)^\s*pikopod\b.*>\s*(out_[a-z_]+\.txt)\s*$`)
	for _, match := range redirectedOutput.FindAllStringSubmatch(text, -1) {
		generated[match[1]] = struct{}{}
	}
	if strings.Contains(text, "python3 mkcast.py") {
		generated["demo.cast"] = struct{}{}
	}
	gifOutput := regexp.MustCompile(`(?m)^\s*agg\b.*\bdemo\.cast\s+demo\.gif\s*$`)
	if gifOutput.MatchString(text) {
		generated["demo.gif"] = struct{}{}
	}
	if len(generated) == 0 {
		t.Fatal("demo README has no generated files to copy back")
	}

	var copyInstruction strings.Builder
	copyStarted := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !copyStarted && strings.HasPrefix(trimmed, "Copy `") {
			copyStarted = true
		}
		if !copyStarted {
			continue
		}
		if trimmed == "" {
			break
		}
		copyInstruction.WriteString(trimmed)
		copyInstruction.WriteByte(' ')
	}
	if !copyStarted {
		t.Fatal("demo README has no copy-back instruction")
	}
	copyText := copyInstruction.String()

	missing := make([]string, 0)
	for name := range generated {
		if !strings.Contains(copyText, "`"+name+"`") {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("copy-back instruction is missing generated files: %s", strings.Join(missing, ", "))
	}
}
