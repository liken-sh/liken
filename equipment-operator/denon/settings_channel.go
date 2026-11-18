package denon

import "fmt"

// channelCommands returns the wire command for each channel volume in
// the settings, in map iteration order, which is random.
func channelCommands(s *Settings) ([]string, error) {
	if len(s.ChannelVolumes) == 0 {
		return nil, nil
	}
	commands := make([]string, 0, len(s.ChannelVolumes))
	for channel, value := range s.ChannelVolumes {
		command, err := ChannelVolumeCommand(channel, value)
		if err != nil {
			return nil, fmt.Errorf("building the volume command of channel %q: %w", channel, err)
		}
		commands = append(commands, command)
	}
	return commands, nil
}
