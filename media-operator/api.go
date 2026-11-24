package main

// These types are written by hand and not imported from `k8s.io/api`.
// Each one has only the fields that this operator reads or writes.

import (
	"encoding/json"
	"slices"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

const (
	mediaAPIVersion = "media.liken.sh/v1alpha1"
	claimAPIVersion = "resource.k8s.io/v1"
	podAPIVersion   = "v1"
)

type ObjectMeta struct {
	Name              string            `json:"name,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Generation        int64             `json:"generation,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
	OwnerReferences   []OwnerReference  `json:"ownerReferences,omitempty"`
	Finalizers        []string          `json:"finalizers,omitempty"`
}

func (m ObjectMeta) deleting() bool { return m.DeletionTimestamp != "" }

// hasFinalizer reports whether the object has the named finalizer.
func (m ObjectMeta) hasFinalizer(finalizer string) bool {
	return slices.Contains(m.Finalizers, finalizer)
}

// withFinalizer and withoutFinalizer return a new slice of finalizers with the named one
// added or removed. They copy so that the object is unchanged when the
// patch that sends the result fails.
func (m ObjectMeta) withFinalizer(finalizer string) []string {
	return append(slices.Clone(m.Finalizers), finalizer)
}

func (m ObjectMeta) withoutFinalizer(finalizer string) []string {
	kept := []string{}
	for _, held := range m.Finalizers {
		if held != finalizer {
			kept = append(kept, held)
		}
	}
	return kept
}

// OwnerReference lets the garbage collector delete a claim or pod when
// the `Play`, `Player`, or `Remote` that owns it is deleted.
// `blockOwnerDeletion` is left unset, because the owner's deletion does
// not need to wait for the claim or pod to go.
type OwnerReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	Controller bool   `json:"controller"`
}

