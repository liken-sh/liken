package main

// The wire types are hand-written, the way liken and the sibling
// operators write theirs. The Kubernetes API is HTTPS that serves
// JSON, and importing client-go for a dozen structs brings informers,
// work queues, and a release cadence this program does not use. Each
// type carries only the fields this operator reads or writes; the
// API server fills in the rest.

import "encoding/json"

// The group this operator serves, and the two Kubernetes groups it
// writes into: claims live under resource.k8s.io and pods under the
// core group, because the objects a Play becomes are ordinary
// Kubernetes objects any tool can read.
const (
	mediaAPIVersion = "media.liken.sh/v1alpha1"
	claimAPIVersion = "resource.k8s.io/v1"
	podAPIVersion   = "v1"
)

// ObjectMeta carries what this operator reads or writes: name and
// namespace for the URL, resourceVersion for the conditional write, uid
// with ownerReferences for garbage collection, and labels so a watch
// selects the operator's own playback pods.
//
// Annotations carry the template hash the operator stamps on a standing
// claim and a standing pod, which is how a pass tells a live object from
// the object it would build now. deletionTimestamp is set by the API
// server on an object that is on its way out, and a standing pair with
// one set is left alone until the delete completes.
type ObjectMeta struct {
	Name              string            `json:"name,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
	OwnerReferences   []OwnerReference  `json:"ownerReferences,omitempty"`
}

// An ownerReference ties an object's life to its owner's: the
// garbage collector deletes the owned object when the owner goes,
// which is this operator's whole teardown. Controller is true
// because exactly one thing manages each pod and claim; there is no
// blockOwnerDeletion, because nothing here needs the owner to wait.
type OwnerReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	Controller bool   `json:"controller"`
}

// A list's own resourceVersion is the revision of the whole
// collection, which is what a watch resumes from.
type ListMeta struct {
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// A Player is equipment, not a running thing. It holds no claims of
// its own. The operator reads the spec and writes the status: the
// spec is the equipment a person declared, and the status is what
// plays on it now.
type Player struct {
	APIVersion string       `json:"apiVersion,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       PlayerSpec   `json:"spec"`
	Status     PlayerStatus `json:"status"`
}

// The status the operator writes onto a Player: whether it plays
// anything now, and the name of the Play that does. Both are
// omitempty, so an idle Player with no activity word yet shows blank
// rather than a zero.
type PlayerStatus struct {
	Activity string `json:"activity,omitempty"`
	Play     string `json:"play,omitempty"`
}

// A Player's activity is the coarse state a person scans for: it
// plays a Play now, a Play is starting on it, or it is free. The
// Play name beside it says which run, so the two columns together
// read as one sentence.
const (
	playerPlaying  = "Playing"
	playerStarting = "Starting"
	playerIdle     = "Idle"
)

type PlayerList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Player `json:"items"`
}

// The three device roles. Display and render are single because one
// pod drives one screen through one GPU; sinks is a list because a
// unit plays through however many outputs it has. The CRD requires a
// display or at least one sink.
//
// Remotes names the controllers the unit owns. Each entry names a Remote
// in the same namespace, and the Play's pod runs one translator sidecar
// for each, so a unit's controllers belong to its spec beside its
// display and its sinks.
type PlayerSpec struct {
	Zone string `json:"zone,omitempty"`

	// The human name of this unit, the one the idle screen and later
	// ambient surfaces show in place of the object name. It is the
	// household's word for the unit, such as Studio Lab. Unset, the idle
	// screen falls back to the Player's object name.
	DisplayName string `json:"displayName,omitempty"`

	Display *PlayerDevice  `json:"display,omitempty"`
	Sinks   []PlayerDevice `json:"sinks,omitempty"`
	Render  *PlayerDevice  `json:"render,omitempty"`
	Remotes []PlayerRemote `json:"remotes,omitempty"`

	// The per-Player override of the audio and subtitle language preferences.
	// A nil list, or an empty Subtitles, means this Player states nothing, so
	// resolution reads the default MediaPreferences instead.
	AudioLanguages    []string `json:"audioLanguages,omitempty"`
	SubtitleLanguages []string `json:"subtitleLanguages,omitempty"`
	Subtitles         string   `json:"subtitles,omitempty"`

	// Idle is this unit's idle screen policy. Resolution reads it field
	// by field over the default MediaPreferences, so a Player states
	// only what differs from the household.
	Idle *IdlePolicy `json:"idle,omitempty"`
}

