package main

// A motion in the fake INDI servers: a change that a driver answers
// with Busy and finishes later, as a mount that parks does. A change
// during the motion interrupts it (inditest.Interrupt).

// move makes a driver answer each change to one property with the
// values sent and the state Busy. The motion ends when a test sets the
// state with setState.
func (w *indiWorld) move(device, property string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.moving[device+"."+property] = true
}
