package main

// A Display is one monitor. It is cluster-scoped, like a Node, because
// a monitor belongs to no namespace. The cluster owner sets in spec the
// values the monitor should keep, and another program can set
// spec.override to turn the monitor dark until it removes the override.
// The operator writes all of status. These structs have only the
// fields this operator reads and writes, and deploy/displays.yaml has
// the full schema.

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// The API group is the DRA driver name, so the device attributes and
// the Display resource use one domain.
const (
	DisplayGroup      = DriverName
	DisplayVersion    = "v1alpha1"
	DisplayAPIVersion = DisplayGroup + "/" + DisplayVersion
	DisplaysPath      = "/apis/" + DisplayGroup + "/" + DisplayVersion + "/displays"
)

// The reasons of these conditions are in reasons.go.
const (
	ConnectedCondition  = "Connected"
	ResponsiveCondition = "Responsive"
)

// CompositorServingCondition reports whether the compositor that draws
// on this screen accepts connections and responds.
const CompositorServingCondition = "CompositorServing"

const overrideOff = "Off"

// overrideIsOff reports whether an override field is set. It ignores
// case, because the CRD accepts both "Off" and "off".
func overrideIsOff(value string) bool {
	return strings.EqualFold(value, overrideOff)
}

type Display struct {
	APIVersion string        `json:"apiVersion,omitempty"`
	Kind       string        `json:"kind,omitempty"`
	Metadata   DisplayMeta   `json:"metadata"`
	Spec       DisplaySpec   `json:"spec"`
	Status     DisplayStatus `json:"status,omitempty"`
}

type DisplayList struct {
	Items []Display `json:"items"`
}

