package stats

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// StatsItem represents a single entry (frontend, backend or server) in HAProxy stats
type StatsItem struct {
	PxName string `json:"pxname"`
	SvName string `json:"svname"`
	Type   int    `json:"type"` // 0=frontend, 1=backend, 2=server, 3=listener
	Status string `json:"status"`
	Weight int    `json:"weight"`

	// Fields holds every field reported by HAProxy for this entry (pxname, scur, rate, ...),
	// with values rendered as strings, like the columns of the "show stat" CSV.
	Fields map[string]string `json:"-"`
}

// HAProxyStats represents the complete stats response from HAProxy
type HAProxyStats struct {
	Stats []StatsItem `json:"stats"`
}

// statField is one field of a stats row in the HAProxy ";json" format.
type statField struct {
	ObjType string `json:"objType"`
	Field   struct {
		Name string `json:"name"`
	} `json:"field"`
	Value struct {
		Value json.RawMessage `json:"value"`
	} `json:"value"`
}

// text renders the field value as a plain string.
func (f statField) text() string {
	raw := bytes.TrimSpace(f.Value.Value)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return string(raw)
}

// UnmarshalJSON parses the response of HAProxy's stats page ("/stats;json"), which is an
// array with one entry per frontend/backend/server, each being an array of field objects:
//
//	[[{"objType":"Frontend","field":{"name":"pxname"},"value":{"type":"str","value":"web"}}, ...], ...]
//
// The flat {"stats":[{"pxname":...}]} shape is accepted as well.
func (h *HAProxyStats) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		type plain HAProxyStats
		return json.Unmarshal(trimmed, (*plain)(h))
	}

	var rows [][]statField
	if err := json.Unmarshal(trimmed, &rows); err != nil {
		return fmt.Errorf("unexpected stats format: %w", err)
	}

	h.Stats = make([]StatsItem, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		item := StatsItem{Fields: make(map[string]string, len(row))}
		for _, f := range row {
			item.Fields[f.Field.Name] = f.text()
		}
		item.PxName = item.Fields["pxname"]
		item.SvName = item.Fields["svname"]
		item.Status = item.Fields["status"]
		item.Weight = atoi(item.Fields["weight"])
		if t, ok := item.Fields["type"]; ok {
			item.Type = atoi(t)
		} else {
			item.Type = typeFromObjType(row[0].ObjType)
		}
		h.Stats = append(h.Stats, item)
	}
	return nil
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// typeFromObjType maps the objType of a stats row to the numeric "type" stat field.
func typeFromObjType(objType string) int {
	switch objType {
	case "Frontend":
		return 0
	case "Backend":
		return 1
	case "Server":
		return 2
	case "Listener":
		return 3
	}
	return -1
}

// StatsSchema represents the schema of HAProxy stats
type StatsSchema struct {
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Type        string              `json:"type"`
	Properties  map[string]Property `json:"properties"`
}

// Property represents a property in the stats schema
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}
