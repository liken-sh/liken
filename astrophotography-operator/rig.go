package main

// The runner reads only what observatory-operator publishes on a
// Reservation and its devices. The Reservation's status.endpoint is the
// telescope's INDI server, its status.session lists the devices of the
// session and marks the guide camera, and each device's
// status.indiDevice is the device's name on the INDI server. The
// session does not change while the Reservation is Ready, so an edit of
// a device during the night takes effect in the next session.

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

// errNotServed is the error for a device whose status.indiDevice is
// empty, because the INDI server does not list the device yet.
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

// imagingCameras returns the cameras of a session that do not guide,
// sorted by key. A target with an optical train is imaged by that
// train's camera. A target with no train can be imaged only when the
// session has exactly one imaging camera.
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

// cameraFor returns the camera that images a target in a session, and
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

// sessionDevice returns the name of the session's device of one kind
// on the given optical train, or on no train when train is "". When
// there are several, it returns the first by name, and when there are
// none, it returns false.
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

// indiAddress returns the address of a Ready reservation's INDI server
// as host:port.
func indiAddress(r *observatory.Reservation) (string, error) {
	e := r.Status.Endpoint
	if e == nil || e.Host == "" {
		return "", fmt.Errorf("reservation %s has no status.endpoint", r.Metadata.Name)
	}
	return net.JoinHostPort(e.Host, strconv.Itoa(int(e.Port))), nil
}

// indiDevice reads a device's name on the INDI server from the status
// of the device's object. served selects the status field.
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

// longitudeOf returns the longitude of the session's observatory in
// degrees east, which sets the date of {night} in a frame's name. When
// the store has no such Observatory it returns 0, so the name still
// renders.
func longitudeOf(o *operator, r *observatory.Reservation) float64 {
	view := o.stores.collections[observatory.ObservatoryKind.Name].View()
	site, ok := informer.Cached[observatory.Observatory](view, r.Metadata.Namespace+"/"+r.Status.Session.Observatory)
	if !ok {
		return 0
	}
	return site.Spec.Location.Longitude
}
