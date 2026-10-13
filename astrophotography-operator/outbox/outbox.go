// Package outbox holds the layout of a reservation's outbox, which the
// runner, the worker, and the promotion Job share (plans 01 and 03).
// observatory-operator mounts the outbox claim at /outbox in each
// camera's pod and points the camera at /outbox/<Camera>. This
// operator's pods mount the same claim at the same path, so a path
// that the runner gives the driver is the path that the worker and the
// promotion Job read.
//
// Inside the camera's directory, each reservation and each target has
// its own directory:
//
//	/outbox/<Camera>/<Reservation>/<Target>/<name>.fits
//	/outbox/<Camera>/<Reservation>/_centering/<FRAMEID>.fits
//
// So a file's path tells which target and reservation the frame belongs
// to, with no list to keep. A Kubernetes name cannot hold "_", so
// _centering never meets a target's directory.
package outbox

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

const (
	// Root is where every pod mounts the outbox claim.
	Root = "/outbox"
	// Centering is the directory of a reservation's centering frames.
	Centering = "_centering"
	// Extension is what the driver adds to each file's name.
	Extension = ".fits"
)

// Frame is a frame's place in the outbox.
type Frame struct {
	Camera      string
	Reservation string
	// Target is empty for a centering frame.
	Target string
	// Name is the file's name with no extension: the name that the
	// target's archive.format gives, which can hold directories, or the
	// FRAMEID of a centering frame.
	Name string
}

// Path answers the frame's path relative to Root.
func (f Frame) Path() string {
	return path.Join(f.Camera, f.Reservation, f.directory(), f.Name) + Extension
}

// Upload answers the frame's directory and its file name with no
// extension, the values the runner writes to the camera's
// UPLOAD_SETTINGS. The driver creates a directory that is missing, and
// takes a prefix with no pattern in it as the file's whole name.
func (f Frame) Upload() (dir, prefix string) {
	full := path.Join(Root, f.Camera, f.Reservation, f.directory(), f.Name)
	return path.Dir(full), path.Base(full)
}

func (f Frame) directory() string {
	if f.Target == "" {
		return Centering
	}
	return f.Target
}

// Parse reads a frame's place from its path relative to Root. A path
// that is no frame of this layout, such as a file in a camera's own
// directory, is an error.
func Parse(p string) (Frame, error) {
	name, fits := strings.CutSuffix(p, Extension)
	parts := strings.Split(name, "/")
	switch {
	case !fits:
		return Frame{}, fmt.Errorf("%s is not a %s file", p, Extension)
	case len(parts) < 4:
		return Frame{}, fmt.Errorf("%s is not inside a target's or %s directory", p, Centering)
	case slices.ContainsFunc(parts, func(part string) bool { return part == "" || part == "." || part == ".." }):
		return Frame{}, fmt.Errorf("%s holds an empty part, '.', or '..'", p)
	case parts[2] == Centering && len(parts) != 4:
		return Frame{}, fmt.Errorf("%s is below %s", p, Centering)
	}
	frame := Frame{Camera: parts[0], Reservation: parts[1], Target: parts[2], Name: strings.Join(parts[3:], "/")}
	if frame.Target == Centering {
		frame.Target = ""
	}
	return frame, nil
}

// TargetGlob answers the pattern of a target's directories in every
// camera's and every reservation's directory, for filepath.Glob. A
// promotion Job walks each directory it matches.
func TargetGlob(target string) string {
	return path.Join(Root, "*", "*", target)
}
