package main

// These tests cover the container resource settings: the file a cluster
// owner patches, the default each value falls back to, and the requests
// and limits that reach every container the operator builds.

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// customSettings differs from the defaults in every value, so a test
// that reads a value back proves where it came from.
func customSettings() resourceSettings {
	return resourceSettings{
		playerContainer:       {CPU: "101m", Memory: "501Mi", MemoryLimit: "1501Mi"},
		displayContainer:      {CPU: "102m", Memory: "502Mi", MemoryLimit: "1502Mi"},
		commandContainer:      {CPU: "103m", Memory: "503Mi", MemoryLimit: "1503Mi"},
		idleContainer:         {CPU: "104m", Memory: "504Mi", MemoryLimit: "1504Mi"},
		remoteReaderContainer: {CPU: "105m", Memory: "505Mi", MemoryLimit: "1505Mi"},
	}
}

const customSettingsFile = `
player:
  requests: {cpu: 101m, memory: 501Mi}
  limits: {memory: 1501Mi}
display:
  requests: {cpu: 102m, memory: 502Mi}
  limits: {memory: 1502Mi}
command:
  requests: {cpu: 103m, memory: 503Mi}
  limits: {memory: 1503Mi}
idle:
  requests: {cpu: 104m, memory: 504Mi}
  limits: {memory: 1504Mi}
reader:
  requests: {cpu: 105m, memory: 505Mi}
  limits: {memory: 1505Mi}
`

// withPlayer is the custom file with the player's entry replaced.
func withPlayer(entry string) string {
	start := strings.Index(customSettingsFile, "player:")
	end := strings.Index(customSettingsFile, "display:")
	return customSettingsFile[:start] + entry + customSettingsFile[end:]
}

// settingsWithPlayer is the custom settings with the player's values
// replaced.
func settingsWithPlayer(player containerResources) resourceSettings {
	settings := customSettings()
	settings[playerContainer] = player
	return settings
}

