package main

import "github.com/liken-sh/liken/observatory-operator/indi/inditest"

// silence makes the driver of a device ignore each getProperties that
// names the device, as a stuck driver does. A getProperties that names
// no device, which a client sends when it connects, is still answered.
// indi.Client.Sync sends one that names the device.
func (w *indiWorld) silence(device string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.silent[device] = true
}

// unsilence makes a silenced driver answer again.
func (w *indiWorld) unsilence(device string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.silent, device)
}

// requests returns how many getProperties naming the device the fake
// servers received.
func (w *indiWorld) requests(device string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.asked[device]
}

// define answers a getProperties with the definitions of device's
// properties, or of every device's when device is empty. As in
// indiserver, a getProperties that names a device reaches only that
// device's driver.
func (w *indiWorld) define(s *fakeServer, c *inditest.Conn, device string) {
	for _, link := range sortedKeys(s.drivers) {
		if w.silent[device] {
			break
		}
		if device != "" && s.drivers[link].device != device {
			continue
		}
		s.drivers[link].reloadDomePolicy()
		for _, prop := range s.drivers[link].props {
			c.Send(prop.Def())
		}
	}
}

// delayDefinitions makes a server delay its response to each getProperties
// that names no device, as a server with slow drivers does. A client is
// then connected but has no devices yet.
func (w *indiWorld) delayDefinitions(server string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.late[server] = true
}

// sendDelayedDefinitions sends the definitions that delayDefinitions held back.
func (w *indiWorld) sendDelayedDefinitions(server string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.late, server)
	for _, c := range w.waiting[server] {
		w.define(w.servers[server], c, "")
	}
	delete(w.waiting, server)
}
