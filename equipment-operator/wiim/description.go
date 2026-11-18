// The client reads the amp's model and manufacturer from its UPnP
// device description. getStatusEx returns a project name such as
// WiiM_Amp_4layer, which names a firmware build and not the product.
// The description gives the product name, such as WiiM Amp.

package wiim

import (
	"context"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/equipment-operator/upnp"
)

const descriptionPath = "/description.xml"

// describeRetryInterval is the wait after a failed description read.
// describe runs after every poll, so without the wait an amp that does
// not serve a description would get a request on every poll.
const describeRetryInterval = time.Minute

// describe reads the description once, after the first answered poll.
// It sends nothing to a device that getStatusEx shows is not a WiiM.
func (c *Client) describe(ctx context.Context) {
	c.mutex.Lock()
	skip := !c.firstReadDone || c.foreign != "" || c.description.Model != "" || time.Now().Before(c.describeRetry)
	c.mutex.Unlock()
	if skip {
		return
	}
	description, err := upnp.FetchVia(ctx, c.dial, c.upnpBase+descriptionPath)
	c.mutex.Lock()
	if err != nil {
		// The model stays empty, and status.wiim.device.model keeps
		// the project name from getStatusEx.
		c.describeRetry = time.Now().Add(describeRetryInterval)
		c.mutex.Unlock()
		return
	}
	c.description = description
	state := c.withDescription(c.state.equipmentState(c.reachable))
	c.mutex.Unlock()
	c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventModel, State: state})
}

// withDescription returns state with the model and manufacturer from
// the description. The caller holds c.mutex.
func (c *Client) withDescription(state equipment.State) equipment.State {
	state.Model = c.description.Model
	state.Manufacturer = c.description.Manufacturer
	return state
}
