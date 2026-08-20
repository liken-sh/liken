// The media operator reconciles Player and Play resources into
// playback pods. A Player declares a unit of equipment; a Play runs
// media on it; the operator turns the pair into claims on the
// hardware operators' devices and one pod that performs the run.
//
// One binary, two modes, the way the audio operator's one image runs
// in several roles. With no argument it is the operator: a
// Deployment that watches Plays, creates claims and pods, and writes
// every status. As `supervise` it is the playback pod's entrypoint:
// it starts mpv against the delivered sockets, drives mpv's IPC, and
// reports the run to the operator. The split matters for trust: the
// playback pod decodes media from the network, so it reports over
// plain HTTP and holds no API credentials, and only the operator
// writes to the API server.
package main

import "os"

// superviseMode is the argument that selects the playback pod's
// role. The player image sets it in its entrypoint, so a pod spec
// only supplies the media to play.
const superviseMode = "supervise"

func main() {
	if len(os.Args) > 1 && os.Args[1] == superviseMode {
		supervise(os.Args[2:])
		return
	}
	operate()
}
