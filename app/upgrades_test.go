package app

import "testing"

// Every upgrade has a unique, non-empty name and a handler.
func TestUpgradeList(t *testing.T) {
	seen := map[string]bool{}
	for _, u := range Upgrades {
		if u.Name == "" || u.Handler == nil {
			t.Fatalf("upgrade %q is incomplete", u.Name)
		}
		if seen[u.Name] {
			t.Fatalf("upgrade %s is listed twice", u.Name)
		}
		seen[u.Name] = true
	}
}
