package main

// bussession.go is a short connection to the broker, for a container or a
// pass that has a fixed set of retained messages to write, or one retained
// message to read, and then ends. Bus (bus.go) holds a connection open for a
// pod's life, reconnects, and drops a publish its queue cannot take, which is
// correct for a report that the next connection publishes again. A work list
// is thousands of messages published once, so a session writes each one to
// the socket itself and blocks until the socket takes it.
//
// The broker reads one connection's packets in order and answers them in
// order. So the PINGRESP to a PINGREQ sent after a run of publishes proves
// that the broker has read every one of them, and the PINGRESP to a PINGREQ
// sent after a SUBSCRIBE proves that it has sent every retained message the
// filter matches.

import (
	"bufio"
	"context"
	"fmt"
	"net"
)

// One session: the connection, and the stop of the watch that closes the
// connection when the caller's context ends.
type busSession struct {
	conn   net.Conn
	reader *bufio.Reader
	stop   func() bool
}

// Dials the broker and completes the CONNECT handshake. The caller's deadline
// bounds every read and write of the session, so a broker that accepts the
// connection and never answers holds the caller no longer than that.
//
// The client identifier must differ from every other client's, because the
// broker disconnects a client when another connects with its identifier.
func openBusSession(ctx context.Context, dial func(context.Context) (net.Conn, error),
	clientID string) (*busSession, error) {
	conn, err := dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("dialing the broker: %w", err)
	}
	if deadline, held := ctx.Deadline(); held {
		conn.SetDeadline(deadline)
	}
	session := &busSession{conn: conn, reader: bufio.NewReader(conn)}
	session.stop = context.AfterFunc(ctx, func() { conn.Close() })
	if _, err := conn.Write(encodeConnect(clientID, busKeepalive, nil)); err != nil {
		session.close()
		return nil, fmt.Errorf("connecting to the broker: %w", err)
	}
	first, body, err := readPacket(session.reader)
	if err == nil && first&0xF0 != mqttConnack {
		err = fmt.Errorf("the first packet has type %#x, want a CONNACK", first&0xF0)
	}
	if err == nil {
		err = parseConnack(body)
	}
	if err != nil {
		session.close()
		return nil, fmt.Errorf("connecting to the broker: %w", err)
	}
	return session, nil
}

// Writes one retained message. An empty payload clears the topic.
func (s *busSession) retain(topic string, payload []byte) error {
	if _, err := s.conn.Write(encodePublish(topic, payload, true)); err != nil {
		return fmt.Errorf("publishing to the broker: %w", err)
	}
	return nil
}

// Returns once the broker has read every packet the session wrote before it.
func (s *busSession) flush() error {
	_, _, err := s.ping("")
	return err
}

// The retained message on one topic, and whether there is one. An empty
// retained message is a cleared topic, so it reads as none.
func (s *busSession) readRetained(topic string) ([]byte, bool, error) {
	if _, err := s.conn.Write(encodeSubscribe(1, topic)); err != nil {
		return nil, false, fmt.Errorf("subscribing on the broker: %w", err)
	}
	return s.ping(topic)
}

// Sends a PINGREQ and reads until its PINGRESP, and returns the last message
// on the topic given that arrived before it. A refused subscription is an
// error, because the read would otherwise report a message that exists as
// absent.
func (s *busSession) ping(topic string) ([]byte, bool, error) {
	if _, err := s.conn.Write(encodePingreq()); err != nil {
		return nil, false, fmt.Errorf("writing to the broker: %w", err)
	}
	var payload []byte
	for {
		first, body, err := readPacket(s.reader)
		if err != nil {
			return nil, false, fmt.Errorf("reading from the broker: %w", err)
		}
		switch first & 0xF0 {
		case mqttPublish:
			if got, message, ok := parsePublish(body); ok && got == topic {
				payload = message
			}
		case mqttSuback:
			if err := parseSuback(body); err != nil {
				return nil, false, err
			}
		case mqttPingresp:
			return payload, len(payload) > 0, nil
		}
	}
}

// Ends the session with a DISCONNECT, so the broker logs a clean close.
func (s *busSession) close() {
	s.stop()
	s.conn.Write(encodeDisconnect())
	s.conn.Close()
}

// The broker a Job's container reaches, and the base its topics extend. The
// operator names both in the container's environment, as it does for the
// reporter.
type busEndpoint struct {
	address, base string
}

// The variables that carry the endpoint.
func (e busEndpoint) env() []EnvVar {
	return []EnvVar{{Name: busAddressVariable, Value: e.address}, {Name: topicBaseVariable, Value: e.base}}
}

// The endpoint a container reads from its environment, on the default base
// where it names none.
func busEndpointOf(address, base string) busEndpoint {
	if base == "" {
		base = defaultTopicBase
	}
	return busEndpoint{address: address, base: base}
}

// Dials the broker over TCP.
func (e busEndpoint) dial(ctx context.Context) (net.Conn, error) {
	dialer := &net.Dialer{}
	return dialer.DialContext(ctx, "tcp", e.address)
}
