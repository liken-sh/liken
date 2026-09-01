package main

// The wire types are hand-written, the way liken and the sibling
// operators write theirs. The Kubernetes API is HTTPS that serves
// JSON, and importing client-go for a dozen structs brings informers,
// work queues, and a release cadence this program does not use. Each
// type carries only the fields this operator reads or writes; the API
// server fills in the rest.

import (
	"slices"
	"time"
)

// The group this operator serves, and the core group it writes into:
// a Library becomes an ordinary pod, so any tool that reads pods reads
// what a Library became.
const (
	libraryAPIVersion = "library.liken.sh/v1alpha1"
	podAPIVersion     = "v1"
)

// The group media-operator serves. This operator reads Players from it
// and writes none, so the group appears here for the read path alone.
const playerAPIVersion = "media.liken.sh/v1alpha1"

// The finalizer this operator holds on every Library. It keeps a
// deleted Library open until the departure in depart.go has swept
// the library's rows out of every surviving agent's catalog. The
// name is in this operator's own group, so the finalizer says which
// controller answers for it.
const libraryFinalizer = "library.liken.sh/cleanup-library"

// Release 2026.08.31-004 named the finalizer sweep. The operator
// still reads that name so a Library which adopted it under that
// release swaps to the current name on a pass, and deletes instead
// of sticking on a finalizer nothing releases.
const formerLibraryFinalizer = "library.liken.sh/sweep"

// ObjectMeta carries what this operator reads or writes: name and
// namespace for the URL, resourceVersion for the conditional write,
// uid with ownerReferences so the garbage collector takes a scanner
// pod with its Library, and labels so one watch selects every scanner
// pod in the cluster.
//
// Annotations carry the template hash the operator stamps on a scanner
// pod, which is how a pass tells a live pod from the pod it would
// build now. deletionTimestamp is set by the API server on an object
// on its way out, and a pod with one set is left alone until the
// delete completes. finalizers is here because the operator holds a
// Library open past the delete request, and that window is where the
// departure in depart.go sweeps the catalog.
type ObjectMeta struct {
	Name              string            `json:"name,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	Generation        int64             `json:"generation,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	Finalizers        []string          `json:"finalizers,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
	OwnerReferences   []OwnerReference  `json:"ownerReferences,omitempty"`
}

// Deleting reports that somebody asked for this object's deletion.
// The API server does not remove an object that carries a finalizer;
// it writes the deletion timestamp instead, and that window is where
// the departure runs.
func (m ObjectMeta) deleting() bool { return m.DeletionTimestamp != "" }

// Holds reports whether this object carries the named finalizer.
func (m ObjectMeta) holds(finalizer string) bool {
	return slices.Contains(m.Finalizers, finalizer)
}

// With answers the finalizer list with one added, and without
// answers it with one removed. Both answer a new slice, because the
// caller's copy is the object as the server has it, and a patch that
// fails must leave that copy alone.
func (m ObjectMeta) with(finalizer string) []string {
	if m.holds(finalizer) {
		return m.Finalizers
	}
	return append(slices.Clone(m.Finalizers), finalizer)
}

func (m ObjectMeta) without(finalizers ...string) []string {
	kept := []string{}
	for _, held := range m.Finalizers {
		if !slices.Contains(finalizers, held) {
			kept = append(kept, held)
		}
	}
	return kept
}

// An ownerReference ties an object's life to its owner's: the garbage
// collector deletes the owned object when the owner goes, which is
// this operator's whole teardown. Controller is true because exactly
// one thing manages each scanner pod; there is no blockOwnerDeletion,
// because nothing here needs the owner to wait.
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

// A Library is a volume of media of one kind. The operator reads the
// spec and writes the status: the spec is what a person declared, and
// the status is what the volume resolved to and what the scanner
// reports.
type Library struct {
	APIVersion string        `json:"apiVersion,omitempty"`
	Kind       string        `json:"kind,omitempty"`
	Metadata   ObjectMeta    `json:"metadata"`
	Spec       LibrarySpec   `json:"spec"`
	Status     LibraryStatus `json:"status"`
}

type LibraryList struct {
	Metadata ListMeta  `json:"metadata"`
	Items    []Library `json:"items"`
}

// A Library names its storage, its kind, and the settings for that
// kind. The settings blocks are pointers because their presence is the
// declaration: the block that matches the kind is there and no other,
// and the CRD's own rule refuses any other combination.
type LibrarySpec struct {
	Storage LibraryStorage   `json:"storage"`
	Kind    string           `json:"kind"`
	Movies  *LibrarySettings `json:"movies,omitempty"`
	Series  *LibrarySettings `json:"series,omitempty"`

	// The metadata providers to ask about a title, in the order they
	// are asked. Enrichment reads the list; nothing acts on it yet.
	Sources []string `json:"sources,omitempty"`

	// The path components the walk skips, wherever they sit under the
	// library root. An owner names the junk their storage keeps, such as
	// a recycle bin or a staging folder, because no fixed list can
	// anticipate every volume's layout.
	Ignore []string `json:"ignore,omitempty"`
}

