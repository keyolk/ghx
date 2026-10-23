package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// The keymap section was a placeholder of scalars; it now takes key lists, and
// a scalar still reads as a list of one.
func TestKeymapAcceptsScalarsAndLists(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("keymap:\n  palette: \";\"\n  status: [f, F]\n"), &c); err != nil {
		t.Fatal(err)
	}
	m := c.Keymap.Map()
	if got := m["palette"]; len(got) != 1 || got[0] != ";" {
		t.Errorf("palette = %v, want [;]", got)
	}
	if got := m["status"]; len(got) != 2 || got[1] != "F" {
		t.Errorf("status = %v, want [f F]", got)
	}
}
