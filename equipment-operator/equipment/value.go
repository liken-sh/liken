package equipment

// A setting the bus carries is a bare JSON scalar: a boolean, a
// number, or a string. SettingValue holds one of those three and
// marshals to and from the scalar, so a driver's Set and Do translate
// the value a message names. The scalar shape is protocol-neutral, so
// a second driver reads the same id table without importing denon.

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SettingValue is one setting as a bare JSON scalar. It is not the
// Driver interface, because a protocol that sets nothing still
// implements Driver; the value type is the vocabulary a protocol that
// sets something speaks.
type SettingValue struct {
	b bool
	n float64
	s string
	t settingKind
}

type settingKind uint8

const (
	kindEmpty settingKind = iota
	kindBool
	kindNumber
	kindString
)

// BoolSettingValue builds a boolean setting value.
func BoolSettingValue(v bool) SettingValue {
	return SettingValue{t: kindBool, b: v}
}

// NumberSettingValue builds a numeric setting value.
func NumberSettingValue(v float64) SettingValue {
	return SettingValue{t: kindNumber, n: v}
}

// StringSettingValue builds a string setting value.
func StringSettingValue(v string) SettingValue {
	return SettingValue{t: kindString, s: v}
}

// Bool answers the value as a boolean and whether it is one.
func (v SettingValue) Bool() (bool, bool) {
	return v.b, v.t == kindBool
}

// Number answers the value as a number and whether it is one.
func (v SettingValue) Number() (float64, bool) {
	return v.n, v.t == kindNumber
}

// String answers the value as a string and whether it is one.
func (v SettingValue) String() (string, bool) {
	return v.s, v.t == kindString
}

// MarshalJSON writes the value as the bare scalar it holds.
func (v SettingValue) MarshalJSON() ([]byte, error) {
	switch v.t {
	case kindBool:
		return json.Marshal(v.b)
	case kindNumber:
		return json.Marshal(v.n)
	case kindString:
		return json.Marshal(v.s)
	}
	return []byte("null"), nil
}

// UnmarshalJSON reads a bare JSON scalar into the matching kind.
func (v *SettingValue) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("empty setting value")
	}
	switch trimmed[0] {
	case '"':
		v.t = kindString
		return json.Unmarshal(trimmed, &v.s)
	case 't', 'f':
		v.t = kindBool
		return json.Unmarshal(trimmed, &v.b)
	default:
		v.t = kindNumber
		return json.Unmarshal(trimmed, &v.n)
	}
}
