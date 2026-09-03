package main

// Which elements each nfo fact owns, and the bytes it writes for them. The
// forms are Kodi's and Jellyfin's own, because those two readers are what a
// person plays the library with. A fact writes no element outside its own
// group.

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// One rating fact per site, and what a reader of the file needs to tell the
// sites apart: the name the rating element carries, the top of that site's
// scale, and which one a reader takes first. Only the TMDb rating carries the
// default mark, because Kodi reads one default, and the other three sit
// beside it.
type ratingSite struct {
	name  string
	max   int
	first bool
}

var ratingSites = map[string]ratingSite{
	factRatingTMDb:           {name: tmdbRatingName, max: tmdbRatingMax, first: true},
	factRatingIMDb:           {name: imdbRatingName, max: imdbRatingMax},
	factRatingRottenTomatoes: {name: rottenTomatoesRatingName, max: rottenTomatoesRatingMax},
	factRatingMetacritic:     {name: metacriticRatingName, max: metacriticRatingMax},
}

// The group of each fact. The rating group names a parent because Kodi holds
// one rating per site inside the ratings element, so the rating of one site
// is the group and the ratings of the other sites stay.
func nfoGroup(fact string) elementGroup {
	if site, held := ratingSites[fact]; held {
		return elementGroup{parent: "ratings", owned: []xmlElement{
			{name: "rating", attribute: "name", value: site.name},
		}}
	}
	switch fact {
	case factOverview:
		return elementGroup{owned: []xmlElement{
			{name: "plot"}, {name: "tagline"}, {name: "genre"},
			{name: "studio"}, {name: "premiered"}, {name: "runtime"},
		}}
	case factCertification:
		return elementGroup{owned: []xmlElement{{name: "mpaa"}}}
	case factCredits:
		return elementGroup{owned: []xmlElement{{name: "actor"}}}
	}
	return elementGroup{}
}

// The group one fact writes, in the order a reader of the file expects. An
// empty value writes no element at all.
func nfoElements(fact string, answer factAnswer) [][]byte {
	if site, held := ratingSites[fact]; held {
		return [][]byte{ratingElement(site, *answer.Rating)}
	}
	switch fact {
	case factOverview:
		return overviewElements(answer)
	case factCertification:
		return [][]byte{textElement("mpaa", answer.Certification)}
	case factCredits:
		return actorElements(answer.Cast)
	}
	return nil
}

func overviewElements(answer factAnswer) [][]byte {
	var elements [][]byte
	if answer.Plot != "" {
		elements = append(elements, textElement("plot", answer.Plot))
	}
	if answer.Tagline != "" {
		elements = append(elements, textElement("tagline", answer.Tagline))
	}
	for _, genre := range answer.Genres {
		elements = append(elements, textElement("genre", genre))
	}
	for _, studio := range answer.Studios {
		elements = append(elements, textElement("studio", studio))
	}
	if answer.Premiered != "" {
		elements = append(elements, textElement("premiered", answer.Premiered))
	}
	if answer.RuntimeMinutes > 0 {
		elements = append(elements, textElement("runtime", strconv.Itoa(answer.RuntimeMinutes)))
	}
	return elements
}

// The rating's form: the site's name, the top of its scale, the mark that
// says a reader takes this one first where the site carries it, the score,
// and the votes where the provider stated a count.
func ratingElement(site ratingSite, rating titleRating) []byte {
	mark := ""
	if site.first {
		mark = ` default="true"`
	}
	out := fmt.Appendf(nil, "<rating name=%q max=%q%s>\n  <value>%s</value>",
		site.name, strconv.Itoa(site.max), mark, strconv.FormatFloat(rating.Value, 'f', -1, 64))
	if rating.Votes > 0 {
		out = fmt.Appendf(out, "\n  <votes>%d</votes>", rating.Votes)
	}
	return append(out, []byte("\n</rating>")...)
}

// An actor element carries the name, the part, the billing order, and the
// picture the provider holds. The person's own ids stay out of it, because
// neither Kodi nor Jellyfin reads an id there.
func actorElements(cast []creditedActor) [][]byte {
	elements := make([][]byte, 0, len(cast))
	for _, actor := range cast {
		out := append([]byte("<actor>\n  "), textElement("name", actor.Name)...)
		if actor.Role != "" {
			out = append(append(out, []byte("\n  ")...), textElement("role", actor.Role)...)
		}
		out = append(append(out, []byte("\n  ")...), textElement("order", strconv.Itoa(actor.Order))...)
		if actor.Thumb != "" {
			out = append(append(out, []byte("\n  ")...), textElement("thumb", actor.Thumb)...)
		}
		elements = append(elements, append(out, []byte("\n</actor>")...))
	}
	return elements
}

// Every value a fact writes is escaped, so a plot with an ampersand in it
// leaves a document a reader can parse. The three characters are escaped by
// hand because encoding/xml writes a newline as a character reference, and a
// plot of several paragraphs must read as one a person wrote.
var xmlText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func textElement(name, value string) []byte {
	var text bytes.Buffer
	text.WriteString("<" + name + ">")
	text.WriteString(xmlText.Replace(value))
	text.WriteString("</" + name + ">")
	return text.Bytes()
}
