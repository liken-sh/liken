// Package indi is a client of the INDI protocol, version 1.7, which is
// XML over TCP, the protocol that indiserver speaks to KStars and to
// every other client. No maintained Go client of INDI exists, so this
// package implements one in pure Go, with no cgo.
//
// A Client keeps its state current from events, as the rest of the
// repository does. A Client opens one connection to one indiserver for
// each call of Run. Run sends getProperties. The def*Vector messages
// that the server sends in reply set the baseline in the Client's
// Store, and each later set*Vector message updates it. When the
// connection ends, the store is emptied, and the next Run fills it from
// a new baseline. When the server restarts, its drivers restart too and
// lose their settings, so nothing from the old connection is still
// valid. Callers read the store, subscribe to its changes, or wait for
// a condition on it. Nothing in the package polls the server on a
// timer.
//
//	c := indi.NewClient("indiserver.observatory:7624")
//	go c.Run(ctx)
//
//	// Connect the focuser once its driver defines CONNECTION.
//	if err := c.ConnectDevice(ctx, "Focuser Simulator"); err != nil {
//		return err
//	}
//
//	// Move it, and wait for the driver to report the result.
//	sent, err := c.SetNumbers("Focuser Simulator", "ABS_FOCUS_POSITION",
//		map[string]float64{"FOCUS_ABSOLUTE_POSITION": 52000})
//	if err != nil {
//		return err
//	}
//	p, err := c.Settle(ctx, sent)
//
// A caller follows changes in one of two ways:
//
//   - Subscribe returns a channel of events: Connected, Disconnected,
//     Defined, Updated, Deleted, Message, and Invalid. An event
//     identifies what changed, and the values are in the store.
//   - WaitFor waits until a condition on the store is true, such as
//     "the device defines CONNECTION" or "the member equals 52000". It
//     checks the condition after each change.
//
// SetNumbers, SetTexts, and SetSwitches send every member of the
// property, and refuse a change that the definition forbids: a
// read-only property, the wrong type, a member that the property does
// not have, or switches that break the switch rule. Settle waits until
// the device reports the result of a change: Busy and then Ok, or Ok at
// once with the values sent. It returns an Alert state as an
// AlertError.
//
// The client sends no enableBLOB unless a caller calls EnableBLOB, so
// by default it receives no frame data. The store records the format
// and the size of each BLOB but not its data. A caller that enabled
// BLOBs receives the data in the update's event.
//
// ParseNumber reads a decimal or sexagesimal number, such as 12:30:00,
// and FormatNumber prints a value in its member's format, including
// INDI's %m formats.
//
// The protocol reference is https://docs.indilib.org/protocol/. Where
// the reference says nothing, the client follows libindi, and a comment
// names the source file: indiserver/ClInfo.cpp for what the server
// sends a client, and libs/indiabstractclient/ for what libindi's own
// client does.
package indi
