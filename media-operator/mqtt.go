package main

// An MQTT 3.1.1 codec for the few packet types the operator uses, in
// place of a client library. The functions build and parse whole
// packets and keep no connection state. bus.go owns the socket.
//
// Everything is QoS 0. Input events are momentary, and the operator
// publishes its reports again after every reconnect, so QoS 1 and 2
// would add nothing.

import (
	"bufio"
	"fmt"
	"io"
)

// The MQTT control packet types, in the high nibble of the first byte of
// the fixed header. The low nibble holds flags that depend on the type.
const (
	mqttConnect   = 0x10
	mqttConnack   = 0x20
	mqttPublish   = 0x30
	mqttSubscribe = 0x80
	mqttSuback    = 0x90
	mqttPingreq   = 0xC0
	mqttPingresp  = 0xD0
)

// mqttRetainFlag is bit 0 of a PUBLISH's first byte.
const mqttRetainFlag = 0x01

// The protocol name and level of MQTT 3.1.1, sent in CONNECT.
const (
	mqttProtocolName  = "MQTT"
	mqttProtocolLevel = 0x04
)

// The CONNECT flag bits this client sets. Clean session asks the broker
// to keep no session state, because the client subscribes and publishes
// again after every connect. The client sends no username or password.
const (
	connectCleanSession = 0x02
	connectWillFlag     = 0x04
	connectWillRetain   = 0x20
)

// mqttMaxRemainingLength is the largest body a control packet can
// have, because the remaining length is at most four bytes of seven bits
// each.
const mqttMaxRemainingLength = 268_435_455

// mqttMaxFieldLength is the longest string or binary field, because
// MQTT 3.1.1 sections 1.5.3 and 3.1.3.3 give a string and the will
// payload a two-byte length.
const mqttMaxFieldLength = 65_535

// encodeRemainingLength encodes the protocol's variable-length integer:
// seven bits of the length in each byte, least significant first, with
// the high bit set on every byte except the last.
func encodeRemainingLength(length int) []byte {
	var encoded []byte
	for {
		digit := byte(length % 128)
		length /= 128
		if length > 0 {
			digit |= 0x80
		}
		encoded = append(encoded, digit)
		if length == 0 {
			return encoded
		}
	}
}

// decodeRemainingLength reads a variable-length integer. It fails after
// four bytes, because the protocol allows no more, and a fifth byte
// means the reader has lost its place in the stream.
func decodeRemainingLength(reader io.ByteReader) (int, error) {
	length := 0
	multiplier := 1
	for count := 0; count < 4; count++ {
		digit, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		length += int(digit&0x7F) * multiplier
		if digit&0x80 == 0 {
			return length, nil
		}
		multiplier *= 128
	}
	return 0, fmt.Errorf("mqtt: the remaining length is longer than four bytes")
}

// appendField appends a value with a two-byte big-endian length prefix,
// the form of every UTF-8 string and of the will payload. It refuses a
// value longer than mqttMaxFieldLength, because the length would
// overflow and the broker would read the rest of the value as the next
// field. field is the value's name in the error message.
func appendField[Value string | []byte](buffer []byte, field string, value Value) ([]byte, error) {
	if len(value) > mqttMaxFieldLength {
		return nil, fmt.Errorf("mqtt: the %s is %d bytes, and the protocol allows at most %d", field, len(value), mqttMaxFieldLength)
	}
	buffer = append(buffer, byte(len(value)>>8), byte(len(value)))
	return append(buffer, value...), nil
}

// packet builds a control packet from the first byte of its fixed
// header and its body. It refuses a body longer than
// mqttMaxRemainingLength before it allocates the frame.
func packet(first byte, body []byte) ([]byte, error) {
	if len(body) > mqttMaxRemainingLength {
		return nil, fmt.Errorf("mqtt: the packet body is %d bytes, and the protocol allows at most %d", len(body), mqttMaxRemainingLength)
	}
	frame := make([]byte, 0, 5+len(body))
	frame = append(frame, first)
	frame = append(frame, encodeRemainingLength(len(body))...)
	return append(frame, body...), nil
}