// Player is a screen and speakers that a person declares. The spec
// selects the devices, and the operator writes in the status what plays
// on them.
type Player struct {
	APIVersion string       `json:"apiVersion,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       PlayerSpec   `json:"spec"`
	Status     PlayerStatus `json:"status"`
}

type PlayerStatus struct {
	Activity string `json:"activity,omitempty"`
	Play     string `json:"play,omitempty"`

	// Power is the `spec.power` that the operator last applied to the
	// hardware. A pass sends power commands only when `spec.power`
	// differs from this value, and the value is in the status so that it
	// survives an operator restart. The first pass copies `spec.power`
	// here without sending any command.
	Power string `json:"power,omitempty"`

	// PoweredOnAt is when the operator last began to turn the `Player`
	// on, as an RFC 3339 time with milliseconds. The idle client
	// publishes the panel state it wants on a retained topic, so after a
	// turn-on the broker still has the off that the client published
	// when the `Player` turned off. The operator ignores an off that
	// arrived no later than this time.
	PoweredOnAt string `json:"poweredOnAt,omitempty"`

	// Panel is the panel state that the `Display` last reported. It is
	// empty until the `Display` reports one.
	Panel string `json:"panel,omitempty"`

	// Receiver is the `Receiver` and its input that the `Player` plays
	// through. It is absent when the `Player` plays straight into its
	// panel.
	Receiver *PlayerReceiverStatus `json:"receiver,omitempty"`

	// Idle is nil when the `Player` has no idle claim.
	Idle *PlayerIdleStatus `json:"idle,omitempty"`

	// Screen is the screen that the idle claim was last allocated. The
	// operator keeps it after the claim is deallocated, so a `Player`
	// whose monitor is unplugged still shows which `Display` to read.
	Screen *PlayerScreenStatus `json:"screen,omitempty"`

	// Sinks is the `Sink` that each `spec.sinks` entry was allocated, in
	// spec order. The idle claim has no sinks, so the operator copies the
	// list from the playback claim of a `Play` and keeps it after the
	// `Play` ends. A capture through media-api still needs a running
	// `Play`, because another `Player` can be using a `Sink` in this
	// list.
	Sinks []PlayerSinkStatus `json:"sinks,omitempty"`

	// Conditions has the `Screen` condition, which follows the
	// `Connected` condition of the `Display` in `screen`, and the
	// `PowerApplied` condition, which reports the result of the last
	// change of `spec.power`.
	Conditions []PlayerCondition `json:"conditions,omitempty"`
}

// PlayerSinkStatus is one allocated `Sink`. `request` is the request of
// the playback claim, `audio0` for the first `spec.sinks` entry. `name`
// is the device name that audio-operator gives the `Sink`, which a
// client passes to the audio API.
type PlayerSinkStatus struct {
	Request string `json:"request,omitempty"`
	Name    string `json:"name,omitempty"`
}

// PlayerScreenStatus is the node that publishes the draw device, and
// the monitor id, which is the name of its `Display`.
type PlayerScreenStatus struct {
	Node    string `json:"node,omitempty"`
	Monitor string `json:"monitor,omitempty"`
}

// A `Player` condition sets no `observedGeneration`.
type PlayerCondition = conditions.Condition

// PlayerReceiverStatus is the `Receiver` input that the `Player`'s
// machine and monitor are connected to. `reachable` is the status of
// that `Receiver`'s `Reachable` condition, and it is empty until
// equipment-operator reports one.
type PlayerReceiverStatus struct {
	Name      string `json:"name,omitempty"`
	Input     string `json:"input,omitempty"`
	Reachable string `json:"reachable,omitempty"`
}

// PlayerIdleStatus is what the controller of the idle screen needs to
// start its client pod. `claim` is the idle claim, which that pod names
// in its `resourceClaims`, and `requests` are the claim's request
// names in claim order. Under `media.liken.sh/none`, only `controller`
// is set, and both idle windows are written as zero.
type PlayerIdleStatus struct {
	Controller string   `json:"controller"`
	Claim      string   `json:"claim,omitempty"`
	Requests   []string `json:"requests,omitempty"`

	// The idle windows, in seconds, after the `Player` and the default
	// `MediaPreferences` are combined. A fade of zero means the screen
	// never fades on its own, and an off window of zero leaves the panel
	// lit. Neither field has `omitempty`, because zero is a setting, and
	// a client must always read a number.
	FadeAfterSeconds int64 `json:"fadeAfterSeconds"`
	OffAfterSeconds  int64 `json:"offAfterSeconds"`

	Bus *PlayerIdleBus `json:"bus,omitempty"`
}

// PlayerIdleBus is the MQTT broker address and the full topic names
// that the idle client reads and writes. The broker and the topic base
// are operator configuration, so the client cannot build the topics
// itself. The operator publishes the volume level on `volumeTopic`, and
// the client only draws it. `volumeTopic` is empty when the `Player`
// has no sinks, and the client then draws no level. `remotes` is in the
// order of `spec.remotes`, because a focus message has an index into
// that list.
type PlayerIdleBus struct {
	Address     string `json:"address"`
	StatusTopic string `json:"statusTopic"`
	VolumeTopic string `json:"volumeTopic,omitempty"`

	// PowerTopic is where the operator publishes wake and sleep for the
	// screen, when `spec.power` changes and when a TV sends a CEC
	// message that needs the screen, such as Set Stream Path.
	PowerTopic string `json:"powerTopic,omitempty"`

	CommandsTopic string             `json:"commandsTopic"`
	PanelTopic    string             `json:"panelTopic"`
	Remotes       []PlayerIdleRemote `json:"remotes,omitempty"`
}

// PlayerIdleRemote is the topic that a controller's presses arrive on
// and the topic of its focus, which names the `Player` that the
// controller drives. The client acts on a press only when the focus
// names this `Player`. The topic that cycles the focus is the focus
// topic plus `/cycle`.
type PlayerIdleRemote struct {
	Events string `json:"events"`
	Focus  string `json:"focus"`
}

// The panel states, as the `Display` reports them.
const (
	panelOn           = "On"
	panelBacklightOff = "BacklightOff"
	panelOff          = "Off"
)

const (
	playerPlaying  = "Playing"
	playerStarting = "Starting"
	playerIdle     = "Idle"
)

// PlayerSpec selects the devices of a `Player`. `display` and `render`
// are single because one pod drives one screen through one GPU, and
// `sinks` is a list because a `Player` can play through several audio
// outputs. The CRD requires a display or at least one sink.
type PlayerSpec struct {
	// Zone is shown in the `Zone` printer column and is the `zone`
	// label of the `media_players` metric. The operator does nothing
	// else with it.
	Zone string `json:"zone,omitempty"`

	// DisplayName is the name that the idle screen shows for this
	// `Player`. When it is unset, the idle screen shows the object name.
	DisplayName string `json:"displayName,omitempty"`

	Display *PlayerDevice  `json:"display,omitempty"`
	Sinks   []PlayerDevice `json:"sinks,omitempty"`
	Render  *PlayerDevice  `json:"render,omitempty"`
	Remotes []PlayerRemote `json:"remotes,omitempty"`

	// The `Player`'s language preferences. A nil list, or an empty
	// `subtitles` or `sdh`, leaves that preference to the default
	// `MediaPreferences`, and a `Play` can override each one.
	AudioLanguages    []string `json:"audioLanguages,omitempty"`
	SubtitleLanguages []string `json:"subtitleLanguages,omitempty"`
	Subtitles         string   `json:"subtitles,omitempty"`
	SDH               string   `json:"sdh,omitempty"`

	// Idle overrides the idle settings of the default
	// `MediaPreferences`, one field at a time, so a `Player` sets only
	// the fields that differ.
	Idle *IdlePolicy `json:"idle,omitempty"`

	// Power is `On` or `Off`, and an absent field means `On`. A
	// remote's power keys and kubectl both write it, and the operator
	// switches the hardware to match (`playerpower.go`).
	Power string `json:"power,omitempty"`

	// Passthrough is `On` or `Off`, and an absent field means `Off`.
	// `On` sends a film's Dolby or DTS track to the A/V receiver
	// encoded, when the sink and the receiver allow it
	// (`passthrough.go`).
	Passthrough string `json:"passthrough,omitempty"`
}

// IdlePolicy is what the idle screen does while nothing plays. A
// `Player` and the default `MediaPreferences` each have one, and the
// operator takes each field from the `Player` when it is set there and
// from `MediaPreferences` when it is not.
type IdlePolicy struct {
	// Image is the container image of the idle screen. It runs its own
	// entrypoint and reads the `Player`'s state from the bus. When
	// neither policy sets it, the operator runs the idle client image of
	// its own version.
	Image string `json:"image,omitempty"`

	// FadeAfterSeconds is how many seconds of inactivity pass before
	// the idle screen fades to black. Zero turns the fade off. It is a
	// pointer because an absent value falls back to `MediaPreferences`
	// and zero does not.
	FadeAfterSeconds *int64 `json:"fadeAfterSeconds,omitempty"`

	// OffAfterSeconds is how many seconds of inactivity pass before the
	// panel goes dark. Zero, or absent in both policies, leaves it lit.
	OffAfterSeconds *int64 `json:"offAfterSeconds,omitempty"`

	// OffMode is how the operator darkens the panel after
	// `offAfterSeconds`.
	OffMode string `json:"offMode,omitempty"`

	// Controller is the name of the operator that draws the idle
	// screen, a domain-qualified name like the `controllerName` of a
	// `GatewayClass`. When neither policy sets it, the controller is
	// `media.liken.sh/idle-screen`, which is this operator.
	Controller string `json:"controller,omitempty"`
}

// A backlight at zero still answers DDC. Some panels stop answering DDC
// when they are powered off, so set `power` only for a panel that is
// known to wake again.
const (
	offModeBacklight = "backlight"
	offModePower     = "power"
)

// PlayerRemote is a controller that the `Player` owns. `name` is a
// `Remote` in the same namespace, and the command sidecar of each `Play`
// reads that controller's presses. The `Keymap` is on the `Remote` and
// not here, so a controller's buttons map the same way on every
// `Player`.
type PlayerRemote struct {
	Name string `json:"name"`

	// DisplayName is the name that the idle screen shows for this
	// controller in its parts list. When it is unset, the idle screen
	// shows `name`.
	DisplayName string `json:"displayName,omitempty"`
}

// PlayerDevice selects one device. `class`, `selector`, and
// `parameters` become the `DeviceClass` name, a CEL selector, and an
// opaque config block of a claim request.
type PlayerDevice struct {
	Class string `json:"class"`

	// DisplayName is the name that the idle screen shows for this
	// device in its parts list, such as Built-in Speakers. When it is
	// unset, the idle screen shows the `DeviceClass` name.
	DisplayName string `json:"displayName,omitempty"`

	Selector   string            `json:"selector,omitempty"`
	Parameters *DeviceParameters `json:"parameters,omitempty"`
}

// DeviceParameters are parameters for the device's DRA driver. `values`
// is raw JSON because the driver defines its shape, and the operator
// copies it onto the claim without reading it.
type DeviceParameters struct {
	Driver string          `json:"driver"`
	Values json.RawMessage `json:"values,omitempty"`
}

// A Play is one run of media on a `Player`. Like a `Job`, it runs once
// to completion, and its status can be read until its
// `ttlSecondsAfterFinished` passes or a person deletes it.
type Play struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PlaySpec   `json:"spec"`
	Status     PlayStatus `json:"status"`
}

// PlaySpec names the `Player` to play on and the items to play in
// order. `start` is the position in the first item where playback
// begins. The CRD allows one entry in `players`, and the field is a
// list so that a later version can allow several without a new field
// name.
type PlaySpec struct {
	Players []string   `json:"players"`
	Items   []PlayItem `json:"items"`
	Start   string     `json:"start,omitempty"`

	// TrickplayInterval is the time that one trickplay tile covers, as
	// a Go duration such as `10s`. Jellyfin writes no manifest beside
	// the sprite sheets, and pads the last sheet, so the interval cannot
	// be computed from the files. When it is empty, the interval is
	// `10s`.
	TrickplayInterval string `json:"trickplayInterval,omitempty"`

	// TTLSecondsAfterFinished is how many seconds a Finished `Play`
	// remains before the operator deletes it. Zero deletes it on the
	// pass that reads it as Finished, and absent uses
	// `defaultTTLSecondsAfterFinished`, so the field is a pointer.
	//
	// It is on the `Play` and not in operator configuration, because
	// the program that creates a `Play` decides how long the record is
	// useful to it. For example, a library app keeps each `Play` for as
	// long as its continue-watching list reads it.
	TTLSecondsAfterFinished *int64 `json:"ttlSecondsAfterFinished,omitempty"`

	// The `Play`'s language preferences, which override the
	// `Player`'s. A nil list, or an empty `subtitles` or `sdh`, leaves
	// that preference to the `Player`.
	AudioLanguages    []string `json:"audioLanguages,omitempty"`
	SubtitleLanguages []string `json:"subtitleLanguages,omitempty"`
	Subtitles         string   `json:"subtitles,omitempty"`
	SDH               string   `json:"sdh,omitempty"`

	// Next is what to play after this `Play`. The display offers it on
	// the scrubber, and the program that wrote the `Play` starts it
	// when a person accepts the offer.
	Next *PlayNext `json:"next,omitempty"`
}

// PlayNext is the offer of what to play next. The display draws
// `reason`, `title`, and `detail` as given. The operator resolves `art`
// the way it resolves an item's art.
type PlayNext struct {
	Reason string `json:"reason,omitempty"`
	Title  string `json:"title,omitempty"`
	Detail string `json:"detail,omitempty"`
	Art    string `json:"art,omitempty"`

	// RemainingSeconds is how long the next item plays from where it
	// starts. The display adds it to the current time to show when the
	// next item would end. Nil means the length is unknown, and the
	// display then shows no end time.
	RemainingSeconds *int64 `json:"remainingSeconds,omitempty"`

	// Request is raw JSON that the program that wrote the `Play`
	// defines. The operator does not read it, and publishes it on the
	// bus when a person accepts the offer.
	Request json.RawMessage `json:"request,omitempty"`
}

// PlayItem is a media URI and an optional `Presentation`. Without a
// `Presentation`, the display shows what mpv reads from the file.
type PlayItem struct {
	URI          string        `json:"uri"`
	Presentation *Presentation `json:"presentation,omitempty"`
}

// Presentation is how the library describes an item, which the display
// prefers over the tags in the file. Its references to files
// beside the media are resolved the way the media URI is, so an `nfs`
// or claim reference becomes a path on the media's mount, and an
// `https` reference stays a URL.
type Presentation struct {
	Type string `json:"type,omitempty"`
	Hint string `json:"hint,omitempty"`

	// Role is the item's part in the work: `trailer`, or empty for the
	// work itself. The display marks a trailer on the line under the
	// title.
	Role string `json:"role,omitempty"`

	Title        string `json:"title,omitempty"`
	Series       string `json:"series,omitempty"`
	Season       int    `json:"season,omitempty"`
	Episode      int    `json:"episode,omitempty"`
	EpisodeTitle string `json:"episodeTitle,omitempty"`
	Year         int    `json:"year,omitempty"`
	Date         string `json:"date,omitempty"`
	Logo         string `json:"logo,omitempty"`

	// The text fields for music. When one is empty, the display shows
	// the file's `artist` or `album` tag.
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`

	// Art is a reference to the item's cover image. The music layout
	// draws it first, and falls back to the picture inside the file and
	// then a cover image beside it.
	Art string `json:"art,omitempty"`

	// Trickplay is a reference to the item's `X.trickplay` directory.
	// The display shows a tile from its sprite sheets at the scrub
	// cursor.
	Trickplay string `json:"trickplay,omitempty"`

	// Appearances is a reference to the item's `X.spans.json` in
	// `.liken/appearances/` beside the video, which lists when each
	// credited person is on screen. While the film is paused, the
	// display shows the people on screen at the playhead.
	Appearances string `json:"appearances,omitempty"`

	// Contributors is a reference to the library's `.contributors`
	// directory. The spans file gives each person's portrait as a path
	// under this directory.
	Contributors string `json:"contributors,omitempty"`

	// Marks are candidate spans for the intro, the recap, the credits,
	// and similar parts of the file. A community database can return
	// several spans of one kind, from different submissions or release
	// versions. The library sends all of them, and the operator passes
	// them on unchanged, so only the display merges them.
	Marks []PlayMark `json:"marks,omitempty"`

	// Subtitles is what the library measured of each subtitle track,
	// which mpv does not report: the number of events, and for an image
	// track the place and height of its text. A forced track with no flag and
	// no title can be found only by its small event count, and a track
	// with no events shows nothing. The command sidecar matches each
	// entry to a track of mpv's `track-list` when it chooses a track.
	// `file` is a base name and not a reference, so the resolver does
	// not rewrite it.
	Subtitles []PlaySubtitle `json:"subtitles,omitempty"`

	// Audio is the format of each audio stream of the file, from the
	// library's probe. mpv reports the profile and the channel layout only
	// of the audio track that it decodes, so the display reads these
	// entries to name the format of the other tracks. The display matches
	// an entry to a track of mpv's `track-list` by `ff-index`. The operator
	// copies the entries to the display and does not read them.
	Audio []PlayAudio `json:"audio,omitempty"`
}

