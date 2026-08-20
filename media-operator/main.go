// The media operator reconciles Player and Play resources into
// playback pods. A Player declares a unit of equipment; a Play runs
// media on it; the operator turns the pair into claims on the
// hardware operators' devices and one pod that performs the run.
//
// One binary, three modes, the way the audio operator's one image
// runs in several roles. With no argument it is the operator: a
// Deployment that watches Plays, creates claims and pods, and writes
// every status. As `supervise` it is the playback pod's entrypoint:
// it starts mpv against the delivered sockets, drives mpv's IPC, and
// reports the run to the operator. As `remote` it is a sidecar in
// that same pod, one per bound Remote: it reads a controller's input
// nodes and writes commands to the same IPC socket. The split
// matters for trust: the playback pod decodes media from the
// network, so its containers report over plain HTTP or drive a local
// socket, hold no API credentials, and only the operator writes to
// the API server.
package main

import "os"

// superviseMode is the argument that selects the playback pod's
// role. The player image sets it in its entrypoint, so a pod spec
// only supplies the media to play.
const superviseMode = "supervise"

// remoteMode is the argument that selects the remote sidecar's
// role. The operator writes it into the sidecar's command, over the
// image's entrypoint, and passes no arguments after it, because the
// compiled keymap arrives in the environment instead.
const remoteMode = "remote"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case superviseMode:
			supervise(os.Args[2:])
			return
		case remoteMode:
			remoteControl()
			return
		}
	}
	operate()
}
