package realtime

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ConvertChangeData converts raw stringified Postgres column values in a postgres_changes
// record map according to the accompanying column schema definitions (matching realtime-js transformers).
func ConvertChangeData(columns []map[string]any, record map[string]any) map[string]any {
	if record == nil {
		return nil
	}
	colTypes := make(map[string]string, len(columns))
	for _, col := range columns {
		name, _ := col["name"].(string)
		typ, _ := col["type"].(string)
		if name != "" && typ != "" {
			colTypes[name] = typ
		}
	}
	out := make(map[string]any, len(record))
	for k, v := range record {
		if typ, ok := colTypes[k]; ok {
			out[k] = ConvertColumnValue(typ, v)
		} else {
			out[k] = v
		}
	}
	return out
}

// ConvertColumnValue converts a single Postgres column value based on its Postgres type name.
func ConvertColumnValue(pgType string, value any) any {
	if value == nil {
		return nil
	}
	s, isStr := value.(string)
	if !isStr {
		return value
	}
	if strings.HasPrefix(pgType, "_") {
		return parsePostgresArray(pgType[1:], s)
	}
	switch pgType {
	case "bool":
		switch strings.ToLower(s) {
		case "t", "true":
			return true
		case "f", "false":
			return false
		}
		return s
	case "int2", "int4", "int8":
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		return s
	case "float4", "float8", "numeric":
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
		return s
	case "json", "jsonb":
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			return parsed
		}
		return s
	default:
		return s
	}
}

func parsePostgresArray(elemType, raw string) []any {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return []any{raw}
	}
	inner := trimmed[1 : len(trimmed)-1]
	if inner == "" {
		return []any{}
	}
	parts := strings.Split(inner, ",")
	res := make([]any, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"`)
		if strings.EqualFold(p, "NULL") {
			res = append(res, nil)
		} else {
			res = append(res, ConvertColumnValue(elemType, p))
		}
	}
	return res
}
