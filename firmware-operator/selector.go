package main

// spec.selector is a CEL expression with the variables of a
// DeviceClass selector. The operator compiles it with the CEL
// environment of Kubernetes' dynamic-resource-allocation module, the
// same one the scheduler uses, so a selector means in a Firmware what
// it means in a DeviceClass.

import (
	"context"
	"fmt"
	"os"

	"k8s.io/dynamic-resource-allocation/cel"
)

// selectors holds each compiled selector by its expression. A compile
// takes milliseconds, and every pass evaluates every rule, so the pass
// compiles an expression once. forget drops the expressions that no
// rule holds any more, so the cache grows only with the rules.
type selectors struct {
	compiled map[string]cel.CompilationResult
}

func newSelectors() *selectors {
	return &selectors{compiled: map[string]cel.CompilationResult{}}
}

// compile answers the compiled selector, or the compile error.
func (s *selectors) compile(expression string) (cel.CompilationResult, error) {
	result, held := s.compiled[expression]
	if !held {
		result = cel.GetCompiler(cel.Features{}).CompileCELExpression(expression, cel.Options{})
		s.compiled[expression] = result
	}
	if result.Error != nil {
		return result, result.Error
	}
	return result, nil
}

// forget keeps only the expressions in use.
func (s *selectors) forget(inUse map[string]bool) {
	for expression := range s.compiled {
		if !inUse[expression] {
			delete(s.compiled, expression)
		}
	}
}

// matches evaluates a compiled selector against one device. An
// evaluation that fails, such as a selector that reads an attribute the
// device does not have without has(), counts as no match for that
// device, the way the scheduler counts it, and the log names the
// failure.
func matches(ctx context.Context, rule string, selector cel.CompilationResult, d device) bool {
	matched, _, err := selector.DeviceMatches(ctx, cel.Device{Driver: driverName, Attributes: d.attributes})
	if err != nil {
		fmt.Fprintf(os.Stderr, "firmware-operator: the selector of the Firmware %s failed on %s: %v\n", rule, d.port(), err)
		return false
	}
	return matched
}