type DisplayMeta struct {
	Name            string `json:"name"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	// The operator puts this UID in the involvedObject of its Events,
	// because kubectl describe matches a resource's Events by UID.
	UID string `json:"uid,omitempty"`
	// The API server increments this when spec changes, and not when
	// status changes. status.unconfirmed and status.written record
	// their entries against it.
	Generation int64 `json:"generation,omitempty"`
}

// DisplaySpec is the values the monitor should keep. A control field is
// a pointer because zero is a valid value, and nil means that the
// operator leaves the control alone. Layout is a plain string because
// an empty string is not a valid name, so empty means no Layout.
type DisplaySpec struct {
	Brightness  *int    `json:"brightness,omitempty"`
	Contrast    *int    `json:"contrast,omitempty"`
	Sharpness   *int    `json:"sharpness,omitempty"`
	ColorPreset *string `json:"colorPreset,omitempty"`
	Input       *string `json:"input,omitempty"`
	AudioVolume *int    `json:"audioVolume,omitempty"`
	AudioMute   *bool   `json:"audioMute,omitempty"`
	// Mode is in the format of status.modes. The operator applies it
	// only while no claim holds the screen, because a mode change
	// restarts the compositor, and a claim's mode parameter takes
	// precedence while the claim holds the screen.
	Mode *string `json:"mode,omitempty"`
	// With no Layout name, or a name that matches no Layout, the screen
	// shows every surface fullscreen with the newest on top. For a
	// missing Layout, the LayoutResolved condition is False.
	Layout   string           `json:"layout,omitempty"`
	Override *DisplayOverride `json:"override,omitempty"`
}

// DisplayOverride turns the monitor dark while it is set, and takes
// precedence over the other fields of DisplaySpec.
type DisplayOverride struct {
	Backlight string `json:"backlight,omitempty"`
	Power     string `json:"power,omitempty"`
}

type DisplayStatus struct {
	Node      string `json:"node,omitempty"`
	Connector string `json:"connector,omitempty"`
	// The same identity that the ResourceSlice publishes as device
	// attributes, so a person can tell which monitor a Display is
	// without reading the slice.
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	Serial       string `json:"serial,omitempty"`
	// The panel's size, from its EDID.
	WidthMillimeters  int `json:"widthMillimeters,omitempty"`
	HeightMillimeters int `json:"heightMillimeters,omitempty"`
	// The CEC physical address of the port that this machine's cable
	// is plugged into, read from the EDID. While the connector reports
	// no address, this keeps the last valid one, and the
	// PhysicalAddressCurrent condition says whether the value is
	// current.
	PhysicalAddress string `json:"physicalAddress,omitempty"`
	// Modes is every mode the card offers for this connector. The
	// ResourceSlice truncates its copy of the list to the attribute
	// length limit, so status has the full list. Like Mode.Kernel, it
	// is empty while the operator has no connection to a compositor.
	Mode         *DisplayMode               `json:"mode,omitempty"`
	Modes        []string                   `json:"modes,omitempty"`
	Capabilities map[string]panelCapability `json:"capabilities,omitempty"`
	Observed     *DisplayValues             `json:"observed,omitempty"`
	Captured     *DisplayValues             `json:"captured,omitempty"`
	// Each write that the monitor did not confirm, with the spec
	// generation it was made for. The operator does not repeat such a
	// write until spec changes, and the record is in status so that a
	// restarted operator does not repeat it either.
	Unconfirmed []DisplayUnconfirmed `json:"unconfirmed,omitempty"`
	// How many confirmed writes the operator made of each declared
	// value in this spec generation. The count limits the writes to a
	// monitor that keeps changing a value by itself, and a restart
	// does not reset it.
	Written []DisplayWritten `json:"written,omitempty"`
	// Surfaces are in the order they arrived.
	Surfaces []DisplaySurface `json:"surfaces,omitempty"`
	Layout   *DisplayLayout   `json:"layout,omitempty"`
	// For each claim that has one, the client that arranges the
	// claim's surfaces inside its region (arrangers.go).
	Arrangers  []DisplayArranger  `json:"arrangers,omitempty"`
	Conditions []DisplayCondition `json:"conditions,omitempty"`
}

// DisplaySurface is one window on this screen. Claim is the
// namespace/name of the claim whose socket the window connected
// through, and is empty for a window that belongs to no claim. Labels
// are the labels that all pods of the claim share with equal values,
// and a region's selector matches against them.
//
// An ID is valid only while the compositor that assigned it runs.
// After a compositor restart, the clients reconnect and get new
// surfaces with new IDs.
type DisplaySurface struct {
	ID     string            `json:"id"`
	Claim  string            `json:"claim,omitempty"`
	Pods   []string          `json:"pods,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	Size   *SurfaceSize      `json:"size,omitempty"`
	Region string            `json:"region,omitempty"`
	// The xdg-shell app_id that the program set on the window. An
	// arrangement refers to a window by this value.
	AppID string `json:"appID,omitempty"`
}

// SurfaceSize is the surface's buffer size in pixels, as the compositor
// reports it.
type SurfaceSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// DisplayLayout is the layout this screen is drawn with. Name is the
// Layout in effect, or "default" when there is none. Regions are in
// stacking order, and Surface is the surface on top in each region.
type DisplayLayout struct {
	Name    string          `json:"name,omitempty"`
	Regions []DisplayRegion `json:"regions,omitempty"`
}

type DisplayRegion struct {
	Name    string `json:"name"`
	Surface string `json:"surface,omitempty"`
}

// DisplayMode is the mode this output runs. Kernel is the mode that the
// card sets on the connector, and Weston is the mode that the
// compositor reports in its wl_output events. Clients draw at Weston's
// mode, so when the two differ, clients draw at the wrong size, and the
// operator restarts the compositor.
//
// Both fields are absent while the operator has no connection to a
// compositor. The operator reads the card only while it has one, and a
// Weston mode from before a restart would be a guess.
type DisplayMode struct {
	Kernel string `json:"kernel,omitempty"`
	Weston string `json:"weston,omitempty"`
}

