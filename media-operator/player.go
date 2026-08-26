package main

// The player mode is the playback pod's entrypoint shim. mpv reads no
// options from the environment, but the display claim delivers the
// surface's app-id in the environment at run time, after the pod spec
// is fixed. So the shim reads that one variable, builds the rest of
// mpv's arguments the way the pod's Play declares them, and execs mpv.
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

// displayAppIDVariable arrives from the display claim's CDI spec, not
// from the operator. The display operator writes it into the
// container's environment at run time, after the pod spec is fixed, so
// the operator could not know the value to set. mpv has no environment
// mechanism for options, so the shim adds the --wayland-app-id flag
// itself when the claim delivered an id.
const displayAppIDVariable = "DISPLAY_APP_ID"

// The path of the display script directory inside the image, the one mpv loads
// with --script. It is a variable rather than a constant so a test can point it
// at a stand-in, the way mpvBinary is.
var displayScriptDir = "/display"

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

// runIdle builds the idle argument vector and execs mpv, so mpv becomes
// the standing idle pod's own process. It draws the clock while no Play
// runs. On any failure it writes the reason to stderr and exits nonzero,
// which the kubelet reads as a pod that failed to start.
func runIdle() {
	argv, err := idleArgv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "idle: %v\n", err)
		os.Exit(1)
	}
	if err := syscall.Exec(argv[0], argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "idle: exec %s: %v\n", argv[0], err)
		os.Exit(1)
	}
}

// idleArgv builds mpv's arguments for the idle client. --idle=yes and
// --force-window=yes hold a window with no file, so the display script
// draws the clock on an empty surface. --no-audio because the clock is
// silent, so the idle pod claims no sink. The gpu video output with the
// Wayland EGL context and the display script match the playback shim, so
// the idle client and a Play draw through the same stack on the same
// screen. --osc=no turns off mpv's built-in on-screen controller,
// because the display draws its own. The window's app-id routes the
// surface to the shared screen when the display claim delivered one.
//
// --input-ipc-server serves the same socket the playback shim serves,
// because the idle command sidecar drives it to recreate the idle
// surface when a Play ends. Weston's kiosk-shell reveals a lower surface
// only along a code path gated on a seat, and liken's compositor runs with
// no input devices and no seat, so it never reveals the idle surface
// again on its own. A freshly mapped surface is revealed along a
// seat-independent path, so recreating the surface is what shows the
// clock again. The idle client still loads no media path and plays no
// file.
//
// argv[0] is the resolved binary path, so a test that points mpvBinary at
// a stand-in reads back the path it set. exec.LookPath fails before mpv
// runs when the image carries no mpv, which the shim reports rather than
// execing nothing.
func idleArgv() ([]string, error) {
	path, err := exec.LookPath(mpvBinary)
	if err != nil {
		return nil, fmt.Errorf("find %s: %w", mpvBinary, err)
	}
	argv := []string{
		path,
		"--vo=gpu",
		"--gpu-context=wayland",
		"--fullscreen",
		"--idle=yes",
		"--force-window=yes",
		"--no-audio",
		"--input-ipc-server=" + mpvSocketPath,
		"--script=" + displayScriptDir,
		"--osc=no",
	}
	if applicationID := os.Getenv(displayAppIDVariable); applicationID != "" {
		argv = append(argv, "--wayland-app-id="+applicationID)
	}
	return argv, nil
}

// playerArgv is the whole of how mpv is told to play. The gpu video
// output with the Wayland EGL context, because the display claim
// delivers a compositor socket and Debian's mpv segfaults in
// --vo=dmabuf-wayland. VAAPI, because the render claim delivers the
// node that decodes. The PipeWire audio output, because Wayland
// carries no audio and the sink claim delivers that socket. The IPC
// server stays because the command sidecar drives that same socket to
// run each named command and read the report. The display script directory
// loads with --script, and the command sidecar drives it over that same IPC
// socket. --osc=no turns off mpv's built-in on-screen controller,
// because the display draws its own. The window's app-id routes
// it to the allocated output when the display claim delivered one.
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
		"--vo=gpu",
		"--gpu-context=wayland",
		"--hwdec=vaapi",
		"--fullscreen",
		"--ao=pipewire",
		"--input-ipc-server=" + mpvSocketPath,
		"--script=" + displayScriptDir,
		"--osc=no",
	}
	// A run of nothing but music draws no video, so the display owns the
	// whole frame instead of annotating the cover art mpv would frame. One
	// item that is not music keeps video on for the whole run.
	//
	// --force-window=yes holds a window over the blanked video, the way it
	// holds one for the idle client with no file. Without it mpv opens no
	// window at all for a run with no video track, and the display draws
	// nothing.
	if allMusic(blocks, len(items)) {
		argv = append(argv, "--vid=no", "--force-window=yes")
	}
	if applicationID := os.Getenv(displayAppIDVariable); applicationID != "" {
		argv = append(argv, "--wayland-app-id="+applicationID)
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
