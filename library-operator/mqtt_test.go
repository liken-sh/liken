package main

// These tests cover the wire codec with no socket: every packet is
// built and read back as bytes, so the encoding is proved against the
// protocol and not against a broker.

import (
	"bufio"
	"bytes"
	"reflect"
	"testing"
)

func TestRemainingLengthRoundTrips(t *testing.T) {
	// The boundaries the variable-byte encoding turns on: the last
	// value each byte count can hold, and the first that needs one
	// more byte.
	cases := []struct {
		length int
		bytes  int
	}{
		{length: 0, bytes: 1},
		{length: 127, bytes: 1},
		{length: 128, bytes: 2},
		{length: 16_383, bytes: 2},
		{length: 16_384, bytes: 3},
		{length: 2_097_151, bytes: 3},
		{length: 2_097_152, bytes: 4},
		{length: 268_435_455, bytes: 4},
	}
	for _, each := range cases {
		encoded := encodeRemainingLength(each.length)
		if len(encoded) != each.bytes {
			t.Errorf("length %d encoded to %d bytes, want %d", each.length, len(encoded), each.bytes)
		}
		decoded, err := decodeRemainingLength(bufio.NewReader(bytes.NewReader(encoded)))
		if err != nil {
			t.Fatalf("length %d: %v", each.length, err)
		}
		if decoded != each.length {
			t.Errorf("length %d decoded to %d", each.length, decoded)
		}
	}
}

// A stream that never ends its length is a lost frame, not a large
// packet, so the decoder refuses it after four bytes.
func TestRemainingLengthRefusesAFifthByte(t *testing.T) {
	runaway := []byte{0x80, 0x80, 0x80, 0x80, 0x01}
	if _, err := decodeRemainingLength(bufio.NewReader(bytes.NewReader(runaway))); err == nil {
		t.Fatal("a length past four bytes produced no error")
	}
}

// frameOf returns the frame an encoder built. Every frame a test or a
// fake broker builds is far below the protocol's limit, so an error
// here is a broken encoder, and the panic ends the test.
func frameOf(frame []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return frame
}

// A body past the protocol's limit has no remaining length that a
// broker accepts, so packet refuses it before it allocates the frame.
// The body is never written, so its pages are never touched and the
// test costs no real memory.
func TestPacketRefusesABodyPastTheProtocolLimit(t *testing.T) {
	frame, err := packet(mqttPublish, make([]byte, mqttMaxRemainingLength+1))
	if err == nil {
		t.Fatalf("a body of %d bytes framed to %d bytes, want an error", mqttMaxRemainingLength+1, len(frame))
	}
}

// MQTT 3.1.1 sections 1.5.3 and 3.1.3.3 give a string and a will
// payload two bytes of length, so 65,535 bytes is the longest that
// each field can carry. A longer field would write a length that
// wraps, and the broker would read the rest of the field as the next
// field. Every encoder refuses the field instead.
func TestEncodersRefuseAFieldPastTwoBytesOfLength(t *testing.T) {
	long := string(make([]byte, mqttMaxFieldLength+1))
	cases := []struct {
		name   string
		encode func() ([]byte, error)
	}{
		{name: "a publish topic", encode: func() ([]byte, error) { return encodePublish(long, nil, false) }},
		{name: "a subscribe filter", encode: func() ([]byte, error) { return encodeSubscribe(1, long) }},
		{name: "a client identifier", encode: func() ([]byte, error) { return encodeConnect(long, 30, nil) }},
		{name: "a will topic", encode: func() ([]byte, error) {
			return encodeConnect("op", 30, &busWill{Topic: long, Payload: []byte("offline")})
		}},
		{name: "a will payload", encode: func() ([]byte, error) {
			return encodeConnect("op", 30, &busWill{Topic: "a/b", Payload: []byte(long)})
		}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			frame, err := each.encode()
			if err == nil {
				t.Errorf("a field of %d bytes framed to %d bytes, want an error", len(long), len(frame))
			}
		})
	}
}

