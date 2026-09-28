package main

import "testing"

// The Deployment states every setting in the environment, and an
// unset variable reads as empty.
func TestReadSettingsTakesTheEnvironment(t *testing.T) {
	t.Run("stated", func(t *testing.T) {
		t.Setenv(podNamespaceVariable, "liken-system")
		t.Setenv(podNameVariable, "equipment-operator-7d9c")
		t.Setenv(busAddressVariable, "bus.liken-system.svc:1883")
		t.Setenv(metricsAddressVariable, ":9200")

		config := readSettings()

		mustMatch(t, config.namespace, "liken-system")
		mustMatch(t, config.pod, "equipment-operator-7d9c")
		mustMatch(t, config.busAddress, "bus.liken-system.svc:1883")
		mustMatch(t, config.metricsAddress, ":9200")
	})

	t.Run("unset", func(t *testing.T) {
		t.Setenv(podNamespaceVariable, "")
		t.Setenv(podNameVariable, "")
		t.Setenv(busAddressVariable, "")
		t.Setenv(metricsAddressVariable, "")

		config := readSettings()

		mustMatch(t, config, settings{})
	})
}
