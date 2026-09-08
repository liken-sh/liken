package main

// The progress store is written by one process per namespace, the
// progress role beside the progress agent, and that process holds no
// Kubernetes credential. The operator is the only API client. So every
// fact that crosses between them crosses the bus, on the topics this
// file names, and the two never share a type beyond the payloads here.
//
// The flow of one Play. The operator publishes the audience: who
// watched, which Watch it belongs to, and the work's aliases. The
// playback pod's sidecar publishes the position on media-operator's
// own status topic. The progress role joins the two into rows and
// publishes what it recorded. When the Play ends, the operator
// publishes its final status off the API, the progress role records
// it and marks the row ended, and the operator reads that mark to
// release the Play's finalizer. Every one of these messages is
// retained, so a restart on either side reads the current state back
// from the broker.
//
// The outside play is the one message in this file that is not retained. The
// jellyfin role publishes it for a play that ran outside the cluster, and the
// next post repeats the position.

import "strings"

// The base of media-operator's topic tree, which the progress role
// reads for each Play's position. liken/media is media-operator's
// default, and the variable moves it with that operator's own setting.
const (
	mediaTopicBaseVariable = "LIBRARY_MEDIA_TOPIC_BASE"
	defaultMediaTopicBase  = "liken/media"
)

// The kinds media-operator's playback sidecar publishes under a Play's
// topic. They are that operator's names, from its plan 03, and this
// operator reads them and writes none of them.
const (
	mediaStatusKind       = "status"
	mediaAvailabilityKind = "availability"
)

// The kinds under a Play's topic and a person's topic in this
// operator's tree.
const (
	playAudienceKind    = "audience"
	playOutsideKind     = "outside"
	playFinalKind       = "final"
	playRecordedKind    = "recorded"
	watchProgressKind   = "progress"
	personForgetKind    = "forget"
	personForgottenKind = "forgotten"
)

// playAudience is what the operator knows about a Play that the
// progress role cannot read for itself: the Player it ran on, the
// people who watched, the Watch it belongs to, and the work's aliases.
// The operator publishes it retained under the Play's audience topic
// as soon as the Play exists, and republishes it on every pass the
// Play is seen, so a progress role that starts late reads it back.
type playAudience struct {
	// The Player the Play ran on, so a Play with no people still has
	// a row a screen can show.
	Player string `json:"player"`
	// The Library the items came from, as its name, or empty for a
	// Play this operator did not create.
	Library string `json:"library,omitempty"`
	// The Watch that owns the Play, by name, or empty.
	Watch string `json:"watch,omitempty"`
	// The people who watched, as Person names, from the Play's owner
	// references. Empty for a Play nobody claimed.
	People []string `json:"people,omitempty"`
	// The work's ids by provider, from the Play's alias annotations:
	// {"tmdb": "2316", "imdb": "tt0386676"}.
	Aliases map[string]string `json:"aliases,omitempty"`
	// The season and episode numbers for an episode, and 0 for a work
	// that has none.
	Season  int `json:"season,omitempty"`
	Episode int `json:"episode,omitempty"`
}

// One play that ran outside this cluster, published by the jellyfin role and
// recorded by the progress role.
// The message is not retained, and the newer at wins over what the row holds.
type outsidePlay struct {
	// The name of the outside player, jellyfin, in the column that names
	// a Player.
	Player string `json:"player"`
	// The people who watched, as Person names.
	People []string `json:"people"`
	// The work's ids by provider, and for an episode the series' ids.
	Aliases map[string]string `json:"aliases"`
	// The season and episode numbers for an episode, and 0 for a work
	// that has neither.
	Season  int `json:"season"`
	Episode int `json:"episode"`
	// The position and the duration in seconds, which is the shape the
	// store holds.
	Position int `json:"position"`
	Duration int `json:"duration"`
	// True on a stop, which marks the row ended.
	Ended bool `json:"ended"`
	// The Unix time of the event, which the store writes as the recorded
	// time.
	At int64 `json:"at"`
}

// playFinal is a Play's last status, read off the API by the operator
// once the Play's phase is Finished or Failed. It closes the gap the
// bus leaves: a progress role that was down for the last report of a
// film still records where the film ended. The positions are H:MM:SS
// as the Play status carries them.
type playFinal struct {
	Phase    string `json:"phase"`
	Item     int    `json:"item"`
	Position string `json:"position"`
	Duration string `json:"duration"`
}

// playRecorded is what the progress role wrote last for one Play. The
// operator releases the Play's finalizer only when Ended is true, so
// a Play is never deleted before its last position is in the store.
type playRecorded struct {
	Item     int    `json:"item"`
	Position string `json:"position"`
	Ended    bool   `json:"ended"`
	// The time of the write, RFC 3339 in UTC.
	At string `json:"at"`
}

// watchProgress is the projection of a Watch out of the store: the
// latest Play recorded against it and where that Play reached. The
// operator writes it into the Watch's status. What comes next is a
// question for the catalog, so it is not here.
type watchProgress struct {
	Play     string `json:"play"`
	Item     int    `json:"item"`
	Position string `json:"position"`
	Duration string `json:"duration"`
	Season   int    `json:"season,omitempty"`
	Episode  int    `json:"episode,omitempty"`
	Ended    bool   `json:"ended"`
	// The time of the last write, RFC 3339 in UTC.
	LastRecorded string `json:"lastRecorded"`
}