// PlayAudio is the format of one audio stream, in ffprobe's own values.
// `stream` is the stream index in the container, which ffprobe reports as
// the index and mpv as `ff-index`. An empty `profile` or `layout` and a
// zero `channels` are values that ffprobe did not report.
type PlayAudio struct {
	Stream   int    `json:"stream"`
	Codec    string `json:"codec,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Layout   string `json:"layout,omitempty"`
}

// PlaySubtitle is what the library measured of one subtitle track. The
// CRD requires exactly one of `stream` and `file`. `stream` is the
// stream index in the container, which ffprobe reports as the index and
// mpv as `ff-index`. `file` is the base name of a subtitle file beside
// the media. `events` is nil when the count is unknown, which differs
// from zero. `image` is nil for a track that is not an image or that the
// library did not measure.
type PlaySubtitle struct {
	Stream *int               `json:"stream,omitempty"`
	File   string             `json:"file,omitempty"`
	Events *int64             `json:"events,omitempty"`
	Image  *PlaySubtitleImage `json:"image,omitempty"`
}

// PlaySubtitleImage is where an image subtitle track draws its text and
// how tall the text is, in rows of the track's own canvas. The display
// uses the three numbers to move the track's last line to the house row
// and shrink it to the house size. The operator copies them to the
// display and does not read them.
type PlaySubtitleImage struct {
	CanvasHeight int `json:"canvasHeight"`
	LineHeight   int `json:"lineHeight"`
	InkBottom    int `json:"inkBottom"`
}

// PlayMark is one candidate span. An absent `start` is the start of the
// file, and an absent `end` is the end of the file, from the duration
// that mpv reports.
type PlayMark struct {
	// Kind is `intro`, `recap`, `credits`, `post-credits` (a scene after
	// the credits), or `preview`. The display ignores a kind it does not
	// know, so a library can send a new kind before the display
	// supports it.
	Kind  string   `json:"kind"`
	Start *float64 `json:"start,omitempty"`
	End   *float64 `json:"end,omitempty"`

	// Source is the database that the span came from, such as
	// `theintrodb`. The display does not read it.
	Source string `json:"source,omitempty"`
}

// PlayStatus is written by the operator. `item` counts from 1, and
// `message` explains the phase when it needs more than one word. Every
// field is omitted when empty, so a printer column with no value is
// blank in kubectl.
type PlayStatus struct {
	Phase    string `json:"phase,omitempty"`
	Activity string `json:"activity,omitempty"`
	Paused   bool   `json:"paused,omitempty"`
	Item     int    `json:"item,omitempty"`
	Position string `json:"position,omitempty"`
	Duration string `json:"duration,omitempty"`
	Pod      string `json:"pod,omitempty"`
	Message  string `json:"message,omitempty"`

	// FinishedAt is when the operator first read the phase as Finished,
	// in RFC 3339. `ttlSecondsAfterFinished` counts from this time, and
	// the time is in the status so that it survives an operator
	// restart.
	FinishedAt string `json:"finishedAt,omitempty"`

	// The language preferences after the `Play`, the `Player`, and the
	// default `MediaPreferences` are combined.
	AudioLanguages    []string `json:"audioLanguages,omitempty"`
	SubtitleLanguages []string `json:"subtitleLanguages,omitempty"`
	Subtitles         string   `json:"subtitles,omitempty"`
	SDH               string   `json:"sdh,omitempty"`

	// The languages of the audio and subtitle tracks that mpv selected,
	// so a person can see when a preferred language matched no track.
	AudioLanguage    string `json:"audioLanguage,omitempty"`
	SubtitleLanguage string `json:"subtitleLanguage,omitempty"`

	// The conditions report parts of the pod that `phase` does not
	// show. `phase` follows the pod's phase, and a sidecar can restart
	// while the pod stays Running.
	Conditions []PlayCondition `json:"conditions,omitempty"`
}

// A `Play` condition sets `observedGeneration`.
type PlayCondition = conditions.Condition

// The `Play` phases, in the words that `Job`s and `Pod`s use. Only
// Finished is final. The operator reconciles a Failed `Play` again and
// can resume it.
const (
	phasePending  = "Pending"
	phaseRunning  = "Running"
	phaseFinished = "Finished"
	phaseFailed   = "Failed"
)

// The activity is the phase combined with the paused flag, so a paused
// `Play` reads Paused while its phase is still Running.
const (
	activityStarting = "Starting"
	activityPlaying  = "Playing"
	activityPaused   = "Paused"
	activityFinished = "Finished"
	activityFailed   = "Failed"
)

// playActivity returns an empty string for an empty phase, so a status
// that is not written yet stays blank.
func playActivity(phase string, paused bool) string {
	switch phase {
	case phasePending:
		return activityStarting
	case phaseRunning:
		if paused {
			return activityPaused
		}
		return activityPlaying
	case phaseFinished:
		return activityFinished
	case phaseFailed:
		return activityFailed
	}
	return ""
}

func terminalPhase(phase string) bool {
	return phase == phaseFinished || phase == phaseFailed
}

// finishedPhase reports whether the phase is Finished. A pass deletes
// the pod and claim of a Finished `Play` and does not reconcile it.
func finishedPhase(phase string) bool {
	return phase == phaseFinished
}

const (
	subtitlesOn   = "on"
	subtitlesOff  = "off"
	subtitlesAuto = "auto"
)

// mediaPreferencesName is the only name that a `MediaPreferences` can
// have. A CEL rule in the CRD enforces it, so a cluster has at most one.
const mediaPreferencesName = "default"

// MediaPreferences is the cluster-wide default for the language, idle,
// and time zone settings. A `Player` overrides its language and idle
// settings, and a `Play` overrides the language settings again.
type MediaPreferences struct {
	APIVersion string               `json:"apiVersion,omitempty"`
	Kind       string               `json:"kind,omitempty"`
	Metadata   ObjectMeta           `json:"metadata"`
	Spec       MediaPreferencesSpec `json:"spec"`
}

type MediaPreferencesSpec struct {
	AudioLanguages    []string `json:"audioLanguages,omitempty"`
	SubtitleLanguages []string `json:"subtitleLanguages,omitempty"`
	Subtitles         string   `json:"subtitles,omitempty"`

	// SDH is `prefer`, `avoid`, or empty. It ranks a subtitle track for
	// the deaf and hard of hearing ahead of, behind, or equal to an
	// ordinary track in the same language.
	SDH string `json:"sdh,omitempty"`

	// TimeZone is an IANA name such as `America/New_York`, with no
	// override on a `Player` or a `Play`. The operator sets it as `TZ`
	// in the playback pod and the idle pod, so their clocks show local
	// time.
	TimeZone string `json:"timeZone,omitempty"`

	Idle *IdlePolicy `json:"idle,omitempty"`
}

// A Remote is one physical controller: its device and, when its model
// needs one, the `Keymap` of that model. The operator runs a reader pod
// for each `Remote`, which publishes the controller's input events on
// the bus whether or not anything plays.
type Remote struct {
	APIVersion string       `json:"apiVersion,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       RemoteSpec   `json:"spec"`
	Status     RemoteStatus `json:"status"`
}

