package main

// jellyfinwebhook.go turns a play that ran outside this cluster into a
// message on the bus. Jellyfin's Webhook plugin posts one body per
// playback event, and one per save of a person's user data. The role turns
// each post into one outside play, which the progress role records.
// Every value of the body is a string. The plugin renders a Handlebars
// template, a variable it does not hold renders empty, and a boolean of the
// server arrives as True or False.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The bound on one post, and how long the server waits for a sender's
// headers, so a connection that opens and says nothing cannot hold a slot.
const (
	jellyfinBodyLimit     = 1 << 20
	jellyfinHeaderTimeout = 10 * time.Second
)

// The two paths the role answers: the endpoint the plugin posts to, and the
// one the kubelet reads.
const (
	jellyfinWebhookPath = "POST /webhook"
	jellyfinHealthPath  = "GET /healthz"
)

// The event names the plugin posts. A stop marks the row ended. The
// fourth, a user-data save, is what the plugin posts when a person marks
// a work played or unplayed by hand, with no playback at all.
const (
	jellyfinStartEvent    = "PlaybackStart"
	jellyfinProgressEvent = "PlaybackProgress"
	jellyfinStopEvent     = "PlaybackStop"
	jellyfinUserDataEvent = "UserDataSaved"
)

// The one save reason the role reads. Every other reason, chiefly
// UpdateUserData, is this role's own write posted back to it. Reading one
// would record the role's write as a play.
const jellyfinToggleReason = "TogglePlayed"

// The name the store's player column carries for a play that ran in Jellyfin.
// The column names a Player, and no Player of this cluster ran it.
const jellyfinPlayerName = "jellyfin"

// One post of the Webhook plugin, as the template in the manual renders it.
type jellyfinEvent struct {
	Event              string `json:"event"`
	User               string `json:"user"`
	UserID             string `json:"userId"`
	ItemID             string `json:"itemId"`
	ItemType           string `json:"itemType"`
	SeriesID           string `json:"seriesId"`
	Season             string `json:"season"`
	Episode            string `json:"episode"`
	PositionTicks      string `json:"positionTicks"`
	RunTimeTicks       string `json:"runTimeTicks"`
	Paused             string `json:"paused"`
	PlayedToCompletion string `json:"playedToCompletion"`
	SaveReason         string `json:"saveReason"`
	Played             string `json:"played"`
	Tmdb               string `json:"tmdb"`
	Imdb               string `json:"imdb"`
	Tvdb               string `json:"tvdb"`
}

