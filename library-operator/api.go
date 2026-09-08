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

// The group this operator serves, and the core group it writes
// into: a Library becomes an ordinary CronJob and its Jobs ordinary
// pods, so any tool that reads them reads what a Library became.
const (
	libraryAPIVersion = "library.liken.sh/v1alpha1"
	podAPIVersion     = "v1"
)

// The group media-operator serves. This operator reads Players from it
// and writes none, so the group appears here for the read path alone.
const playerAPIVersion = "media.liken.sh/v1alpha1"

// The group people-operator serves. A Person is a subject of the whole
// cluster, and this operator holds a finalizer on each one and writes
// nothing else.
const personAPIVersion = "people.liken.sh/v1alpha1"

// The kinds this operator names in an owner reference, and reads back
// off a Play. A Play's owners say who watched it and which Watch it
// belongs to, which is what an audience is.
const (
	watchKind  = "Watch"
	personKind = "Person"
)

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
// departure in depart.go sweeps the catalog. generateName is the prefix
// the API server mints a name from; a Play carries it in place of a
// name, because every start of a title is its own Play and the operator
// keeps no record of what it created.
type ObjectMeta struct {
	Name              string            `json:"name,omitempty"`
	GenerateName      string            `json:"generateName,omitempty"`
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
// one thing manages each owned object; there is no blockOwnerDeletion,
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
	// Franchises is the settings block of the franchises kind: the image
	// every block carries, and the art claim that this kind alone names.
	Franchises *LibraryFranchises `json:"franchises,omitempty"`

	// The metadata providers to ask about a title, in the order they
	// are asked. Enrichment reads the list; nothing acts on it yet.
	Sources []string `json:"sources,omitempty"`

	// The path components the walk skips, wherever they sit under the
	// library root. An owner names the junk their storage keeps, such as
	// a recycle bin or a staging folder, because no fixed list can
	// anticipate every volume's layout.
	Ignore []string `json:"ignore,omitempty"`

	// One time per fact, by the names status.gaps uses: an attempt
	// this fact made before that time does not count, so every title
	// is in that fact's gap again and the fact asks a provider again
	// and rewrites its own files and rows in place.
	// Nothing is deleted, and a fact this map does not name is
	// untouched.
	Refresh map[string]time.Time `json:"refresh,omitempty"`

	// How often the full walk runs.
	Scan LibraryScan `json:"scan,omitzero"`

	// Whether this library builds the thumbnail sheets a scrub bar reads, which
	// costs hours of CPU over a whole library on the first run.
	Trickplay LibraryTrickplay `json:"trickplay,omitzero"`
}

// The trickplay block of the spec, off unless the owner turns it on.
type LibraryTrickplay struct {
	Enabled bool `json:"enabled,omitempty"`
}

// The schedule the Library's full walk runs on, as the cron
// expression a CronJob takes.
type LibraryScan struct {
	Schedule string `json:"schedule,omitempty"`
}

// Once an hour, which is the interval a library with a webhook
// needs as a backstop and a library with none can live on.
const defaultScanSchedule = "0 * * * *"

// The schedule the CronJob takes: the Library's own, or the
// default when it names none. The CRD defaults the field, so a Library
// from the API server always carries one, and this answers for the ones
// built in this program.
func (s LibrarySpec) scanSchedule() string {
	if s.Scan.Schedule != "" {
		return s.Scan.Schedule
	}
	return defaultScanSchedule
}

// The kinds of media a Library holds. Each one names a settings block
// on the spec and a scanner to run, and a new kind is a new block
// beside the ones here.
const (
	libraryKindMovies = "movies"
	libraryKindSeries = "series"
	// A franchises library walks the franchise.yaml files on its storage
	// claim, and writes the art it downloads into the second claim that
	// its settings block names.
	libraryKindFranchises = "franchises"
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
	case libraryKindFranchises:
		if s.Franchises == nil {
			return nil
		}
		return &s.Franchises.LibrarySettings
	}
	return nil
}

// artClaim is the claim a franchises scan writes its art into, and empty
// for every other kind. The checkout is read-only, so the art has to land
// on a claim of its own.
func (s LibrarySpec) artClaim() string {
	if s.Kind != libraryKindFranchises || s.Franchises == nil {
		return ""
	}
	return s.Franchises.Art.Claim
}

// screenClaim is the claim that holds the files a screen reads: the art
// claim of a franchises library, and the storage claim of every other
// kind. The screen pod, the play request, and the enrich Job all ask it,
// so no one of them decides the question on its own.
func (s LibrarySpec) screenClaim() string {
	if claim := s.artClaim(); claim != "" {
		return claim
	}
	return s.Storage.Claim
}