// IdlePolicy is what the idle screen does while nothing plays. The same
// block is the override on a Player and the default on the household's
// MediaPreferences, so the two tiers resolve field by field.
type IdlePolicy struct {
	// FadeAfterSeconds is the quiet stretch before the idle screen
	// fades to black. Zero disables the automatic fade. A pointer,
	// because zero and absent differ: absent defers to the next tier.
	FadeAfterSeconds *int64 `json:"fadeAfterSeconds,omitempty"`
}

// One controller the Player owns. Name is the Remote in the same
// namespace. Keymap is a per-unit override of that Remote's own Keymap:
// set it, and this controller maps one way on this unit and its base
// keymap's way on another, so one gamepad's cross is play-pause on the
// theater and something else on the gaming unit.
type PlayerRemote struct {
	Name string `json:"name"`

	// The human name of this controller, the one the idle screen shows in
	// its parts list, such as Studio Dualsense Controller. Unset, the idle
	// screen falls back to Name, the Remote this entry references.
	DisplayName string `json:"displayName,omitempty"`

	Keymap string `json:"keymap,omitempty"`
}

// One device selection. The three fields become a DeviceClass name,
// a CEL selector, and an opaque config block on the claim.
type PlayerDevice struct {
	Class string `json:"class"`

	// The human name of this selection, the one the idle screen shows in
	// its parts list, such as Portable Screen or Built-in Speakers. Unset,
	// the idle screen falls back to the DeviceClass name, which says what
	// the selection is.
	DisplayName string `json:"displayName,omitempty"`

	Selector   string            `json:"selector,omitempty"`
	Parameters *DeviceParameters `json:"parameters,omitempty"`
}

// Values is raw JSON because the driver defines the parameters and
// this operator carries them onto the claim unread.
type DeviceParameters struct {
	Driver string          `json:"driver"`
	Values json.RawMessage `json:"values,omitempty"`
}

// A Play is one run of media on a Player, with a lifecycle analogous
// to a Job: it runs once to completion, and it stays for its status
// until its ttlSecondsAfterFinished passes or a person deletes it.
type Play struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PlaySpec   `json:"spec"`
	Status     PlayStatus `json:"status"`
}

// A Play names the players it runs on and the items to play in order,
// and Start is where in the first item the run begins. Players is a
// list of one today, because the carriage layer will let one Play
// reach several players in sync, and a list that grows a second
// element changes no field name when it does.
type PlaySpec struct {
	Players []string   `json:"players"`
	Items   []PlayItem `json:"items"`
	Start   string     `json:"start,omitempty"`
	// The seconds one trickplay tile covers, as a Go duration like 10s.
	// Jellyfin writes no manifest beside the sheets, and the last sheet is
	// padded, so the tile count cannot be read back. The Play declares the
	// interval, and it defaults to 10s when this is empty.
	TrickplayInterval string `json:"trickplayInterval,omitempty"`

	// How long a Finished Play stands before the operator deletes it, in
	// seconds. The field is a pointer because zero and absent mean
	// different things: zero deletes the Play on the pass that sees it
	// finished, and absent takes defaultTTLSecondsAfterFinished. A plain
	// int64 would read an unset field as zero and delete every Play at
	// once.
	//
	// The window is the Play's own affair and not operator configuration,
	// because whoever creates a Play knows how long the record is worth
	// keeping: a library app sets the window its continue-watching feature
	// reads, and two apps on one cluster choose differently.
	TTLSecondsAfterFinished *int64 `json:"ttlSecondsAfterFinished,omitempty"`

	// The per-Play override of the language preferences, the most specific tier.
	// A nil list, or an empty Subtitles, means this Play states nothing, so
	// resolution reads the Player next.
	AudioLanguages    []string `json:"audioLanguages,omitempty"`
	SubtitleLanguages []string `json:"subtitleLanguages,omitempty"`
	Subtitles         string   `json:"subtitles,omitempty"`
}

