package main

// media-operator reads audio-operator's cluster-scoped Sink for its
// volume scale and the level the sink reports, and writes the Sink's
// status.session to request a level. audio-operator writes the rest of
// the status. This operator writes status.session by server-side apply
// under its own field manager, so neither writer removes the other's
// fields.

import (
	"encoding/json"
	"net/http"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const (
	sinkAPIVersion = "audio.liken.sh/v1alpha1"
	sinksPath      = "/apis/audio.liken.sh/v1alpha1/sinks"
)

// The volume scale in percent when the Sink's spec does not set one.
const (
	defaultSinkMax  = 100
	defaultSinkStep = 5
)

// Sink declares only the fields this operator reads or writes.
type Sink struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       SinkSpec   `json:"spec"`
	Status     SinkStatus `json:"status"`
}

type SinkSpec struct {
	Volume *SinkVolume `json:"volume,omitempty"`
}

// SinkVolume is in percent: the default level, the highest level a
// request may set, and how far one key press moves the level.
type SinkVolume struct {
	Level *int `json:"level,omitempty"`
	Max   *int `json:"max,omitempty"`
	Step  *int `json:"step,omitempty"`
}

// UnmarshalJSON also accepts a bare number as the default level, so that
// a Sink from an older audio-operator, whose spec.volume is an integer,
// does not fail the list of Sinks.
func (v *SinkVolume) UnmarshalJSON(data []byte) error {
	var level int
	if err := json.Unmarshal(data, &level); err == nil {
		*v = SinkVolume{Level: &level}
		return nil
	}
	type plain SinkVolume
	return json.Unmarshal(data, (*plain)(v))
}

// Passthrough lists the encoded formats that the sink accepts, by
// PipeWire's IEC958 codec names, such as AC3 and TrueHD (passthrough.go).
type SinkStatus struct {
	Observed    SinkObserved `json:"observed"`
	Session     *SinkSession `json:"session,omitempty"`
	Passthrough []string     `json:"passthrough,omitempty"`
}

// SinkObserved is the volume and mute state the sink last reported.
type SinkObserved struct {
	Volume *int  `json:"volume,omitempty"`
	Mute   *bool `json:"mute,omitempty"`
}

// SinkSession is the Player that controls the sink's volume, and its
// latest request in percent.
type SinkSession struct {
	Player    string         `json:"player"`
	VolumeAsk *SinkVolumeAsk `json:"volumeAsk,omitempty"`
}

type SinkVolumeAsk struct {
	Level int    `json:"level"`
	Mute  bool   `json:"mute"`
	At    string `json:"at"`
}

// sinkScale returns the sink's maximum and step, with the defaults for
// values the spec leaves out.
func sinkScale(sink *Sink) (max, step float64) {
	max, step = defaultSinkMax, defaultSinkStep
	if sink.Spec.Volume == nil {
		return max, step
	}
	if stated := sink.Spec.Volume.Max; stated != nil && *stated > 0 {
		max = float64(*stated)
	}
	if stated := sink.Spec.Volume.Step; stated != nil && *stated > 0 {
		step = float64(*stated)
	}
	return max, step
}

// sinkStatusApply is the body of the server-side apply that writes
// status.session.
type sinkStatusApply struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ObjectMeta        `json:"metadata"`
	Status     sinkSessionStatus `json:"status"`
}

type sinkSessionStatus struct {
	Session *SinkSession `json:"session,omitempty"`
}

// ApplySinkSession replaces status.session, under this operator's field
// manager.
func ApplySinkSession(c *apiclient.Client, name string, session *SinkSession) error {
	body, err := json.Marshal(&sinkStatusApply{
		APIVersion: sinkAPIVersion,
		Kind:       "Sink",
		Metadata:   ObjectMeta{Name: name},
		Status:     sinkSessionStatus{Session: session},
	})
	if err != nil {
		return err
	}
	path := sinksPath + "/" + name + "/status?fieldManager=" + applyFieldManager
	return c.Request(http.MethodPatch, path, applyContentType, body, nil)
}