// encodeConnect builds the CONNECT packet. keepalive is in seconds, and
// will may be nil.
func encodeConnect(clientID string, keepalive uint16, will *busWill) ([]byte, error) {
	flags := byte(connectCleanSession)
	if will != nil {
		flags |= connectWillFlag
		if will.Retained {
			flags |= connectWillRetain
		}
	}

	body, err := appendField(nil, "protocol name", mqttProtocolName)
	if err != nil {
		return nil, err
	}
	body = append(body, mqttProtocolLevel, flags)
	body = append(body, byte(keepalive>>8), byte(keepalive))
	if body, err = appendField(body, "client identifier", clientID); err != nil {
		return nil, err
	}
	if will != nil {
		if body, err = appendField(body, "will topic", will.Topic); err != nil {
			return nil, err
		}
		if body, err = appendField(body, "will payload", will.Payload); err != nil {
			return nil, err
		}
	}
	return packet(mqttConnect, body)
}

// encodePublish builds a QoS 0 PUBLISH. When retained is true, the
// broker stores the payload as the topic's last value and sends it to
// every later subscriber. A QoS 0 PUBLISH has no packet identifier, and
// the payload has no length field of its own (MQTT 3.1.1 section 3.3.3),
// so only the remaining length limits the payload.
func encodePublish(topic string, payload []byte, retained bool) ([]byte, error) {
	first := byte(mqttPublish)
	if retained {
		first |= mqttRetainFlag
	}
	body, err := appendField(nil, "topic", topic)
	if err != nil {
		return nil, err
	}
	body = append(body, payload...)
	return packet(first, body)
}

// encodeSubscribe builds a SUBSCRIBE for one topic filter at QoS 0. The
// protocol requires bit 1 of a SUBSCRIBE's first byte to be set. The
// broker repeats packetID in its SUBACK.
func encodeSubscribe(packetID uint16, filter string) ([]byte, error) {
	body, err := appendField([]byte{byte(packetID >> 8), byte(packetID)}, "topic filter", filter)
	if err != nil {
		return nil, err
	}
	body = append(body, 0x00)
	return packet(mqttSubscribe|0x02, body)
}

func encodePingreq() []byte {
	return []byte{mqttPingreq, 0x00}
}

// readPacket reads one control packet and returns the first byte of its
// fixed header and its body.
func readPacket(reader *bufio.Reader) (byte, []byte, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	length, err := decodeRemainingLength(reader)
	if err != nil {
		return 0, nil, err
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return 0, nil, err
	}
	return first, body, nil
}

// parseConnack returns an error when the CONNACK's return code, its
// second byte, is not zero.
func parseConnack(body []byte) error {
	if len(body) < 2 {
		return fmt.Errorf("mqtt: the CONNACK body is %d bytes, and it must be 2", len(body))
	}
	if body[1] != 0x00 {
		return fmt.Errorf("mqtt: the broker refused the connection with code %d", body[1])
	}
	return nil
}

// parseSuback returns an error when any return code after the packet
// identifier is 0x80, the code for a refused subscription.
func parseSuback(body []byte) error {
	if len(body) < 3 {
		return fmt.Errorf("mqtt: the SUBACK body is %d bytes, and it must be at least 3", len(body))
	}
	for _, code := range body[2:] {
		if code == 0x80 {
			return fmt.Errorf("mqtt: the broker refused the subscription")
		}
	}
	return nil
}

// parsePublish splits the body of a received PUBLISH into its topic and
// payload. The client subscribes at QoS 0 only, so the broker sends no
// packet identifier and the payload starts right after the topic. ok is
// false when the body is shorter than its topic length says.
func parsePublish(body []byte) (topic string, payload []byte, ok bool) {
	if len(body) < 2 {
		return "", nil, false
	}
	topicLength := int(body[0])<<8 | int(body[1])
	if len(body) < 2+topicLength {
		return "", nil, false
	}
	return string(body[2 : 2+topicLength]), body[2+topicLength:], true
}
