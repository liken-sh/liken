package main

// The MQTT 3.1.1 wire codec, written straight against the protocol
// the way apiclient.go writes the Kubernetes client. The broker is
// Mosquitto and the protocol is a published standard, so a client
// that speaks the few packets this operator needs costs less than a
// third-party library and its release cadence. This operator uses
// QoS 0 alone: input events are momentary and a report is republished
// on every reconnect, so the delivery guarantees of QoS 1 and QoS 2
// buy nothing here.
//
// The functions below build and read whole packets and hold no
// connection state. bus.go owns the socket and drives them.

import (
	"bufio"
	"fmt"
	"io"
)

// The MQTT control packet types, in the high nibble of a fixed
// header's first byte. The low nibble carries per-type flags, which
// matter here only for a PUBLISH, where bit 0 is the retain flag.
const (
	mqttConnect   = 0x10
	mqttConnack   = 0x20
	mqttPublish   = 0x30
	mqttSubscribe = 0x80
	mqttSuback    = 0x90
	mqttPingreq   = 0xC0
	mqttPingresp  = 0xD0
)

// The MQTT 3.1.1 protocol name and level. The name is the literal
// string "MQTT" and the level is 4, which together tell the broker
// which version of the protocol this client speaks.
const (
	mqttProtocolName  = "MQTT"
	mqttProtocolLevel = 0x04
)

// The CONNECT flag bits this client sets. Clean session starts every
// connection with no server-side state, which is correct because the
// client re-subscribes and re-publishes on each connect. The will
// bits arrive only when the caller states a will. Username and
// password stay unset, because the in-cluster network is the trust
// boundary and the broker accepts the cluster's own pods.
const (
	connectCleanSession = 0x02
	connectWillFlag     = 0x04
	connectWillRetain   = 0x20
)

// encodeRemainingLength writes the packet length in the variable-byte
// form the protocol uses: seven bits of length per byte, and the high
// bit set on every byte but the last. One byte covers up to 127, and
// four bytes cover the 268435455 the protocol allows.
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

// decodeRemainingLength reads the variable-byte length back. It reads
// at most four bytes, because a fifth would exceed the protocol's
// limit and marks a stream that has lost frame alignment.
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
	return 0, fmt.Errorf("mqtt: remaining length runs past four bytes")
}

// appendString writes one length-prefixed UTF-8 string, the shape the
// protocol uses for a topic, a filter, and the client identifier: two
// bytes of length, most significant first, then the bytes.
func appendString(buffer []byte, value string) []byte {
	buffer = append(buffer, byte(len(value)>>8), byte(len(value)))
	return append(buffer, value...)
}

// appendBytes writes one length-prefixed byte string, the shape a will
// payload takes.
func appendBytes(buffer []byte, value []byte) []byte {
	buffer = append(buffer, byte(len(value)>>8), byte(len(value)))
	return append(buffer, value...)
}

// packet frames one control packet: a fixed-header first byte, then
// the remaining length, then the body. Every encode function ends
// here, so the length is computed once from the finished body.
func packet(first byte, body []byte) []byte {
	frame := make([]byte, 0, 2+len(body))
	frame = append(frame, first)
	frame = append(frame, encodeRemainingLength(len(body))...)
	return append(frame, body...)
}

// encodeConnect builds the first packet the client sends. The body is
// the protocol name and level, one flags byte, the keepalive in
// seconds, and the payload: the client identifier and, when the caller
// states one, the will topic and payload. The client authenticates
// with nothing more than its identifier, because the broker accepts
// the cluster's own pods.
func encodeConnect(clientID string, keepalive uint16, will *busWill) []byte {
	flags := byte(connectCleanSession)
	if will != nil {
		flags |= connectWillFlag
		if will.Retained {
			flags |= connectWillRetain
		}
	}

	var body []byte
	body = appendString(body, mqttProtocolName)
	body = append(body, mqttProtocolLevel, flags)
	body = append(body, byte(keepalive>>8), byte(keepalive))
	body = appendString(body, clientID)
	if will != nil {
		body = appendString(body, will.Topic)
		body = appendBytes(body, will.Payload)
	}
	return packet(mqttConnect, body)
}

// encodePublish builds a QoS 0 PUBLISH. The retain flag is bit 0 of
// the first byte, and a retained publish tells the broker to hold this
// payload as the topic's last value and deliver it to every later
// subscriber. QoS 0 carries no packet identifier, so the body is the
// length-prefixed topic and then the raw payload.
func encodePublish(topic string, payload []byte, retained bool) []byte {
	first := byte(mqttPublish)
	if retained {
		first |= 0x01
	}
	body := appendString(nil, topic)
	body = append(body, payload...)
	return packet(first, body)
}

// encodeSubscribe builds a SUBSCRIBE for one topic filter at QoS 0.
// The fixed header is 0x82, because bit 1 is reserved and must be set
// on a SUBSCRIBE. The body is the packet identifier the broker echoes
// in its SUBACK, then the length-prefixed filter and one byte of
// requested QoS.
func encodeSubscribe(packetID uint16, filter string) []byte {
	body := []byte{byte(packetID >> 8), byte(packetID)}
	body = appendString(body, filter)
	body = append(body, 0x00)
	return packet(mqttSubscribe|0x02, body)
}

// encodePingreq builds the keepalive packet. It carries no body, so it
// is the two bytes 0xC0 0x00, and the broker answers with a PINGRESP.
func encodePingreq() []byte {
	return []byte{mqttPingreq, 0x00}
}

// readPacket reads one whole control packet: the fixed-header first
// byte, the remaining length, and that many bytes of body. It returns
// the first byte so the caller reads both the packet type in the high
// nibble and the flags in the low nibble.
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

// parseConnack reads the broker's answer to a CONNECT. The body is one
// byte of acknowledge flags and one byte of return code, and a return
// code other than zero is the broker refusing the connection.
func parseConnack(body []byte) error {
	if len(body) < 2 {
		return fmt.Errorf("mqtt: a CONNACK carried %d bytes, want 2", len(body))
	}
	if body[1] != 0x00 {
		return fmt.Errorf("mqtt: the broker refused the connection with code %d", body[1])
	}
	return nil
}

// parseSuback reads the broker's answer to a SUBSCRIBE. The body is the
// echoed packet identifier and one return code per filter, and a
// return code of 0x80 is the broker refusing that subscription.
func parseSuback(body []byte) error {
	if len(body) < 3 {
		return fmt.Errorf("mqtt: a SUBACK carried %d bytes, want at least 3", len(body))
	}
	for _, code := range body[2:] {
		if code == 0x80 {
			return fmt.Errorf("mqtt: the broker refused a subscription")
		}
	}
	return nil
}

// parsePublish reads an inbound PUBLISH body into its topic and
// payload. The client subscribes at QoS 0 alone, so an inbound publish
// carries no packet identifier and the payload begins right after the
// length-prefixed topic.
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
