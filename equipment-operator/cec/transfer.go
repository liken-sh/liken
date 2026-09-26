package cec

// Sending and receiving messages, and reading the adapter's events.

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// A Result is what one transmit did on the wire. Acked says that the
// destination pulled the acknowledge bit, which is the whole answer to
// a poll. Nacked says that the destination left the bit unset, which
// is the one failure that means no device holds the address; a lost
// arbitration or a wire error says nothing about the destination. Reply is the answer a request waited for, and it is nil when
// the request asked for none, when the reply did not arrive in time,
// and when the destination answered with a Feature Abort.
type Result struct {
	Acked   bool
	Nacked  bool
	Aborted bool
	Reply   *Message
	// Status describes a transmit that failed, in the kernel's words,
	// so a log line says why.
	Status string
}

// Transmit sends one message and, when reply is not zero, waits up to
// timeout for the destination's answer with that opcode. The handle
// must be blocking, because a non-blocking transmit returns before the
// message is on the wire.
//
// A message the destination does not acknowledge is not an error: it
// is a Result with Acked false. The error is for a call the kernel
// refused.
func (d *Device) Transmit(message Message, reply Opcode, timeout time.Duration) (Result, error) {
	raw := KernelMsg{}
	if err := encode(&raw, message); err != nil {
		return Result{}, err
	}
	if reply != 0 {
		raw.Reply = byte(reply)
		raw.Timeout = uint32(timeout / time.Millisecond)
	}
	if err := d.ioctl(RequestTransmit, unsafe.Pointer(&raw)); err != nil {
		return Result{}, err
	}
	result := Result{
		Acked:   raw.TxStatus&TxStatusOK != 0,
		Nacked:  raw.TxStatus&TxStatusNack != 0,
		Aborted: raw.RxStatus&RxStatusFeatureAbort != 0,
		Status:  txStatusText(raw.TxStatus),
	}
	if reply != 0 && raw.RxStatus&RxStatusOK != 0 && !result.Aborted {
		answer := decode(&raw)
		result.Reply = &answer
	}
	return result, nil
}

// encode fills the kernel's message from ours.
func encode(raw *KernelMsg, message Message) error {
	if len(message.Body)+1 > maxMessageSize {
		return fmt.Errorf("a CEC message holds at most %d bytes after its header, and this one holds %d", maxMessageSize-1, len(message.Body))
	}
	raw.Msg[0] = byte(message.From)<<4 | byte(message.To)&0xf
	copy(raw.Msg[1:], message.Body)
	raw.Len = uint32(len(message.Body) + 1)
	return nil
}

// decode reads a message out of the kernel's.
func decode(raw *KernelMsg) Message {
	length := min(int(raw.Len), maxMessageSize)
	message := Message{
		From: LogicalAddress(raw.Msg[0] >> 4),
		To:   LogicalAddress(raw.Msg[0] & 0xf),
	}
	if length > 1 {
		message.Body = append([]byte(nil), raw.Msg[1:length]...)
	}
	return message
}

// txStatusText names the failure bits of a transmit, the words the
// kernel documentation uses for each bit.
func txStatusText(status uint8) string {
	names := []struct {
		bit  uint8
		name string
	}{
		{TxStatusArbLost, "ARB_LOST"},
		{TxStatusNack, "NACK"},
		{TxStatusLowDrive, "LOW_DRIVE"},
		{TxStatusError, "ERROR"},
		{TxStatusMaxRetries, "MAX_RETRIES"},
		{TxStatusAborted, "ABORTED"},
		{TxStatusTimeout, "TIMEOUT"},
	}
	text := ""
	for _, each := range names {
		if status&each.bit == 0 {
			continue
		}
		if text != "" {
			text += " "
		}
		text += each.name
	}
	return text
}

// ErrNothingReceived says that no message arrived within the timeout.
var ErrNothingReceived = errors.New("no CEC message arrived")

// Receive reads the next message the kernel queued for this handle,
// waiting up to timeout. A monitor receives every message it can hear,
// and a follower receives the ones the kernel does not answer.
func (d *Device) Receive(timeout time.Duration) (Message, error) {
	raw := KernelMsg{Timeout: uint32(timeout / time.Millisecond)}
	if err := d.ioctl(RequestReceive, unsafe.Pointer(&raw)); err != nil {
		if errors.Is(err, unix.ETIMEDOUT) || errors.Is(err, unix.EAGAIN) {
			return Message{}, ErrNothingReceived
		}
		return Message{}, err
	}
	return decode(&raw), nil
}

// An Event is a change the kernel reports about the adapter itself.
// A state change carries the adapter's new physical address and the
// logical addresses it now holds. Lost counts messages the kernel
// dropped because this handle did not read them in time.
type Event struct {
	StateChange bool
	Physical    PhysicalAddress
	LogicalMask uint16
	Lost        uint32
}

// NextEvent reads the oldest event the kernel queued for this handle.
// Call it after Wait reports an event; on a blocking handle it waits
// for one otherwise.
func (d *Device) NextEvent() (Event, error) {
	var raw KernelEvent
	if err := d.ioctl(RequestDequeueEvent, unsafe.Pointer(&raw)); err != nil {
		return Event{}, err
	}
	switch raw.Event {
	case EventStateChange:
		// struct cec_event_state_change starts the union with two
		// __u16 fields: the physical address, then the logical address
		// mask. The first uint32 of the union holds both, the address
		// in its low half on a little-endian machine, which every
		// machine liken runs on is.
		return Event{
			StateChange: true,
			Physical:    PhysicalAddress(raw.Raw[0] & 0xffff),
			LogicalMask: uint16(raw.Raw[0] >> 16),
		}, nil
	case EventLostMsgs:
		return Event{Lost: raw.Raw[0]}, nil
	}
	return Event{}, nil
}
