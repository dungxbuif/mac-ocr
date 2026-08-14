package validation

import (
	"fmt"
	"regexp"
	"strings"
)

type FieldErrors map[string]string

func (f FieldErrors) Add(field, format string, args ...any) {
	f[field] = fmt.Sprintf(format, args...)
}

func (f FieldErrors) Err() error {
	if len(f) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("validation failed:")
	for field, msg := range f {
		sb.WriteString(" ")
		sb.WriteString(field)
		sb.WriteString("=")
		sb.WriteString(msg)
		sb.WriteString(";")
	}
	return fmt.Errorf("%s", strings.TrimSuffix(sb.String(), ";"))
}

func (f FieldErrors) Has() bool { return len(f) > 0 }

var ulidLike = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

func IsULID(s string) bool { return ulidLike.MatchString(s) }

func (f FieldErrors) RequireNonEmpty(field, value string) {
	if strings.TrimSpace(value) == "" {
		f.Add(field, "must not be empty")
	}
}

func (f FieldErrors) RequireULID(field, value string) {
	if !IsULID(value) {
		f.Add(field, "must be a valid ULID")
	}
}

func (f FieldErrors) MaxLen(field, value string, max int) {
	if len([]rune(value)) > max {
		f.Add(field, "must be at most %d characters", max)
	}
}

func (f FieldErrors) Range(field string, value, min, max int) {
	if value < min || value > max {
		f.Add(field, "must be between %d and %d", min, max)
	}
}

func (f FieldErrors) ValidateKey(field, key string) {
	f.RequireNonEmpty(field, key)
	f.MaxLen(field, key, 1024)
	if key == "" {
		return
	}
	for _, r := range key {
		if r >= 0x20 && r <= 0x7e {
			continue
		}
		f.Add(field, "contains invalid character %q", r)
		break
	}
}
