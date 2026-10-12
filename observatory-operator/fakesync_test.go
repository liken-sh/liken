package main

// A client asks for the properties of every device with a
// getProperties when it connects, and asks a driver for its properties
// with a getProperties that names the device (indi.Client.Sync). These
// helpers make a server or a driver that does not answer, and count how
// often the operator asks.

// silence makes the driver of a device leave each getProperties that
// names the device unanswered, as a driver that is stuck does.
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

// requests answers how many times a client sent a getProperties that
// names a device.
func (w *indiWorld) requests(device string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.asked[device]
}

// define answers a getProperties that names device, or every device
// when device is empty. indiserver hands a getProperties that names a
// device to that device's driver alone.
func (w *indiWorld) define(s *fakeServer, c *fakeConn, device string) {
	for _, link := range sortedKeys(s.drivers) {
		if w.silent[device] {
			break
		}
		if device != "" && s.drivers[link].device != device {
			continue
		}
		s.drivers[link].reloadDomePolicy()
		for _, prop := range s.drivers[link].props {
			c.send(prop.def())
		}
	}
}

// answerLater makes a server leave the getProperties that a new client
// sends first unanswered, as a server whose drivers are slow to answer
// does, so the client is connected and knows no device.
func (w *indiWorld) answerLater(server string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.late[server] = true
}

// answerLate answers each getProperties that answerLater held.
func (w *indiWorld) answerLate(server string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.late, server)
	for _, c := range w.waiting[server] {
		w.define(w.servers[server], c, "")
	}
	delete(w.waiting, server)
}