// A PlayItem is one entry in the list: the media URI and an optional
// Presentation. The block is optional because a loose file needs only
// its URI, and the display falls back to what mpv reads from the file.
type PlayItem struct {
	URI          string        `json:"uri"`
	Presentation *Presentation `json:"presentation,omitempty"`
}

// A Presentation declares what mpv cannot read from the file, so the
// display renders an item the way the library that fed liken describes
// it, not the way a container's tags happen to read.
//
// It carries the text fields and two art references, the logo and the
// trickplay directory. The resolver rewrites each the way it rewrites the
// media URI, so an nfs reference shares the media's mount and an https
// reference stays a URL.
type Presentation struct {
	Type         string `json:"type,omitempty"`
	Hint         string `json:"hint,omitempty"`
	Title        string `json:"title,omitempty"`
	Series       string `json:"series,omitempty"`
	Season       int    `json:"season,omitempty"`
	Episode      int    `json:"episode,omitempty"`
	EpisodeTitle string `json:"episodeTitle,omitempty"`
	Year         int    `json:"year,omitempty"`
	Date         string `json:"date,omitempty"`
	Logo         string `json:"logo,omitempty"`
	// A reference to the item's X.trickplay directory, resolved the way the
	// logo is. The display shows a tile from its sprite sheets on the scrub
	// cursor.
	Trickplay string `json:"trickplay,omitempty"`
}

// The status the operator alone writes: the phase, the activity
// word, the paused flag, the item counting from 1, the playhead, the
// pod's name, and the message that says why when a word is not
// enough. Every field is omitempty so a column with nothing to say
// shows blank in kubectl rather than a zero.
type PlayStatus struct {
	Phase    string `json:"phase,omitempty"`
	Activity string `json:"activity,omitempty"`
	Paused   bool   `json:"paused,omitempty"`
	Item     int    `json:"item,omitempty"`
	Position string `json:"position,omitempty"`
	Duration string `json:"duration,omitempty"`
	Pod      string `json:"pod,omitempty"`
	Message  string `json:"message,omitempty"`

	// When the operator first read this run's phase as Finished, in RFC
	// 3339. The time-to-live after finishing counts from here and not from
	// the Play's creation, so the window measures the end of the film. It
	// lives on the status rather than in the operator's memory, so an
	// operator that restarts reads the clock back from the API server.
	FinishedAt string `json:"finishedAt,omitempty"`

	// The preferences this run resolved, the console-parity record of what the
	// three tiers settled on.
	AudioLanguages    []string `json:"audioLanguages,omitempty"`
	SubtitleLanguages []string `json:"subtitleLanguages,omitempty"`
	Subtitles         string   `json:"subtitles,omitempty"`

	// The language of the audio track and the subtitle track mpv selected, so a
	// code that matched no track shows plainly.
	AudioLanguage    string `json:"audioLanguage,omitempty"`
	SubtitleLanguage string `json:"subtitleLanguage,omitempty"`
}

// The four phases, in the words Jobs and Pods use so nobody learns a
// new vocabulary. Finished and Failed are terminal: a phase moves
// forward only.
const (
	phasePending  = "Pending"
	phaseRunning  = "Running"
	phaseFinished = "Finished"
	phaseFailed   = "Failed"
)

// The activity is the one word a person reads to know what the Play
// is doing right now. The phase is the lifecycle, which never goes
// backward; the activity folds the paused flag into that lifecycle,
// so a paused run reads Paused where the phase still reads Running.
const (
	activityStarting = "Starting"
	activityPlaying  = "Playing"
	activityPaused   = "Paused"
	activityFinished = "Finished"
	activityFailed   = "Failed"
)

