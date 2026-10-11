package main

// A client asks a driver for its properties with a getProperties that
// names the device (indi.Client.Sync). These helpers make a driver that
// does not answer, and count how often the operator asks.

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