func TestParseResourceSettings(t *testing.T) {
	defaults := defaultResourceSettings()
	defaultPlayer := defaults[playerContainer]
	cases := []struct {
		name  string
		file  string
		want  resourceSettings
		lines []string
	}{
		{
			name: "a full file",
			file: customSettingsFile,
			want: customSettings(),
		},
		{
			name: "a missing memory limit",
			file: withPlayer("player:\n  requests: {cpu: 101m, memory: 501Mi}\n"),
			want: settingsWithPlayer(containerResources{
				CPU: "101m", Memory: "501Mi", MemoryLimit: defaultPlayer.MemoryLimit,
			}),
			lines: []string{"player: limits.memory is unset; using the default " + defaultPlayer.MemoryLimit},
		},
		{
			name: "a quantity that does not parse",
			file: withPlayer("player:\n  requests: {cpu: fast, memory: 501Mi}\n  limits: {memory: 1501Mi}\n"),
			want: settingsWithPlayer(containerResources{
				CPU: defaultPlayer.CPU, Memory: "501Mi", MemoryLimit: "1501Mi",
			}),
			lines: []string{`player: requests.cpu "fast" is not a quantity; using the default ` + defaultPlayer.CPU},
		},
		{
			name:  "a cpu limit",
			file:  withPlayer("player:\n  requests: {cpu: 101m, memory: 501Mi}\n  limits: {cpu: 500m, memory: 1501Mi}\n"),
			want:  customSettings(),
			lines: []string{"player: limits.cpu is ignored, because no container states a cpu limit"},
		},
		{
			name: "a memory request above its limit",
			file: withPlayer("player:\n  requests: {cpu: 101m, memory: 2Gi}\n  limits: {memory: 1501Mi}\n"),
			want: settingsWithPlayer(containerResources{
				CPU: "101m", Memory: defaultPlayer.Memory, MemoryLimit: defaultPlayer.MemoryLimit,
			}),
			lines: []string{"player: requests.memory 2Gi is above limits.memory 1501Mi; using the defaults " +
				defaultPlayer.Memory + " and " + defaultPlayer.MemoryLimit},
		},
		{
			name: "quantities written as YAML numbers",
			file: withPlayer("player:\n  requests: {cpu: 1, memory: 5e8}\n  limits: {memory: 1e9}\n"),
			want: settingsWithPlayer(containerResources{
				CPU: "1", Memory: "500000000", MemoryLimit: "1000000000",
			}),
		},
		{
			name: "a null value",
			file: withPlayer("player:\n  requests: {cpu: null, memory: 501Mi}\n  limits: {memory: 1501Mi}\n"),
			want: settingsWithPlayer(containerResources{
				CPU: defaultPlayer.CPU, Memory: "501Mi", MemoryLimit: "1501Mi",
			}),
			lines: []string{"player: requests.cpu is unset; using the default " + defaultPlayer.CPU},
		},
		{
			name:  "an entry that is not a map",
			file:  withPlayer("player: 3\n"),
			want:  settingsWithPlayer(defaultPlayer),
			lines: []string{"player: is not a map of requests and limits; using its defaults"},
		},
		{
			name: "a block that is not a map",
			file: withPlayer("player:\n  requests: {cpu: 101m, memory: 501Mi}\n  limits: 1501Mi\n"),
			want: settingsWithPlayer(containerResources{
				CPU: "101m", Memory: "501Mi", MemoryLimit: defaultPlayer.MemoryLimit,
			}),
			lines: []string{"player: limits is not a map; using the default limits"},
		},
		{
			name: "a negative quantity",
			file: withPlayer("player:\n  requests: {cpu: -1, memory: 501Mi}\n  limits: {memory: 1501Mi}\n"),
			want: settingsWithPlayer(containerResources{
				CPU: defaultPlayer.CPU, Memory: "501Mi", MemoryLimit: "1501Mi",
			}),
			lines: []string{"player: requests.cpu -1 is below zero; using the default " + defaultPlayer.CPU},
		},
		{
			name: "a memory limit of zero",
			file: withPlayer("player:\n  requests: {cpu: 101m, memory: 0}\n  limits: {memory: 0}\n"),
			want: settingsWithPlayer(containerResources{
				CPU: "101m", Memory: "0", MemoryLimit: defaultPlayer.MemoryLimit,
			}),
			lines: []string{"player: limits.memory is zero; using the default " + defaultPlayer.MemoryLimit},
		},
		{
			name:  "a container the operator does not build",
			file:  customSettingsFile + "mixer:\n  requests: {cpu: 1m}\n",
			want:  customSettings(),
			lines: []string{"mixer: is not a container this operator builds; ignored"},
		},
		{
			name:  "a file that is not YAML",
			file:  "player: [",
			want:  defaults,
			lines: []string{"the file does not parse"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var lines []string
			got := parseResourceSettings([]byte(tc.file), func(line string) { lines = append(lines, line) })

			if !maps.Equal(got, tc.want) {
				t.Errorf("settings = %v, want %v", got, tc.want)
			}
			if len(lines) != len(tc.lines) {
				t.Fatalf("lines = %q, want %q", lines, tc.lines)
			}
			for index, line := range lines {
				if !strings.Contains(line, tc.lines[index]) {
					t.Errorf("line = %q, want it to say %q", line, tc.lines[index])
				}
			}
		})
	}
}

// An empty file sets no value, so every value is its default, and each
// one says so on its own line.
func TestAnEmptySettingsFileUsesEveryDefault(t *testing.T) {
	var lines []string
	got := parseResourceSettings(nil, func(line string) { lines = append(lines, line) })

	if !maps.Equal(got, defaultResourceSettings()) {
		t.Errorf("settings = %v, want the defaults", got)
	}
	if want := 3 * len(defaultResourceSettings()); len(lines) != want {
		t.Errorf("%d lines, want one for each of the %d values: %q", len(lines), want, lines)
	}
}