// A field of exactly 65,535 bytes is the longest that two bytes of
// length express, so it encodes and reads back whole.
func TestEncodePublishCarriesATopicAtTheFieldLimit(t *testing.T) {
	topic := string(bytes.Repeat([]byte{'t'}, mqttMaxFieldLength))
	frame := frameOf(encodePublish(topic, []byte("hi"), false))

	_, body, err := readPacket(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatal(err)
	}
	gotTopic, payload, ok := parsePublish(body)
	if !ok {
		t.Fatal("the publish body did not parse")
	}
	if gotTopic != topic || string(payload) != "hi" {
		t.Errorf("read back a topic of %d bytes and payload %q, want %d bytes and %q", len(gotTopic), payload, len(topic), "hi")
	}
}

// MQTT 3.1.1 section 3.3.3 gives a PUBLISH payload no length of its
// own: it is the rest of the body. So a payload longer than any
// two-byte field still encodes and reads back whole.
func TestEncodePublishCarriesAPayloadPastTwoBytesOfLength(t *testing.T) {
	sent := bytes.Repeat([]byte{'p'}, mqttMaxFieldLength+1)
	frame := frameOf(encodePublish("a/b", sent, true))

	_, body, err := readPacket(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatal(err)
	}
	topic, payload, ok := parsePublish(body)
	if !ok {
		t.Fatal("the publish body did not parse")
	}
	if topic != "a/b" || !bytes.Equal(payload, sent) {
		t.Errorf("read back topic %q and a payload of %d bytes, want %q and %d bytes", topic, len(payload), "a/b", len(sent))
	}
}

func TestEncodeConnectWithNoWill(t *testing.T) {
	packet := frameOf(encodeConnect("op", 30, nil))

	want := []byte{
		mqttConnect, 0x0E,
		0x00, 0x04, 'M', 'Q', 'T', 'T',
		mqttProtocolLevel,
		connectCleanSession,
		0x00, 0x1E,
		0x00, 0x02, 'o', 'p',
	}
	if !bytes.Equal(packet, want) {
		t.Errorf("connect = %v, want %v", packet, want)
	}
}

// A will sets three things: the will flag, the will retain bit when
// the will is retained, and the topic and payload in the payload after
// the client identifier.
func TestEncodeConnectCarriesARetainedWill(t *testing.T) {
	packet := frameOf(encodeConnect("op", 30, &busWill{
		Topic:    "a/b",
		Payload:  []byte("offline"),
		Retained: true,
	}))

	flags := packet[9]
	if flags&connectWillFlag == 0 {
		t.Errorf("flags = %#x, want the will flag set", flags)
	}
	if flags&connectWillRetain == 0 {
		t.Errorf("flags = %#x, want the will retain bit set", flags)
	}
	if !bytes.Contains(packet, []byte("a/b")) || !bytes.Contains(packet, []byte("offline")) {
		t.Errorf("connect = %v, want the will topic and payload", packet)
	}
}

func TestEncodePublishSetsTheRetainBit(t *testing.T) {
	plain := frameOf(encodePublish("a/b", []byte("hi"), false))
	if plain[0] != mqttPublish {
		t.Errorf("first byte = %#x, want %#x", plain[0], mqttPublish)
	}
	retained := frameOf(encodePublish("a/b", []byte("hi"), true))
	if retained[0] != mqttPublish|0x01 {
		t.Errorf("first byte = %#x, want the retain bit set", retained[0])
	}
	// The body is the length-prefixed topic and then the raw payload,
	// with no packet identifier at QoS 0.
	want := []byte{mqttPublish, 0x07, 0x00, 0x03, 'a', '/', 'b', 'h', 'i'}
	if !bytes.Equal(plain, want) {
		t.Errorf("publish = %v, want %v", plain, want)
	}
}

func TestEncodeSubscribeNamesOneFilterAtQoSZero(t *testing.T) {
	packet := frameOf(encodeSubscribe(1, "a/+/c"))

	want := []byte{
		mqttSubscribe | 0x02, 0x0A,
		0x00, 0x01,
		0x00, 0x05, 'a', '/', '+', '/', 'c',
		0x00,
	}
	if !bytes.Equal(packet, want) {
		t.Errorf("subscribe = %v, want %v", packet, want)
	}
}

