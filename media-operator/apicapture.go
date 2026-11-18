package main

// This file serves the three capture aspects of a Player: screen,
// audio, and media. The screen aspect redirects to the display API
// and the audio aspect redirects to the audio API, because those APIs
// own the hardware and the capture options. The media aspect combines
// screen and audio in this API. A request is handled in this order:
// authorize, choose the form, validate the query, read the Player,
// respond. Authorization is first so that a 403 does not reveal
// whether the Player exists. The query is validated before the Player
// is read so that a 400 costs no API call.

import (
	"net/http"
	"net/url"
	"strings"
)

// retryAfterSeconds is the Retry-After value of every 503 from this
// API, except when the API relays the Retry-After of an upstream.
const retryAfterSeconds = "5"

// captureAspect is one capture route family of a Player. subresource
// is the players subresource that the access review checks, and keys
// are the query keys the route accepts.
type captureAspect struct {
	name        string
	subresource string
	forms       []mediaForm
	keys        []string
}

// The aspect names. Each is also the players subresource that RBAC
// checks, so a Role rule on players/screen grants the screen routes.
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

// serveCapture handles one capture request in the order that the file
// comment gives. negotiated is true for a route with no extension, and
// otherwise form is the form that the extension selected.
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

// notAcceptable responds 406 with a problem document whose Acceptable
// list has the type and href of every form of the aspect (RFC 9110
// section 15.5.7). The list includes the forms of the other
// extensions, so a client that asked for the wrong extension learns
// the right ones.
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
	e.writeProblemDocument(document)
}

// redirectToScreen redirects to the Display route of the monitor in
// the Player's status.screen. When status.screen is empty it responds
// 409 and not 404, because the Player exists and the caller can fix
// it by running a Play or by giving the Player a display.
func (s *apiServer) redirectToScreen(e *apiExchange, player *Player, form mediaForm, query captureQuery) {
	monitor := rememberedMonitor(player)
	if monitor == "" {
		e.fail(problemNoNode, http.StatusConflict, "This Player has no screen",
			"the Player has no status.screen. Run a Play on it, or set its spec.display")
		return
	}
	e.redirect(displayTarget(s.display, monitor, form.Extension, query, 0), query)
}

// redirectToSink redirects to the Sink route of the Player's first
// sink. With no running Play, tappableSinks returns nothing and the
// response is 409.
func (s *apiServer) redirectToSink(e *apiExchange, player *Player, form mediaForm, query captureQuery) {
	sinks := tappableSinks(player)
	if len(sinks) == 0 {
		e.fail(problemNotPlaying, http.StatusConflict, "This Player plays nothing",
			notPlayingDetail(player, audioAspectName))
		return
	}
	// A Location names one stream, so the redirect goes to the first
	// sink in spec.sinks order and the others are in Link headers.
	// The media aspect captures every sink together.
	target := audioTarget(s.audio, sinks[0].Name, form.Extension, query, 0)
	for _, sink := range sinks[1:] {
		addLink(e.writer.Header(),
			audioTarget(s.audio, sink.Name, form.Extension, query, 0), relAudio, "")
	}
	e.redirect(target, query)
}

// notPlayingDetail returns the detail of a 409, which tells the caller
// what to do next. A Player that plays but has no status.sinks needs
// spec.sinks. Otherwise the caller must run a Play, and the media
// aspect's detail does not mention sound because that aspect needs the
// screen too.
func notPlayingDetail(player *Player, aspect string) string {
	if player.Status.Activity == playerPlaying {
		return "this Player resolves no Sink. Set spec.sinks on it and run a Play"
	}
	if aspect == audioAspectName {
		return "run a Play on this Player to open its sound"
	}
	return "this Player resolves nothing to capture until a Play runs on it"
}

// rememberedMonitor returns the Display name in status.screen, or an
// empty string when no claim has resolved a screen yet.
func rememberedMonitor(player *Player) string {
	if player.Status.Screen == nil {
		return ""
	}
	return player.Status.Screen.Monitor
}

// tappableSinks returns status.sinks while a Play runs and nil
// otherwise. status.sinks keeps its values after a Play ends, and
// another Player may then use the same Sinks.
func tappableSinks(player *Player) []PlayerSinkStatus {
	if player.Status.Activity != playerPlaying {
		return nil
	}
	return player.Status.Sinks
}

// redirect responds 307 and not 301 or 302. The target changes when
// the Player's claim moves to another monitor, so the client must keep
// requesting this URI (RFC 9110 section 15.4.8). The Link with
// rel="related" points to media.mp4 for the same time span, for a
// client that wants sound with the picture. It is not rel="alternate"
// because the combined stream is a different aspect of the Player and
// not a replacement for the screen alone.
func (e *apiExchange) redirect(target string, query captureQuery) {
	header := e.writer.Header()
	header.Set("Location", target)
	header.Set("Cache-Control", "no-store")
	addLink(header, e.composedTarget(query), relRelated, "video/mp4")
	e.writeStatus(http.StatusTemporaryRedirect)
}

// composedTarget returns the media.mp4 path of this Player with the
// request's t= span. It applies no lead-in, because the media route
// adds the lead-in itself.
func (e *apiExchange) composedTarget(query captureQuery) string {
	path := e.aspectPath(mediaAspectName, "mp4")
	if temporal := query.shiftedTemporal(0); temporal != "" {
		return path + "?t=" + temporal
	}
	return path
}

// displayTarget returns the display API URL for a monitor's screen,
// which the screen aspect redirects to and the media aspect reads
// from. It carries the query keys that the screen aspect accepts.
func displayTarget(base, monitor, extension string, query captureQuery, lead float64) string {
	return base + "/v1/display/displays/" + url.PathEscape(monitor) + "/screen." + extension +
		captureValues(query, lead, screenCaptureKeys)
}

// audioTarget returns the audio API URL for a sink, which the audio
// aspect redirects to and the media aspect reads from. It carries t=
// and bitrate=.
func audioTarget(base, sink, extension string, query captureQuery, lead float64) string {
	return base + "/v1/audio/sinks/" + url.PathEscape(sink) + "/audio." + extension +
		captureValues(query, lead, audioCaptureKeys)
}

// captureValues builds the upstream query string by hand and not with
// url.Values. url.Values.Encode sorts the keys and writes the comma of
// a Media Fragments span as %2C. Here the keys come out in the order
// of keys, so a test can compare a Location as a string, and t=0,10
// stays readable.
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