// The file the base ships states every default, so a cluster owner who
// copies it starts from the values the operator uses, and a start with
// the base logs nothing.
func TestTheShippedSettingsFileStatesEveryDefault(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("deploy", "container-resources.yaml"))
	mustSucceed(t, err)
	var lines []string

	got := parseResourceSettings(data, func(line string) { lines = append(lines, line) })

	if !maps.Equal(got, defaultResourceSettings()) || len(lines) != 0 {
		t.Errorf("settings = %v with lines %q, want the defaults and no line", got, lines)
	}
}

// A missing file is one line and every default, so the operator starts
// with no ConfigMap mounted.
func TestAMissingSettingsFileUsesEveryDefault(t *testing.T) {
	var lines []string

	got := loadResourceSettings(filepath.Join(t.TempDir(), "absent.yaml"),
		func(line string) { lines = append(lines, line) })

	if !maps.Equal(got, defaultResourceSettings()) || len(lines) != 1 {
		t.Errorf("settings = %v with lines %q, want the defaults and one line", got, lines)
	}
}

func TestEveryContainerGetsItsConfiguredRequestsAndMemoryLimit(t *testing.T) {
	cases := []struct {
		name       string
		containers []string
		created    func(t *testing.T, media *operator, cluster *fakeCluster) *Pod
	}{
		{
			name:       "the playback pod",
			containers: []string{playerContainer, commandContainer, displayContainer},
			created: func(t *testing.T, media *operator, cluster *fakeCluster) *Pod {
				cluster.plays["movie"] = housePlay("nfs://nas/movies/film.mkv")
				cluster.players["theater"] = housePlayer()
				media.pass()
				return cluster.pods["movie-playback"]
			},
		},
		{
			name:       "the idle pod",
			containers: []string{idleContainer},
			created: func(t *testing.T, media *operator, cluster *fakeCluster) *Pod {
				media.idleDisplayClass = "display-draw"
				mustSucceed(t, media.reconcileIdle(standingIdlePlayer(), "", nil))
				return cluster.pods["theater-idle"]
			},
		},
		{
			name:       "the remote pod",
			containers: []string{remoteReaderContainer},
			created: func(t *testing.T, media *operator, cluster *fakeCluster) *Pod {
				mustSucceed(t, media.reconcileRemote(standingRemote(), claimRead{}))
				return cluster.pods["sofa-remote"]
			},
		},
	}
	settings := customSettings()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := newFakeCluster()
			media := testOperator(t, cluster, make(chan struct{}, 1))
			media.resources = settings

			pod := tc.created(t, media, cluster)

			if pod == nil {
				t.Fatalf("no pod was created: %v", cluster.requests)
			}
			for _, name := range tc.containers {
				want := settings[name]
				got := podContainer(t, pod, name).Resources
				if !maps.Equal(got.Requests, ResourceList{"cpu": want.CPU, "memory": want.Memory}) {
					t.Errorf("%s requests = %v, want %+v", name, got.Requests, want)
				}
				if !maps.Equal(got.Limits, ResourceList{"memory": want.MemoryLimit}) {
					t.Errorf("%s limits = %v, want memory %s and no cpu", name, got.Limits, want.MemoryLimit)
				}
			}
		})
	}
}

// podContainer finds one container of a pod, ordinary or init, by name.
func podContainer(t *testing.T, pod *Pod, name string) Container {
	t.Helper()
	for _, container := range append(pod.Spec.Containers, pod.Spec.InitContainers...) {
		if container.Name == name {
			return container
		}
	}
	t.Fatalf("pod %s has no container %s", pod.Metadata.Name, name)
	return Container{}
}

// An operator built with no settings, as most tests build one, gives
// every container the defaults.
func TestAnOperatorWithNoSettingsUsesTheDefaults(t *testing.T) {
	pod := resourceSettings(nil).apply(testPod(t))
	want := defaultResourceSettings()[playerContainer]

	got := podContainer(t, pod, playerContainer).Resources
	if got.Requests["memory"] != want.Memory || got.Limits["memory"] != want.MemoryLimit {
		t.Errorf("player resources = %+v, want the default %+v", got, want)
	}
}
