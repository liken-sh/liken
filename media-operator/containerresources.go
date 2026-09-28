package main

// This file holds the cpu and memory settings of every container the
// operator builds: the player, the display, and the command sidecar of
// a playback pod, the idle client, and the reader of a Remote's pod.
//
// A container with no request is in the `BestEffort` QoS class, and
// under memory pressure the kubelet evicts `BestEffort` pods first, so
// the film a person watches would be the first pod to go. Each container
// states a cpu request, a memory request, and a memory limit, so every
// pod is `Burstable`.
//
// The requests are near the steady use of a 1920x1080 screen, measured
// on a home cluster, because the scheduler subtracts each request from
// the machine's allocatable memory. A request sized for a 3840x2160
// screen keeps a `Play` `Pending` on a 1GB machine, which is the only
// machine that has its screen. The memory limits are above the highest
// use measured on a 3840x2160 screen, with about half again as headroom,
// so a 3840x2160 film is not killed at the defaults. No container
// states a cpu limit, because a cpu limit throttles the decoder, and
// the result is dropped frames.
//
// A cluster owner changes the values without a build. The kustomize
// base generates the ConfigMap media-operator-resources from
// deploy/container-resources.yaml, and the operator reads the file once
// at start. The generator adds a hash of the content to the ConfigMap's
// name, so a changed value rolls the operator's pod, and the operator
// needs no watch on the file. A value that is missing or does not parse
// takes its default, with one log line, and the operator still starts.

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"
)

// containerResourcesPath is where the Deployment mounts the generated
// ConfigMap.
const containerResourcesPath = "/etc/media-operator/resources.yaml"

// containerResources is one container's settings, each a Kubernetes
// quantity.
type containerResources struct {
	CPU         string
	Memory      string
	MemoryLimit string
}

// resourceSettings maps a container's name to its settings. A nil map
// gives every container its default.
type resourceSettings map[string]containerResources

// ResourceList is a Kubernetes quantity for each resource name, such as
// "cpu" and "memory".
type ResourceList map[string]string

// defaultResourceSettings are the values the operator uses when the file
// does not state them, and deploy/container-resources.yaml states the
// same values. The measurements come from a home cluster over eight days
// of films. Working set is the number the kubelet ranks eviction by, and
// cpu is the rate over five minutes.
func defaultResourceSettings() resourceSettings {
	return resourceSettings{
		// mpv decodes the film and holds its demuxer cache. On a 1920x1080
		// screen it measured 431Mi of working set and 83m of cpu at the
		// 90th percentile. On a 3840x2160 screen its highest working set
		// was 617Mi.
		playerContainer: {CPU: "80m", Memory: "432Mi", MemoryLimit: "1Gi"},
		// The display's working set is mostly shared memory for the
		// buffers of its surface, so it grows with the screen. On a
		// 1920x1080 screen it measured 141Mi and 6m of cpu at the 90th
		// percentile. On a 3840x2160 screen its highest working set was
		// 416Mi.
		displayContainer: {CPU: "10m", Memory: "144Mi", MemoryLimit: "640Mi"},
		// The command sidecar reads mpv's socket and the bus. It measured
		// 11Mi and 9m at the 90th percentile, and 13Mi at most, on
		// either screen. It runs the pod build, which links client-go's
		// reflector for the api's watches, and which starts about 4MB
		// larger than the measured build: 0.5MB of it heap, the rest
		// the pages of a larger binary. The request is the p90 with
		// that growth and some room, and the limit is twice the
		// request.
		commandContainer: {CPU: "10m", Memory: "16Mi", MemoryLimit: "32Mi"},
		// The idle client draws the idle screen while nothing plays. It
		// measured 95Mi and 1m on a 3840x1600 test screen, over one
		// minute of samples. The cpu request is rounded up from that one
		// sample, and the limit allows for a larger screen.
		idleContainer: {CPU: "5m", Memory: "96Mi", MemoryLimit: "256Mi"},
		// The reader waits on a controller's input nodes. It measured
		// 3.4Mi at the 90th percentile and 4.8Mi at most, and less than
		// 1m of cpu. It runs the pod build too.
		remoteReaderContainer: {CPU: "1m", Memory: "4Mi", MemoryLimit: "16Mi"},
	}
}

// loadResourceSettings reads the mounted file. A file that is absent
// gives every default, so the operator runs with no ConfigMap mounted.
func loadResourceSettings(path string, report func(string)) resourceSettings {
	data, err := os.ReadFile(path)
	if err != nil {
		report(fmt.Sprintf("media.liken.sh: container resources: %v; using every default", err))
		return defaultResourceSettings()
	}
	return parseResourceSettings(data, report)
}

