package main

// This file sets the cpu and memory requests and the memory limit of
// every container the operator builds: the player, display, and command
// containers of a playback pod, the idle container, and the reader
// container of a Remote's pod.
//
// Every container has requests, so every pod is in the Burstable QoS
// class. Under memory pressure the kubelet evicts BestEffort pods first,
// and that would stop the film a person is watching.
//
// The requests are sized for a 1920x1080 screen, because the scheduler
// subtracts them from the node's allocatable memory, and a machine with
// a screen can have 1GB. Requests sized for 3840x2160 would leave a Play
// Pending on such a machine. The memory limits are sized for a
// 3840x2160 screen, so the kubelet does not kill a film at that size.
// No container has a cpu limit, because a throttled decoder drops
// frames.
//
// A cluster owner changes the values without a build. The kustomize
// base generates the ConfigMap media-operator-resources from
// deploy/container-resources.yaml, and the operator reads the mounted
// file once at start. The generator adds a hash of the content to the
// ConfigMap's name, so a changed value restarts the operator's pod.

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"
)

// containerResourcesPath is where the operator's Deployment mounts the
// generated ConfigMap.
const containerResourcesPath = "/etc/media-operator/resources.yaml"

// containerResources holds one container's settings. Each field is the
// text of a Kubernetes quantity.
type containerResources struct {
	CPU         string
	Memory      string
	MemoryLimit string
}

// resourceSettings maps a container name to its settings. A nil map
// means every container uses its default.
type resourceSettings map[string]containerResources

// ResourceList maps a resource name, such as "cpu" or "memory", to the
// text of a quantity.
type ResourceList map[string]string

// defaultResourceSettings returns the values that apply when the file
// does not set them. deploy/container-resources.yaml sets the same
// values.
func defaultResourceSettings() resourceSettings {
	return resourceSettings{
		playerContainer: {CPU: "80m", Memory: "432Mi", MemoryLimit: "1Gi"},
		// Most of the display's memory is shared memory for its surface
		// buffers, so its use grows with the screen size.
		displayContainer:      {CPU: "10m", Memory: "144Mi", MemoryLimit: "640Mi"},
		commandContainer:      {CPU: "10m", Memory: "16Mi", MemoryLimit: "32Mi"},
		idleContainer:         {CPU: "5m", Memory: "96Mi", MemoryLimit: "256Mi"},
		remoteReaderContainer: {CPU: "1m", Memory: "4Mi", MemoryLimit: "16Mi"},
	}
}

// loadResourceSettings reads the file at path. When the file cannot be
// read, every container gets its defaults, so the operator runs with no
// ConfigMap mounted.
func loadResourceSettings(path string, report func(string)) resourceSettings {
	data, err := os.ReadFile(path)
	if err != nil {
		report(fmt.Sprintf("media.liken.sh: container resources: %v, so every container uses its default", err))
		return defaultResourceSettings()
	}
	return parseResourceSettings(data, report)
}

// parseResourceSettings reads the settings from the file's YAML. Each
// missing or invalid value falls back to its own default and reports
// one line, and the other values keep what the file sets. The file has
// one entry per container name, shaped like a container's resources
// block:
//
//	player:
//	  requests: {cpu: 80m, memory: 432Mi}
//	  limits: {memory: 1Gi}
//
// The file is decoded into generic maps, because a struct decode fails
// on one entry of the wrong type and every container would lose its
// values.
func parseResourceSettings(data []byte, report func(string)) resourceSettings {
	defaults := defaultResourceSettings()
	file := map[string]any{}
	if err := yaml.Unmarshal(data, &file); err != nil {
		report(fmt.Sprintf("media.liken.sh: container resources: the file does not parse: %v, so every container uses its default", err))
		return defaults
	}
	reportFor := func(container, message string) {
		report(fmt.Sprintf("media.liken.sh: container resources: %s: %s", container, message))
	}

	settings := resourceSettings{}
	for _, name := range slices.Sorted(maps.Keys(defaults)) {
		fallback := defaults[name]
		entry, isMap := mapping(file[name])
		if !isMap {
			reportFor(name, "is not a map of requests and limits, so it uses its defaults")
			settings[name] = fallback
			continue
		}
		requests, requestsOK := mapping(entry["requests"])
		if !requestsOK {
			reportFor(name, "requests is not a map, so it uses the default requests")
		}
		limits, limitsOK := mapping(entry["limits"])
		if !limitsOK {
			reportFor(name, "limits is not a map, so it uses the default limits")
		}
		value := func(field string, values map[string]any, valid bool, key, defaultValue string) string {
			if !valid {
				return defaultValue
			}
			raw, present := quantityText(values[key])
			if !present {
				reportFor(name, fmt.Sprintf("%s is unset, so it uses the default %s", field, defaultValue))
				return defaultValue
			}
			quantity, err := resource.ParseQuantity(raw)
			if err != nil {
				reportFor(name, fmt.Sprintf("%s %q is not a quantity, so it uses the default %s", field, raw, defaultValue))
				return defaultValue
			}
			if quantity.Sign() < 0 {
				reportFor(name, fmt.Sprintf("%s %s is below zero, so it uses the default %s", field, raw, defaultValue))
				return defaultValue
			}
			// A request of zero is valid. A memory limit of zero kills the
			// container when it starts.
			if field == "limits.memory" && quantity.Sign() == 0 {
				reportFor(name, fmt.Sprintf("%s is zero, so it uses the default %s", field, defaultValue))
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
			reportFor(name, "limits.cpu is ignored, because no container sets a cpu limit")
		}
		// The API server rejects a pod whose memory request is above its
		// limit. Both values fall back together, because the default of
		// one can still be above or below the file's value of the other.
		request, limit := resource.MustParse(chosen.Memory), resource.MustParse(chosen.MemoryLimit)
		if request.Cmp(limit) > 0 {
			reportFor(name, fmt.Sprintf("requests.memory %s is above limits.memory %s, so both use the defaults %s and %s",
				chosen.Memory, chosen.MemoryLimit, fallback.Memory, fallback.MemoryLimit))
			chosen.Memory, chosen.MemoryLimit = fallback.Memory, fallback.MemoryLimit
		}
		settings[name] = chosen
	}
	for _, name := range slices.Sorted(maps.Keys(file)) {
		if _, known := defaults[name]; !known {
			reportFor(name, "is not a container that media-operator builds, so it is ignored")
		}
	}
	return settings
}

// mapping returns value as a map. An absent or null value is an empty
// map. The second result is false when value is anything else.
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

// quantityText returns a YAML value as the text of a quantity, and
// false when the value is absent or null. A number is formatted without
// an exponent, so 1e9 becomes "1000000000". Any other type, such as a
// list, becomes text that resource.ParseQuantity rejects.
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

// of returns the settings for a container name. It uses the default
// when the map has no entry, and returns false for a container that has
// no default either.
func (s resourceSettings) of(name string) (containerResources, bool) {
	if settings, found := s[name]; found {
		return settings, true
	}
	settings, found := defaultResourceSettings()[name]
	return settings, found
}

// apply sets the requests and the memory limit on each container of the
// pod that has settings, and keeps the claims in each container's
// resources. The idle and Remote pods get their settings before the
// template hash is computed, so a changed value replaces each of those
// pods once, as a changed image does. A running playback pod keeps its
// values, and the next playback pod gets the new ones.
func (s resourceSettings) apply(pod *Pod) *Pod {
	set := func(containers []Container) {
		for index := range containers {
			settings, found := s.of(containers[index].Name)
			if !found {
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