// The kinds of media a Library holds. Each one names a settings block
// on the spec and a scanner to run, and a new kind is a new block
// beside the ones here.
const (
	libraryKindMovies = "movies"
	libraryKindSeries = "series"
)

// Settings is the block that matches the kind, which is the block the
// scanner receives. Keeping the choice here keeps the Go side and the
// CRD's rule saying the same thing, so a new kind is one more case
// here and one more clause there.
func (s LibrarySpec) settings() *LibrarySettings {
	switch s.Kind {
	case libraryKindMovies:
		return s.Movies
	case libraryKindSeries:
		return s.Series
	}
	return nil
}

// LibraryStorage is the volume and the directory inside it. The
// scanner mounts the claim read-only, and the operator reads the
// PersistentVolume behind it for the volume's kind and address.
type LibraryStorage struct {
	Claim string `json:"claim"`

	// The directory inside the claim this library starts at, always an
	// absolute path from the root of the volume. The CRD defaults it
	// to / and refuses a relative path, so the scanner takes this
	// field as it stands.
	Root string `json:"root,omitempty"`
}

// LibrarySettings is one kind's settings block. One struct serves both
// kinds, because in this plan each block holds the same one field. The
// naming conventions each kind needs are the scanner plan's, and they
// split this into a struct per kind when they arrive.
type LibrarySettings struct {
	// The scanner image to run in place of the one the project ships
	// for the kind, which is how a person supplies a scanner of their
	// own. Empty means the operator's own image.
	Image string `json:"image,omitempty"`
}

// LibraryStatus is what the operator reports on a Library: the volume
// the claim resolved to, the scanner's report, the pod that runs the
// scanner, and the conditions.
//
// The counts carry no omitempty. A library of zero titles is a
// real answer, and a column that reads 0 says it, where an omitted
// field reads as nothing at all. The conditions say whether a report
// arrived.
//
// RemovedLastSweep is the count of catalog rows the scanner's last full
// sweep removed, folded from the bus report. A partial walk that pruned
// too much shows here, so a mass delete is visible without a shell.
type LibraryStatus struct {
	Volume *LibraryVolume `json:"volume,omitempty"`
	// Phase says what the scanner is doing, in one word a person reads at
	// a glance: one of the four values below. Where Ready is the condition a
	// program matches on, Phase is the sentence a person reads.
	Phase        string `json:"phase,omitempty"`
	Titles       int    `json:"titles"`
	Unidentified int    `json:"unidentified"`
	// Items is how many item rows the catalog holds for this library,
	// across the movies, series, and episodes tables, and Files how many
	// file rows. Both are the catalog's own counts, read after the prune,
	// so they describe what a screen can read and not what one walk saw.
	Items            int       `json:"items"`
	Files            int       `json:"files"`
	RemovedLastSweep int       `json:"removedLastSweep"`
	LastWalk         time.Time `json:"lastWalk,omitzero"`
	LastChange       time.Time `json:"lastChange,omitzero"`
	Pod              string    `json:"pod,omitempty"`
	// Webhook is the URL of the scanner's webhook endpoint, the address a
	// person gives to Radarr, Sonarr, or Jellyfin.
	Webhook    string      `json:"webhook,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// LibraryVolume is the PersistentVolume the claim is bound to,
// reported here so that whoever plays a title from this library reads
// the volume without a second request for the claim. Type is the name of the volume's
// source key, and the NFS pair is filled only for an NFS volume.
type LibraryVolume struct {
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`
	Server string `json:"server,omitempty"`
	Path   string `json:"path,omitempty"`
}

