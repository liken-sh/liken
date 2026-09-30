// The receiver's model, read from its UPnP description. Port 23 carries
// no command that names the model. A receiver of the generation the
// denonavr library calls AVR-X 2016 serves its AIOS device description
// over plain HTTP on port 60006, and the root device names the maker
// and the model. denonavr's const.py names that port and path, and
// the library reads the manufacturer and the modelName from it. An
// older receiver serves /description.xml on port 8080 instead, and the
// client does not read it. The client reads the description once for
// each connection until one read succeeds, so a receiver that does not
// serve it costs one GET per connection and never a steady stream.

package denon

import (
	"context"
	"net"

	"github.com/liken-sh/equipment-operator/equipment"
	"github.com/liken-sh/equipment-operator/upnp"
)

// The port and the path of the AIOS description.
const (
	aiosDescriptionPort = "60006"
	aiosDescriptionPath = "/upnp/desc/aios_device/aios_device.xml"
)

// describe starts one read of the description when the client has no
// model and no read is in flight. It runs beside the connection, so a
// receiver slow to answer the description never delays a command.
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
		description, err := upnp.Fetch(ctx, url)
		d.mutex.Lock()
		d.describing = false
		if err != nil {
			// A receiver without HEOS serves no description, and its model
			// stays empty. The next connection asks again.
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