// playActivity is the phase and the paused flag folded into one
// word. A Play with no phase yet has no activity either, so an
// unwritten status stays blank.
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

// A Play in a terminal phase gets no further reconcile. Its pod and
// claim stay for reading until the Play is deleted, and the garbage
// collector tears them down then.
func terminalPhase(phase string) bool {
	return phase == phaseFinished || phase == phaseFailed
}

// finishedPhase reports the one terminal phase the pass acts on. Only a
// Finished Play is done, so the pass skips it. A Failed Play resumes, so the
// pass reconciles it. terminalPhase still counts both, for the focus rule
// that a crashed run holds no controller.
func finishedPhase(phase string) bool {
	return phase == phaseFinished
}

type PlayList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Play   `json:"items"`
}

// The three subtitle modes a preference tier may state.
const (
	subtitlesOn   = "on"
	subtitlesOff  = "off"
	subtitlesAuto = "auto"
)

// mediaPreferencesName is the one name a MediaPreferences may take. The CRD
// pins it with a CEL rule, so a second default is rejected at apply, and this
// operator reads the singleton by this name.
const mediaPreferencesName = "default"

// MediaPreferences is the cluster-scoped household default for audio and
// subtitle languages, the lowest of the three tiers a Play resolves.
type MediaPreferences struct {
	APIVersion string               `json:"apiVersion,omitempty"`
	Kind       string               `json:"kind,omitempty"`
	Metadata   ObjectMeta           `json:"metadata"`
	Spec       MediaPreferencesSpec `json:"spec"`
}

// MediaPreferencesSpec holds the default language fields. Resolution reads
// each field only when no more specific tier states it.
type MediaPreferencesSpec struct {
	AudioLanguages    []string `json:"audioLanguages,omitempty"`
	SubtitleLanguages []string `json:"subtitleLanguages,omitempty"`
	Subtitles         string   `json:"subtitles,omitempty"`

	// The household wall-clock zone, an IANA name like America/New_York. One
	// per cluster, with no per-Play or per-Player override. The player pod
	// reads it as TZ, so the display clock shows local time.
	TimeZone string `json:"timeZone,omitempty"`

	// Idle is the household default idle screen policy, read for each
	// field a Player's own block leaves unset.
	Idle *IdlePolicy `json:"idle,omitempty"`
}

type MediaPreferencesList struct {
	Metadata ListMeta           `json:"metadata"`
	Items    []MediaPreferences `json:"items"`
}

// A Remote is one physical controller: the device it is, the Keymap
// for its model, and the player it drives. This operator only reads
// it, and only at the moment a Play's pod is built.
type Remote struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       RemoteSpec `json:"spec"`
}

// A Remote holds its device selector and the Keymap for its model,
// and it names no player. A Player names the Remotes it owns through
// spec.remotes, so the unit that owns a controller is the one that
// lists it.
type RemoteSpec struct {
	Device RemoteDevice `json:"device"`
	Keymap string       `json:"keymap"`
}

// The controller is selected the way a Player's display is, by a
// DeviceClass and a CEL expression. There is no parameters field,
// because nothing prepares an input device the way a codec prepares
// a sink.
type RemoteDevice struct {
	Class    string `json:"class"`
	Selector string `json:"selector,omitempty"`
}

type RemoteList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Remote `json:"items"`
}

// A Keymap is one controller model's table from buttons and axes to
// named actions, written once per model and shared by every Remote
// of that model.
type Keymap struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       KeymapSpec `json:"spec"`
}

// Buttons and axes are separate lists because they bind differently:
// a button is a press, and an axis entry names a direction as well.
// The CRD requires at least one entry across the two.
type KeymapSpec struct {
	Buttons []KeymapButton `json:"buttons,omitempty"`
	Axes    []KeymapAxis   `json:"axes,omitempty"`
}

// KeymapList is the cluster-scoped collection the operator lists and
// watches, so a Keymap edit wakes the loop that recompiles and
// republishes it.
type KeymapList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Keymap `json:"items"`
}

