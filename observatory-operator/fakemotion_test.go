package main

// move makes a fake driver respond to each change of one property with
// the new values and the state Busy, as a mount does while it parks.
// The motion ends when the test calls setState, and another change
// during the motion interrupts it (inditest.Interrupt).
func (w *indiWorld) move(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.moving[device+"."+property] = true
}
