package main

// The declared FITS keywords of plan 20: the session records the merged
// list of each imaging camera, and Configure writes it to the camera's
// FITS_HEADER.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// headerWrites answers each write to a FITS_HEADER that clients sent
// since the change from, as the server, the device, and the members.
func headerWrites(w *world, from int) []string {
	return slices.DeleteFunc(slices.Clone(w.indi.changes()[from:]), func(c string) bool {
		return !strings.Contains(c, ".FITS_HEADER ")
	})
}

// written answers the change that writes one keyword to the east
// telescope's imaging camera.
func written(name, value, comment string) string {
	return "east-telescope CCD Simulator.FITS_HEADER KEYWORD_NAME=" + name + " KEYWORD_VALUE=" + value + " KEYWORD_COMMENT=" + comment
}

// declare sets the spec.fitsKeywords of one resource, and removes the
// field when keywords is empty.
func (w *world) declare(kind observatory.Kind, name string, keywords ...map[string]any) {
	w.t.Helper()
	w.amend(kind, name, func(spec map[string]any) {
		if len(keywords) == 0 {
			delete(spec, "fitsKeywords")
			return
		}
		items := make([]any, len(keywords))
		for i, k := range keywords {
			items[i] = k
		}
		spec["fitsKeywords"] = items
	})
}

func fitsKeyword(name, value, comment string) map[string]any {
	k := map[string]any{"name": name, "value": value}
	if comment != "" {
		k["comment"] = comment
	}
	return k
}

// exampleKeywords are the writes of the keywords that the example
// declares: OBSERVER on the Observatory lab, and TELESCOP on the
// Telescope east.
var exampleKeywords = []string{
	written("OBSERVER", "Simulated Observer", ""),
	written("TELESCOP", "East refractor", "the Telescope's own name"),
}

// Configure writes the merged keywords of the four levels to the
// imaging camera, one write for each keyword, in the order of first
// appearance with the most specific value. The guide camera gets none,
// even when its own spec declares keywords, and with no keywords at any
// level, no camera gets a write.
func TestConfigureWritesTheMergedKeywordsToTheImagingCamera(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		declare     func(w *world)
		reservation []any
		want        []string
	}{
		{"the example's keywords", func(*world) {}, nil, exampleKeywords},
		{"no keywords at any level", func(w *world) {
			w.declare(observatory.ObservatoryKind, "lab")
			w.declare(observatory.TelescopeKind, "east")
		}, nil, nil},
		{"every level, with the most specific value of each name", func(w *world) {
			w.declare(observatory.ObservatoryKind, "lab", fitsKeyword("OBSERVER", "Site", ""), fitsKeyword("SITENAME", "Lab", "where"))
			w.declare(observatory.TelescopeKind, "east", fitsKeyword("TELESCOP", "Esprit 100", ""), fitsKeyword("OBSERVER", "Scope", ""))
			w.declare(observatory.CameraKind, "east-main", fitsKeyword("INSTRUME", "ASI2600MM", "camera"), fitsKeyword("SITENAME", "Pier 2", ""))
			w.declare(observatory.CameraKind, "east-guide", fitsKeyword("INSTRUME", "ASI120MM", ""))
		}, []any{fitsKeyword("OBSERVER", "Ada", "tonight"), fitsKeyword("OBJECT", "M42", "")}, []string{
			written("OBSERVER", "Ada", "tonight"),
			written("SITENAME", "Pier 2", ""),
			written("TELESCOP", "Esprit 100", ""),
			written("INSTRUME", "ASI2600MM", "camera"),
			written("OBJECT", "M42", ""),
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.declare(w)
				spec := map[string]any{"telescope": "east", "holder": "desktop"}
				if c.reservation != nil {
					spec["fitsKeywords"] = c.reservation
				}
				w.reserve("east-tonight", spec)
				w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)

				if got := headerWrites(w, 0); !slices.Equal(got, c.want) {
					t.Errorf("FITS_HEADER writes = %q, want %q", got, c.want)
				}
			})
		})
	}
}

// The session records the merged keywords of the imaging camera, and
// an edit during the session changes neither the record nor what a
// driver that restarts gets. A driver that restarts starts with no
// keywords, so it gets every keyword of the record again. The next
// session records the edit.
func TestAKeywordEditWaitsForTheNextSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.declare(observatory.ObservatoryKind, "lab", fitsKeyword("OBSERVER", "Grace", ""))
		w.declare(observatory.TelescopeKind, "east")
		w.settle()
		from := len(w.indi.changes())
		w.api.deleteNamed(podsCollection, "east-main-camera")
		w.until(5*time.Minute, "the restarted driver did not get its keywords", func() bool {
			return len(headerWrites(w, from)) == len(exampleKeywords)
		})

		if got := headerWrites(w, from); !slices.Equal(got, exampleKeywords) {
			t.Errorf("writes after the restart = %q, want %q", got, exampleKeywords)
		}
		r, _ := w.reservation("east-tonight")
		if got := r.Status.Session.FITSKeywords["Camera/east-main"]; len(got) != 2 || got[0].Value != "Simulated Observer" {
			t.Errorf("the record = %+v, want the example's keywords", got)
		}

		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the Reservation is still there", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		from = len(w.indi.changes())
		w.reserve("east-tomorrow", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tomorrow", observatory.ReservationReady, 10*time.Minute)
		if got, want := headerWrites(w, from), []string{written("OBSERVER", "Grace", "")}; !slices.Equal(got, want) {
			t.Errorf("the next session's writes = %q, want %q", got, want)
		}
	})
}

// A retry is a person saying that the specs are fixed, so a retry that
// changes the keywords records them again and runs Configure again,
// which writes the new list.
func TestARetryThatChangesTheKeywordsRunsConfigureAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		var from int
		failGuiderThenRetry(w, func(w *world) {
			from = len(w.indi.changes())
			w.declare(observatory.ReservationKind, "east-tonight", fitsKeyword("OBJECT", "M31", ""))
		})
		r := w.phase("east-tonight", observatory.ReservationReady, 15*time.Minute)

		want := append(slices.Clone(exampleKeywords), written("OBJECT", "M31", ""))
		if got := headerWrites(w, from); !slices.Equal(got, want) {
			t.Errorf("writes after the retry = %q, want %q", got, want)
		}
		if got := r.Status.Session.FITSKeywords["Camera/east-main"]; len(got) != 3 {
			t.Errorf("the record = %+v, want 3 keywords", got)
		}
	})
}
