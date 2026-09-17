package main

// This file serves the three capture aspects of a Player. The screen
// aspect redirects to the display API and the audio aspect redirects
// to the audio API, because each sibling owns its hardware and its
// capture rule. The media aspect composes the two here. Every capture
// request takes the same order: authorize, negotiate the form,
// validate the query, read the Player, then answer. Authorization
// comes first so a 403 never says whether the Player exists, and the
// query is checked before the Player is read so a 400 costs nothing.

import (
	"net/http"
	"net/url"
	"strings"
)

// retryAfterSeconds is the Retry-After every 503 of this API carries,
// unless an upstream's own value is relayed in its place.
const retryAfterSeconds = "5"

// captureAspect is one aspect of a Player: the noun in the path, the
// players subresource the access review names, the forms it serves,
// and the query keys its grammar accepts.
type captureAspect struct {
	name        string
	subresource string
	forms       []mediaForm
	keys        []string
}

// The three aspect names. Each is also the players subresource RBAC
// checks, so a grant reads as the route it opens.
const (
	screenAspectName = "screen"
	audioAspectName  = "audio"
	mediaAspectName  = "media"
)

func captureAspects() []captureAspect {
	return []captureAspect{
		{name: screenAspectName, subresource: screenAspectName, forms: screenForms, keys: screenCaptureKeys},
		{name: audioAspectName, subresource: audioAspectName, forms: audioForms, keys: audioCaptureKeys},
		{name: mediaAspectName, subresource: mediaAspectName, forms: composedForms, keys: mediaCaptureKeys},
	}
}

// serveCapture answers one capture request. The checks run in cost
// order: the access review, the Accept field, the query, then the
// Player read. A refused query answers 400 before any upstream is
// called, so a bad request opens no capture on a sidecar and takes no
// frame from a unit.
func (s *apiServer) serveCapture(e *apiExchange, aspect captureAspect, form mediaForm, negotiated bool) {
	if !e.authorize(aspect.subresource) {
		return
	}
	if negotiated {
		chosen, ok := negotiate(e.request.Header.Get("Accept"), aspect.forms)
		if !ok {
			e.notAcceptable(aspect)
			return
		}
		form = chosen
	} else if !acceptable(e.request.Header.Get("Accept"), form) {
		e.notAcceptable(aspect)
		return
	}
	query, err := parseCaptureQuery(e.request.URL.RawQuery, aspect.keys)
	if err != nil {
		e.fail(aboutBlank, http.StatusBadRequest, "", err.Error())
		return
	}
	player, ok := e.player()
	if !ok {
		return
	}
	switch aspect.name {
	case screenAspectName:
		s.redirectToScreen(e, player, form, query)
	case audioAspectName:
		s.redirectToSink(e, player, form, query)
	default:
		s.serveComposed(e, player, form, query, negotiated)
	}
}

// notAcceptable answers 406 with the acceptable member, a list of
// {type, href} pairs for every form of the aspect. An extension route
// lists its siblings as well as its own type, RFC 9110 section
// 15.5.7, so a client that asked for the wrong extension is told the
// right one.
func (e *apiExchange) notAcceptable(aspect captureAspect) {
	document := newProblem(problemNotAcceptable, http.StatusNotAcceptable,
		"Nothing this route serves is acceptable",
		"the Accept field excludes every representation of "+aspect.name, e.instance())
	for _, form := range aspect.forms {
		document.Acceptable = append(document.Acceptable, acceptableForm{
			Type: form.Type,
			Href: e.aspectPath(aspect.name, form.Extension),
		})
	}
	e.answerProblem(document)
}

// redirectToScreen sends the client to the Display route of the
// screen the Player remembers. A Player with no status.screen answers
// 409 rather than 404, because the Player exists and the caller can
// act: running a Play, or giving the Player a display, resolves the
// screen.
func (s *apiServer) redirectToScreen(e *apiExchange, player *Player, form mediaForm, query captureQuery) {
	monitor := rememberedMonitor(player)
	if monitor == "" {
		e.fail(problemNoNode, http.StatusConflict, "This Player has no screen",
			"the Player carries no status.screen; run a Play on it, or give it a display")
		return
	}
	e.redirect(displayTarget(s.display, monitor, form.Extension, query, 0), query)
}

