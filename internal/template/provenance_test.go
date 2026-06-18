package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestContentHashStableAndIgnoresLock(t *testing.T) {
	rt := &ResolvedTemplate{
		FS: fstest.MapFS{
			"root/template.toml":  &fstest.MapFile{Data: []byte("[template]\nname=\"x\"\nversion=\"1.0.0\"\n")},
			"root/agents/a.md":    &fstest.MapFile{Data: []byte("agent")},
			"root/.template.lock": &fstest.MapFile{Data: []byte("old lock")},
		},
		Root: "root",
	}
	h1, err := ContentHash(rt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h1, "sha256:") {
		t.Fatalf("hash missing sha256 prefix: %s", h1)
	}

	rt.FS = fstest.MapFS{
		"root/template.toml":  &fstest.MapFile{Data: []byte("[template]\nname=\"x\"\nversion=\"1.0.0\"\n")},
		"root/agents/a.md":    &fstest.MapFile{Data: []byte("agent")},
		"root/.template.lock": &fstest.MapFile{Data: []byte("different lock")},
	}
	h2, err := ContentHash(rt)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("lock file should not affect hash: %s != %s", h1, h2)
	}

	rt.FS = fstest.MapFS{
		"root/template.toml": &fstest.MapFile{Data: []byte("[template]\nname=\"x\"\nversion=\"1.0.0\"\n")},
		"root/agents/a.md":   &fstest.MapFile{Data: []byte("changed")},
	}
	h3, err := ContentHash(rt)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h3 {
		t.Fatalf("source content change should affect hash: %s", h3)
	}
}

func TestLoadLockValidatesRequiredFields(t *testing.T) {
	tmp := t.TempDir()
	valid := filepath.Join(tmp, "valid.lock")
	if err := os.WriteFile(valid, []byte(`
[template]
ref = "bundled"
name = "default"
version = "1.0.0"
content_hash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := LoadLock(valid)
	if err != nil {
		t.Fatalf("valid lock: %v", err)
	}
	if l.Template.Ref != "bundled" {
		t.Errorf("ref = %q", l.Template.Ref)
	}

	for name, body := range map[string]string{
		"missing-ref": `[template]
content_hash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`,
		"missing-hash": `[template]
ref = "bundled"
`,
		"bad-hash": `[template]
ref = "bundled"
content_hash = "sha256:not-a-hex-digest"
`,
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(tmp, name+".lock")
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadLock(p); err == nil {
				t.Fatal("expected invalid lock to fail")
			}
		})
	}
}
