package record

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Canonicalize serializes a JSON value tree per RFC 8785 (JSON Canonicalization
// Scheme): UTF-8 output, object keys sorted, no insignificant whitespace, fixed
// string escaping. The accepted value tree is restricted to string, bool, nil,
// int/int64, json.Number, []any, and map[string]any — float64 is rejected so the
// client and the Python server can agree byte-for-byte without implementing the
// ECMAScript float algorithm. (Tool inputs that contain numbers arrive as
// json.Number, whose literal source text is preserved.)
//
// NOTE on key sorting: RFC 8785 sorts by UTF-16 code units. All keys produced by
// this package are ASCII, for which byte order equals code-unit order. The only
// non-ASCII keys possible are inside a tool_use input, which is canonicalized to a
// string once on the client and treated as opaque by the server, so its internal
// key order only needs to be self-consistent (byte order is).
func Canonicalize(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := encodeValue(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Hash returns the lowercase hex SHA-256 of the canonical form of v.
func Hash(v any) (string, []byte, error) {
	cb, err := Canonicalize(v)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(cb)
	return hex.EncodeToString(sum[:]), cb, nil
}

func encodeValue(b *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		encodeString(b, t)
	case int:
		b.WriteString(strconv.FormatInt(int64(t), 10))
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
	case json.Number:
		// Preserve the literal source representation (deterministic for a fixed
		// source transcript). Opaque to the server hash.
		b.WriteString(t.String())
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := encodeValue(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeString(b, k)
			b.WriteByte(':')
			if err := encodeValue(b, t[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("canonicalize: unsupported type %T", v)
	}
	return nil
}

// encodeString writes a JSON string per RFC 8785 §3.2.2.2: escape " and \ and the
// C0 control characters (short forms where defined, \u00xx otherwise); emit every
// other code point as literal UTF-8.
func encodeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
