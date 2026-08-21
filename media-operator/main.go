// The media operator reconciles Player and Play resources into
// playback pods, and Remote resources into standing pods that read a
// controller. A Player declares a unit of equipment; a Play runs media
// on it; a Remote drives a Play. The operator turns them into claims
// on the hardware operators' devices, the pods that perform the work,
// and the statuses a person reads.
//
// One binary, five roles, the way the audio operator's one image runs
// in several roles. With no argument it is the operator: a Deployment
// that watches Plays, Remotes, Players, and Keymaps, creates claims and
// pods, publishes the compiled keymaps, and writes every status. As
// `player` it is the playback pod's entrypoint shim: it appends the
// display's app-id flag and execs mpv. As `remote` it is the standing
// remote pod: it reads a controller's input nodes and publishes each
// event to the bus. As `command` it is the playback pod's command
// sidecar: it owns mpv's IPC socket, runs each named command from the
// commands topic, and publishes the report. As `translate` it is a
// per-controller sidecar: it turns a controller's evdev events into
// named commands on the bus.
//
// The split is the trust boundary. The playback pod decodes media
// pulled off the network, so it is the least trusted process in the
// system and holds no Kubernetes credentials. Its containers speak
// only to the bus and to the local mpv socket, and the operator alone
// writes a Play's status.
package main

import "os"

// The arguments that select the pod roles. The operator writes each
// into a container's command, over the image's entrypoint. The operator
// itself runs with no argument.
const (
	playerMode    = "player"
	remoteMode    = "remote"
	commandMode   = "command"
	translateMode = "translate"
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
		case translateMode:
			runTranslator()
			return
		}
	}
	operate()
}
