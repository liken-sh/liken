package main

// What the runner reads from observatory-operator (plan 01). It reads
// only what a Reservation and its devices publish: the reservation's
// status.endpoint is the telescope's INDI server, status.session names
// each device of the session and marks the guide camera, and each
// device's status.indiDevice is its name on that server. The session is
// a record that does not change while the reservation is Ready, so a
// person's edit of a device during the night reaches the next session,
// not this one.

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// errNotServed is the error of a device whose driver does not define
// it on the server yet: its status.indiDevice is empty. The runner
// waits for the device's next change.
var errNotServed = errors.New("not served on the INDI server yet")

// camera is the imaging camera of one optical train.
type camera struct {
	// name is the Camera's name: the directory of its frames in the
	// outbox, /outbox/<name>, which observatory-operator sets.
	name string
	// key is the camera's key in status.session, such as
	// Camera/east-main.
	key   string
	train string
}

// imagingCameras answers the cameras of a session that do not guide,
// sorted by key. A target with a train is imaged by its train's camera,
// and a target with no train only when the session has exactly one.
func imagingCameras(session *observatory.Session) []camera {
	var out []camera
	for key, d := range session.Devices {
		name, isCamera := strings.CutPrefix(key, observatory.CameraKind.Name+"/")
		if isCamera && !d.Guides {
			out = append(out, camera{name: name, key: key, train: d.OpticalTrain})
		}
	}
	slices.SortFunc(out, func(a, b camera) int { return strings.Compare(a.key, b.key) })
	return out
}

// cameraFor answers the camera that images a target in a session, and
// false when the session cannot image it.
func cameraFor(session *observatory.Session, t *astrophotography.Target) (camera, bool) {
	cameras := imagingCameras(session)
	if t.Spec.OpticalTrain == "" {
		if len(cameras) != 1 {
			return camera{}, false
		}
		return cameras[0], true
	}
	for _, c := range cameras {
		if c.train == t.Spec.OpticalTrain {
			return c, true
		}
	}
	return camera{}, false
}

// sessionDevice answers the name of a session's device of one kind,
// on one train or on none, sorted first by name, and false when the
// session has none.
func sessionDevice(session *observatory.Session, kind observatory.Kind, train string) (string, bool) {
	var names []string
	for key, d := range session.Devices {
		if name, ok := strings.CutPrefix(key, kind.Name+"/"); ok && d.OpticalTrain == train {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", false
	}
	slices.Sort(names)
	return names[0], true
}

// indiAddress answers the INDI server of a Ready reservation, as
// host:port.
func indiAddress(r *observatory.Reservation) (string, error) {
	e := r.Status.Endpoint
	if e == nil || e.Host == "" {
		return "", fmt.Errorf("reservation %s has no status.endpoint", r.Metadata.Name)
	}
	return net.JoinHostPort(e.Host, strconv.Itoa(int(e.Port))), nil
}

// indiDevice reads a device's name on the INDI server from its status.
func indiDevice[T any, P informer.Object[T]](o *operator, kind observatory.Kind, namespace, name string, served func(*T) string) (string, error) {
	key := namespace + "/" + name
	view := o.stores.collections[kind.Name].View()
	device, err := informer.ReadOne[T, P](o.client, informer.Held{View: view}, key, kind.Path(namespace)+"/"+name)
	if err != nil {
		return "", fmt.Errorf("reading %s %s: %w", kind.Name, name, err)
	}
	if served(device) == "" {
		return "", fmt.Errorf("%s %s is %w", kind.Name, name, errNotServed)
	}
	return served(device), nil
}

func cameraDevice(o *operator, namespace, name string) (string, error) {
	return indiDevice[observatory.Camera](o, observatory.CameraKind, namespace, name,
		func(c *observatory.Camera) string { return c.Status.IndiDevice })
}

func wheelDevice(o *operator, namespace, name string) (string, error) {
	return indiDevice[observatory.FilterWheel](o, observatory.FilterWheelKind, namespace, name,
		func(w *observatory.FilterWheel) string { return w.Status.IndiDevice })
}

func mountDevice(o *operator, namespace, name string) (string, error) {
	return indiDevice[observatory.Mount](o, observatory.MountKind, namespace, name,
		func(m *observatory.Mount) string { return m.Status.IndiDevice })
}

// longitudeOf answers the longitude of the session's observatory, in
// degrees east, for the {night} of a frame's name. An observatory the
// store does not hold gives 0, Greenwich, so a name still renders.
func longitudeOf(o *operator, r *observatory.Reservation) float64 {
	view := o.stores.collections[observatory.ObservatoryKind.Name].View()
	site, ok := informer.Cached[observatory.Observatory](view, r.Metadata.Namespace+"/"+r.Status.Session.Observatory)
	if !ok {
		return 0
	}
	return site.Spec.Location.Longitude
}
