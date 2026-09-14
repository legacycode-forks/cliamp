package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDecodesMultilineTOMLValuesAndPreservesSemantics(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`[plex]
libraries = [
  "Music #1", # quoted hash is data
  "Audiobooks",
]

[plugins.lastfm]
api_key = "key#value" # comment
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Plex.Libraries, []string{"Music #1", "Audiobooks"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Plex.Libraries = %#v, want %#v", got, want)
	}
	if got := cfg.Plugins["lastfm"]["api_key"]; got != "key#value" {
		t.Errorf("plugin api_key = %q, want %q", got, "key#value")
	}
}

func TestLoadPreservesLegacyTopLevelPluginValues(t *testing.T) {
	writeConfigForTest(t, "[plugins]\napi_key = \"x\"\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Plugins[""]["api_key"]; got != "x" {
		t.Fatalf("top-level plugin api_key = %q, want %q", got, "x")
	}
}

func TestLoadConvertsPluginScalarValuesToStrings(t *testing.T) {
	writeConfigForTest(t, "[plugins.example]\nretries = 3\nenabled = true\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Plugins["example"]["retries"]; got != "3" {
		t.Errorf("plugin retries = %q, want %q", got, "3")
	}
	if got := cfg.Plugins["example"]["enabled"]; got != "true" {
		t.Errorf("plugin enabled = %q, want %q", got, "true")
	}
}

func TestLoadUsesLastRepeatedKeyValue(t *testing.T) {
	writeConfigForTest(t, "[plex]\ntoken = \"a\"\ntoken = \"b\"\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Plex.Token; got != "b" {
		t.Fatalf("Plex.Token = %q, want %q", got, "b")
	}
}

func TestLoadMergesYouTubeAliasesLastValueWins(t *testing.T) {
	writeConfigForTest(t, "[yt] # legacy alias\nclient_id = \"old\"\n\n[ytmusic]\nclient_id = \"canonical\"\n\n[youtube]\nclient_secret = \"secret\"\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.YouTubeMusic.ClientID != "canonical" || cfg.YouTubeMusic.ClientSecret != "secret" || !cfg.YouTubeMusic.Enabled {
		t.Fatalf("youtube config = %+v", cfg.YouTubeMusic)
	}
}

func writeConfigForTest(t *testing.T, data string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
