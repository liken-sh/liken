package solver

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The expected values are what the real engine returned for the
// fixture's frame. The fixture's CD matrix has a positive determinant,
// which astrometry.net calls negative parity.
func TestAFieldThatSolvesReturnsItsSolution(t *testing.T) {
	solution, err := fakeEngineAt(t, engineSolves).Solve(t.Context(), readFixture(t, fixtureAXY))
	if err != nil {
		t.Fatalf("solving the fixture frame: %v", err)
	}
	cases := []struct {
		name      string
		got, want float64
		within    float64
	}{
		{"ra", solution.Center.RA, 29.6070, 1e-4},
		{"dec", solution.Center.Dec, 59.8193, 1e-4},
		{"scale", solution.Scale, 2.6815, 1e-3},
		{"rotation", solution.Rotation, 180.0, 0.05},
		{"index", float64(solution.Index), 4207, 0},
	}
	for _, c := range cases {
		if math.Abs(c.got-c.want) > c.within {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !solution.Solved || solution.Parity != ParityNegative {
		t.Errorf("solved %v with parity %q, want solved with %q", solution.Solved, solution.Parity, ParityNegative)
	}
}

func TestAFieldThatDoesNotSolveReturnsNoSolution(t *testing.T) {
	solution, err := fakeEngineAt(t, engineMisses).Solve(t.Context(), readFixture(t, fixtureAXY))
	if err != nil || solution.Solved {
		t.Errorf("Solve returned %+v and %v, want an empty response and no error", solution, err)
	}
}

// Solve deletes each job's directory, so the emptyDir does not fill up.
func TestTheJobDirectoryIsDeletedWhenTheJobEnds(t *testing.T) {
	cases := []string{engineSolves, engineMisses, engineFails}
	for _, behavior := range cases {
		t.Run(behavior, func(t *testing.T) {
			engine := fakeEngineAt(t, behavior)
			_, _ = engine.Solve(t.Context(), readFixture(t, fixtureAXY))
			left, _ := os.ReadDir(engine.Work)
			if len(left) != 1 || left[0].Name() != "engine.cfg" {
				t.Errorf("the work directory holds %v, want only engine.cfg", left)
			}
		})
	}
}

func TestAnEngineThatFailsReturnsItsLog(t *testing.T) {
	_, err := fakeEngineAt(t, engineFails).Solve(t.Context(), readFixture(t, fixtureAXY))
	if err == nil || !strings.Contains(err.Error(), "Failed to load index file") {
		t.Errorf("Solve returned %v, want an error that holds the engine's log", err)
	}
}

func TestAnEngineThatHangsIsStoppedAtTheTimeout(t *testing.T) {
	engine := fakeEngineAt(t, engineHangs)
	engine.Timeout = 200 * time.Millisecond
	started := time.Now()
	_, err := engine.Solve(t.Context(), readFixture(t, fixtureAXY))
	if err == nil || time.Since(started) > 5*time.Second {
		t.Errorf("Solve returned %v after %s, want an error within 5s of the 200ms timeout", err, time.Since(started))
	}
}

// A WCS file that Solve cannot read is an error, not a missing
// solution. FAKE_ENGINE_WCS makes the fake engine write the .axy fixture
// as the WCS file, which is FITS with no CRVAL1.
func TestAnUnreadableWCSFileIsAnError(t *testing.T) {
	cases := []string{engineWritesADirectory, engineWritesGarbage, engineSolves}
	for _, behavior := range cases {
		t.Run(behavior, func(t *testing.T) {
			engine := fakeEngineAt(t, behavior)
			axy, err := filepath.Abs(fixtureAXY)
			if err != nil {
				t.Fatalf("resolving the path of %s: %v", fixtureAXY, err)
			}
			t.Setenv("FAKE_ENGINE_WCS", axy)
			if _, err := engine.Solve(t.Context(), readFixture(t, fixtureAXY)); err == nil {
				t.Error("Solve returned no error, want one for the unreadable WCS file")
			}
		})
	}
}

// replaceCard returns a copy of the .axy in which the card for key is
// replaced by card, padded to 80 characters.
func replaceCard(axy []byte, key, card string) []byte {
	card += strings.Repeat(" ", 80-len(card))
	out := bytes.Clone(axy)
	for at := 0; at < len(out); at += 80 {
		if strings.TrimRight(string(out[at:at+8]), " ") == key {
			copy(out[at:at+80], card)
			return out
		}
	}
	return out
}

// Solve refuses an .axy that is not FITS, names no WCS file, or names
// an output outside the job's directory.
func TestAStarListTheSolverDoesNotRunIsRefused(t *testing.T) {
	axy := readFixture(t, fixtureAXY)
	cases := []struct {
		name string
		axy  []byte
	}{
		{"not FITS", []byte("not a FITS file")},
		{"no WCS file", replaceCard(axy, "ANWCS", "COMMENT no WCS file")},
		{"a WCS file elsewhere", replaceCard(axy, "ANWCS", "ANWCS   = '/etc/field.wcs'")},
		{"a solved file above", replaceCard(axy, "ANSOLVED", "ANSOLVED= '../field.solved'")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := fakeEngineAt(t, engineSolves).Solve(t.Context(), c.axy)
			if !errors.Is(err, ErrBadStarList) {
				t.Errorf("Solve returned %v, want %v", err, ErrBadStarList)
			}
		})
	}
}

func TestAWorkDirectoryThatIsGoneIsAnError(t *testing.T) {
	engine := fakeEngineAt(t, engineSolves)
	engine.Work += "/gone"
	if _, err := engine.Solve(t.Context(), readFixture(t, fixtureAXY)); err == nil {
		t.Error("Solve returned no error, want one for the missing work directory")
	}
}
