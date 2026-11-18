// Package outbox defines where frames go in a reservation's outbox
// claim. observatory-operator mounts the claim at /outbox in each
// camera's pod. This operator mounts it at the same path in the worker
// pods, which read centering frames, and in the promotion Job, which
// moves frames to the archive. So the path that the runner tells the
// camera driver to write is the same path in every pod.
//
//	/outbox/<Camera>/<Reservation>/<Target>/<name>.fits
//	/outbox/<Camera>/<Reservation>/_centering/<FRAMEID>.fits
//
// The path of a frame gives its camera, reservation, and target, so no
// other record of them is needed. A Kubernetes name cannot contain "_",
// so _centering never collides with a target's directory.
package outbox

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

const (
	Root      = "/outbox"
	Centering = "_centering"
	// Extension is the suffix that the driver adds to each file's name.
	Extension = ".fits"
)

// Frame is the location of a frame in the outbox.
type Frame struct {
	Camera      string
	Reservation string
	// Target is empty for a centering frame.
	Target string
	// Name has no extension. For a light frame it is the name rendered
	// from the target's spec.archive.format, which can contain slashes.
	// For a centering frame it is the FRAMEID.
	Name string
}

// Path returns the frame's path relative to Root.
func (f Frame) Path() string {
	return path.Join(f.Camera, f.Reservation, f.directory(), f.Name) + Extension
}

// Upload returns the UPLOAD_DIR and UPLOAD_PREFIX that the runner
// writes to the camera's UPLOAD_SETTINGS. The INDI driver creates a
// missing directory, and uses a prefix with no placeholder in it as the
// whole file name.
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

// Parse reads a frame's location from its path relative to Root. It
// returns an error for a path that is not a frame of this layout, such
// as a file directly in a camera's directory.
func Parse(p string) (Frame, error) {
	name, fits := strings.CutSuffix(p, Extension)
	parts := strings.Split(name, "/")
	switch {
	case !fits:
		return Frame{}, fmt.Errorf("%q is not a %s file", p, Extension)
	case len(parts) < 4:
		return Frame{}, fmt.Errorf("%q is not inside a target's directory or the %s directory", p, Centering)
	case slices.ContainsFunc(parts, func(part string) bool { return part == "" || part == "." || part == ".." }):
		return Frame{}, fmt.Errorf("%q has an empty, '.', or '..' path element", p)
	case parts[2] == Centering && len(parts) != 4:
		return Frame{}, fmt.Errorf("%q is in a subdirectory of %s, which holds only files", p, Centering)
	}
	frame := Frame{Camera: parts[0], Reservation: parts[1], Target: parts[2], Name: strings.Join(parts[3:], "/")}
	if frame.Target == Centering {
		frame.Target = ""
	}
	return frame, nil
}

// TargetGlob returns the filepath.Glob pattern that matches a target's
// directory under every camera and every reservation.
func TargetGlob(target string) string {
	return path.Join(Root, "*", "*", target)
}