// screenRoot is the directory inside the screen's claim that holds this
// library's files. The storage root applies to the storage claim alone,
// and the art of a franchises library lands at the root of its own claim,
// so a screen and a play reference read the art from there.
func (s LibrarySpec) screenRoot() string {
	if s.artClaim() != "" {
		return "/"
	}
	return s.Storage.Root
}

// LibraryStorage is the volume and the directory inside it. Every kind
// mounts the claim read-only, a franchises library included, because the
// claim holds the truth the scan walks and the scan writes nothing to it.
// The operator reads the PersistentVolume behind the claim for the
// volume's kind and address.
type LibraryStorage struct {
	Claim string `json:"claim,omitempty"`

	// The directory inside the claim this library starts at, always an
	// absolute path from the root of the volume. The CRD defaults it
	// to / and refuses a relative path, so the scanner takes this
	// field as it stands.
	Root string `json:"root,omitempty"`
}

// LibrarySettings is the one setting every kind's block carries. The
// movies and series blocks are this struct alone, and the franchises block
// embeds it beside the art claim.
type LibrarySettings struct {
	// The scanner image to run in place of the one the project ships
	// for the kind, which is how a person supplies a scanner of their
	// own. Empty means the operator's own image.
	Image string `json:"image,omitempty"`
}

// LibraryFranchises is the settings block of the franchises kind: the
// image every block carries, and the art claim that this kind alone names.
// The CRD requires the art claim, because a franchises scan always
// downloads art and the checkout cannot take it.
type LibraryFranchises struct {
	LibrarySettings
	Art LibraryArt `json:"art,omitzero"`
}