// A KeymapRepeat makes a binding repeat while the control is held. The
// player pod fires the action on the press, waits the delay, then
// re-fires it every interval until the reader publishes the release. The
// delay and the interval are durations, like 400ms or 1s, and each takes
// a default when it is empty. A binding with no repeat block fires once
// per press, whatever the action is.
type KeymapRepeat struct {
	Delay    string `json:"delay,omitempty"`
	Interval string `json:"interval,omitempty"`
}

// Press is an evdev key name out of buttonCodes, Action is a word
// from the vocabulary in input.go, and Amount belongs only to the
// three actions that move by one: seek, volume, and chapter.
type KeymapButton struct {
	Press  string        `json:"press"`
	Action string        `json:"action"`
	Amount int           `json:"amount,omitempty"`
	Repeat *KeymapRepeat `json:"repeat,omitempty"`
}

// An axis entry adds the value, because a hat axis reports -1 and 1
// as its two presses and 0 as the release: one axis is two bindable
// directions.
type KeymapAxis struct {
	Axis   string        `json:"axis"`
	Value  int           `json:"value"`
	Action string        `json:"action"`
	Amount int           `json:"amount,omitempty"`
	Repeat *KeymapRepeat `json:"repeat,omitempty"`
}

// A ResourceClaim is the request for hardware. The operator writes
// only the spec; the scheduler writes an allocation into the status,
// and nothing here reads it, because the pod's own phase already
// says whether the devices were found.
type ResourceClaim struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   ObjectMeta        `json:"metadata"`
	Spec       ResourceClaimSpec `json:"spec"`
}

type ResourceClaimSpec struct {
	Devices DeviceClaim `json:"devices"`
}

// Requests name the devices by role and config carries driver
// parameters for them. Both are claim-level, so one claim covers the
// whole player.
type DeviceClaim struct {
	Requests []DeviceRequest            `json:"requests,omitempty"`
	Config   []DeviceClaimConfiguration `json:"config,omitempty"`
}

// The request name is the role the pod refers to, and exactly is
// DRA's one-of that holds a plain request.
type DeviceRequest struct {
	Name    string              `json:"name"`
	Exactly *ExactDeviceRequest `json:"exactly,omitempty"`
}

// ExactCount with count 1 asks for one device, no more offered and
// no fewer accepted. The selector list is omitted when the class
// alone chooses the device.
type ExactDeviceRequest struct {
	DeviceClassName string             `json:"deviceClassName"`
	AllocationMode  string             `json:"allocationMode,omitempty"`
	Count           int                `json:"count,omitempty"`
	Selectors       []DeviceSelector   `json:"selectors,omitempty"`
	Tolerations     []DeviceToleration `json:"tolerations,omitempty"`
}

// A selector is a CEL expression over device.attributes, the same
// expression a hand-written claim would carry.
type DeviceSelector struct {
	CEL *CELDeviceSelector `json:"cel,omitempty"`
}

type CELDeviceSelector struct {
	Expression string `json:"expression"`
}

// A device taint evicts the pod that holds the device. A toleration
// with tolerationSeconds is how long the play survives an unplugged
// cable.
type DeviceToleration struct {
	Key               string `json:"key,omitempty"`
	Operator          string `json:"operator,omitempty"`
	Effect            string `json:"effect,omitempty"`
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty"`
}

// An opaque config block reaches the driver unread by the scheduler,
// and requests names which of the claim's requests it applies to.
type DeviceClaimConfiguration struct {
	Requests []string                   `json:"requests,omitempty"`
	Opaque   *OpaqueDeviceConfiguration `json:"opaque,omitempty"`
}

type OpaqueDeviceConfiguration struct {
	Driver     string          `json:"driver"`
	Parameters json.RawMessage `json:"parameters"`
}

// The playback pod. The operator writes its spec once and reads its
// status every pass, because the pod's phase is where the Play's
// phase comes from.
type Pod struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PodSpec    `json:"spec"`
	Status     PodStatus  `json:"status"`
}

