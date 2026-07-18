//go:build !wasm

package runtime

import (
	"fmt"
	"strings"
)

type protoField struct {
	name string
	typ  string
	tag  uint64
}

func parseProtoSchema(schema string) (map[string]protoField, map[uint64]protoField) {
	byName := make(map[string]protoField)
	byTag := make(map[uint64]protoField)

	// Normalize semicolons and braces to newlines
	schema = strings.ReplaceAll(schema, ";", "\n")
	schema = strings.ReplaceAll(schema, "{", "\n")
	schema = strings.ReplaceAll(schema, "}", "\n")

	lines := strings.Split(schema, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "syntax") || strings.HasPrefix(line, "message") || line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 4 && parts[2] == "=" {
			typ := parts[0]
			name := parts[1]
			tagVal := parts[3]
			var tag uint64
			fmt.Sscanf(tagVal, "%d", &tag)
			field := protoField{name: name, typ: typ, tag: tag}
			byName[name] = field
			byTag[tag] = field
		}
	}
	return byName, byTag
}

func encodeVarint(x uint64) []byte {
	var buf []byte
	for x >= 0x80 {
		buf = append(buf, byte(x|0x80))
		x >>= 7
	}
	buf = append(buf, byte(x))
	return buf
}

func decodeVarint(buf []byte) (uint64, int) {
	var x uint64
	var s uint
	for i, b := range buf {
		if b < 0x80 {
			if i > 9 || i == 9 && b > 1 {
				return 0, 0
			}
			return x | uint64(b)<<s, i + 1
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, 0
}

// ProtoEncode encodes map payload into binary protobuf buffer based on schema definition.
func ProtoEncode(objIn, schemaIn interface{}) interface{} {
	obj, ok := objIn.(map[string]interface{})
	if !ok {
		return []byte{}
	}
	schema := toString(schemaIn)
	byName, _ := parseProtoSchema(schema)

	var buf []byte
	for k, val := range obj {
		field, ok := byName[k]
		if !ok {
			continue
		}
		switch field.typ {
		case "int32", "int64", "uint32", "uint64", "bool":
			tag := field.tag<<3 | 0
			buf = append(buf, encodeVarint(tag)...)
			var intVal uint64
			switch v := val.(type) {
			case float64:
				intVal = uint64(v)
			case float32:
				intVal = uint64(v)
			case int:
				intVal = uint64(v)
			case int8:
				intVal = uint64(v)
			case int16:
				intVal = uint64(v)
			case int32:
				intVal = uint64(v)
			case int64:
				intVal = uint64(v)
			case uint:
				intVal = uint64(v)
			case uint8:
				intVal = uint64(v)
			case uint16:
				intVal = uint64(v)
			case uint32:
				intVal = uint64(v)
			case uint64:
				intVal = v
			case bool:
				if v {
					intVal = 1
				} else {
					intVal = 0
				}
			}
			buf = append(buf, encodeVarint(intVal)...)
		case "string":
			tag := field.tag<<3 | 2
			buf = append(buf, encodeVarint(tag)...)
			strVal := toString(val)
			buf = append(buf, encodeVarint(uint64(len(strVal)))...)
			buf = append(buf, []byte(strVal)...)
		case "bytes":
			tag := field.tag<<3 | 2
			buf = append(buf, encodeVarint(tag)...)
			var bytesVal []byte
			if b, ok := val.([]byte); ok {
				bytesVal = b
			} else if s, ok := val.(string); ok {
				bytesVal = []byte(s)
			}
			buf = append(buf, encodeVarint(uint64(len(bytesVal)))...)
			buf = append(buf, bytesVal...)
		}
	}
	return buf
}

// ProtoDecode decodes binary protobuf buffer back to map payload.
func ProtoDecode(bytesIn, schemaIn interface{}) interface{} {
	var buf []byte
	switch b := bytesIn.(type) {
	case []byte:
		buf = b
	case string:
		buf = []byte(b)
	default:
		return map[string]interface{}{}
	}

	schema := toString(schemaIn)
	_, byTag := parseProtoSchema(schema)
	result := make(map[string]interface{})

	idx := 0
	for idx < len(buf) {
		tagWire, n := decodeVarint(buf[idx:])
		if n == 0 {
			break
		}
		idx += n

		tag := tagWire >> 3
		wire := tagWire & 7

		field, ok := byTag[tag]
		if !ok {
			switch wire {
			case 0:
				_, n := decodeVarint(buf[idx:])
				idx += n
			case 1:
				idx += 8
			case 2:
				length, n := decodeVarint(buf[idx:])
				idx += n + int(length)
			case 5:
				idx += 4
			}
			continue
		}

		switch field.typ {
		case "int32", "int64", "uint32", "uint64":
			val, n := decodeVarint(buf[idx:])
			idx += n
			result[field.name] = float64(val)
		case "bool":
			val, n := decodeVarint(buf[idx:])
			idx += n
			result[field.name] = val != 0
		case "string":
			length, n := decodeVarint(buf[idx:])
			idx += n
			if idx+int(length) > len(buf) {
				break
			}
			strVal := string(buf[idx : idx+int(length)])
			idx += int(length)
			result[field.name] = strVal
		case "bytes":
			length, n := decodeVarint(buf[idx:])
			idx += n
			if idx+int(length) > len(buf) {
				break
			}
			bytesVal := buf[idx : idx+int(length)]
			idx += int(length)
			result[field.name] = string(bytesVal)
		}
	}
	return result
}
