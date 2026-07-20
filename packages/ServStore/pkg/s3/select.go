package s3

import (
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// SelectObjectContentRequest represents the XML payload for S3 Select.
type SelectObjectContentRequest struct {
	XMLName             xml.Name            `xml:"SelectObjectContentRequest"`
	Expression          string              `xml:"Expression"`
	ExpressionType      string              `xml:"ExpressionType"`
	InputSerialization  InputSerialization  `xml:"InputSerialization"`
	OutputSerialization OutputSerialization `xml:"OutputSerialization"`
}

type InputSerialization struct {
	CSV  *CSVSerialization  `xml:"CSV"`
	JSON *JSONSerialization `xml:"JSON"`
}

type CSVSerialization struct {
	FileHeaderInfo      string `xml:"FileHeaderInfo"` // "USE", "IGNORE", "NONE"
	RecordDelimiter     string `xml:"RecordDelimiter"`
	FieldDelimiter      string `xml:"FieldDelimiter"`
	QuoteCharacter      string `xml:"QuoteCharacter"`
	QuoteEscapeCharacter string `xml:"QuoteEscapeCharacter"`
	Comments            string `xml:"Comments"`
}

type JSONSerialization struct {
	Type string `xml:"Type"` // "DOCUMENT" or "LINES"
}

type OutputSerialization struct {
	CSV  *CSVSerialization  `xml:"CSV"`
	JSON *JSONSerialization `xml:"JSON"`
}

// parseSelectObjectContentRequest parses the XML body of a SelectObjectContent request.
func parseSelectObjectContentRequest(r *http.Request) (*SelectObjectContentRequest, error) {
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var req SelectObjectContentRequest
	if err := xml.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	if req.ExpressionType == "" {
		req.ExpressionType = "SQL"
	}
	_ = uuid.New()
	return &req, nil
}

// handleSelectObjectContent parses the query and evaluates it against the source file.
func (g *Gateway) handleSelectObjectContent(w http.ResponseWriter, r *http.Request, bucket, key string) {
	req, err := parseSelectObjectContentRequest(r)
	if err != nil {
		g.writeError(w, http.StatusBadRequest, "InvalidXML", err.Error())
		return
	}

	// Fetch target object
	reader, objVer, err := g.store.GetObject(r.Context(), bucket, key, "")
	if err != nil {
		g.writeError(w, http.StatusNotFound, "NoSuchKey", err.Error())
		return
	}
	defer reader.Close()

	// Parse expression
	queryExpr := strings.TrimSpace(req.Expression)
	// We want to extract target projection and filter condition.
	// For simplicity, we support basic parsing:
	// "SELECT <projection> FROM s3object s WHERE <condition>"
	projection, condition, limitVal, err := parseSimpleSQL(queryExpr)
	if err != nil {
		g.writeError(w, http.StatusBadRequest, "InvalidQuery", fmt.Sprintf("failed to parse expression: %v", err))
		return
	}

	// Read input
	var records []map[string]string
	var headers []string

	isJSON := req.InputSerialization.JSON != nil
	if isJSON {
		// Parse JSON (Lines format or standard Document format)
		dec := json.NewDecoder(reader)
		for {
			var val interface{}
			if err := dec.Decode(&val); err != nil {
				if err == io.EOF {
					break
				}
				g.writeError(w, http.StatusBadRequest, "InvalidJSON", err.Error())
				return
			}
			switch m := val.(type) {
			case map[string]interface{}:
				rec := make(map[string]string)
				for k, v := range m {
					rec[k] = fmt.Sprintf("%v", v)
				}
				records = append(records, rec)
			case []interface{}:
				for _, item := range m {
					if itemMap, ok := item.(map[string]interface{}); ok {
						rec := make(map[string]string)
						for k, v := range itemMap {
							rec[k] = fmt.Sprintf("%v", v)
						}
						records = append(records, rec)
					}
				}
			}
		}
	} else {
		// Parse CSV
		comma := ','
		if req.InputSerialization.CSV != nil && req.InputSerialization.CSV.FieldDelimiter != "" {
			comma = rune(req.InputSerialization.CSV.FieldDelimiter[0])
		}
		csvReader := csv.NewReader(reader)
		csvReader.Comma = comma
		csvReader.FieldsPerRecord = -1
		rawRecords, err := csvReader.ReadAll()
		if err != nil {
			g.writeError(w, http.StatusBadRequest, "InvalidCSV", err.Error())
			return
		}

		useHeaders := false
		if req.InputSerialization.CSV != nil && req.InputSerialization.CSV.FileHeaderInfo == "USE" {
			useHeaders = true
		}

		if len(rawRecords) > 0 {
			if useHeaders {
				headers = rawRecords[0]
				rawRecords = rawRecords[1:]
			}
			for _, row := range rawRecords {
				rec := make(map[string]string)
				for i, val := range row {
					// Add index-based keys: _1, _2, etc.
					rec[fmt.Sprintf("_%d", i+1)] = val
					if useHeaders && i < len(headers) {
						rec[headers[i]] = val
					}
				}
				records = append(records, rec)
			}
		}
	}

	// Filter & Project records
	var filteredRecords []string
	count := 0

	for _, rec := range records {
		if limitVal >= 0 && count >= limitVal {
			break
		}

		// Evaluate WHERE clause
		match, err := evaluateCondition(rec, condition)
		if err != nil {
			g.writeError(w, http.StatusBadRequest, "EvaluationError", err.Error())
			return
		}
		if !match {
			continue
		}

		// Project fields
		projected, err := projectRecord(rec, projection)
		if err != nil {
			g.writeError(w, http.StatusBadRequest, "ProjectionError", err.Error())
			return
		}

		// Serialize output record
		var serialized string
		if req.OutputSerialization.JSON != nil {
			m := make(map[string]interface{})
			for k, v := range projected {
				m[k] = v
			}
			b, _ := json.Marshal(m)
			serialized = string(b) + "\n"
		} else {
			// CSV Output
			var row []string
			if projection == "*" {
				// Retain order based on keys if CSV
				for i := 1; ; i++ {
					k := fmt.Sprintf("_%d", i)
					v, exists := projected[k]
					if !exists {
						break
					}
					row = append(row, v)
				}
			} else {
				// Specific fields
				fields := strings.Split(projection, ",")
				for _, f := range fields {
					f = strings.TrimSpace(f)
					// Strip alias s.
					f = strings.TrimPrefix(f, "s.")
					row = append(row, projected[f])
				}
			}
			var buf bytes.Buffer
			csvWriter := csv.NewWriter(&buf)
			if req.OutputSerialization.CSV != nil && req.OutputSerialization.CSV.FieldDelimiter != "" {
				csvWriter.Comma = rune(req.OutputSerialization.CSV.FieldDelimiter[0])
			}
			_ = csvWriter.Write(row)
			csvWriter.Flush()
			serialized = buf.String()
		}

		filteredRecords = append(filteredRecords, serialized)
		count++
	}

	// Write Event Stream response
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)

	// Stream Records event
	for _, rec := range filteredRecords {
		_ = writeEventFrame(w, "Records", "text/plain", []byte(rec))
	}

	// Stream Stats event
	statsPayload := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><Stats><BytesScanned>%d</BytesScanned><BytesProcessed>%d</BytesProcessed><BytesReturned>%d</BytesReturned></Stats>`,
		objVer.Size, objVer.Size, len(strings.Join(filteredRecords, "")))
	_ = writeEventFrame(w, "Stats", "text/xml", []byte(statsPayload))

	// Stream End event
	_ = writeEventFrame(w, "End", "", nil)
}

func parseSimpleSQL(query string) (string, string, int, error) {
	// Simple SQL parsing for "SELECT <projection> FROM s3object s WHERE <condition> LIMIT <limit>"
	// Normalize spacing
	q := strings.ReplaceAll(query, "\n", " ")
	q = strings.ReplaceAll(q, "\t", " ")

	selectIdx := strings.Index(strings.ToUpper(q), "SELECT ")
	fromIdx := strings.Index(strings.ToUpper(q), " FROM ")
	if selectIdx == -1 || fromIdx == -1 {
		return "", "", -1, fmt.Errorf("invalid SQL syntax, must start with SELECT ... FROM ...")
	}

	projection := strings.TrimSpace(q[selectIdx+7 : fromIdx])

	rest := q[fromIdx+6:]
	// Strip s3object s / s3object
	whereIdx := strings.Index(strings.ToUpper(rest), " WHERE ")
	limitIdx := strings.Index(strings.ToUpper(rest), " LIMIT ")

	var condition string
	var limitVal = -1

	var endOfFrom int
	if whereIdx != -1 {
		endOfFrom = whereIdx
	} else if limitIdx != -1 {
		endOfFrom = limitIdx
	} else {
		endOfFrom = len(rest)
	}
	_ = rest[:endOfFrom] // FROM table name is ignored since it must be s3object

	if whereIdx != -1 {
		var endOfWhere int
		if limitIdx != -1 {
			endOfWhere = limitIdx
		} else {
			endOfWhere = len(rest)
		}
		condition = strings.TrimSpace(rest[whereIdx+7 : endOfWhere])
	}

	if limitIdx != -1 {
		limitStr := strings.TrimSpace(rest[limitIdx+7:])
		l, err := strconv.Atoi(limitStr)
		if err == nil {
			limitVal = l
		}
	}

	return projection, condition, limitVal, nil
}

func evaluateCondition(rec map[string]string, condition string) (bool, error) {
	if condition == "" {
		return true, nil
	}

	// Support basic condition patterns like:
	// s.name = 'Alice'
	// s._1 = 'Alice'
	// s.age > 30
	// AND / OR support recursively/sequentially
	conds := strings.Split(condition, " AND ")
	for _, cond := range conds {
		cond = strings.TrimSpace(cond)
		match, err := evaluateSingleCondition(rec, cond)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
}

func evaluateSingleCondition(rec map[string]string, cond string) (bool, error) {
	parts := strings.SplitN(cond, "=", 2)
	if len(parts) == 2 {
		lhs := strings.TrimSpace(parts[0])
		rhs := strings.TrimSpace(parts[1])
		lhs = strings.TrimPrefix(lhs, "s.")
		rhs = strings.Trim(rhs, "'\"") // Strip quotes

		val, exists := rec[lhs]
		if !exists {
			return false, nil
		}
		return val == rhs, nil
	}

	parts = strings.SplitN(cond, ">", 2)
	if len(parts) == 2 {
		lhs := strings.TrimSpace(parts[0])
		rhs := strings.TrimSpace(parts[1])
		lhs = strings.TrimPrefix(lhs, "s.")
		rhs = strings.Trim(rhs, "'\"")

		val, exists := rec[lhs]
		if !exists {
			return false, nil
		}
		vFloat, err1 := strconv.ParseFloat(val, 64)
		rFloat, err2 := strconv.ParseFloat(rhs, 64)
		if err1 == nil && err2 == nil {
			return vFloat > rFloat, nil
		}
		return val > rhs, nil
	}

	parts = strings.SplitN(cond, "<", 2)
	if len(parts) == 2 {
		lhs := strings.TrimSpace(parts[0])
		rhs := strings.TrimSpace(parts[1])
		lhs = strings.TrimPrefix(lhs, "s.")
		rhs = strings.Trim(rhs, "'\"")

		val, exists := rec[lhs]
		if !exists {
			return false, nil
		}
		vFloat, err1 := strconv.ParseFloat(val, 64)
		rFloat, err2 := strconv.ParseFloat(rhs, 64)
		if err1 == nil && err2 == nil {
			return vFloat < rFloat, nil
		}
		return val < rhs, nil
	}

	return true, nil
}

func projectRecord(rec map[string]string, projection string) (map[string]string, error) {
	projected := make(map[string]string)
	if projection == "*" {
		return rec, nil
	}

	fields := strings.Split(projection, ",")
	for _, f := range fields {
		f = strings.TrimSpace(f)
		cleanF := strings.TrimPrefix(f, "s.")
		if val, ok := rec[cleanF]; ok {
			projected[cleanF] = val
		} else {
			projected[cleanF] = ""
		}
	}
	return projected, nil
}

// writeEventFrame encodes and writes an S3 Select event stream message to w.
func writeEventFrame(w io.Writer, eventType, contentType string, payload []byte) error {
	var headersBuf bytes.Buffer

	// Write Event Type Header
	writeHeader(&headersBuf, ":event-type", eventType)
	// Write Message Type Header
	writeHeader(&headersBuf, ":message-type", "event")
	// Write Content Type Header if specified
	if contentType != "" {
		writeHeader(&headersBuf, ":content-type", contentType)
	}

	headersLen := uint32(headersBuf.Len())
	payloadLen := uint32(len(payload))
	totalLen := uint32(12) + headersLen + payloadLen + uint32(4) // 12 bytes preamble + headers + payload + 4 bytes msg CRC

	// Preamble
	preamble := make([]byte, 12)
	binary.BigEndian.PutUint32(preamble[0:4], totalLen)
	binary.BigEndian.PutUint32(preamble[4:8], headersLen)
	preambleCRC := crc32.ChecksumIEEE(preamble[0:8])
	binary.BigEndian.PutUint32(preamble[8:12], preambleCRC)

	// Write preamble, headers, payload
	if _, err := w.Write(preamble); err != nil {
		return err
	}
	if _, err := w.Write(headersBuf.Bytes()); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}

	// Message CRC
	fullMessage := append(preamble, headersBuf.Bytes()...)
	fullMessage = append(fullMessage, payload...)
	msgCRC := crc32.ChecksumIEEE(fullMessage)
	msgCRCBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(msgCRCBuf, msgCRC)

	_, err := w.Write(msgCRCBuf)
	return err
}

func writeHeader(buf *bytes.Buffer, name, value string) {
	buf.WriteByte(byte(len(name)))
	buf.WriteString(name)
	buf.WriteByte(7) // Header value type 7 is string
	binary.Write(buf, binary.BigEndian, uint16(len(value)))
	buf.WriteString(value)
}
