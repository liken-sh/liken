package main

// The report contract between the supervisor and the operator. The
// supervisor POSTs this JSON to the operator's /report endpoint on
// every change and every few seconds while position advances; the
// operator folds it into the Play's status. This is the whole of
// what the playback pod can say to the control plane, and it
// carries no API object: the operator decides what any report means
// for the phase.

// playReport is one observation of the run, as the supervisor sees
// it through mpv's IPC socket.
type playReport struct {
	// Namespace and Name identify the Play. The operator trusts
	// them only together with Token.
	Namespace string `json:"namespace"`
	Name      string `json:"name"`

	// Token proves the report comes from the pod the operator
	// created for this Play. The operator mints it into the pod's
	// environment, so only that pod and readers of the pod spec
	// hold it.
	Token string `json:"token"`

	// Paused is the player holding the current item still. The
	// phase stays Running.
	Paused bool `json:"paused"`

	// Item is the URI now playing, counting from 1 in spec order.
	Item int `json:"item"`

	// Position and Duration are the playhead and the length of the
	// current item, each as H:MM:SS. Duration is empty until the
	// player has read the item's header.
	Position string `json:"position"`
	Duration string `json:"duration"`
}

// Environment variable names in the playback pod. The operator sets
// them on the pod it creates; the supervisor reads them. They are
// constants here so the two modes of this binary cannot drift.
const (
	playNamespaceVariable = "MEDIA_PLAY_NAMESPACE"
	playNameVariable      = "MEDIA_PLAY_NAME"
	playTokenVariable     = "MEDIA_PLAY_TOKEN"
	operatorURLVariable   = "MEDIA_OPERATOR_URL"

	// playStartVariable carries spec.start when the Play declares
	// one. The supervisor turns it into mpv's --start, so the run
	// begins where the spec says instead of at zero.
	playStartVariable = "MEDIA_PLAY_START"
)
