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

// The engine solved the fixture's frame at 29.6070°, 59.8193°, with
// index 4207, at 2.68 arcseconds per pixel, as the image's probe in
// plan 02 recorded. Its CD matrix has a positive determinant, which
// solve-field reports as negative parity, and a rotation of 180°.
func TestAFieldThatSolvesAnswersItsSolution(t *testing.T) {
	answer, err := fakeEngineAt(t, engineSolves).Solve(t.Context(), readFixture(t, fixtureAXY))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		got, want float64
		within    float64
	}{
		{"ra", answer.Center.RA, 29.6070, 1e-4},
		{"dec", answer.Center.Dec, 59.8193, 1e-4},
		{"scale", answer.Scale, 2.6815, 1e-3},
		{"rotation", answer.Rotation, 180.0, 0.05},
		{"index", float64(answer.Index), 4207, 0},
	}
	for _, c := range cases {
		if math.Abs(c.got-c.want) > c.within {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !answer.Solved || answer.Parity != ParityNegative {
		t.Errorf("solved %v with parity %q, want solved with %q", answer.Solved, answer.Parity, ParityNegative)
	}
}

// The engine writes no file for a field it does not solve, and its exit
// ends the job.
func TestAFieldThatDoesNotSolveAnswersNoSolution(t *testing.T) {
	answer, err := fakeEngineAt(t, engineMisses).Solve(t.Context(), readFixture(t, fixtureAXY))
	if err != nil || answer.Solved {
		t.Errorf("answered %+v and %v, want no solution", answer, err)
	}
}

// Each job's files go when it ends, so the emptyDir does not fill.
func TestTheJobsFilesGoWhenItEnds(t *testing.T) {
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

func TestAnEngineThatFailsAnswersItsLog(t *testing.T) {
	_, err := fakeEngineAt(t, engineFails).Solve(t.Context(), readFixture(t, fixtureAXY))
	if err == nil || !strings.Contains(err.Error(), "Failed to load index file") {
		t.Errorf("answered %v, want the engine's log", err)
	}
}

func TestAnEngineThatHangsIsStoppedAtTheTimeout(t *testing.T) {
	engine := fakeEngineAt(t, engineHangs)
	engine.Timeout = 200 * time.Millisecond
	started := time.Now()
	_, err := engine.Solve(t.Context(), readFixture(t, fixtureAXY))
	if err == nil || time.Since(started) > 5*time.Second {
		t.Errorf("answered %v after %s, want an error at the timeout", err, time.Since(started))
	}
}

// A WCS file the solver cannot read is an error, not a solution: one
// that is a directory, one that is not FITS, and one with no CRVAL1,
// for which the fake writes the .axy itself.
func TestAWCSFileThatDoesNotReadIsAnError(t *testing.T) {
	cases := []string{engineWritesADirectory, engineWritesGarbage, engineSolves}
	for _, behavior := range cases {
		t.Run(behavior, func(t *testing.T) {
			engine := fakeEngineAt(t, behavior)
			axy, err := filepath.Abs(fixtureAXY)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAKE_ENGINE_WCS", axy)
			if _, err := engine.Solve(t.Context(), readFixture(t, fixtureAXY)); err == nil {
				t.Error("no error")
			}
		})
	}
}

// replaceCard answers the .axy with the card of one key replaced by
// another card, padded to 80 characters.
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

// The solver runs only an .axy whose outputs stay in the job's
// directory, so the engine writes nothing anywhere else in the pod.
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
				t.Errorf("answered %v, want %v", err, ErrBadStarList)
			}
		})
	}
}

func TestAWorkDirectoryThatIsGoneIsAnError(t *testing.T) {
	engine := fakeEngineAt(t, engineSolves)
	engine.Work += "/gone"
	if _, err := engine.Solve(t.Context(), readFixture(t, fixtureAXY)); err == nil {
		t.Error("no error")
	}
}