// RemoteStatus is written by the operator. `player` is the `Player`
// that the controller's retained focus message names, so kubectl shows
// which `Player` a press reaches. It is empty while no `Player` in the
// namespace lists the `Remote`.
//
// `unbound` lists each code that the controller declares and its
// `Keymap` does not bind. It is absent until the reader pod has
// reported.
//
// `peripheral` is the bluetooth-operator `Peripheral` of the claimed
// device, which shows the controller's link and charge. Its name is the
// device's address in lowercase dashed form, which is also the device
// name in the claim's allocation. It is empty while the claim has no
// allocation, and for a controller that another DRA driver publishes.
type RemoteStatus struct {
	Player     string        `json:"player,omitempty"`
	Peripheral string        `json:"peripheral,omitempty"`
	Unbound    []UnboundCode `json:"unbound,omitempty"`
}

// UnboundCode is one code that the controller declares and the `Keymap`
// does not bind: the raw evdev code, its kernel name when it has one,
// and the event type that carries it.
type UnboundCode struct {
	Code uint16 `json:"code"`
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
}

// RemoteSpec names no `Player`, because a `Player` lists the `Remote`s
// it owns in `spec.remotes`.
//
// `discovery` is for finding the codes of a controller. The reader pod
// reads every device node of the claim and logs each event in the form
// that a `Keymap` uses. Changing it replaces the reader pod.
type RemoteSpec struct {
	Device    RemoteDevice `json:"device"`
	Keymap    string       `json:"keymap,omitempty"`
	Discovery bool         `json:"discovery,omitempty"`
}