// parseResourceSettings reads each value from the file and falls back to
// its default one value at a time, so one bad value costs that value
// alone. Each fallback reports one line. The file follows a container's
// resources block, one entry per container name:
//
//	player:
//	  requests: {cpu: 80m, memory: 432Mi}
//	  limits: {memory: 1Gi}
//
// The file is read as plain YAML values and not into a Go struct,
// because a struct decode fails the whole file on one entry of the wrong
// shape, and every other container would lose its values with it.
func parseResourceSettings(data []byte, report func(string)) resourceSettings {
	defaults := defaultResourceSettings()
	file := map[string]any{}
	if err := yaml.Unmarshal(data, &file); err != nil {
		report(fmt.Sprintf("media.liken.sh: container resources: the file does not parse: %v; using every default", err))
		return defaults
	}
	say := func(container, message string) {
		report(fmt.Sprintf("media.liken.sh: container resources: %s: %s", container, message))
	}

	settings := resourceSettings{}
	for _, name := range slices.Sorted(maps.Keys(defaults)) {
		fallback := defaults[name]
		entry, isMap := mapping(file[name])
		if !isMap {
			say(name, "is not a map of requests and limits; using its defaults")
			settings[name] = fallback
			continue
		}
		requests, requestsOK := mapping(entry["requests"])
		if !requestsOK {
			say(name, "requests is not a map; using the default requests")
		}
		limits, limitsOK := mapping(entry["limits"])
		if !limitsOK {
			say(name, "limits is not a map; using the default limits")
		}
		value := func(field string, values map[string]any, valid bool, key, defaultValue string) string {
			if !valid {
				return defaultValue
			}
			raw, present := quantityText(values[key])
			if !present {
				say(name, fmt.Sprintf("%s is unset; using the default %s", field, defaultValue))
				return defaultValue
			}
			quantity, err := resource.ParseQuantity(raw)
			if err != nil {
				say(name, fmt.Sprintf("%s %q is not a quantity; using the default %s", field, raw, defaultValue))
				return defaultValue
			}
			if quantity.Sign() < 0 {
				say(name, fmt.Sprintf("%s %s is below zero; using the default %s", field, raw, defaultValue))
				return defaultValue
			}
			// A memory limit of zero would kill the container at its
			// start. A request of zero is valid.
			if field == "limits.memory" && quantity.Sign() == 0 {
				say(name, fmt.Sprintf("%s is zero; using the default %s", field, defaultValue))
				return defaultValue
			}
			return raw
		}
		chosen := containerResources{
			CPU:         value("requests.cpu", requests, requestsOK, "cpu", fallback.CPU),
			Memory:      value("requests.memory", requests, requestsOK, "memory", fallback.Memory),
			MemoryLimit: value("limits.memory", limits, limitsOK, "memory", fallback.MemoryLimit),
		}
		if _, present := limits["cpu"]; present {
			say(name, "limits.cpu is ignored, because no container states a cpu limit")
		}
		// The API server refuses a pod whose request is above its limit,
		// so such a pair would fail every pod of this kind. Both values
		// fall back together, because either one alone can still be out
		// of order with the other.
		request, limit := resource.MustParse(chosen.Memory), resource.MustParse(chosen.MemoryLimit)
		if request.Cmp(limit) > 0 {
			say(name, fmt.Sprintf("requests.memory %s is above limits.memory %s; using the defaults %s and %s",
				chosen.Memory, chosen.MemoryLimit, fallback.Memory, fallback.MemoryLimit))
			chosen.Memory, chosen.MemoryLimit = fallback.Memory, fallback.MemoryLimit
		}
		settings[name] = chosen
	}
	for _, name := range slices.Sorted(maps.Keys(file)) {
		if _, known := defaults[name]; !known {
			say(name, "is not a container this operator builds; ignored")
		}
	}
	return settings
}

// mapping reads one YAML value as a map. An absent or null value is an
// empty map, so each of its values is unset. Any other value is not a
// map.
func mapping(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, true
	case map[string]any:
		return typed, true
	default:
		return nil, false
	}
}

// quantityText turns one YAML value into the text of a quantity. A
// number is written the way Go formats it, so 1 is "1" and 1e9 is
// "1000000000". An absent or null value is unset. Any other value, such
// as a list, is text that no quantity parses.
func quantityText(value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return "", false
	case string:
		return typed, true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	default:
		return fmt.Sprint(typed), true
	}
}

// of answers one container's settings, and the default where the map
// holds none.
func (s resourceSettings) of(name string) (containerResources, bool) {
	if settings, held := s[name]; held {
		return settings, true
	}
	settings, held := defaultResourceSettings()[name]
	return settings, held
}

// apply writes the requests and the memory limit onto every container of
// a pod that has settings, beside the claims the builder wrote. It runs
// before the template hash is stamped, so a changed value rolls the
// standing pods once, the way a changed image does.
func (s resourceSettings) apply(pod *Pod) *Pod {
	set := func(containers []Container) {
		for index := range containers {
			settings, held := s.of(containers[index].Name)
			if !held {
				continue
			}
			containers[index].Resources.Requests = ResourceList{"cpu": settings.CPU, "memory": settings.Memory}
			containers[index].Resources.Limits = ResourceList{"memory": settings.MemoryLimit}
		}
	}
	set(pod.Spec.InitContainers)
	set(pod.Spec.Containers)
	return pod
}
