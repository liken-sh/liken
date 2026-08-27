package main

// The Display resource: one object per monitor, cluster-scoped
// because a panel is physical and belongs to no namespace, like a
// Node. The operator writes the whole of status. The resting spec is
// the cluster owner's declaration of how the panel rests, and
// spec.override is a temporary layer a machine writer sets and later
// lifts. These structs hold only the fields this operator reads and
// writes; the CRD in deploy/displays.yaml is the full schema.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// The API group is the driver's own name, so one domain names
// the driver, the attributes, and this resource.
const (
	DisplayGroup      = DriverName
	DisplayVersion    = "v1alpha1"
	DisplayAPIVersion = DisplayGroup + "/" + DisplayVersion
	DisplaysPath      = "/apis/" + DisplayGroup + "/" + DisplayVersion + "/displays"
)

// The two conditions the operator publishes, and the reason a
// panel that answers no DDC/CI carries.
const (
	ConnectedCondition  = "Connected"
	ResponsiveCondition = "Responsive"
	NoDDCReplyReason    = "NoDDCReply"
)

// The one value each override field takes. The block states
// what the panel is held at, and its absence is what lifts it.
const overrideOff = "off"

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
}

// The settings the panel rests at. Every field is a pointer
// because the absence of a field is what says the operator invents
// nothing, and zero is a value a panel takes.
type DisplaySpec struct {
	Brightness  *int    `json:"brightness,omitempty"`
	Contrast    *int    `json:"contrast,omitempty"`
	Sharpness   *int    `json:"sharpness,omitempty"`
	ColorPreset *string `json:"colorPreset,omitempty"`
	Input       *string `json:"input,omitempty"`
	AudioVolume *int    `json:"audioVolume,omitempty"`
	AudioMute   *bool   `json:"audioMute,omitempty"`
	// The mode the screen rests at, one string in the
	// status.modes form. It is not an override: a temporary mode is
	// what a claim's own mode parameter is. The operator applies it
	// only while no claim holds the screen, because a mode lands
	// through the compositor and a mode change restarts it.
	Mode *string `json:"mode,omitempty"`
	// The panel input this machine's cable occupies, one of
	// status.capabilities.input.values. It is a declared fact and not
	// a request: the operator never writes it to the panel. What it
	// governs is the darkening override, which waits while the panel
	// shows another input, because brightness and power are
	// panel-global and darkening would dim somebody else's picture.
	AttachedInput *string          `json:"attachedInput,omitempty"`
	Override      *DisplayOverride `json:"override,omitempty"`
}

// The temporary layer above the resting one, and the two states
// it carries.
type DisplayOverride struct {
	Backlight string `json:"backlight,omitempty"`
	Power     string `json:"power,omitempty"`
}

type DisplayStatus struct {
	Node      string `json:"node,omitempty"`
	Connector string `json:"connector,omitempty"`
	// The monitor's own identity, the same three facts the
	// slice publishes as attributes, so a person reading the resource
	// knows which screen it is without crossing to the slice.
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	Serial       string `json:"serial,omitempty"`
	// The panel's physical size, as the monitor states it.
	WidthMillimeters  int `json:"widthMillimeters,omitempty"`
	HeightMillimeters int `json:"heightMillimeters,omitempty"`
	// The mode the output drives now, absent while it drives
	// nothing, and every mode the card offers for this connector.
	// Status has no attribute-length limit, so this list is whole
	// where the slice's is cut to fit.
	// The input this machine's cable occupies, as the operator
	// derived it from the EDID's physical address. A declaration in
	// spec.attachedInput wins over it, and this field always reports
	// what was derived, so a person can check it against the cabling.
	AttachedInput string                     `json:"attachedInput,omitempty"`
	CurrentMode   string                     `json:"currentMode,omitempty"`
	Modes         []string                   `json:"modes,omitempty"`
	Capabilities  map[string]panelCapability `json:"capabilities,omitempty"`
	Observed      *DisplayValues             `json:"observed,omitempty"`
	Captured      *DisplayValues             `json:"captured,omitempty"`
	Conditions    []DisplayCondition         `json:"conditions,omitempty"`
}

// One value of each control, in the panel's own numbers for the
// continuous controls and in the published names for the others. Both
// observed and captured carry this shape, so a captured value reads
// the same as the observed value it was taken from.
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