// RemoteDevice selects the controller the way `PlayerDevice` selects a
// display. A `Remote` uses `parameters` to tell the DRA driver which
// classes of input device to give the pod.
type RemoteDevice struct {
	Class      string            `json:"class"`
	Selector   string            `json:"selector,omitempty"`
	Parameters *DeviceParameters `json:"parameters,omitempty"`
}

// A Keymap maps the controls of one controller model to the kernel key
// names that they should report. Every `Remote` of that model can share
// it, and a model whose controls already report the right names needs
// none.
type Keymap struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       KeymapSpec `json:"spec"`
}

// The CRD requires at least one entry in `buttons` and `axes`
// together.
type KeymapSpec struct {
	Buttons []KeymapButton `json:"buttons,omitempty"`
	Axes    []KeymapAxis   `json:"axes,omitempty"`
}

// KeymapRepeat makes a key repeat while the control is held. The reader
// pod publishes the press, waits `delay`, and then publishes value 2
// every `interval` until the release. Both are Go durations, and each
// has a default when empty. Without this block, a control repeats only
// when the kernel repeats it.
type KeymapRepeat struct {
	Delay    string `json:"delay,omitempty"`
	Interval string `json:"interval,omitempty"`
}

// Press is the control, as an evdev key name from `buttonCodes`. Key is
// the `KEY_*` name to report in its place, or `none` to drop the
// control.
type KeymapButton struct {
	Press  string        `json:"press"`
	Key    string        `json:"key"`
	Repeat *KeymapRepeat `json:"repeat,omitempty"`
}

