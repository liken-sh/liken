package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlayerArgvBuildsMPVsCommand(t *testing.T) {
	mpv := useMPV(t)
	useSocket(t, "/tmp/test-mpv.sock")

	cases := []struct {
		name          string
		applicationID string
		start         string
		items         []string
		want          []string
	}{
		{
			name:  "one film with no display claim",
			items: []string{"/media/0/film.mkv"},
			want: []string{
				"--vo=gpu", "--gpu-context=wayland", "--hwdec=vaapi", "--fullscreen",
				"--ao=pipewire", "--input-ipc-server=/tmp/test-mpv.sock",
				"--", "/media/0/film.mkv",
			},
		},
		{
			name:          "the display claim names the surface",
			applicationID: "display-0",
			items:         []string{"https://media.example.net/one.mkv", "/media/0/two.mkv"},
			want: []string{
				"--vo=gpu", "--gpu-context=wayland", "--hwdec=vaapi", "--fullscreen",
				"--ao=pipewire", "--input-ipc-server=/tmp/test-mpv.sock",
				"--wayland-app-id=display-0",
				"--", "https://media.example.net/one.mkv", "/media/0/two.mkv",
			},
		},
		{
			// The declared start becomes --start ahead of the items, so
			// the first file begins where the spec says.
			name:  "the spec declares a start",
			start: "0:10:00",
			items: []string{"/media/0/film.mkv"},
			want: []string{
				"--vo=gpu", "--gpu-context=wayland", "--hwdec=vaapi", "--fullscreen",
				"--ao=pipewire", "--input-ipc-server=/tmp/test-mpv.sock",
				"--start=0:10:00",
				"--", "/media/0/film.mkv",
			},
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Setenv(displayAppIDVariable, each.applicationID)
			t.Setenv(playStartVariable, each.start)
			argv, err := playerArgv(each.items)
			mustSucceed(t, err)
			// argv[0] is the resolved binary, so the want list is the
			// mpv path followed by the arguments the shim built.
			mustMatchAll(t, argv, append([]string{mpv}, each.want...))
		})
	}
}

func TestPlayerArgvFailsWhenTheImageCarriesNoMPV(t *testing.T) {
	was := mpvBinary
	t.Cleanup(func() { mpvBinary = was })
	mpvBinary = "definitely-not-a-real-mpv-binary"

	_, err := playerArgv([]string{"/media/0/film.mkv"})
	mustFail(t, err)
}

// useMPV writes a stand-in mpv the shim can resolve, points mpvBinary
// at it, and returns its path. The stand-in never runs; playerArgv only
// resolves the path, so the file's contents do not matter, only that it
// is executable.
func useMPV(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-mpv")
	mustSucceed(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
	was := mpvBinary
	t.Cleanup(func() { mpvBinary = was })
	mpvBinary = path
	return path
}

// useSocket moves the mpv IPC socket path for the length of one test,
// so the shim writes the --input-ipc-server flag the test expects.
func useSocket(t *testing.T, path string) {
	t.Helper()
	was := mpvSocketPath
	t.Cleanup(func() { mpvSocketPath = was })
	mpvSocketPath = path
}