// The standard condition shape, held here for the reason the
// slice structs are held here: this program writes these fields and no
// others.
type DisplayCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime"`
}

// The two states a condition takes here. Unknown is never
// written: the operator either read the panel or it did not.
const (
	conditionTrue  = "True"
	conditionFalse = "False"
)

// Whether the override holds the panel dark, and by which of
// the two states. Power wins when a writer states both, because a
// panel that is off is dark either way.
func (s DisplaySpec) override() (string, bool) {
	if s.Override == nil {
		return "", false
	}
	if s.Override.Power == overrideOff {
		return powerControl, true
	}
	if s.Override.Backlight == overrideOff {
		return brightnessControl, true
	}
	return "", false
}

// The values the operator last saw, as the resource publishes
// them. Nothing observed publishes nothing.
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

// One control's value written into a values block, in the shape
// that control publishes.
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

// One control's value out of a values block, as the number the
// wire carries. This is the direction a restore reads: the captured
// value goes back to the panel it came from.
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

// The same read against the resting declaration. The two blocks
// hold the same controls apart from power, which no spec declares: a
// resting power would fight the override that turns the panel off.
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

// Whether a values block states anything at all. A block that
// states nothing is cleared rather than published empty.
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

// The condition with this type replaced, and the timestamp kept
// when nothing about it changed. A timestamp that moved on every pass
// would make every pass a write.
func setCondition(conditions []DisplayCondition, next DisplayCondition) []DisplayCondition {
	for index, current := range conditions {
		if current.Type != next.Type {
			continue
		}
		if current.Status == next.Status && current.Reason == next.Reason && current.Message == next.Message {
			return conditions
		}
		if current.Status == next.Status {
			next.LastTransitionTime = current.LastTransitionTime
		}
		updated := make([]DisplayCondition, len(conditions))
		copy(updated, conditions)
		updated[index] = next
		return updated
	}
	return append(conditions, next)
}

func getDisplay(c *Client, name string) (*Display, error) {
	return get[Display](c, DisplaysPath+"/"+name)
}

func listDisplays(c *Client) ([]Display, error) {
	list, err := get[DisplayList](c, DisplaysPath)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// The create carries an empty spec. The operator states nothing
// about how a panel should rest: the resource exists so a person or a
// machine writer can, and an empty spec writes nothing to the wire.
func createDisplay(c *Client, name string) (*Display, error) {
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

// The status write goes to the status subresource, so a spec a
// person edited between the read and the write is not overwritten.
func writeDisplayStatus(c *Client, display *Display, status DisplayStatus) (*Display, error) {
	written := *display
	written.APIVersion = DisplayAPIVersion
	written.Kind = "Display"
	written.Status = status
	body, err := json.Marshal(&written)
	if err != nil {
		return nil, err
	}
	updated := &Display{}
	path := DisplaysPath + "/" + display.Metadata.Name + "/status"
	if err := c.RequestJSON(http.MethodPut, path, body, updated); err != nil {
		return nil, err
	}
	return updated, nil
}

// How long one watch connection lives before the API server
// closes it and the operator opens another. A watch that never ends
// holds a connection through every network fault in between.
const displayWatchTimeout = 290 * time.Second

// How long the operator waits before it opens the watch again.
const displayWatchRetry = 5 * time.Second

// The watch turns a spec that changed into one wake. Nothing of
// the event is read but its arrival: the pass that follows reads every
// Display again, the same way every other wake in this operator works.
func watchDisplays(ctx context.Context, c *Client, wake func()) {
	for ctx.Err() == nil {
		if err := streamDisplays(ctx, c, wake); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "watching displays: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(displayWatchRetry):
		}
	}
}

// One watch connection. It starts at the present, because an
// event carries nothing the pass uses and a missed event costs one
// backstop tick.
func streamDisplays(ctx context.Context, c *Client, wake func()) error {
	path := fmt.Sprintf("%s?watch=true&timeoutSeconds=%d", DisplaysPath, int(displayWatchTimeout.Seconds()))
	body, err := c.Watch(ctx, path)
	if err != nil {
		return err
	}
	defer drain(body)

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type string `json:"type"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		wake()
	}
}
