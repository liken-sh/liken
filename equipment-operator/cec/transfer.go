package cec

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// A Result is the outcome of one transmit. Acked is true when the
// destination pulled the acknowledge bit low, which is all that a poll
// checks. Nacked is true when no device pulled the bit low. Only Nacked
// shows that no device uses the address: a lost arbitration or a wire
// error says nothing about the destination. Reply is nil when the
// request waited for no reply, when no reply arrived in time, and when
// the destination replied with a Feature Abort.
type Result struct {
	Acked   bool
	Nacked  bool
	Aborted bool
	Reply   *Message
	// Status lists the kernel's names for the failure bits of the
	// transmit, for log lines.
	Status string
}

// Transmit sends one message. When reply is not zero, it also waits up
// to timeout for a message with that opcode from the destination. The
// handle must be blocking, because on a non-blocking handle the call
// returns before the message is on the wire.
//
// A message that the destination does not acknowledge is not an error.
// It returns a Result with Acked false. The error is for a call that
// the kernel refuses.
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

func encode(raw *KernelMsg, message Message) error {
	if len(message.Body)+1 > maxMessageSize {
		return fmt.Errorf("a CEC message holds at most %d bytes after its header, and this one holds %d", maxMessageSize-1, len(message.Body))
	}
	raw.Msg[0] = byte(message.From)<<4 | byte(message.To)&0xf
	copy(raw.Msg[1:], message.Body)
	raw.Len = uint32(len(message.Body) + 1)
	return nil
}

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

// txStatusText joins the names of the failure bits set in status, as the
// kernel documentation spells them.
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

// ErrNothingReceived is returned by Receive when no message arrives
// within the timeout.
var ErrNothingReceived = errors.New("no CEC message arrived")

// Receive returns the next message the kernel queued for this handle,
// waiting up to timeout. A monitor handle receives every message on the
// bus. A follower handle receives only the messages that the kernel does
// not answer itself.
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

// An Event is something the kernel reports about the adapter itself.
// For a state change, Physical is the adapter's new physical address and
// LogicalMask is the set of logical addresses it now holds. Lost is the
// number of messages the kernel dropped because this handle did not read
// them in time.
type Event struct {
	StateChange bool
	Physical    PhysicalAddress
	LogicalMask uint16
	Lost        uint32
}

// NextEvent returns the oldest event the kernel queued for this handle.
// Call it after Wait reports an event. On a blocking handle it waits for
// an event when none is queued.
func (d *Device) NextEvent() (Event, error) {
	var raw KernelEvent
	if err := d.ioctl(RequestDequeueEvent, unsafe.Pointer(&raw)); err != nil {
		return Event{}, err
	}
	switch raw.Event {
	case EventStateChange:
		// struct cec_event_state_change starts the union with two __u16
		// fields: the physical address, then the logical address mask.
		// Raw[0] covers both, with the physical address in the low half
		// on a little-endian machine, as every machine `liken` runs on
		// is.
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
