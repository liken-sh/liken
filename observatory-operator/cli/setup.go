package main

// The set-up and take-down verbs. A person confirms that they set a
// resource up, such as a portable telescope carried out and polar
// aligned, by writing the SetUp condition into the resource's status,
// and a spec.requires that names SetUp holds a reservation until then.
// The CLI shares no code with the operator, so it names the group, the
// kinds, and the fields it writes here.
//
// The write is a server-side apply to the status subresource. The CRD
// lists the conditions as a map keyed by type, so the apply touches
// the SetUp condition and status.setUpUntil and nothing the operator
// writes, and the API server records the CLI as their manager. The
// operator's status writer keeps both as they are, until SetUp
// expires.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const (
	setUpVerb    = "set-up"
	takeDownVerb = "take-down"

	group      = "observatory.liken.sh"
	apiVersion = group + "/v1alpha1"

	// fieldManager names the CLI in the managedFields of the status it
	// writes.
	fieldManager = "kubectl-liken-observatory"

	// The condition and its reasons, as the operator's observatory
	// package names them.
	conditionSetUp  = "SetUp"
	reasonSetUp     = "SetUp"
	reasonTakenDown = "TakenDown"
)

// A kind that a person can set up: each kind with procedures, whose
// spec.requires can name SetUp, and whose status has setUpUntil.
type confirmable struct {
	kind, plural string
}

// confirmables are the kinds the verbs accept, in the order of the
// operator's tree.
var confirmables = []confirmable{
	{"Observatory", "observatories"},
	{"Telescope", "telescopes"},
	{"Mount", "mounts"},
	{"Camera", "cameras"},
	{"FilterWheel", "filterwheels"},
	{"Focuser", "focusers"},
	{"Rotator", "rotators"},
	{"DustCap", "dustcaps"},
	{"FlatPanel", "flatpanels"},
	{"PolarAligner", "polaraligners"},
	{"GPS", "gpses"},
	{"Dome", "domes"},
	{"WeatherStation", "weatherstations"},
	{"Switch", "switches"},
	{"Receiver", "receivers"},
}

// kindNamed finds a kind the way kubectl does: by its name, its
// singular, or its plural, in any case.
func kindNamed(word string) (confirmable, error) {
	lower := strings.ToLower(word)
	for _, c := range confirmables {
		if lower == strings.ToLower(c.kind) || lower == c.plural {
			return c, nil
		}
	}
	return confirmable{}, fmt.Errorf("%q is not a kind that can be set up; use one of %s", word, strings.Join(kindWords(), ", "))
}

// kindWords lists the kinds as a person types them.
func kindWords() []string {
	var words []string
	for _, c := range confirmables {
		words = append(words, strings.ToLower(c.kind))
	}
	return words
}

func (c confirmable) resource() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: group, Version: "v1alpha1", Resource: c.plural}
}

// The verbs' flags and their two positional arguments.
type setUpOptions struct {
	// Up is true for set-up and false for take-down.
	Up         bool
	Kind, Name string
	Until, By  string
	Force      bool
	Now        time.Time
}

// parseUntil reads --until as a time in RFC 3339, or as a duration
// from now, and answers nil when it is empty.
func parseUntil(text string, now time.Time) (*time.Time, error) {
	if text == "" {
		return nil, nil
	}
	if at, err := time.Parse(time.RFC3339, text); err == nil {
		at = at.UTC()
		return &at, nil
	}
	if d, err := time.ParseDuration(text); err == nil && d > 0 {
		at := now.Add(d).UTC().Truncate(time.Second)
		return &at, nil
	}
	return nil, fmt.Errorf("--until %q is neither a time such as 2026-10-13T04:30:00Z nor a duration such as 6h", text)
}

