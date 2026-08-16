package transform

import (
	"fmt"
	"unicode/utf8"

	"github.com/social-sync/digestive/internal/value"
)

// null replaces any value with SQL NULL.
func newNull() Transformer {
	return TransformerFunc(func(value.Value) (value.Value, error) {
		return value.Null, nil
	})
}

// constant replaces any non-null value with a fixed literal. NULL is left as
// NULL (redacting a NULL to a literal would fabricate data).
func newConstant(spec Spec) (Transformer, error) {
	if spec.Value == nil {
		return nil, fmt.Errorf("constant transform requires a 'value'")
	}
	lit := *spec.Value
	return TransformerFunc(func(v value.Value) (value.Value, error) {
		if v.Null {
			return v, nil
		}
		return value.Text(lit), nil
	}), nil
}

// mask keeps the first KeepFirst and last KeepLast runes and replaces the rest
// with MaskChar (default '*'). NULL passes through unchanged.
func newMask(spec Spec) (Transformer, error) {
	fill := "*"
	if spec.MaskChar != "" {
		if utf8.RuneCountInString(spec.MaskChar) != 1 {
			return nil, fmt.Errorf("mask 'mask_char' must be a single character, got %q", spec.MaskChar)
		}
		fill = spec.MaskChar
	}
	if spec.KeepFirst < 0 || spec.KeepLast < 0 {
		return nil, fmt.Errorf("mask 'keep_first' and 'keep_last' must not be negative")
	}
	keepFirst, keepLast := spec.KeepFirst, spec.KeepLast

	return TransformerFunc(func(v value.Value) (value.Value, error) {
		if v.Null {
			return v, nil
		}
		runes := []rune(v.String())
		n := len(runes)
		// When the kept regions overlap or cover the whole string, mask
		// nothing extra but never reveal more than the original length.
		if keepFirst+keepLast >= n {
			return v, nil
		}
		masked := make([]rune, 0, n)
		fillRune := []rune(fill)[0]
		for i := 0; i < n; i++ {
			if i < keepFirst || i >= n-keepLast {
				masked = append(masked, runes[i])
			} else {
				masked = append(masked, fillRune)
			}
		}
		return value.Text(string(masked)), nil
	}), nil
}