func TestEncodePingreqIsTwoBytes(t *testing.T) {
	if want := []byte{mqttPingreq, 0x00}; !bytes.Equal(encodePingreq(), want) {
		t.Errorf("pingreq = %v, want %v", encodePingreq(), want)
	}
}

// A PUBLISH encoded by this codec reads back through readPacket and
// parsePublish as the topic and payload it went in as.
func TestReadPacketReadsBackAPublish(t *testing.T) {
	frame := frameOf(encodePublish("libraries/house/movies/status", []byte(`{"titles":12}`), true))
	reader := bufio.NewReader(bytes.NewReader(frame))

	first, body, err := readPacket(reader)
	if err != nil {
		t.Fatal(err)
	}
	if first&0xF0 != mqttPublish {
		t.Errorf("type = %#x, want a publish", first)
	}
	topic, payload, ok := parsePublish(body)
	if !ok {
		t.Fatal("the publish body did not parse")
	}
	if topic != "libraries/house/movies/status" {
		t.Errorf("topic = %q", topic)
	}
	if string(payload) != `{"titles":12}` {
		t.Errorf("payload = %q", payload)
	}
}

func TestParseConnackReadsTheReturnCode(t *testing.T) {
	if err := parseConnack([]byte{0x00, 0x00}); err != nil {
		t.Errorf("an accepted connack was refused: %v", err)
	}
	if err := parseConnack([]byte{0x00, 0x05}); err == nil {
		t.Error("a refused connack produced no error")
	}
	if err := parseConnack([]byte{0x00}); err == nil {
		t.Error("a short connack produced no error")
	}
}

func TestParseSubackReadsTheGrantedCodes(t *testing.T) {
	if err := parseSuback([]byte{0x00, 0x01, 0x00}); err != nil {
		t.Errorf("a granted suback was refused: %v", err)
	}
	if err := parseSuback([]byte{0x00, 0x01, 0x80}); err == nil {
		t.Error("a refused suback produced no error")
	}
}

// A topic longer than its body claims is a torn frame, and parsePublish
// refuses it rather than reading past the body.
func TestParsePublishRefusesAShortBody(t *testing.T) {
	if _, _, ok := parsePublish([]byte{0x00, 0x05, 'a'}); ok {
		t.Error("a short publish body parsed")
	}
	if _, _, ok := parsePublish([]byte{0x00}); ok {
		t.Error("a one-byte publish body parsed")
	}
}

// A CONNECT built here reads back with the fields it went in as, so
// the encoder and reader agree on framing.
func TestReadPacketReadsBackAConnect(t *testing.T) {
	frame := frameOf(encodeConnect("library-operator", 30, nil))
	first, body, err := readPacket(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatal(err)
	}
	if first != mqttConnect {
		t.Errorf("type = %#x, want a connect", first)
	}
	head := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', mqttProtocolLevel, connectCleanSession}
	if !reflect.DeepEqual(body[:len(head)], head) {
		t.Errorf("connect head = %v, want %v", body[:len(head)], head)
	}
}

// A stream that ends inside a fixed header is a broken connection, not
// a packet, and every read of one returns the error rather than a
// partial packet.
func TestReadPacketRefusesATruncatedStream(t *testing.T) {
	cases := []struct {
		name  string
		bytes []byte
	}{
		{name: "a first byte and no length", bytes: []byte{mqttPublish}},
		{name: "a length longer than the body", bytes: []byte{mqttPublish, 0x05, 'a'}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			_, _, err := readPacket(bufio.NewReader(bytes.NewReader(each.bytes)))
			if err == nil {
				t.Error("a truncated stream produced no error")
			}
		})
	}
}

// A SUBACK carries a packet identifier and at least one return code, so
// a shorter body is a torn frame.
func TestParseSubackRefusesAShortBody(t *testing.T) {
	if err := parseSuback([]byte{0x00}); err == nil {
		t.Error("a short suback produced no error")
	}
}
