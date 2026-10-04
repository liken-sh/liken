package denon

// Channel volumes are a map of trims keyed by channel name.

import "fmt"

// channelCommands answers the wire command for every channel the
// settings declare.
func channelCommands(s *Settings) ([]string, error) {
	if len(s.ChannelVolumes) == 0 {
		return nil, nil
	}
	commands := make([]string, 0, len(s.ChannelVolumes))
	for channel, value := range s.ChannelVolumes {
		command, err := ChannelVolumeCommand(channel, value)
		if err != nil {
			return nil, fmt.Errorf("channel %q: %w", channel, err)
		}
		commands = append(commands, command)
	}
	return commands, nil
}
