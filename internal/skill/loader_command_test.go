package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoader_CommandScan_Integration(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude", "commands", "ops"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "commands", "ops", "new.md"), []byte("---\nname: New\nskill: openspec-new-change\n---\nhelp"), 0644); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	loader := NewLoader(nil, registry)
	fl := NewFileLoader(loader.Registry(), nil, nil, nil)
	cl := NewCommandLoader(NewCommandRegistry(), nil)
	loader.SetFileLoader(fl, dir)
	loader.SetCommandLoader(cl)

	if err := loader.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if _, ok := cl.Registry().Get("ops:new"); !ok {
		t.Fatalf("expected command ops:new loaded via loader")
	}
}
