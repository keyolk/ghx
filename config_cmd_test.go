package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/keyolk/ghx/internal/config"
	"gopkg.in/yaml.v3"
)

// The starter template is handed to users as their config; if it does not parse,
// or parses to something other than what its comments promise, ghx starts with
// silently wrong settings.
func TestStarterConfigParses(t *testing.T) {
	var c config.Config
	if err := yaml.Unmarshal([]byte(starterConfig), &c); err != nil {
		t.Fatalf("the starter config does not parse: %v", err)
	}
	if len(c.Sources) != 4 {
		t.Errorf("sources = %d, want 4", len(c.Sources))
	}
	if c.Sources[0].Name != "My PRs" || c.Sources[0].Query != "author:@me state:open" {
		t.Errorf("first source = %#v, want My PRs with author:@me state:open", c.Sources[0])
	}
	if !c.RepoDetectionEnabled() {
		t.Error("detect_repo should be on in the starter config")
	}
	if c.PollInterval != "30s" {
		t.Errorf("poll_interval = %q, want 30s", c.PollInterval)
	}
	if c.DiffSplitRatio != 40 {
		t.Errorf("diff_split_ratio = %d, want 40", c.DiffSplitRatio)
	}
}

// Writing then loading it must round-trip through the real Load path.
func TestStarterConfigLoadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	p := filepath.Join(dir, ".config", "ghx", "config.yaml")

	if err := ensureConfigFile(p); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Sources) != 4 {
		t.Errorf("loaded %d sources, want 4", len(got.Sources))
	}
	if _, statErr := os.Stat(p); statErr != nil {
		t.Errorf("config was not written: %v", statErr)
	}
}
