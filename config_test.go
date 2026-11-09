package main

import (
	"strings"
	"testing"
)

func TestConfiguredHostLookup(t *testing.T) {
	cfg, err := LoadBytes([]byte("tools:\n  - host: Install.Example.com\n    repo: owner/tool\n"), "test")
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := cfg.Lookup("INSTALL.EXAMPLE.COM:443")
	if !ok || tool.Repo != "owner/tool" {
		t.Fatalf("configured host did not resolve")
	}
	if _, ok := cfg.Lookup("unknown.example.com"); ok {
		t.Fatal("unknown host resolved")
	}
}

func TestDuplicateHostsRejected(t *testing.T) {
	_, err := LoadBytes([]byte("tools:\n  - {host: x.example, repo: a/b}\n  - {host: X.EXAMPLE, repo: c/d}\n"), "test")
	if err == nil {
		t.Fatal("duplicate hosts accepted")
	}
}

func TestLandingEscapesDisplayName(t *testing.T) {
	page := renderLanding(Tool{Host: "x.example", Repo: "owner/tool", DisplayName: "<script>"})
	if strings.Contains(page, "<script>") || !strings.Contains(page, "&lt;script&gt;") {
		t.Fatal("display name was not escaped")
	}
}