// The alias annotations on a Play, one per provider, and the two
// number annotations an episode carries beside them. The operator
// writes them when it creates a Play, and reads them back off any
// Play it sees, so a Play created by hand with the same annotations is
// recorded the same way.
const (
	aliasAnnotationPrefix = "library.liken.sh/alias."
	seasonAnnotation      = "library.liken.sh/season"
	episodeAnnotation     = "library.liken.sh/episode"
	// The Library the items came from, by name. The audience carries
	// it, so a row in the store names the library that holds the item
	// without a read of the catalog.
	libraryAnnotation = "library.liken.sh/library"
)

// The finalizer the operator holds on a Play until its last position
// is recorded, and on a Person until their rows are gone from every
// namespace's store.
const progressFinalizer = "library.liken.sh/progress"

// Carries the audience of one Play. Retained; the operator publishes
// and clears it.
func playAudienceTopic(base, namespace, name string) string {
	return base + "/plays/" + namespace + "/" + name + "/" + playAudienceKind
}

// Carries one play that ran outside this cluster. Not retained; the jellyfin
// role publishes it and nothing clears it.
func playOutsideTopic(base, namespace, name string) string {
	return base + "/plays/" + namespace + "/" + name + "/" + playOutsideKind
}

// Carries the last status of an ended Play. Retained; the operator
// publishes and clears it.
func playFinalTopic(base, namespace, name string) string {
	return base + "/plays/" + namespace + "/" + name + "/" + playFinalKind
}

// Carries what the progress role last wrote for one Play. Retained;
// the progress role publishes it, and the operator clears it with the
// other two when it releases the Play.
func playRecordedTopic(base, namespace, name string) string {
	return base + "/plays/" + namespace + "/" + name + "/" + playRecordedKind
}

// The subscriptions that reach every Play's audience, final, and
// recorded message in one namespace. The progress role subscribes to
// the first two; the operator subscribes to the third across every
// namespace with the wildcard form.
func playAudienceFilter(base, namespace string) string {
	return base + "/plays/" + namespace + "/+/" + playAudienceKind
}

func playFinalFilter(base, namespace string) string {
	return base + "/plays/" + namespace + "/+/" + playFinalKind
}

func playRecordedFilter(base string) string {
	return base + "/plays/+/+/" + playRecordedKind
}

// Reaches every outside play in one namespace, which the progress role
// records beside the Plays of its own.
func playOutsideFilter(base, namespace string) string {
	return base + "/plays/" + namespace + "/+/" + playOutsideKind
}

// Carries one Watch's projection out of the store. Retained; the
// progress role publishes it, and the operator writes it to status.
func watchProgressTopic(base, namespace, name string) string {
	return base + "/watches/" + namespace + "/" + name + "/" + watchProgressKind
}

func watchProgressFilter(base string) string {
	return base + "/watches/+/+/" + watchProgressKind
}

// Carries the operator's request to forget one person, to every
// namespace's progress role at once. Retained, with an empty payload
// as the clear. A person is cluster-scoped, so the topic has no
// namespace.
func personForgetTopic(base, person string) string {
	return base + "/people/" + person + "/" + personForgetKind
}

func personForgetFilter(base string) string {
	return base + "/people/+/" + personForgetKind
}

// Carries one namespace's answer that the person's rows are gone.
// Retained; the progress role publishes it, and the operator clears
// it with the request once every namespace has answered.
func personForgottenTopic(base, person, namespace string) string {
	return base + "/people/" + person + "/" + personForgottenKind + "/" + namespace
}

func personForgottenFilter(base string) string {
	return base + "/people/+/" + personForgottenKind + "/+"
}

// The status and availability topics media-operator's playback sidecar
// publishes for one Play, and the filters that reach every Play's in
// one namespace. The shapes are media-operator's, from its plan 03.
func mediaPlayStatusFilter(mediaBase, namespace string) string {
	return mediaBase + "/plays/" + namespace + "/+/" + mediaStatusKind
}

func mediaPlayAvailabilityFilter(mediaBase, namespace string) string {
	return mediaBase + "/plays/" + namespace + "/+/" + mediaAvailabilityKind
}

// Carries online or offline for one namespace's progress role, the
// container beside the standing progress agent. Retained, with offline
// as the Last Will, so a killed pod does not read as a running role.
func progressAvailabilityTopic(base, namespace string) string {
	return base + "/progress/" + namespace + "/" + libraryAvailabilityKind
}

// The subscription that reaches every namespace's progress role.
func progressAvailabilityFilter(base string) string {
	return base + "/progress/+/" + libraryAvailabilityKind
}

// parsePlayTopic reads the namespace, the Play name, and the kind out
// of a topic under either tree's plays branch. It answers ok false for
// any other shape.
func parsePlayTopic(base, topic string) (namespace, name, kind string, ok bool) {
	rest, found := strings.CutPrefix(topic, base+"/plays/")
	if !found {
		return "", "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// parseWatchTopic reads the namespace and the Watch name out of a
// watch progress topic.
func parseWatchTopic(base, topic string) (namespace, name string, ok bool) {
	rest, found := strings.CutPrefix(topic, base+"/watches/")
	if !found {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] != watchProgressKind {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// parsePersonTopic reads the person and, for a forgotten message, the
// namespace out of a topic under the people branch. The namespace is
// empty for a forget request.
func parsePersonTopic(base, topic string) (person, kind, namespace string, ok bool) {
	rest, found := strings.CutPrefix(topic, base+"/people/")
	if !found {
		return "", "", "", false
	}
	parts := strings.Split(rest, "/")
	switch {
	case len(parts) == 2 && parts[1] == personForgetKind && parts[0] != "":
		return parts[0], parts[1], "", true
	case len(parts) == 3 && parts[1] == personForgottenKind && parts[0] != "" && parts[2] != "":
		return parts[0], parts[1], parts[2], true
	}
	return "", "", "", false
}
