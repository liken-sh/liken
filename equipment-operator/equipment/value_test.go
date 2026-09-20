package equipment

import (
	"encoding/json"
	"testing"
)

func TestSettingValueMarshalsToABareScalar(t *testing.T) {
	cases := []struct {
		name  string
		value SettingValue
		want  string
	}{
		{"a boolean", BoolSettingValue(true), "true"},
		{"a number", NumberSettingValue(3), "3"},
		{"a string", StringSettingValue("auto"), `"auto"`},
		{"an empty value", SettingValue{}, "null"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			raw, err := json.Marshal(one.value)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			if string(raw) != one.want {
				t.Errorf("got %s, want %s", raw, one.want)
			}
		})
	}
}

func TestSettingValueUnmarshalRejectsBadScalars(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		t.Run(raw, func(t *testing.T) {
			var value SettingValue
			if err := json.Unmarshal([]byte(raw), &value); err == nil {
				t.Errorf("unmarshal of %q did not error", raw)
			}
		})
	}
}

func TestSettingValueUnmarshalsFromABareScalar(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		check func(SettingValue) bool
	}{
		{"a boolean", "false", func(v SettingValue) bool {
			got, ok := v.Bool()
			return ok && !got
		}},
		{"a number", "3.5", func(v SettingValue) bool {
			got, ok := v.Number()
			return ok && got == 3.5
		}},
		{"a string", `"auto"`, func(v SettingValue) bool {
			got, ok := v.String()
			return ok && got == "auto"
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			var value SettingValue
			if err := json.Unmarshal([]byte(one.raw), &value); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if !one.check(value) {
				t.Errorf("unmarshal of %s read the wrong kind", one.raw)
			}
		})
	}
}