// PodList is the pod collection ListPlaybackPods returns. Its
// resourceVersion is where the pod watch begins.
type PodList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Pod    `json:"items"`
}

// The pod spec's few fields: restartPolicy Never because the pod's
// end is the play's end, and the short grace period because mpv
// exits promptly on SIGTERM.
//
// initContainers is where a native sidecar goes: an init container
// with restartPolicy Always starts before the ordinary containers,
// runs beside them, and restarts alone, without ending the pod.
type PodSpec struct {
	RestartPolicy                 string             `json:"restartPolicy,omitempty"`
	TerminationGracePeriodSeconds *int64             `json:"terminationGracePeriodSeconds,omitempty"`
	ResourceClaims                []PodResourceClaim `json:"resourceClaims,omitempty"`
	InitContainers                []Container        `json:"initContainers,omitempty"`
	Containers                    []Container        `json:"containers"`
	Volumes                       []Volume           `json:"volumes,omitempty"`
}

// A pod names a claim once, and its containers refer to that name
// request by request, which is what keeps a device out of a
// container that must not hold it.
type PodResourceClaim struct {
	Name              string `json:"name"`
	ResourceClaimName string `json:"resourceClaimName,omitempty"`
}

// Command replaces the image's entrypoint, which is how one image
// runs the playback pod's two roles. RestartPolicy is set only on an
// init container, where Always is what makes it a sidecar.
type Container struct {
	Name          string               `json:"name"`
	Image         string               `json:"image"`
	Command       []string             `json:"command,omitempty"`
	Args          []string             `json:"args,omitempty"`
	Env           []EnvVar             `json:"env,omitempty"`
	Resources     ResourceRequirements `json:"resources"`
	VolumeMounts  []VolumeMount        `json:"volumeMounts,omitempty"`
	RestartPolicy string               `json:"restartPolicy,omitempty"`
}

// resources.claims is how a container holds one of the pod's
// claims, and request narrows it to one role inside that claim.
type ResourceRequirements struct {
	Claims []ContainerClaim `json:"claims,omitempty"`
}

type ContainerClaim struct {
	Name    string `json:"name"`
	Request string `json:"request,omitempty"`
}

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type VolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// An inline NFS volume needs no PersistentVolume and no CSI driver.
// The kubelet mounts it with the kernel's NFS client, through the
// mount helper liken's image carries.
type Volume struct {
	Name     string                `json:"name"`
	NFS      *NFSVolumeSource      `json:"nfs,omitempty"`
	EmptyDir *EmptyDirVolumeSource `json:"emptyDir,omitempty"`
}

type NFSVolumeSource struct {
	Server   string `json:"server"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

// An emptyDir is a directory the kubelet creates with the pod and
// deletes with it, which is all two containers need to share one
// socket.
//
// SizeLimit caps the volume. The IPC volume leaves it empty, so it marshals as
// {}. The art volume sets it, so a runaway decode cannot fill the node disk.
type EmptyDirVolumeSource struct {
	SizeLimit string `json:"sizeLimit,omitempty"`
}

// The pod status fields the phase derivation reads. The container's
// terminated state is the specific half of a failure message,
// because it carries the exit code.
type PodStatus struct {
	Phase             string            `json:"phase,omitempty"`
	Reason            string            `json:"reason,omitempty"`
	Message           string            `json:"message,omitempty"`
	ContainerStatuses []ContainerStatus `json:"containerStatuses,omitempty"`
}

type ContainerStatus struct {
	Name  string         `json:"name"`
	State ContainerState `json:"state"`
}

type ContainerState struct {
	Terminated *ContainerStateTerminated `json:"terminated,omitempty"`
}

type ContainerStateTerminated struct {
	ExitCode int    `json:"exitCode"`
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
}

// The pod phases Kubernetes reports, named here so the derivation
// reads as the mapping it is.
const (
	podPending   = "Pending"
	podRunning   = "Running"
	podSucceeded = "Succeeded"
	podFailed    = "Failed"
)
