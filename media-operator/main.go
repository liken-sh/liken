// media-operator is one program that runs in several roles, chosen by
// its first argument. With no argument it is the operator, a Deployment
// that watches Players, Plays, Remotes, and Keymaps. It creates claims
// and pods, publishes each Remote's key table, and writes every status.
// The other roles run in pods:
//
//   - `player` is the entrypoint of the playback pod. It builds mpv's
//     arguments and execs mpv.
//   - `command` is the playback pod's command sidecar. It owns mpv's
//     IPC socket, reads the controllers' key events and the commands
//     topic, and publishes the report.
//   - `remote` is the pod that reads one controller. It reads the
//     controller's input nodes, translates each event through the key
//     table the operator publishes for that Remote, and publishes the
//     kernel's key name to the bus.
//   - `serve-blocks` runs the sidecar's code that sends each item's
//     presentation block to the display, against a local mpv socket,
//     so you can work on the display with no cluster and no bus.
//   - `api` is the media-api Deployment. It serves HTTPS for a Player,
//     redirects screen and audio requests to the display and audio
//     APIs, and muxes the two into one stream. It is a role of this
//     program because it uses the same Player types, API client,
//     metrics code, and version. It has its own image because the mux
//     needs ffmpeg.
//
// The program has two builds. The pod build, with the build tag pod,
// runs every role except the operator, and leaves out client-go's
// leader election, which only the operator needs (leader_pod.go).
//
// The roles divide along a trust boundary. The playback pod decodes
// media pulled off the network, so it is the least trusted process in
// the system and has no Kubernetes credentials. Its containers talk
// only to the bus and to the local mpv socket, and only the operator
// writes a Play's status.
package main

import "os"

// The paths of the two builds in the operator image. The operator runs
// the full build, and the containers it creates run the pod build. The
// player image and the api image contain only the pod build.
const (
	operatorBinary = "/media-operator"
	podBinary      = "/media-operator-pod"
)

// The arguments that select a role. The operator writes one into the
// command of each container it creates, overriding the image's
// entrypoint. The operator itself runs with no argument.
const (
	playerMode  = "player"
	remoteMode  = "remote"
	commandMode = "command"
	blocksMode  = "serve-blocks"
	apiMode     = "api"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case playerMode:
			runPlayer(os.Args[2:])
			return
		case remoteMode:
			runReader()
			return
		case commandMode:
			runCommand()
			return
		case blocksMode:
			runBlocksServe()
			return
		case apiMode:
			runAPI()
			return
		}
	}
	operate()
}
