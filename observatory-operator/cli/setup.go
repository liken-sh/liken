package main

// The set-up and take-down verbs write the SetUp condition into a
// resource's status, so a person can say that a portable telescope was
// carried out and polar aligned. A reservation whose resources list
// SetUp in spec.requires waits for it. The CLI shares no code with the
// operator, so this file repeats the API group, the kinds, and the
// fields it writes.
//
// The write is a server-side apply to the status subresource. The CRD
// lists conditions as a map keyed by type, so the apply changes only
// the SetUp condition and status.setUpUntil. The operator's status
// writer leaves both as they are until SetUp expires.

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

	fieldManager = "kubectl-liken-observatory"

	// These match the operator's observatory package.
	conditionSetUp  = "SetUp"
	reasonSetUp     = "SetUp"
	reasonTakenDown = "TakenDown"
)

// confirmable is a kind that a person can set up: a kind with
// procedures, whose status has setUpUntil.
type confirmable struct {
	kind, plural string
}

// confirmables lists the kinds the verbs accept, in the order of the
// operator's kinds, which a test checks.
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

// kindNamed finds a kind the way kubectl does, by its singular name
// or its plural, in any letter case.
func kindNamed(word string) (confirmable, error) {
	lower := strings.ToLower(word)
	for _, c := range confirmables {
		if lower == strings.ToLower(c.kind) || lower == c.plural {
			return c, nil
		}
	}
	return confirmable{}, fmt.Errorf("%q is not a kind that can be set up, the kinds are %s", word, strings.Join(kindWords(), ", "))
}

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

type setUpOptions struct {
	// Up is true for set-up and false for take-down.
	Up         bool
	Kind, Name string
	Until, By  string
	Force      bool
	Now        time.Time
}

// parseUntil reads --until as an RFC 3339 time or as a duration from
// now. It returns nil when text is empty.
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

// setUpCondition returns the condition a verb writes. It keeps the
// current lastTransitionTime unless the status or the reason changes. The
// operator expires SetUp at the first dawn after that time, so a second
// set-up in one night does not extend it. A take-down after the
// operator expired SetUp changes the reason, so it gets a new time, and
// the operator reads that as the person's answer to a notify action.
func setUpCondition(up bool, who string, current map[string]any, now time.Time) map[string]any {
	status, reason, message := "True", reasonSetUp, "Set up by "+who
	if !up {
		status, reason, message = "False", reasonTakenDown, "Taken down by "+who
	}
	since := now.UTC().Truncate(time.Second).Format(time.RFC3339)
	if current["status"] == status && current["reason"] == reason {
		if was, ok := current["lastTransitionTime"].(string); ok {
			since = was
		}
	}
	return map[string]any{
		"type": conditionSetUp, "status": status, "reason": reason, "message": message,
		"lastTransitionTime": since,
	}
}

// setUpApply returns the object the verb applies. An apply without
// --until removes the setUpUntil that an earlier apply wrote, because
// the CLI manages that field.
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

func currentSetUp(object *unstructured.Unstructured) map[string]any {
	list, _, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
	for _, item := range list {
		if c, ok := item.(map[string]any); ok && c["type"] == conditionSetUp {
			return c
		}
	}
	return nil
}

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

// writeSetUp warns or refuses when the operator's version differs from
// the CLI's, then applies the new SetUp condition.
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
	condition := setUpCondition(opts.Up, person(ctx, c, opts.By), currentSetUp(object), opts.Now)
	// Force takes the condition's fields back from the operator, which
	// manages them after it writes an expiry.
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
