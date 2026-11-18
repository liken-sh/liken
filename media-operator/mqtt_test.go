package main

import (
	"bufio"
	"bytes"
	"reflect"
	"testing"
)

func TestRemainingLengthRoundTrips(t *testing.T) {
	// The largest value that fits in each number of bytes, and the
	// smallest value that needs one more byte.
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

func TestRemainingLengthRefusesAFifthByte(t *testing.T) {
	runaway := []byte{0x80, 0x80, 0x80, 0x80, 0x01}
	if _, err := decodeRemainingLength(bufio.NewReader(bytes.NewReader(runaway))); err == nil {
		t.Fatal("a length past four bytes produced no error")
	}
}

// frameOf panics on an encoder's error, because the frames in these
// tests are far below the protocol's limits.
func frameOf(frame []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return frame
}

// The test never writes to the body, so the operating system does not
// allocate its pages.
func TestPacketRefusesABodyPastTheProtocolLimit(t *testing.T) {
	frame, err := packet(mqttPublish, make([]byte, mqttMaxRemainingLength+1))
	if err == nil {
		t.Fatalf("a body of %d bytes framed to %d bytes, want an error", mqttMaxRemainingLength+1, len(frame))
	}
}

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

func TestEncodePublishCarriesATopicAtTheFieldLimit(t *testing.T) {
	topic := string(bytes.Repeat([]byte{'t'}, mqttMaxFieldLength))
	frame := frameOf(encodePublish(topic, []byte("hi"), false))

	_, body, err := readPacket(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatalf("reading the PUBLISH packet: %v", err)
	}
	gotTopic, payload, ok := parsePublish(body)
	if !ok {
		t.Fatal("the publish body did not parse")
	}
	if gotTopic != topic || string(payload) != "hi" {
		t.Errorf("read back a topic of %d bytes and payload %q, want %d bytes and %q", len(gotTopic), payload, len(topic), "hi")
	}
}

func TestEncodePublishCarriesAPayloadPastTwoBytesOfLength(t *testing.T) {
	sent := bytes.Repeat([]byte{'p'}, mqttMaxFieldLength+1)
	frame := frameOf(encodePublish("a/b", sent, true))

	_, body, err := readPacket(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatalf("reading the PUBLISH packet: %v", err)
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

// A will's topic and payload follow the client identifier.
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
	// At QoS 0 the body is the topic with its length prefix and then the
	// payload, with no packet identifier.
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

func TestReadPacketReadsBackAPublish(t *testing.T) {
	frame := frameOf(encodePublish("plays/house/movie/status", []byte(`{"item":1}`), true))
	reader := bufio.NewReader(bytes.NewReader(frame))

	first, body, err := readPacket(reader)
	if err != nil {
		t.Fatalf("reading the PUBLISH packet: %v", err)
	}
	if first&0xF0 != mqttPublish {
		t.Errorf("type = %#x, want a publish", first)
	}
	topic, payload, ok := parsePublish(body)
	if !ok {
		t.Fatal("the publish body did not parse")
	}
	if topic != "plays/house/movie/status" {
		t.Errorf("topic = %q, want plays/house/movie/status", topic)
	}
	if string(payload) != `{"item":1}` {
		t.Errorf("payload = %q, want {\"item\":1}", payload)
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

func TestParsePublishRefusesAShortBody(t *testing.T) {
	if _, _, ok := parsePublish([]byte{0x00, 0x05, 'a'}); ok {
		t.Error("a short publish body parsed")
	}
	if _, _, ok := parsePublish([]byte{0x00}); ok {
		t.Error("a one-byte publish body parsed")
	}
}

func TestReadPacketReadsBackAConnect(t *testing.T) {
	frame := frameOf(encodeConnect("media-operator", 30, nil))
	first, body, err := readPacket(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatalf("reading the CONNECT packet: %v", err)
	}
	if first != mqttConnect {
		t.Errorf("type = %#x, want a connect", first)
	}
	head := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', mqttProtocolLevel, connectCleanSession}
	if !reflect.DeepEqual(body[:len(head)], head) {
		t.Errorf("connect head = %v, want %v", body[:len(head)], head)
	}
}