// A hat axis reports -1 and 1 as its two presses and 0 as the release,
// so each direction of an axis is its own entry with its own `value`.
type KeymapAxis struct {
	Axis   string        `json:"axis"`
	Value  int           `json:"value"`
	Key    string        `json:"key"`
	Repeat *KeymapRepeat `json:"repeat,omitempty"`
}

// The operator writes the spec of a ResourceClaim, and the scheduler
// writes the allocation into the status. A pass reads the allocation to
// learn which screen the idle claim was given and which `Sink`s the
// playback claim was given.
type ResourceClaim struct {
	APIVersion string               `json:"apiVersion,omitempty"`
	Kind       string               `json:"kind,omitempty"`
	Metadata   ObjectMeta           `json:"metadata"`
	Spec       ResourceClaimSpec    `json:"spec"`
	Status     *ResourceClaimStatus `json:"status,omitempty"`
}

type ResourceClaimSpec struct {
	Devices DeviceClaim `json:"devices"`
}

type DeviceClaim struct {
	Requests    []DeviceRequest            `json:"requests,omitempty"`
	Config      []DeviceClaimConfiguration `json:"config,omitempty"`
	Constraints []DeviceConstraint         `json:"constraints,omitempty"`
}

// DeviceConstraint makes the scheduler allocate devices with the same
// value of `matchAttribute` to the named requests, so that they come
// from the same piece of equipment.
type DeviceConstraint struct {
	Requests       []string `json:"requests,omitempty"`
	MatchAttribute string   `json:"matchAttribute,omitempty"`
}

