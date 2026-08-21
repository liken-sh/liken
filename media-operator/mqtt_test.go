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

func TestEncodeConnectWithNoWill(t *testing.T) {
	packet := encodeConnect("op", 30, nil)

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
	packet := encodeConnect("op", 30, &busWill{
		Topic:    "a/b",
		Payload:  []byte("offline"),
		Retained: true,
	})

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
	plain := encodePublish("a/b", []byte("hi"), false)
	if plain[0] != mqttPublish {
		t.Errorf("first byte = %#x, want %#x", plain[0], mqttPublish)
	}
	retained := encodePublish("a/b", []byte("hi"), true)
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
	packet := encodeSubscribe(1, "a/+/c")

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
	frame := encodePublish("plays/house/movie/status", []byte(`{"item":1}`), true)
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
	if topic != "plays/house/movie/status" {
		t.Errorf("topic = %q", topic)
	}
	if string(payload) != `{"item":1}` {
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
	frame := encodeConnect("media-operator", 30, nil)
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
