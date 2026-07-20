//go:build !wasm

package runtime

import (
	"bytes"
	"encoding/csv"
)

// CSVParse parses a CSV string into a slice of string slices.
func CSVParse(content string) interface{} {
	r := csv.NewReader(bytes.NewBufferString(content))
	records, err := r.ReadAll()
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	res := make([]interface{}, len(records))
	for i, row := range records {
		rowVal := make([]interface{}, len(row))
		for j, val := range row {
			rowVal[j] = val
		}
		res[i] = rowVal
	}
	return res
}

// CSVStringify stringifies a matrix or slice of slices into a CSV string.
func CSVStringify(rows interface{}, headers interface{}) interface{} {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if headers != nil {
		if hSlice, ok := headers.([]interface{}); ok {
			hStrings := make([]string, len(hSlice))
			for i, val := range hSlice {
				if s, ok := val.(string); ok {
					hStrings[i] = s
				} else {
					hStrings[i] = ""
				}
			}
			if err := w.Write(hStrings); err != nil {
				return [2]interface{}{nil, err.Error()}
			}
		}
	}

	if rSlice, ok := rows.([]interface{}); ok {
		for _, row := range rSlice {
			if fields, ok := row.([]interface{}); ok {
				rowStrings := make([]string, len(fields))
				for i, val := range fields {
					if s, ok := val.(string); ok {
						rowStrings[i] = s
					} else {
						rowStrings[i] = ""
					}
				}
				if err := w.Write(rowStrings); err != nil {
					return [2]interface{}{nil, err.Error()}
				}
			}
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return [2]interface{}{nil, err.Error()}
	}

	return buf.String()
}