// DisplayValues has one value for each control: the monitor's own
// number for a continuous control, and the published name for the
// others. status.observed and status.captured both use it.
type DisplayValues struct {
	Brightness  *int    `json:"brightness,omitempty"`
	Contrast    *int    `json:"contrast,omitempty"`
	Sharpness   *int    `json:"sharpness,omitempty"`
	ColorPreset *string `json:"colorPreset,omitempty"`
	Input       *string `json:"input,omitempty"`
	AudioVolume *int    `json:"audioVolume,omitempty"`
	AudioMute   *bool   `json:"audioMute,omitempty"`
	Power       *string `json:"power,omitempty"`
}

// The CRD's condition schema has no observedGeneration, so the operator
// leaves it zero. The CRD accepts the empty message that the shared
// type writes for a condition with none.
type DisplayCondition = conditions.Condition

// The operator sets a condition to True or False and never to Unknown,
// because each condition comes from a read that either succeeded or
// failed.
const (
	conditionTrue  = conditions.True
	conditionFalse = conditions.False
)

// override returns the control that spec.override turns off, and false
// when no override is set. The CRD refuses an override that sets both
// fields.
func (s DisplaySpec) override() (string, bool) {
	if s.Override == nil {
		return "", false
	}
	if overrideIsOff(s.Override.Power) {
		return powerControl, true
	}
	if overrideIsOff(s.Override.Backlight) {
		return brightnessControl, true
	}
	return "", false
}

// observedValues converts the raw control values the operator last
// read into the form status publishes, and returns nil when it read
// none.
func observedValues(observed map[byte]uint16) *DisplayValues {
	if len(observed) == 0 {
		return nil
	}
	values := &DisplayValues{}
	for code, raw := range observed {
		values.set(code, raw)
	}
	return values
}

func (v *DisplayValues) set(code byte, raw uint16) {
	switch code {
	case vcpBrightness:
		v.Brightness = numberOf(raw)
	case vcpContrast:
		v.Contrast = numberOf(raw)
	case vcpSharpness:
		v.Sharpness = numberOf(raw)
	case vcpAudioVolume:
		v.AudioVolume = numberOf(raw)
	case vcpColorPreset:
		v.ColorPreset = nameOf(code, raw)
	case vcpInput:
		v.Input = nameOf(code, raw)
	case vcpAudioMute:
		muted := valueName(code, raw) == audioMuted
		v.AudioMute = &muted
	case vcpPowerMode:
		v.Power = nameOf(code, raw)
	}
}

// raw converts one control's value back to the number that is written
// to the monitor, and returns false when the field is unset or has no
// such number.
func (v DisplayValues) raw(code byte) (uint16, bool) {
	switch code {
	case vcpBrightness:
		return numberValue(v.Brightness)
	case vcpContrast:
		return numberValue(v.Contrast)
	case vcpSharpness:
		return numberValue(v.Sharpness)
	case vcpAudioVolume:
		return numberValue(v.AudioVolume)
	case vcpColorPreset:
		return nameValue(code, v.ColorPreset)
	case vcpInput:
		return nameValue(code, v.Input)
	case vcpAudioMute:
		return muteValue(v.AudioMute)
	case vcpPowerMode:
		return nameValue(code, v.Power)
	}
	return 0, false
}

// raw does the same for the values in spec. Spec has no power field,
// so it returns false for power.
func (s DisplaySpec) raw(code byte) (uint16, bool) {
	values := DisplayValues{
		Brightness: s.Brightness, Contrast: s.Contrast, Sharpness: s.Sharpness,
		ColorPreset: s.ColorPreset, Input: s.Input, AudioVolume: s.AudioVolume,
		AudioMute: s.AudioMute,
	}
	if code == vcpPowerMode {
		return 0, false
	}
	return values.raw(code)
}

// empty reports whether no control has a value, so a nil block and a
// block with no values both mean that nothing was captured.
func (v *DisplayValues) empty() bool {
	return v == nil || *v == DisplayValues{}
}