// setUpCondition answers the condition a verb writes. The transition
// time moves when the status or the reason changes, so a take-down
// after the operator expired the condition counts as the person's
// answer to a notify action, and a second set-up in one night keeps
// the time of the first, and so its dawn.
func setUpCondition(up bool, who string, held map[string]any, now time.Time) map[string]any {
	status, reason, message := "True", reasonSetUp, "Set up by "+who
	if !up {
		status, reason, message = "False", reasonTakenDown, "Taken down by "+who
	}
	since := now.UTC().Truncate(time.Second).Format(time.RFC3339)
	if held["status"] == status && held["reason"] == reason {
		if was, ok := held["lastTransitionTime"].(string); ok {
			since = was
		}
	}
	return map[string]any{
		"type": conditionSetUp, "status": status, "reason": reason, "message": message,
		"lastTransitionTime": since,
	}
}

// setUpApply answers the object the verb applies: the SetUp condition,
// and status.setUpUntil when the person gave one. A later apply with no
// until removes the one an earlier apply wrote, because the CLI is its
// manager.
func setUpApply(c confirmable, namespace, name string, condition map[string]any, until *time.Time) *unstructured.Unstructured {
	status := map[string]any{"conditions": []any{condition}}
	if until != nil {
		status["setUpUntil"] = until.Format(time.RFC3339)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       c.kind,
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"status":     status,
	}}
}

// heldSetUp answers the SetUp condition that the resource holds now,
// or nil.
func heldSetUp(object *unstructured.Unstructured) map[string]any {
	list, _, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
	for _, item := range list {
		if c, ok := item.(map[string]any); ok && c["type"] == conditionSetUp {
			return c
		}
	}
	return nil
}

// runSetUp checks the arguments, builds the clients, and writes the
// condition.
func runSetUp(ctx context.Context, getter genericclioptions.RESTClientGetter, opts setUpOptions, stdout, stderr io.Writer) error {
	verb := takeDownVerb
	if opts.Up {
		verb = setUpVerb
	}
	if opts.Kind == "" || opts.Name == "" {
		return fmt.Errorf("%s needs a kind and a name, such as %s telescope east", verb, verb)
	}
	kind, err := kindNamed(opts.Kind)
	if err != nil {
		return err
	}
	until, err := parseUntil(opts.Until, opts.Now)
	if err != nil {
		return err
	}
	if !opts.Up && until != nil {
		return fmt.Errorf("--until applies to %s, not to %s", setUpVerb, takeDownVerb)
	}
	c, err := observatoryClients(getter)
	if err != nil {
		return err
	}
	return writeSetUp(ctx, c, kind, opts, until, stdout, stderr)
}

// writeSetUp reads the operator's version and warns or refuses on
// drift, reads the resource for the condition it holds, and applies
// the new one.
func writeSetUp(ctx context.Context, c clients, kind confirmable, opts setUpOptions, until *time.Time, stdout, stderr io.Writer) error {
	operator, err := operatorVersion(ctx, c.typed)
	if err != nil {
		fmt.Fprintf(stderr, "reading the operator version: %v\n", err)
	}
	switch action, message := decideVersionAction(version, operator, ""); action {
	case actionRefuse:
		return fmt.Errorf("%s", message)
	case actionWarn:
		if !opts.Force {
			fmt.Fprintln(stderr, message)
		}
	}

	resources := c.dynamic.Resource(kind.resource()).Namespace(c.namespace)
	object, err := resources.Get(ctx, opts.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	condition := setUpCondition(opts.Up, person(ctx, c, opts.By), heldSetUp(object), opts.Now)
	// The apply forces, because the operator's write of an expiry
	// makes it a manager of the condition's fields, and the person's
	// word replaces it.
	if _, err := resources.Apply(ctx, opts.Name, setUpApply(kind, c.namespace, opts.Name, condition, until),
		metav1.ApplyOptions{FieldManager: fieldManager, Force: true}, "status"); err != nil {
		return err
	}
	switch {
	case !opts.Up:
		fmt.Fprintf(stdout, "%s %s is taken down\n", kind.kind, opts.Name)
	case until != nil:
		fmt.Fprintf(stdout, "%s %s is set up until %s\n", kind.kind, opts.Name, until.Format(time.RFC3339))
	default:
		fmt.Fprintf(stdout, "%s %s is set up until the next astronomical dawn at its Observatory\n", kind.kind, opts.Name)
	}
	return nil
}
