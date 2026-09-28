package denon

// Channel volumes are a map of trims keyed by channel name, and their
// bus ids are dynamic: channel.<NAME>.

import (
	"fmt"
	"strings"

	"github.com/liken-sh/equipment-operator/equipment"
)

// channelPrefix names a channel volume on the bus. The channel name
// follows it, so the id is channel.<NAME>.
const channelPrefix = "channel."

// validChannelToken answers whether one channel name is well formed:
// non-empty, and free of the dot and whitespace that would make a
// channel.<NAME> id ambiguous.
func validChannelToken(channel string) bool {
	if channel == "" {
		return false
	}
	return !strings.ContainsAny(channel, ". \t\n\r\v\f")
}

// setChannel folds one bus value into the channel volumes and answers
// the wire command. A malformed channel name is an error that names
// the id, so channel.FL.extra never routes to the wire.
func setChannel(s *Settings, channel string, v equipment.SettingValue) (string, error) {
	if !validChannelToken(channel) {
		return "", fmt.Errorf("channel id %q is malformed", channelPrefix+channel)
	}
	value, ok := v.Number()
	if !ok {
		return "", settingTypeError(channelPrefix+channel, "a number")
	}
	command, err := ChannelVolumeCommand(channel, value)
	if err != nil {
		return "", err
	}
	if s.ChannelVolumes == nil {
		s.ChannelVolumes = map[string]float64{}
	}
	s.ChannelVolumes[channel] = value
	return command, nil
}

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