// LibraryArt names the writable claim, in the Library's namespace, that a
// franchises scan downloads the art into. It is the claim a screen mounts
// for this library, because the art is what a screen reads of a franchise.
type LibraryArt struct {
	Claim string `json:"claim,omitempty"`
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
	// One entry per worker, from the reporter: the Job that ran
	// last for that worker and when it finished.
	Runs []libraryRun `json:"runs,omitempty"`
	// Gaps is one count per fact of the rows that fact has left to fill.
	// Waiting is the titles whose identity ended in candidates for a person to
	// choose from, and Unresolved the titles no provider could name. All three
	// are the reporter's own numbers.
	Gaps       map[string]int `json:"gaps,omitempty"`
	Waiting    int            `json:"waiting"`
	Unresolved int            `json:"unresolved"`
	// The titles a fact left because another writer holds the element group it
	// writes. The repair is to stop that writer for this library.
	Fights int `json:"fights"`
	// Webhook is the URL of this Library's webhook endpoint on the
	// operator, the address a person gives to Radarr, Sonarr, or
	// Jellyfin.
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

// The one name a MediaPreferences may take. media-operator's CRD pins it,
// and this operator reads the singleton by this name.
const mediaPreferencesName = "default"

// The household defaults media-operator owns, read here for one field:
// the wall-clock zone every screen shows. Nothing is written back, so
// the type carries that field alone.
type MediaPreferences struct {
	APIVersion string               `json:"apiVersion,omitempty"`
	Kind       string               `json:"kind,omitempty"`
	Metadata   ObjectMeta           `json:"metadata"`
	Spec       MediaPreferencesSpec `json:"spec"`
}

// The one field of the household defaults this operator reads.
type MediaPreferencesSpec struct {
	// The household wall-clock zone, an IANA name like America/New_York.
	// The browser pod reads it as TZ, so its clock and its day's draw
	// follow the house and not UTC.
	TimeZone string `json:"timeZone,omitempty"`
}

// The collection ListMediaPreferences answers. Its resourceVersion is
// where the watch begins.
type MediaPreferencesList struct {
	Metadata ListMeta           `json:"metadata"`
	Items    []MediaPreferences `json:"items"`
}

// The household zone the list holds: the default MediaPreferences' own,
// or nothing where the cluster states none. A pod with no TZ reads UTC,
// the way media-operator's own pods do.
func householdZone(list *MediaPreferencesList) string {
	for _, preferences := range list.Items {
		if preferences.Metadata.Name == mediaPreferencesName {
			return preferences.Spec.TimeZone
		}
	}
	return ""
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
//
// FadeAfterSeconds and OffAfterSeconds are the seconds before the
// screen fades and the seconds before the panel goes dark.
// media-operator resolves both and always writes them, because zero is
// a policy and an absent field is not one. The browser holds the
// timers, through the media-screen crate, so media-operator settles the
// policy and the client runs it.
type PlayerIdleStatus struct {
	Controller string   `json:"controller"`
	Claim      string   `json:"claim,omitempty"`
	Requests   []string `json:"requests,omitempty"`

	FadeAfterSeconds int64 `json:"fadeAfterSeconds"`
	OffAfterSeconds  int64 `json:"offAfterSeconds"`

	Bus *PlayerIdleBus `json:"bus,omitempty"`
}

// PlayerIdleBus is the broker and every topic a delegate's client reads
// or writes, as media-operator publishes them on the Player's status.
// This operator copies each one onto the browser container as the
// MEDIA_ variable of the same name, and the media-screen crate the
// browser links reads them there. An older media-operator publishes no
// block, and the browser then takes the keyboard alone.
type PlayerIdleBus struct {
	// The broker's address, which the browser connects to for every
	// topic below and for the play request it publishes on this
	// operator's own tree.
	Address string `json:"address"`
	// The Player's retained status. The browser reads the activity off
	// it: while the unit plays, the film covers the browser, and while
	// it plays nothing the browser draws its screens.
	StatusTopic string `json:"statusTopic"`
	// The Player's retained level. The browser reads the level and the
	// muted flag off it and draws them as a fading row on a live
	// message, and a volume key steps from the last level it delivered
	// and publishes the next one back on this topic. Empty for a unit
	// with no sinks, which is the speaker gate: no level is drawn and
	// no volume key publishes.
	VolumeTopic string `json:"volumeTopic,omitempty"`
	// VolumeOwnerTopic is VolumeTopic with "/owner" after it. A retained
	// non-empty payload on it names equipment that owns the room's
	// level, and while that mark stands the browser draws no level,
	// because the equipment carries its own indicator. An empty payload
	// clears the mark, and the row then shows what the last level said.
	// An older media-operator publishes no such field, and the browser
	// then draws the level with no gate over it.
	VolumeOwnerTopic string `json:"volumeOwnerTopic,omitempty"`
	// The Player's commands topic. The browser acts on one command, the
	// operator's re-present, which puts the browser back on the page it
	// left once a Play ends, and only while the unit plays nothing.
	CommandsTopic string `json:"commandsTopic"`
	// The retained topic the browser states its panel desire on: off
	// once its off window passes with no press, and on again at the
	// next press.
	PanelTopic string `json:"panelTopic"`
	// The unit's controllers, in spec.remotes order. The browser
	// subscribes to each one's events and focus topics.
	Remotes []PlayerIdleRemote `json:"remotes,omitempty"`
}

// PlayerIdleRemote is one of the unit's controllers as a client reads
// it: the topic its presses arrive on and the topic its focus mark is
// on. The list is in spec.remotes order, because that position is the
// index a focus moment carries.
type PlayerIdleRemote struct {
	Events string `json:"events"`
	Focus  string `json:"focus"`
}

// Play is media-operator's unit of playback: the Players it plays on
// and the items it plays in order. This operator creates a Play, stamps
// its audience onto the metadata, and reads its status back for the
// last position of a Play that ended.
type Play struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PlaySpec   `json:"spec"`
	Status     PlayStatus `json:"status,omitempty"`
}

// The collection ListPlays answers, across every namespace.
type PlayList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Play   `json:"items"`
}

// PlayStatus is what media-operator reports about a Play, in the fields
// this operator reads: which item was playing and where the playhead
// reached. The bus carries the same numbers while the Play runs, and
// these are where they stand once it ends.
type PlayStatus struct {
	Phase string `json:"phase,omitempty"`
	Item  int    `json:"item,omitempty"`
	// The playhead and the item's length, as H:MM:SS.
	Position string `json:"position,omitempty"`
	Duration string `json:"duration,omitempty"`
}

// The two phases that end a Play, which are media-operator's own
// words. The operator publishes a Play's last status once it reads
// one of them, and never again after that.
const (
	playPhaseFinished = "Finished"
	playPhaseFailed   = "Failed"
)

// ended reports that this Play will report no further position: it
// finished, it failed, or somebody deleted it mid-run.
func (p *Play) ended() bool {
	return p.Status.Phase == playPhaseFinished || p.Status.Phase == playPhaseFailed ||
		p.Metadata.deleting()
}