func numberOf(raw uint16) *int { value := int(raw); return &value }

func nameOf(code byte, raw uint16) *string { name := valueName(code, raw); return &name }

func numberValue(value *int) (uint16, bool) {
	if value == nil || *value < 0 || *value > 0xffff {
		return 0, false
	}
	return uint16(*value), true
}

func nameValue(code byte, name *string) (uint16, bool) {
	if name == nil {
		return 0, false
	}
	return valueRaw(code, *name)
}

func muteValue(muted *bool) (uint16, bool) {
	if muted == nil {
		return 0, false
	}
	name := audioUnmuted
	if *muted {
		name = audioMuted
	}
	return valueRaw(vcpAudioMute, name)
}

// setCondition runs conditions.Set on a copy of list. A pass builds the
// new status from the published one and compares the two to decide
// whether to write, so it must not change the published list.
func setCondition(list []DisplayCondition, next DisplayCondition) []DisplayCondition {
	updated := slices.Clone(list)
	conditions.Set(&updated, next)
	return updated
}

func listDisplays(c *apiclient.Client) ([]Display, error) {
	list, err := apiclient.Get[DisplayList](c, DisplaysPath)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// createDisplay creates a Display with an empty spec, so the operator
// writes nothing to the monitor until a person or another program sets
// spec.
func createDisplay(c *apiclient.Client, name string) (*Display, error) {
	display := &Display{
		APIVersion: DisplayAPIVersion,
		Kind:       "Display",
		Metadata:   DisplayMeta{Name: name},
	}
	body, err := json.Marshal(display)
	if err != nil {
		return nil, err
	}
	created := &Display{}
	if err := c.RequestJSON(http.MethodPost, DisplaysPath, body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// openDisplays starts the Display watch and returns its store, which
// the passes read (objectcache.go). The watch covers the whole
// cluster, because a Display is named from the monitor's EDID, so a
// monitor moved to this node keeps its Display.
//
// An update wakes the passes only when it changes the UID, the
// generation, the deletion timestamp, or status.node. Both passes on
// this node write status, so waking on every status write would run
// each pass again after its own write with nothing to do. status.node
// changes when a node adopts the monitor, and the placement pass must
// run then, because it reports a dark screen on each Display whose
// status.node is this node.
func openDisplays(ctx context.Context, client dynamic.Interface, wake func(), readings *metrics) *informer.Collection {
	return wakeWatch(ctx, client, kindDisplay, informer.Source{Resource: displayResource}, displayEdits(wake), wake, readings)
}

// displayEdits returns the Display watch's handler. It wakes on every
// add and delete, and on an update whose editMark differs.
func displayEdits(wake func()) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(any) { wake() },
		UpdateFunc: func(before, after any) {
			was, is := editOf(before), editOf(after)
			if !was.read || !is.read || was != is {
				wake()
			}
		},
		DeleteFunc: func(any) { wake() },
	}
}

// editMark is the fields of an object whose change wakes the passes.
// The informer gives an update handler both the old and the new copy,
// so the handler compares their marks and keeps no state of its own.
// The UID is in the mark because, after a gap in the watch, an object
// that was deleted and created again with the same name arrives as an
// update, and its generation can equal the old one's.
type editMark struct {
	uid        string
	generation int64
	deleting   bool
	node       string
	// read is false when the value is not an object. Such an update
	// wakes the passes, because there is nothing to compare.
	read bool
}

func nodeOf(item *unstructured.Unstructured) string {
	node, _, _ := unstructured.NestedString(item.Object, "status", "node")
	return node
}

func editOf(object any) editMark {
	item, ok := object.(*unstructured.Unstructured)
	if !ok {
		return editMark{}
	}
	return editMark{
		uid:        string(item.GetUID()),
		generation: item.GetGeneration(),
		deleting:   item.GetDeletionTimestamp() != nil,
		node:       nodeOf(item),
		read:       true,
	}
}