// A Player is media-operator's unit of equipment. This operator reads
// one for its status.idle alone: the block media-operator publishes for the controller it
// delegated the idle screen to. Nothing here is written back, so the type
// carries no spec.
type Player struct {
	APIVersion string       `json:"apiVersion,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	Metadata   ObjectMeta   `json:"metadata"`
	Status     PlayerStatus `json:"status"`
}

// The idle block the Player carries, and an empty one for a Player that
// carries none. The empty block names no controller and no claim, so a Player
// with no idle status is a Player this operator stands nothing for.
func (p *Player) idle() PlayerIdleStatus {
	if p.Status.Idle == nil {
		return PlayerIdleStatus{}
	}
	return *p.Status.Idle
}

// Whether this operator stands the Player's idle screen. The pass acts
// on status.idle and never on spec.idle: the spec can inherit its controller
// from MediaPreferences, and media-operator alone resolves those tiers.
func (p *Player) delegated() bool {
	return p.idle().Controller == screenController
}

// The collection ListPlayers answers. Its resourceVersion is where the
// player watch begins.
type PlayerList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Player `json:"items"`
}

// The half of a Player's status this operator acts on. The idle block
// is absent on a Player that stands no idle screen.
type PlayerStatus struct {
	Idle *PlayerIdleStatus `json:"idle,omitempty"`
}

// What media-operator publishes for the idle controller it delegated
// to. Controller names that controller, and this operator acts only on its own
// name. Claim is the ResourceClaim media-operator stood for the screen, in the
// Player's namespace, and Requests names the requests in it the browser
// container states. The requests are media-operator's own list: render is
// there only for a Player whose display claim holds one.
type PlayerIdleStatus struct {
	Controller string   `json:"controller,omitempty"`
	Claim      string   `json:"claim,omitempty"`
	Requests   []string `json:"requests,omitempty"`
}

// The condition types this operator publishes. Bound reports the
// storage and Ready reports the scanner. A library whose claim never
// binds shows the cause in Bound, and Ready reads NotBound beside it.
// Departing reports the teardown of a deleted Library: it is the one
// condition here whose True states work in progress rather than a
// healthy fact, and its reason says how far the teardown reached.
const (
	conditionBound     = "Bound"
	conditionReady     = "Ready"
	conditionDeparting = "Departing"
)

// The reasons each condition takes. A reason is one CamelCase word a
// program matches on, and the message beside it is the sentence.
const (
	reasonBound          = "Bound"
	reasonClaimNotFound  = "ClaimNotFound"
	reasonClaimUnbound   = "ClaimUnbound"
	reasonVolumeNotFound = "VolumeNotFound"

	reasonReady        = "Ready"
	reasonNotBound     = "NotBound"
	reasonNoCatalog    = "NoCatalog"
	reasonManyCatalogs = "ManyCatalogs"
	reasonPodPending   = "PodPending"
	reasonPodFailed    = "PodFailed"
	reasonNoReport     = "NoReport"

	// The Departing condition's reasons, in the order depart.go
	// reaches them: the scanner pod is stopping, the cleanup pod is
	// deleting the rows, the sweep ran here and a survivor's catalog
	// still holds the rows, and the cleanup cannot run at all.
	// Blocked covers a cleanup pod that failed or cannot be
	// scheduled, and a catalog volume that is gone in a namespace
	// with no single Catalog to size a new one from.
	reasonStoppingScanner  = "StoppingScanner"
	reasonSweeping         = "Sweeping"
	reasonAwaitingSurvivor = "AwaitingSurvivor"
	reasonBlocked          = "Blocked"
)

// The values status.phase takes. libraryPhase in status.go derives
// the first four from the Ready condition, the scanner's
// availability on the bus, and the newest report. Departing is not
// derived: depart.go writes it on a Library with a deletion
// timestamp, for as long as the finalizer holds the object open.
const (
	phasePending   = "Pending"
	phaseOffline   = "Offline"
	phaseScanning  = "Scanning"
	phaseIdle      = "Idle"
	phaseDeparting = "Departing"
)

// ConditionStatus is a condition's verdict. It is a string rather than
// a bool because there is a third state: an operator must be able to
// say when it cannot tell yet.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition mirrors metav1.Condition, the shape Kubernetes uses
// everywhere, and liken's own. Anyone who reads kubectl describe
// output on a Pod already knows how to read one of these.
//
// ObservedGeneration records which metadata.generation the condition
// judged. Generation counts spec edits, so a reader can tell "Ready,
// for the spec as it stands" from "Ready, but for a spec two edits
// ago".
type Condition struct {
	Type               string          `json:"type"`
	Status             ConditionStatus `json:"status"`
	ObservedGeneration int64           `json:"observedGeneration,omitempty"`
	Reason             string          `json:"reason,omitempty"`
	Message            string          `json:"message,omitempty"`
	LastTransitionTime time.Time       `json:"lastTransitionTime"`
}

// SetCondition adds or updates a condition by type. It keeps the
// Kubernetes rule that makes lastTransitionTime meaningful: the time
// moves only when Status flips, not on every write. That is what lets
// kubectl get answer "how long has this library been Ready?" instead
// of only "when did the operator last say so?".
func SetCondition(conditions []Condition, condition Condition, now time.Time) []Condition {
	condition.LastTransitionTime = now
	for i, existing := range conditions {
		if existing.Type != condition.Type {
			continue
		}
		if existing.Status == condition.Status {
			condition.LastTransitionTime = existing.LastTransitionTime
		}
		conditions[i] = condition
		return conditions
	}
	return append(conditions, condition)
}