// The role's two endpoints. The method is part of the pattern, so a request
// with another method gets a 405 from the mux and never reaches the handler.
func (j *jellyfin) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(jellyfinWebhookPath, j.webhook)
	mux.HandleFunc(jellyfinHealthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// One post. A body of another shape is a bad request, and a body this role
// cannot map is answered as read, because Jellyfin retries what it cannot
// deliver and nothing here improves on the next post.
func (j *jellyfin) webhook(w http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(request.Body, jellyfinBodyLimit))
	event := jellyfinEvent{}
	if err := json.Unmarshal(body, &event); err != nil {
		j.logf("a jellyfin webhook reads as no event: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ctx, done := context.WithTimeout(request.Context(), jellyfinRequestTimeout)
	defer done()
	name, play, ok := j.outsideOf(ctx, event)
	if !ok {
		w.WriteHeader(http.StatusOK)
		return
	}

	payload, _ := json.Marshal(play)
	// The message is not retained. A progress role that was down for one
	// post catches the next, ten seconds later, and a stop repeats the
	// final position.
	j.publish(playOutsideTopic(j.topicBase, j.namespace, name), payload, false)
	w.WriteHeader(http.StatusNoContent)
}

// The Play name and the payload one event becomes, or ok false for an event
// the role drops.
// The name is the user and the item, so one person's progress in one item is
// one row that moves, and a rewatch moves it again.
func (j *jellyfin) outsideOf(ctx context.Context, event jellyfinEvent) (string, outsidePlay, bool) {
	user, item := jellyfinID(event.UserID), jellyfinID(event.ItemID)
	if event.User == "" || user == "" || item == "" {
		j.logf("a jellyfin webhook names no user or no item")
		return "", outsidePlay{}, false
	}
	if !jellyfinCarried(event) {
		return "", outsidePlay{}, false
	}
	duration := jellyfinSeconds(jellyfinNumber(event.RunTimeTicks))
	position := jellyfinPosition(jellyfinNumber(event.PositionTicks), duration, jellyfinMarked(event))
	// The echo drop. A position this role wrote a moment ago is its own
	// write coming back, and recording it would set the row's recorded
	// time forward for no new fact.
	if j.out.echoes.echoed(user, item, position) {
		return "", outsidePlay{}, false
	}
	aliases := j.aliasesOf(ctx, event)
	if len(aliases) == 0 {
		j.logf("a jellyfin webhook for the item %s names no provider ids", item)
		return "", outsidePlay{}, false
	}

	return jellyfinPlayerName + "-" + user + "-" + item, outsidePlay{
		Player:   jellyfinPlayerName,
		People:   []string{event.User},
		Aliases:  aliases,
		Season:   jellyfinCount(event.Season),
		Episode:  jellyfinCount(event.Episode),
		Position: position,
		Duration: duration,
		Ended:    strings.EqualFold(event.Event, jellyfinStopEvent) || jellyfinFlag(event.PlayedToCompletion),
		At:       j.now().Unix(),
	}, true
}

// Whether one post reaches the bus. A playback event always does. A
// user-data save does only when its reason is TogglePlayed, a mark a
// person set by hand.
func jellyfinCarried(event jellyfinEvent) bool {
	if !strings.EqualFold(event.Event, jellyfinUserDataEvent) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(event.SaveReason), jellyfinToggleReason)
}

// Whether the post is a mark a person set by hand that says they played
// the work.
func jellyfinMarked(event jellyfinEvent) bool {
	return strings.EqualFold(event.Event, jellyfinUserDataEvent) && jellyfinFlag(event.Played)
}

// One id from the post, in the spelling Jellyfin's API writes: 32
// hexadecimal digits with no dashes. The plugin renders the same Guid
// through Handlebars, which has no Guid formatter and writes the dashed
// form. Two spellings of one id would make two rows and break the echo
// drop. A value that is not a Guid is returned as the post spells it.
func jellyfinID(value string) string {
	held := strings.ToLower(strings.TrimSpace(value))
	stripped := strings.ReplaceAll(held, "-", "")
	if _, err := hex.DecodeString(stripped); err != nil || len(stripped) != 32 {
		return held
	}
	return stripped
}

// The identity of the work the event names. An episode takes the series'
// provider ids, because that is the identity the catalog gives an episode
// play, and the payload carries the episode's own ids alone.
func (j *jellyfin) aliasesOf(ctx context.Context, event jellyfinEvent) map[string]string {
	if strings.EqualFold(event.ItemType, jellyfinEpisodeType) {
		return j.index.seriesAliases(ctx, jellyfinID(event.SeriesID))
	}
	return jellyfinProviders(map[string]string{
		"tmdb": event.Tmdb,
		"imdb": event.Imdb,
		"tvdb": event.Tvdb,
	})
}

// The position of a play Jellyfin reports as played, which is the end of
// the work. Jellyfin stores played as a state of its own, and a mark set
// by hand has a position of zero. When the post states no run time, the
// position stays as the post gave it, because nothing says where the end
// is.
func jellyfinPosition(ticks int64, duration int, played bool) int {
	if played && duration > 0 {
		return duration
	}
	return jellyfinSeconds(ticks)
}

// One number of the payload. A field the template did not render is empty and
// reads as zero, and so does a field of any other shape.
func jellyfinNumber(value string) int64 {
	number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return number
}

// One count of the payload, which is a season or an episode number.
func jellyfinCount(value string) int {
	return int(jellyfinNumber(value))
}

// One boolean of the payload. The server writes True and False, and a
// template of another make writes true and false, so the read is without
// case.
func jellyfinFlag(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}
