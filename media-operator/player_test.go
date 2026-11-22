package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPlayerArgvBuildsMPVsCommand(t *testing.T) {
	mpv := useMPV(t)
	useSocket(t, "/tmp/test-mpv.sock")

	cases := []struct {
		name    string
		start   string
		verbose string
		items   []string
		blocks  []json.RawMessage
		want    []string
	}{
		{
			name:  "a remote film and a local one",
			items: []string{"https://media.example.net/one.mkv", "/media/0/two.mkv"},
			want: []string{
				"--vo=dmabuf-wayland", "--hwdec=vaapi", "--fullscreen",
				"--keepaspect-window=no",
				"--ao=pipewire", "--input-ipc-server=/tmp/test-mpv.sock",
				"--osc=no",
				"--load-console=no",
				"--osd-font=Source Sans 3",
				"--input-conf=memory://CLOSE_WIN quit 7",
				"--wayland-app-id=liken-media-video",
				"--tls-verify=yes",
				"--quiet",
				"--", "https://media.example.net/one.mkv", "/media/0/two.mkv",
			},
		},
		{
			name:  "an album and a track, both music",
			items: []string{"/ipc/album-1.edl", "/media/1/track.flac"},
			blocks: []json.RawMessage{
				json.RawMessage(`{"type":"music","hint":"album"}`),
				json.RawMessage(`{"type":"music"}`),
			},
			want: []string{
				"--vo=dmabuf-wayland", "--hwdec=vaapi", "--fullscreen",
				"--keepaspect-window=no",
				"--ao=pipewire", "--input-ipc-server=/tmp/test-mpv.sock",
				"--osc=no",
				"--load-console=no",
				"--osd-font=Source Sans 3",
				"--input-conf=memory://CLOSE_WIN quit 7",
				"--wayland-app-id=liken-media-video",
				"--tls-verify=yes",
				"--quiet",
				"--vid=no", "--force-window=yes",
				"--", "/ipc/album-1.edl", "/media/1/track.flac",
			},
		},
		{
			name:  "a film beside an album",
			items: []string{"/media/1/film.mkv", "/ipc/album-2.edl"},
			blocks: []json.RawMessage{
				json.RawMessage(`{"type":"video","hint":"movie"}`),
				json.RawMessage(`{"type":"music","hint":"album"}`),
			},
			want: []string{
				"--vo=dmabuf-wayland", "--hwdec=vaapi", "--fullscreen",
				"--keepaspect-window=no",
				"--ao=pipewire", "--input-ipc-server=/tmp/test-mpv.sock",
				"--osc=no",
				"--load-console=no",
				"--osd-font=Source Sans 3",
				"--input-conf=memory://CLOSE_WIN quit 7",
				"--wayland-app-id=liken-media-video",
				"--tls-verify=yes",
				"--quiet",
				"--", "/media/1/film.mkv", "/ipc/album-2.edl",
			},
		},
		{
			name:  "the spec declares a start",
			start: "0:10:00",
			items: []string{"/media/0/film.mkv"},
			want: []string{
				"--vo=dmabuf-wayland", "--hwdec=vaapi", "--fullscreen",
				"--keepaspect-window=no",
				"--ao=pipewire", "--input-ipc-server=/tmp/test-mpv.sock",
				"--osc=no",
				"--load-console=no",
				"--osd-font=Source Sans 3",
				"--input-conf=memory://CLOSE_WIN quit 7",
				"--wayland-app-id=liken-media-video",
				"--tls-verify=yes",
				"--quiet",
				"--start=0:10:00",
				"--", "/media/0/film.mkv",
			},
		},
		{
			name:    "the operator asks for mpv's full output",
			verbose: "1",
			items:   []string{"/media/0/film.mkv"},
			want: []string{
				"--vo=dmabuf-wayland", "--hwdec=vaapi", "--fullscreen",
				"--keepaspect-window=no",
				"--ao=pipewire", "--input-ipc-server=/tmp/test-mpv.sock",
				"--osc=no",
				"--load-console=no",
				"--osd-font=Source Sans 3",
				"--input-conf=memory://CLOSE_WIN quit 7",
				"--wayland-app-id=liken-media-video",
				"--tls-verify=yes",
				"--", "/media/0/film.mkv",
			},
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Setenv(playStartVariable, each.start)
			t.Setenv(playerVerboseVariable, each.verbose)
			argv, err := playerArgv(each.items, each.blocks)
			mustSucceed(t, err)
			mustMatchAll(t, argv, append([]string{mpv}, each.want...))
		})
	}
}

// An option with spaces in its value, such as the subtitle font, is one
// argument of mpv's command line, because the operator separates options
// with newlines and not with spaces.
func TestPlayerArgvKeepsAnOptionWithSpacesAsOneArgument(t *testing.T) {
	mpv := useMPV(t)
	useSocket(t, "/tmp/test-mpv.sock")
	t.Setenv(playStartVariable, "")
	t.Setenv(playerVerboseVariable, "")
	t.Setenv(playerOptionsVariable, "--alang=en\n--sub-font=Source Sans 3\n--sub-font-size=38")

	argv, err := playerArgv([]string{"/media/0/film.mkv"}, nil)
	mustSucceed(t, err)

	mustMatchAll(t, argv[len(argv)-5:], []string{
		"--alang=en", "--sub-font=Source Sans 3", "--sub-font-size=38", "--", "/media/0/film.mkv",
	})
	mustMatch(t, argv[0], mpv)
}

func TestPlayerArgvFailsWhenTheImageHasNoMPV(t *testing.T) {
	was := mpvBinary
	t.Cleanup(func() { mpvBinary = was })
	mpvBinary = "definitely-not-a-real-mpv-binary"

	_, err := playerArgv([]string{"/media/0/film.mkv"}, nil)
	mustFail(t, err)
}

// useMPV points mpvBinary at a stand-in and returns its path.
// playerArgv never runs the file, so it only has to be executable.
func useMPV(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-mpv")
	mustSucceed(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
	was := mpvBinary
	t.Cleanup(func() { mpvBinary = was })
	mpvBinary = path
	return path
}

func useSocket(t *testing.T, path string) {
	t.Helper()
	was := mpvSocketPath
	t.Cleanup(func() { mpvSocketPath = was })
	mpvSocketPath = path
}
