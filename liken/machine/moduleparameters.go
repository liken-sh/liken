package machine

// A module parameter is a setting a driver reads once, when the
// kernel loads it, and never again. The spec declares one as a
// "<module>.<parameter>" key, which is the kernel's own spelling:
// the same form the kernel command line takes and the same path
// /sys/module/<module>/parameters/<parameter> mirrors, so a person
// who knows the parameter can find the declaration by its name. The
// functions here turn that flat map into what each consumer needs:
// the string finit_module takes, the names to read back, and the
// modules the map mentions.

import (
	"maps"
	"slices"
	"strings"
)

// splitModuleParameterKey splits the dotted key into its module and
// parameter halves. Admission's pattern allows exactly one dot, so
// the first dot is the split; a key that reaches here another way,
// for example from a hand-written manifest on a stick, still splits
// or is rejected as no key at all.
func splitModuleParameterKey(key string) (module, parameter string, ok bool) {
	module, parameter, ok = strings.Cut(key, ".")
	if !ok || module == "" || parameter == "" {
		return "", "", false
	}
	return module, parameter, true
}

// ModuleParameterString builds the string finit_module takes for one
// module: the parameter=value pairs whose keys name it, sorted by
// parameter name and joined with spaces. The sort makes the string
// stable, so the same spec produces the same boot record on every
// boot and the drift comparison stays a string comparison.
func ModuleParameterString(module string, parameters map[string]string) string {
	var pairs []string
	for _, key := range slices.Sorted(maps.Keys(parameters)) {
		name, parameter, ok := splitModuleParameterKey(key)
		if !ok || name != module {
			continue
		}
		pairs = append(pairs, parameter+"="+parameters[key])
	}
	return strings.Join(pairs, " ")
}

// ModuleParameterNames lists the parameter names declared for one
// module, sorted, which is the list the readback walks under
// /sys/module/<module>/parameters after the load.
func ModuleParameterNames(module string, parameters map[string]string) []string {
	var names []string
	for _, key := range slices.Sorted(maps.Keys(parameters)) {
		name, parameter, ok := splitModuleParameterKey(key)
		if !ok || name != module {
			continue
		}
		names = append(names, parameter)
	}
	return names
}

// ModuleParameterModules lists the modules the key set mentions,
// sorted, in the keys' own exact spelling. Admission has no
// normalizer, and module names take - and _ interchangeably, so a
// key must spell its module the way spec.modules spells it; the CEL
// rule on the spec states that requirement and this function does
// not soften it.
func ModuleParameterModules(parameters map[string]string) []string {
	names := map[string]bool{}
	for key := range parameters {
		if module, _, ok := splitModuleParameterKey(key); ok {
			names[module] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}
