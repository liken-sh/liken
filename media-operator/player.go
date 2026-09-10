package main

// The player mode is the playback pod's entrypoint shim. mpv reads no
// options from the environment, so the shim reads what the operator set
// there, builds mpv's arguments the way the pod's Play declares them,
// and execs mpv.
// Because it execs, the shim replaces itself, so mpv is the pod's own
// process. The kubelet then sends mpv the grace-period SIGTERM and
// reads its exit code, and a zero code is a Play that ran to the end.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// mpvBinary is a variable rather than a constant because the pod runs
// the mpv its image carries, and a test points it at a stand-in that
// needs no display and no sound card.
var mpvBinary = "mpv"

// The path of the display script directory inside the image, the one mpv loads
// with --script. It is a variable rather than a constant so a test can point it
// at a stand-in, the way mpvBinary is.
var displayScriptDir = "/display"

// overlayFont is the family the overlay draws in, installed as two OTF
// files in the player image and named here for mpv's own OSD. The brand
// crate carries the same family for the idle screen, and display/theme.lua
// names it in its ASS tags, which is what draws the overlay's text.
const overlayFont = "Source Sans 3"

// runPlayer builds mpv's argument vector and execs mpv, so mpv becomes
// the container's process. On any failure it writes the reason to
// stderr and exits nonzero, which the kubelet reads as a pod that
// failed to start its player.
func runPlayer(items []string) {
	// The shim reads the same blocks the command sidecar reads, because the
	// block is where an item declares its shape, and the album expansion
	// needs that declaration before mpv sees any argument.
	blocks := parsePresentations(os.Getenv(presentationsVariable))
	entries, err := expandItems(items, blocks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "player: %v\n", err)
		os.Exit(1)
	}
	argv, err := playerArgv(entries, blocks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "player: %v\n", err)
		os.Exit(1)
	}
	// syscall.Exec replaces this process with mpv, so it returns only
	// when the exec itself fails. The resolved path is argv[0] because
	// the kernel runs the file the path names, not a PATH lookup.
	if err := syscall.Exec(argv[0], argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "player: exec %s: %v\n", argv[0], err)
		os.Exit(1)
	}
}

// playerArgv is the whole of how mpv is told to play. The dmabuf-wayland
// video output, because the display claim delivers a compositor socket
// and this output hands each decoded frame to the compositor as a
// dmabuf: no shader pass in mpv, and a fullscreen surface can go to a
// display plane. The gpu output renders every frame through EGL and
// costs a render thread plus the compositor's copy of it, which matters
// on a passively cooled machine. VAAPI, because the render claim
// delivers the node that decodes, and this output needs a hardware
// surface. The PipeWire audio output, because Wayland
// carries no audio and the sink claim delivers that socket. The IPC
// server stays because the command sidecar drives that same socket to
// run each named command and read the report. The display script directory
// loads with --script, and the command sidecar drives it over that same IPC
// socket. --osc=no turns off mpv's built-in on-screen controller,
// because the display draws its own.
//
// The list ends with -- because a media path that starts with a dash
// would otherwise read as a flag.
//
// --no-input-terminal is deliberately absent. mpv installs its SIGTERM
// handler only on the terminal-input path, and without the handler the
// kubelet's SIGTERM ends the player the hard way instead of letting it
// quit.
//
// argv[0] is the resolved binary path, so a test that points mpvBinary
// at a stand-in reads back the path it set. exec.LookPath fails before
// mpv runs when the image carries no mpv, which the shim reports rather
// than execing nothing.
func playerArgv(items []string, blocks []json.RawMessage) ([]string, error) {
	path, err := exec.LookPath(mpvBinary)
	if err != nil {
		return nil, fmt.Errorf("find %s: %w", mpvBinary, err)
	}
	argv := []string{
		path,
		"--vo=dmabuf-wayland",
		"--hwdec=vaapi",
		"--fullscreen",
		"--ao=pipewire",
		"--input-ipc-server=" + mpvSocketPath,
		"--script=" + displayScriptDir,
		"--osc=no",
		// mpv's own OSD messages draw in the brand family; libass resolves
		// it through fontconfig against the two OTF files the image installs.
		"--osd-font=" + overlayFont,
	}
	// --quiet is the default because mpv prints its status line about
	// eight times a second, this process's stdout is the pod log on the
	// machine's disk, and containerd and the kubelet tail it. --quiet
	// drops that line and keeps warnings and errors. MEDIA_PLAYER_VERBOSE
	// with any value brings the full output back; the shim adds no -v.
	if os.Getenv(playerVerboseVariable) == "" {
		argv = append(argv, "--quiet")
	}
	// A run of nothing but music draws no video, so the display owns the
	// whole frame instead of annotating the cover art mpv would frame. One
	// item that is not music keeps video on for the whole run.
	//
	// --force-window=yes holds a window over the blanked video. Without it
	// mpv opens no window at all for a run with no video track, and the
	// display draws nothing.
	if allMusic(blocks, len(items)) {
		argv = append(argv, "--vid=no", "--force-window=yes")
	}
	// The declared start applies to the first file mpv loads and to no
	// later playlist entry, which is exactly what spec.start means: the
	// run begins here, and later items begin at their own start.
	if start := os.Getenv(playStartVariable); start != "" {
		argv = append(argv, "--start="+start)
	}
	// The operator resolved these language and subtitle flags and joined them with
	// newlines. The shim forwards each one to mpv, unread.
	if options := os.Getenv(playerOptionsVariable); options != "" {
		for _, option := range strings.Split(options, "\n") {
			if option != "" {
				argv = append(argv, option)
			}
		}
	}
	argv = append(argv, "--")
	return append(argv, items...), nil
}
