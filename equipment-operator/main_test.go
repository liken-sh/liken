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
		t.Setenv(networkDiscoveryVariable, "off")

		config, err := readSettings()

		mustSucceed(t, err)
		mustMatch(t, config.namespace, "liken-system")
		mustMatch(t, config.pod, "equipment-operator-7d9c")
		mustMatch(t, config.busAddress, "bus.liken-system.svc:1883")
		mustMatch(t, config.metricsAddress, ":9200")
		mustMatch(t, config.networkDiscoveryOff, true)
	})

	t.Run("unset", func(t *testing.T) {
		t.Setenv(podNamespaceVariable, "")
		t.Setenv(podNameVariable, "")
		t.Setenv(busAddressVariable, "")
		t.Setenv(metricsAddressVariable, "")
		t.Setenv(networkDiscoveryVariable, "")

		config, err := readSettings()

		mustSucceed(t, err)
		mustMatch(t, config, settings{})
	})
}

// Network discovery is on unless the Deployment turns it off, and a
// value other than on or off stops the operator, so a misspelled off
// never leaves discovery on.
func TestNetworkDiscoveryTakesOnOrOff(t *testing.T) {
	cases := []struct {
		value string
		off   bool
		fails bool
	}{
		{value: "", off: false},
		{value: "on", off: false},
		{value: "off", off: true},
		{value: "Off", fails: true},
		{value: "false", fails: true},
		{value: "disabled", fails: true},
	}
	for _, one := range cases {
		t.Run(one.value, func(t *testing.T) {
			t.Setenv(networkDiscoveryVariable, one.value)

			config, err := readSettings()

			mustMatch(t, err != nil, one.fails)
			mustMatch(t, config.networkDiscoveryOff, one.off)
		})
	}
}
