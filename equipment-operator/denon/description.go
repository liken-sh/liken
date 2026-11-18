// The client reads the receiver's model from its UPnP description,
// because the ASCII control protocol on port 23 has no command that
// returns the model. A receiver of the generation that the denonavr
// library calls AVR-X 2016 serves its AIOS device description over
// plain HTTP on port 60006, and the root device has the manufacturer
// and the modelName. denonavr's const.py gives that port and path. An older
// receiver serves /description.xml on port 8080, which this client does
// not read. Until one read succeeds, the client tries again on each
// connection, so a receiver that serves no description costs one GET
// per connection.

package denon

import (
	"context"
	"net"

	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/equipment-operator/upnp"
)

// The port and path of the AIOS device description.
const (
	aiosDescriptionPort = "60006"
	aiosDescriptionPath = "/upnp/desc/aios_device/aios_device.xml"
)

// describe starts a read of the description on its own goroutine when
// the client has no model yet and no read is running, so a receiver
// that is slow to serve the description never delays a command.
func (d *Client) describe(ctx context.Context) {
	d.mutex.Lock()
	if d.state.model != "" || d.describing {
		d.mutex.Unlock()
		return
	}
	d.describing = true
	url := d.descriptionURL
	if url == "" {
		url = "http://" + net.JoinHostPort(d.peer, aiosDescriptionPort) + aiosDescriptionPath
	}
	d.mutex.Unlock()

	go func() {
		description, err := upnp.FetchVia(ctx, d.dialer(), url)
		d.mutex.Lock()
		d.describing = false
		if err != nil {
			// A receiver without HEOS serves no description, and its model
			// stays empty.
			d.mutex.Unlock()
			return
		}
		d.state.model = description.Model
		d.state.manufacturer = description.Manufacturer
		state := d.equipmentState(d.state)
		d.mutex.Unlock()
		d.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventModel, State: state})
	}()
}