// A Watch is a set of people on one item, and the record of where that
// set reached. The operator writes the owner references and the status;
// a person or the browser writes the spec.
type Watch struct {
	APIVersion string      `json:"apiVersion,omitempty"`
	Kind       string      `json:"kind,omitempty"`
	Metadata   ObjectMeta  `json:"metadata"`
	Spec       WatchSpec   `json:"spec"`
	Status     WatchStatus `json:"status"`
}

type WatchList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Watch  `json:"items"`
}

// WatchSpec is the set of people and the item they watch. Progress
// belongs to the set, so two Watches on one item with different people
// are two records.
type WatchSpec struct {
	People []string  `json:"people,omitempty"`
	Item   WatchItem `json:"item"`
}

// WatchItem names the item as the catalog names it: the Library in the
// Watch's own namespace, and the slug of a movie or a series inside it.
type WatchItem struct {
	Library string `json:"library"`
	Slug    string `json:"slug"`
}

// WatchStatus is the projection of the progress store the operator
// writes. Every field describes the last Play recorded against this
// Watch, so the whole block is one observation and the operator writes
// it whole.
type WatchStatus struct {
	Play         string `json:"play,omitempty"`
	Item         int    `json:"item,omitempty"`
	Position     string `json:"position,omitempty"`
	Duration     string `json:"duration,omitempty"`
	Season       int    `json:"season,omitempty"`
	Episode      int    `json:"episode,omitempty"`
	Ended        bool   `json:"ended,omitempty"`
	LastRecorded string `json:"lastRecorded,omitempty"`
}

// A Person is a subject of the cluster, cluster-scoped and owned by
// people-operator. A Play names the people who watched it and a Watch
// the people who share it, both through owner references, so this
// operator reads a Person for its name and its uid alone.
type Person struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PersonSpec `json:"spec"`
}

type PersonList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Person `json:"items"`
}

// The one field of a Person this operator reads, for the name a screen
// shows in place of the object's own.
type PersonSpec struct {
	DisplayName string `json:"displayName,omitempty"`
}

// PlaySpec names the Players and the items. A request from one screen
// names one Player.
type PlaySpec struct {
	Players []string   `json:"players,omitempty"`
	Items   []PlayItem `json:"items,omitempty"`
	// Where the first item begins, as a time the player accepts, such as
	// 0:10:00 or 600. Empty, the run begins at the start of the item. The
	// operator does not parse or check the value; the player does.
	Start string `json:"start,omitempty"`
}

// PlayItem is one item: the media reference the Player accepts, and
// the words the film's own display shows. The reference is a claim
// URI, which mounts the library's claim read-only on the playback pod.
type PlayItem struct {
	URI          string            `json:"uri"`
	Presentation *PlayPresentation `json:"presentation,omitempty"`
}

// PlayPresentation is what the display shows about one item, in
// media-operator's own field names. Every field is optional, because
// the catalog holds what the volume holds and no more.
type PlayPresentation struct {
	Type         string `json:"type,omitempty"`
	Hint         string `json:"hint,omitempty"`
	Title        string `json:"title,omitempty"`
	Series       string `json:"series,omitempty"`
	Season       int    `json:"season,omitempty"`
	Episode      int    `json:"episode,omitempty"`
	EpisodeTitle string `json:"episodeTitle,omitempty"`
	Year         int    `json:"year,omitempty"`
	Date         string `json:"date,omitempty"`
	Art          string `json:"art,omitempty"`
	Trickplay    string `json:"trickplay,omitempty"`
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
	reasonNoReport     = "NoReport"
	// The namespace's catalog pod is not up, the schedule does
	// not stand yet, and the namespace's reporter has left the bus.
	reasonCatalogPending = "CatalogPending"
	reasonScanPending    = "ScanPending"
	reasonOffline        = "Offline"

	// The Departing condition's reasons, in the order depart.go
	// reaches them: a scan Job of this library is still running, the
	// cleanup Job is deleting the rows, the Job finished and the
	// reporter has not echoed it yet, and the cleanup cannot run at
	// all. Blocked covers a cleanup Job that failed and a namespace
	// with more than one Catalog.
	reasonScanRunning  = "ScanRunning"
	reasonSweeping     = "Sweeping"
	reasonAwaitingEcho = "AwaitingEcho"
	reasonBlocked      = "Blocked"
	// An enricher Job of this library is still running. It writes onto the
	// volume and into the catalog the sweep is emptying.
	reasonEnrichRunning = "EnrichRunning"
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
	phaseEnriching = "Enriching"
	phaseIdle      = "Idle"
	phaseDeparting = "Departing"
	// Failed means the last scan of this library failed and wrote no
	// rows. The tables hold what the last good scan left.
	phaseFailed = "Failed"
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
