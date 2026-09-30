// The amp's model, read from its UPnP description. getStatusEx names a
// project such as WiiM_Amp_4layer, which is a firmware build and not a
// name a person knows the amp by. The description beside the
// MediaRenderer names the model, such as WiiM Amp, and the maker.

package wiim

import (
	"context"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
	"github.com/liken-sh/equipment-operator/upnp"
)

// descriptionPath is where a LinkPlay device serves its description on
// the UPnP port.
const descriptionPath = "/description.xml"

// describeRetryInterval is the wait after a failed read before the
// next. The read rides on the poll, so an amp that does not serve its
// description gets one read a minute and not one on every poll.
const describeRetryInterval = time.Minute

// describe reads the description once the amp has answered a poll as a
// WiiM, and then never again while the client runs. A device that is
// not a WiiM gets no read, because the client sends it getStatusEx and
// nothing else.
func (c *Client) describe(ctx context.Context) {
	c.mutex.Lock()
	skip := !c.surveyed || c.foreign != "" || c.description.Model != "" || time.Now().Before(c.describeRetry)
	c.mutex.Unlock()
	if skip {
		return
	}
	description, err := upnp.Fetch(ctx, c.upnpBase+descriptionPath)
	c.mutex.Lock()
	if err != nil {
		// The model stays empty; the status shows the project under
		// status.wiim.device.model all the same.
		c.describeRetry = time.Now().Add(describeRetryInterval)
		c.mutex.Unlock()
		return
	}
	c.description = description
	state := c.withDescription(c.state.equipmentState(c.reachable))
	c.mutex.Unlock()
	c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventModel, State: state})
}

// withDescription adds the model and the maker to a state. The caller
// holds the mutex.
func (c *Client) withDescription(state equipment.State) equipment.State {
	state.Model = c.description.Model
	state.Manufacturer = c.description.Manufacturer
	return state
}
