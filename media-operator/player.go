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
	"fmt"
	"os"
	"os/exec"
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

// runPlayer builds mpv's argument vector and execs mpv, so mpv becomes
// the container's process. On any failure it writes the reason to
// stderr and exits nonzero, which the kubelet reads as a pod that
// failed to start its player.
func runPlayer(items []string) {
	argv, err := playerArgv(items)
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

// playerArgv is the whole of how mpv is told to play. The gpu video
// output with the Wayland EGL context, because the display claim
// delivers a compositor socket and Debian's mpv segfaults in
// --vo=dmabuf-wayland. VAAPI, because the render claim delivers the
// node that decodes. The PipeWire audio output, because Wayland
// carries no audio and the sink claim delivers that socket. The IPC
// server stays because the command sidecar drives that same socket to
// run each named command and read the report. The window's app-id routes
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
func playerArgv(items []string) ([]string, error) {
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
	argv = append(argv, "--")
	return append(argv, items...), nil
}