// A container refers to a request by its name. `exactly` is the DRA
// field for a request with no alternatives.
type DeviceRequest struct {
	Name    string              `json:"name"`
	Exactly *ExactDeviceRequest `json:"exactly,omitempty"`
}

type ExactDeviceRequest struct {
	DeviceClassName string             `json:"deviceClassName"`
	AllocationMode  string             `json:"allocationMode,omitempty"`
	Count           int                `json:"count,omitempty"`
	Selectors       []DeviceSelector   `json:"selectors,omitempty"`
	Tolerations     []DeviceToleration `json:"tolerations,omitempty"`
}

type DeviceSelector struct {
	CEL *CELDeviceSelector `json:"cel,omitempty"`
}

type CELDeviceSelector struct {
	Expression string `json:"expression"`
}

// A device taint evicts the pod that uses the device. A toleration
// with `tolerationSeconds` sets how long a `Play` keeps running after
// its cable is unplugged.
type DeviceToleration struct {
	Key               string `json:"key,omitempty"`
	Operator          string `json:"operator,omitempty"`
	Effect            string `json:"effect,omitempty"`
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty"`
}

// The scheduler passes an opaque config block to the DRA driver without
// reading it. `requests` are the claim requests that it applies to.
type DeviceClaimConfiguration struct {
	Requests []string                   `json:"requests,omitempty"`
	Opaque   *OpaqueDeviceConfiguration `json:"opaque,omitempty"`
}

type OpaqueDeviceConfiguration struct {
	Driver     string          `json:"driver"`
	Parameters json.RawMessage `json:"parameters"`
}

// Pod is a pod that the operator creates: the playback pod of a `Play`,
// the idle pod of a `Player`, or the reader pod of a `Remote`.
type Pod struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PodSpec    `json:"spec"`
	Status     PodStatus  `json:"status"`
}

// A playback pod has `restartPolicy` `Never`, because the end of the
// pod is the end of the `Play`. Its native sidecars are in
// `initContainers`: an init container with `restartPolicy: Always`
// starts before the ordinary containers, runs beside them, and restarts
// without ending the pod.
type PodSpec struct {
	RestartPolicy string `json:"restartPolicy,omitempty"`
	// Every pod that this operator builds sets this to false, which
	// keeps the namespace's default `ServiceAccount` token out of it.
	AutomountServiceAccountToken  *bool              `json:"automountServiceAccountToken,omitempty"`
	TerminationGracePeriodSeconds *int64             `json:"terminationGracePeriodSeconds,omitempty"`
	ResourceClaims                []PodResourceClaim `json:"resourceClaims,omitempty"`
	InitContainers                []Container        `json:"initContainers,omitempty"`
	Containers                    []Container        `json:"containers"`
	Volumes                       []Volume           `json:"volumes,omitempty"`
	Tolerations                   []Toleration       `json:"tolerations,omitempty"`
}

// Toleration tolerates a taint on a node. `DeviceToleration` tolerates
// a taint on a device.
type Toleration struct {
	Key      string `json:"key"`
	Operator string `json:"operator,omitempty"`
	Value    string `json:"value,omitempty"`
	Effect   string `json:"effect,omitempty"`
}

