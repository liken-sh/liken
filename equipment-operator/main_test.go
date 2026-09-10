package main

import "testing"

// The Deployment states every setting in the environment, and an
// unset variable reads as empty.
func TestReadSettingsTakesTheEnvironment(t *testing.T) {
	t.Run("stated", func(t *testing.T) {
		t.Setenv(podNamespaceVariable, "liken-system")
		t.Setenv(busAddressVariable, "bus.liken-system.svc:1883")
		t.Setenv(metricsAddressVariable, ":9260")

		config := readSettings()

		mustMatch(t, config.namespace, "liken-system")
		mustMatch(t, config.busAddress, "bus.liken-system.svc:1883")
		mustMatch(t, config.metricsAddress, ":9260")
	})

	t.Run("unset", func(t *testing.T) {
		t.Setenv(podNamespaceVariable, "")
		t.Setenv(busAddressVariable, "")
		t.Setenv(metricsAddressVariable, "")

		config := readSettings()

		mustMatch(t, config, settings{})
	})
}
