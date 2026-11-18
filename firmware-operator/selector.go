package main

// spec.selector is a CEL expression with the variables of a DeviceClass
// selector. The operator compiles it with the CEL environment of the
// k8s.io/dynamic-resource-allocation module, which the scheduler also
// uses, so a selector matches the same devices in a Firmware as in a
// DeviceClass.

import (
	"context"
	"fmt"
	"os"

	"k8s.io/dynamic-resource-allocation/cel"
)

// selectors caches compiled selectors by expression text, because every
// pass evaluates every Firmware and compiling costs much more than
// evaluating.
type selectors struct {
	compiled map[string]cel.CompilationResult
}

func newSelectors() *selectors {
	return &selectors{compiled: map[string]cel.CompilationResult{}}
}

// compile caches a failed compile too, and returns its error each time.
func (s *selectors) compile(expression string) (cel.CompilationResult, error) {
	result, cached := s.compiled[expression]
	if !cached {
		result = cel.GetCompiler(cel.Features{}).CompileCELExpression(expression, cel.Options{})
		s.compiled[expression] = result
	}
	if result.Error != nil {
		return result, result.Error
	}
	return result, nil
}

func (s *selectors) forget(inUse map[string]bool) {
	for expression := range s.compiled {
		if !inUse[expression] {
			delete(s.compiled, expression)
		}
	}
}

// matches returns false and logs the error when the evaluation fails,
// as it does for a selector that reads an attribute the device lacks
// without has(). The scheduler fails the whole allocation on such an
// error instead.
func matches(ctx context.Context, rule string, selector cel.CompilationResult, d device) bool {
	matched, _, err := selector.DeviceMatches(ctx, cel.Device{Driver: driverName, Attributes: d.attributes})
	if err != nil {
		fmt.Fprintf(os.Stderr, "firmware-operator: evaluating the selector of the Firmware %s on %s returned an error: %v\n", rule, d.port(), err)
		return false
	}
	return matched
}
