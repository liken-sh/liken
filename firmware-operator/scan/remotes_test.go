package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigureRemotesDisablesEveryShippedRemote(t *testing.T) {
	dir := t.TempDir()
	forms := map[string]string{
		"lvfs.conf":     "[fwupd Remote]\nEnabled=true\nType=download\n",
		"spaced.conf":   "[fwupd Remote]\n Enabled = true \n",
		"disabled.conf": "[fwupd Remote]\nEnabled=false\n",
		"notes.txt":     "Enabled=true\n",
	}
	for name, body := range forms {
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}

	if err := configureRemotes(dir, "mirror", "https://m.test/firmware.xml.zst"); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"lvfs.conf":     "[fwupd Remote]\nEnabled=false\nType=download\n",
		"spaced.conf":   "[fwupd Remote]\n Enabled = false\n",
		"disabled.conf": "[fwupd Remote]\nEnabled=false\n",
		"notes.txt":     "Enabled=true\n",
	}
	for name, body := range want {
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if string(got) != body {
			t.Errorf("%s is %q, want %q", name, got, body)
		}
	}
}

func TestConfigureRemotesFailsOnAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "dir.conf"), 0o755)

	if err := configureRemotes(dir, "x", "https://x"); err == nil {
		t.Error("expected an error")
	}
}
