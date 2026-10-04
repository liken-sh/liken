package main

// The media layer's half of the audio operator's Sink: the volume scale
// a press steps in, the level the sink reports, and the session this
// operator applies to ask for a level. A Sink is cluster-scoped, because
// an endpoint is hardware on a machine and belongs to no namespace.
//
// The audio operator writes the status, and this operator writes only
// status.session, by server-side apply under its own field manager, so
// neither writer removes the other's fields.

import (
	"encoding/json"
	"net/http"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const (
	sinkAPIVersion = "audio.liken.sh/v1alpha1"
	sinksPath      = "/apis/audio.liken.sh/v1alpha1/sinks"
)

// The scale a Sink states when its spec states none, in percent: the
// values a press used before a Sink stated a scale.
const (
	defaultSinkMax  = 100
	defaultSinkStep = 5
)

// A Sink carries only what this operator reads or writes.
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

// SinkVolume is the sink's volume scale, in percent: the resting level,
// the loudest level an ask may set, and the distance one press moves the
// level.
type SinkVolume struct {
	Level *int `json:"level,omitempty"`
	Max   *int `json:"max,omitempty"`
	Step  *int `json:"step,omitempty"`
}

// UnmarshalJSON reads the volume as an object, and a bare number as the
// resting level. A Sink that an audio operator with the integer schema
// still serves then converts, and the view lists every Sink.
func (v *SinkVolume) UnmarshalJSON(data []byte) error {
	var level int
	if err := json.Unmarshal(data, &level); err == nil {
		*v = SinkVolume{Level: &level}
		return nil
	}
	type plain SinkVolume
	return json.Unmarshal(data, (*plain)(v))
}

type SinkStatus struct {
	Observed SinkObserved `json:"observed"`
	Session  *SinkSession `json:"session,omitempty"`
}

// SinkObserved is the level and the mute the sink last reported.
type SinkObserved struct {
	Volume *int  `json:"volume,omitempty"`
	Mute   *bool `json:"mute,omitempty"`
}

// SinkSession names the Player whose asks the block carries, and the
// newest level ask, in percent.
type SinkSession struct {
	Player    string         `json:"player"`
	VolumeAsk *SinkVolumeAsk `json:"volumeAsk,omitempty"`
}

type SinkVolumeAsk struct {
	Level int    `json:"level"`
	Mute  bool   `json:"mute"`
	At    string `json:"at"`
}

// sinkScale reads the sink's scale, with the defaults for what the spec
// leaves out.
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

// The body of a session apply carries the status alone, and the
// session alone inside it.
type sinkStatusApply struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ObjectMeta        `json:"metadata"`
	Status     sinkSessionStatus `json:"status"`
}

type sinkSessionStatus struct {
	Session *SinkSession `json:"session,omitempty"`
}

// ApplySinkSession writes status.session and nothing else, under this
// operator's field manager. Each apply states the whole block, so the
// newest ask replaces the one before it.
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
