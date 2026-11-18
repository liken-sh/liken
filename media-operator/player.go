package main

// The player mode is the entrypoint of the playback pod's player
// container. mpv takes its options only as arguments, so this code
// reads the environment variables that the operator set from the Play,
// builds the argument list, and execs mpv. Exec makes mpv the
// container's main process, so the kubelet sends SIGTERM to mpv and
// reads its exit code, and exit code 0 means the Play ran to its end.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// mpvBinary is a variable so that a test can point it at a stand-in.
var mpvBinary = "mpv"

// compositorLostExit is the exit code of mpv when its Wayland
// connection closes or breaks. mpv then acts as if the CLOSE_WIN key
// were pressed, and by default that key quits with exit code 0, which
// makes the pod Succeeded and the operator reads as a film that played
// to its end. With CLOSE_WIN bound to a quit with this code, the pod
// fails, and the operator recreates it at the last position that mpv
// reported.
const compositorLostExit = 7

// overlayFont is the font family of mpv's on-screen display.
// Dockerfile.player installs it from brand/fonts, and the idle screen
// uses the same family.
const overlayFont = "Source Sans 3"

// videoAppID is the Wayland app_id of mpv's window. The pod's display
// container arranges the claim's surfaces by app_id, and its own window
// is liken-media-overlay. display/src/arranger.rs has the same value.
const videoAppID = "liken-media-video"

// runPlayer execs mpv. On a failure it writes the reason to stderr and
// exits with code 1, so the pod fails.
func runPlayer(items []string) {
	// An item's presentation block says whether it is an album, which
	// expandItems needs before any item becomes an mpv argument.
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
	// syscall.Exec does not search PATH, so argv[0] is the full path
	// that playerArgv resolved.
	if err := syscall.Exec(argv[0], argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "player: starting %s: %v\n", argv[0], err)
		os.Exit(1)
	}
}

// playerArgv returns mpv's full argument list, with argv[0] resolved
// from mpvBinary on PATH. It fails when the image has no mpv.
//
//   - --vo=dmabuf-wayland hands each decoded frame to the compositor as
//     a dmabuf, so mpv runs no shader pass and a fullscreen surface can
//     go to a hardware display plane. The gpu output renders through
//     EGL, which costs a render thread and a compositor copy, and that
//     heat matters on a machine with passive cooling.
//   - --hwdec=vaapi decodes on the render claim's render node, because
//     dmabuf-wayland needs a hardware decoded surface.
//   - --ao=pipewire plays through the sink claim's socket.
//   - --input-ipc-server opens the socket that the command container
//     uses to send commands to mpv.
//   - --osc=no turns off mpv's on-screen controller, because the
//     display container draws its own, and --load-console=no turns off
//     the console, which nothing opens.
//
// --no-input-terminal is left out on purpose. mpv installs its SIGTERM
// handler only when terminal input is on, and without that handler the
// kubelet's SIGTERM kills mpv at once instead of letting it quit
// cleanly.
//
// The "--" before the items keeps a path that starts with a dash from
// being read as an option.
func playerArgv(items []string, blocks []json.RawMessage) ([]string, error) {
	path, err := exec.LookPath(mpvBinary)
	if err != nil {
		return nil, err
	}
	argv := []string{
		path,
		"--vo=dmabuf-wayland",
		"--hwdec=vaapi",
		"--fullscreen",
		// By default mpv sizes its window to the film's aspect ratio, so
		// a 2.4:1 film in a 1920 by 1080 region gets a 1920 by 800
		// buffer, which the compositor stretches to the region. With
		// this option the window fills the region and mpv letterboxes
		// the film.
		"--keepaspect-window=no",
		"--ao=pipewire",
		"--input-ipc-server=" + mpvSocketPath,
		"--osc=no",
		"--load-console=no",
		"--osd-font=" + overlayFont,
		// memory:// is an inline config file, so nothing is written to
		// a root filesystem that may be read-only.
		"--input-conf=memory://CLOSE_WIN quit " + strconv.Itoa(compositorLostExit),
		"--wayland-app-id=" + videoAppID,
		// mpv's --tls-verify defaults to no, which accepts any
		// certificate for an https:// item. mpv checks the chain against
		// the CA bundle that the image copies from the trust component.
		"--tls-verify=yes",
	}
	// mpv prints its status line many times a second into the pod log,
	// which is written to the machine's disk. --quiet drops the status
	// line and keeps warnings and errors.
	if os.Getenv(playerVerboseVariable) == "" {
		argv = append(argv, "--quiet")
	}
	// When every item is music, mpv shows no cover art, and the display
	// container draws the whole frame. --vid applies to the whole run,
	// so one item that is not music keeps video on. mpv opens no window
	// for a run with no video track, so --force-window=yes keeps one
	// open for the display container to draw over.
	if allMusic(blocks, len(items)) {
		argv = append(argv, "--vid=no", "--force-window=yes")
	}
	// mpv applies --start to the first file only, which matches
	// spec.start: later items begin at their own start.
	if start := os.Getenv(playStartVariable); start != "" {
		argv = append(argv, "--start="+start)
	}
	// The operator resolved the language, subtitle, and S/PDIF
	// passthrough options, and joined them with newlines.
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