// A pod names a claim once, and each container lists the requests of
// that claim that it uses, so a container gets no device that it does
// not list.
type PodResourceClaim struct {
	Name              string `json:"name"`
	ResourceClaimName string `json:"resourceClaimName,omitempty"`
}

// Command selects the mode of the sidecar image, which runs as the
// command sidecar of a playback pod and as the reader of a `Remote`'s
// pod. `restartPolicy` is set only on an init container, where `Always`
// makes it a native sidecar.
type Container struct {
	Name          string               `json:"name"`
	Image         string               `json:"image"`
	Command       []string             `json:"command,omitempty"`
	Args          []string             `json:"args,omitempty"`
	Env           []EnvVar             `json:"env,omitempty"`
	Ports         []ContainerPort      `json:"ports,omitempty"`
	Resources     ResourceRequirements `json:"resources"`
	VolumeMounts  []VolumeMount        `json:"volumeMounts,omitempty"`
	RestartPolicy string               `json:"restartPolicy,omitempty"`
}

// A `PodMonitor` selects a scrape target by the port's name, so the
// port number can change without an edit to the `PodMonitor`.
type ContainerPort struct {
	Name          string `json:"name"`
	ContainerPort int32  `json:"containerPort"`
}

// `containerresources.go` sets `requests` and `limits`.
type ResourceRequirements struct {
	Requests ResourceList     `json:"requests,omitempty"`
	Limits   ResourceList     `json:"limits,omitempty"`
	Claims   []ContainerClaim `json:"claims,omitempty"`
}

type ContainerClaim struct {
	Name    string `json:"name"`
	Request string `json:"request,omitempty"`
}

type EnvVar struct {
	Name      string        `json:"name"`
	Value     string        `json:"value,omitempty"`
	ValueFrom *EnvVarSource `json:"valueFrom,omitempty"`
}

// A pod can learn its own UID only through the downward API, because
// the API server assigns the UID after the operator sends the pod.
type EnvVarSource struct {
	FieldRef *ObjectFieldSelector `json:"fieldRef,omitempty"`
}

type ObjectFieldSelector struct {
	FieldPath string `json:"fieldPath"`
}

type VolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// An inline NFS volume needs no `PersistentVolume` and no CSI driver.
// The kubelet mounts it with the kernel's NFS client, through the mount
// helper in the `liken` OS image.
type Volume struct {
	Name                  string                             `json:"name"`
	NFS                   *NFSVolumeSource                   `json:"nfs,omitempty"`
	PersistentVolumeClaim *PersistentVolumeClaimVolumeSource `json:"persistentVolumeClaim,omitempty"`
	EmptyDir              *EmptyDirVolumeSource              `json:"emptyDir,omitempty"`
}

type NFSVolumeSource struct {
	Server   string `json:"server"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

// The kubelet resolves the `PersistentVolumeClaim` when it starts the
// pod, so the operator reads no `PersistentVolumeClaim` or
// `PersistentVolume`, and its RBAC has no rule for them. The volume is
// read-only here and on the mount, because a playback pod never writes
// to a library.
type PersistentVolumeClaimVolumeSource struct {
	ClaimName string `json:"claimName"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// The containers of a playback pod share files, such as mpv's IPC
// socket, through an emptyDir.
type EmptyDirVolumeSource struct{}

// PodStatus has the fields that the operator reads to derive the phase
// of a `Play`. The kubelet reports the native sidecars, the command
// sidecar and the display, under `initContainerStatuses`.
type PodStatus struct {
	Phase                 string            `json:"phase,omitempty"`
	Reason                string            `json:"reason,omitempty"`
	Message               string            `json:"message,omitempty"`
	Conditions            []PodCondition    `json:"conditions,omitempty"`
	ContainerStatuses     []ContainerStatus `json:"containerStatuses,omitempty"`
	InitContainerStatuses []ContainerStatus `json:"initContainerStatuses,omitempty"`
}

// The scheduler writes why it cannot place a pod, such as a missing
// claim, in the `PodScheduled` condition and not in the pod's message.
// The kubelet sets the `DisruptionTarget` condition when it ends a pod,
// and its lastTransitionTime is when the pod stopped running.
type PodCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// LastState shows how a restarting sidecar last exited, which its
// current state does not.
type ContainerStatus struct {
	Name         string         `json:"name"`
	State        ContainerState `json:"state"`
	LastState    ContainerState `json:"lastState"`
	RestartCount int            `json:"restartCount,omitempty"`
}

type ContainerState struct {
	Running    *ContainerStateRunning    `json:"running,omitempty"`
	Terminated *ContainerStateTerminated `json:"terminated,omitempty"`
}

// The operator reads only whether the running block is present.
type ContainerStateRunning struct{}

type ContainerStateTerminated struct {
	ExitCode   int    `json:"exitCode"`
	Reason     string `json:"reason,omitempty"`
	Message    string `json:"message,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

const (
	podPending   = "Pending"
	podRunning   = "Running"
	podSucceeded = "Succeeded"
	podFailed    = "Failed"
)
