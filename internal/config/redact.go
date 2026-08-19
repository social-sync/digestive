package config

import (
	"reflect"

	"gopkg.in/yaml.v3"
)

// RedactedMarker replaces the value of any field tagged `redact:"true"`.
const RedactedMarker = "***REDACTED***"

// Redacted returns the effective configuration as a generic tree with every
// sensitive field (tagged `redact:"true"`) blanked, suitable for embedding in
// an audit record. It never mutates the receiver.
//
// The config is deep-copied via a YAML round-trip, the copy is walked and
// redacted in place, then re-serialised to a map keyed by the YAML field names
// so the embedded config reads exactly like config.yaml (snake_case keys).
func (c *Config) Redacted() (map[string]any, error) {
	raw, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}
	var cp Config
	if err := yaml.Unmarshal(raw, &cp); err != nil {
		return nil, err
	}

	redactValue(reflect.ValueOf(&cp).Elem())

	redacted, err := yaml.Marshal(&cp)
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err := yaml.Unmarshal(redacted, &tree); err != nil {
		return nil, err
	}
	return tree, nil
}

// redactValue walks v, replacing any non-empty string field tagged
// `redact:"true"` with RedactedMarker. It recurses through pointers, structs,
// and slices. Maps are skipped: no sensitive fields live inside map values, and
// their entries are not addressable for in-place mutation.
func redactValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Ptr:
		if !v.IsNil() {
			redactValue(v.Elem())
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			fv := v.Field(i)
			if field.Tag.Get("redact") == "true" && fv.Kind() == reflect.String {
				if fv.CanSet() && fv.String() != "" {
					fv.SetString(RedactedMarker)
				}
				continue
			}
			redactValue(fv)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			redactValue(v.Index(i))
		}
	}
}