// redirectToSink sends the client to the Sink route of the Player's
// first sink. The memory in status.sinks stands between runs, but a
// tap through it needs a running Play: a remembered Sink that another
// unit is using is never tapped through this one, so with no Play the
// answer is 409 not-playing.
func (s *apiServer) redirectToSink(e *apiExchange, player *Player, form mediaForm, query captureQuery) {
	sinks := tappableSinks(player)
	if len(sinks) == 0 {
		e.fail(problemNotPlaying, http.StatusConflict, "This Player plays nothing",
			notPlayingDetail(player, audioAspectName))
		return
	}
	// A unit with several sinks redirects to the first, in spec.sinks
	// order, and links the others. One Location names one stream;
	// the composed route is where every sink plays at once.
	target := audioTarget(s.audio, sinks[0].Name, form.Extension, query, 0)
	for _, sink := range sinks[1:] {
		addLink(e.writer.Header(),
			audioTarget(s.audio, sink.Name, form.Extension, query, 0), relAudio, "")
	}
	e.redirect(target, query)
}

// notPlayingDetail names the action that clears the 409, because a
// 409 is sent only where the caller can act and the detail is where
// the action is stated. The composed route resolves no screen either
// where it reaches this, so its sentence names the whole unit and not
// the sound alone.
func notPlayingDetail(player *Player, aspect string) string {
	if player.Status.Activity == playerPlaying {
		return "this Player resolves no Sink; state spec.sinks on it and run a Play"
	}
	if aspect == audioAspectName {
		return "run a Play on this Player to open its sound"
	}
	return "run a Play on this Player; it resolves nothing to capture until one runs"
}

// rememberedMonitor is the screen memory, the Display name, empty
// until a claim has resolved once.
func rememberedMonitor(player *Player) string {
	if player.Status.Screen == nil {
		return ""
	}
	return player.Status.Screen.Monitor
}

// tappableSinks is the sink memory while a Play runs, and nothing
// otherwise. A Sink is allocated to this Player only while a Play
// runs, and between runs the same Sink may be playing another unit's
// sound.
func tappableSinks(player *Player) []PlayerSinkStatus {
	if player.Status.Activity != playerPlaying {
		return nil
	}
	return player.Status.Sinks
}

// redirect answers 307 and never 301 or 302. A Player's screen moves
// when its claim moves, so the target is temporary and the client
// must keep using this URI, RFC 9110 section 15.4.8. The related link
// points to the composed media.mp4 at the same span, for a client
// that wanted the sound with the picture; it is related and not
// alternate, because the muxed stream is a different aspect of the
// Player and no substitute for the screen alone.
func (e *apiExchange) redirect(target string, query captureQuery) {
	header := e.writer.Header()
	header.Set("Location", target)
	header.Set("Cache-Control", "no-store")
	addLink(header, e.composedTarget(query), relRelated, "video/mp4")
	e.answer(http.StatusTemporaryRedirect)
}

// composedTarget is the composed media.mp4 of this Player at the same
// t= span the request asked for, with no lead-in applied, because the
// composed route adds the lead-in itself.
func (e *apiExchange) composedTarget(query captureQuery) string {
	path := e.aspectPath(mediaAspectName, "mp4")
	if temporal := query.shiftedTemporal(0); temporal != "" {
		return path + "?t=" + temporal
	}
	return path
}

// displayTarget is the display API route this API's screen aspect
// redirects to or composes from. The screen knobs travel with it:
// t=, xywh=, width=, height=, framerate=, and quality=.
func displayTarget(base, monitor, extension string, query captureQuery, lead float64) string {
	return base + "/v1/display/displays/" + url.PathEscape(monitor) + "/screen." + extension +
		captureValues(query, lead, screenCaptureKeys)
}

// audioTarget is the audio API route this API's audio aspect redirects
// to or composes from, with t= and bitrate=.
func audioTarget(base, sink, extension string, query captureQuery, lead float64) string {
	return base + "/v1/audio/sinks/" + url.PathEscape(sink) + "/audio." + extension +
		captureValues(query, lead, audioCaptureKeys)
}

// captureValues writes the upstream query by hand rather than through
// url.Values, for two reasons. The keys go out in one fixed order, so
// a Location and a Link are the same bytes for the same request and a
// test compares them byte for byte. And the comma of a Media
// Fragments span stays a comma: url.Values would escape it to %2C,
// which the sibling parses the same but which a person reading the
// Location would not read as t=0,10.
func captureValues(query captureQuery, lead float64, keys []string) string {
	values := map[string]string{
		"t":         query.shiftedTemporal(lead),
		"xywh":      query.XYWH,
		"width":     query.Width,
		"height":    query.Height,
		"framerate": query.Framerate,
		"quality":   query.Quality,
		"bitrate":   query.Bitrate,
	}
	var pairs []string
	for _, key := range keys {
		if value := values[key]; value != "" {
			pairs = append(pairs, key+"="+value)
		}
	}
	if len(pairs) == 0 {
		return ""
	}
	return "?" + strings.Join(pairs, "&")
}
