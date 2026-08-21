// The media operator reconciles Player and Play resources into
// playback pods, and Remote resources into standing pods that read a
// controller. A Player declares a unit of equipment; a Play runs media
// on it; a Remote drives a Play. The operator turns them into claims
// on the hardware operators' devices, the pods that perform the work,
// and the statuses a person reads.
//
// One binary, four modes, the way the audio operator's one image runs
// in several roles. With no argument it is the operator: a Deployment
// that watches Plays and Remotes, creates claims and pods, subscribes
// to the bus, and writes every status. As `player` it is the playback
// pod's entrypoint shim: it appends the display's app-id flag and
// execs mpv. As `remote` it is the standing remote pod: it reads a
// controller's input nodes and publishes each event to the bus. As
// `bridge` it is the playback pod's sidecar: it subscribes to the bus,
// applies each remote's keymap, and drives mpv's local IPC socket.
//
// The split is the trust boundary. The playback pod decodes media
// pulled off the network, so it is the least trusted process in the
// system and holds no Kubernetes credentials. Its containers speak
// only to the bus and to the local mpv socket, and the operator alone
// writes a Play's status.
package main

import "os"

// The arguments that select the three pod roles. The operator writes
// each into a container's command, over the image's entrypoint. The
// operator itself runs with no argument.
const (
	playerMode = "player"
	remoteMode = "remote"
	bridgeMode = "bridge"
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
		case bridgeMode:
			runBridge()
			return
		}
	}
	operate()
}
