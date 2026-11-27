package ga

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var timeType = reflect.TypeOf(time.Time{})

func NewTool[A any](name, desc string, fn func(*Context, A) *ToolResult) *Tool {
	return &Tool{
		Name:        name,
		Description: desc,
		Params:      SchemaOf[A](),
		Handler: func(c *Context, raw json.RawMessage) *ToolResult {
			var in A
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &in); err != nil {
					return Err(fmt.Errorf("invalid arguments: %w", err))
				}
			}
			return fn(c, in)
		},
	}
}

func NewToolRaw(name, desc string, params map[string]any, fn func(*Context, json.RawMessage) *ToolResult) *Tool {
	return &Tool{
		Name:        name,
		Description: desc,
		Params:      params,
		Handler: func(c *Context, args json.RawMessage) *ToolResult {
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			return fn(c, args)
		},
	}
}

func SchemaOf[T any]() map[string]any {
	var zero T
	return schemaFor(reflect.TypeOf(zero))
}

func schemaFor(t reflect.Type) map[string]any {
	if t == nil {
		return map[string]any{"type": "object"}
	}
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
		if t == timeType {
			return map[string]any{"type": "string", "format": "date-time"}
		}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaFor(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Elem())}
	case reflect.Struct:
		return structSchema(t)
	case reflect.Interface:
		return map[string]any{}
	}
	return map[string]any{}
}

func structSchema(t reflect.Type) map[string]any {
	props := map[string]any{}
	var required []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, opts := parseJSONTag(f.Tag.Get("json"))
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		ps := schemaFor(f.Type)
		if d := f.Tag.Get("desc"); d != "" {
			ps["description"] = d
		}
		if e := f.Tag.Get("enum"); e != "" {
			vals := make([]string, 0, 4)
			for _, s := range strings.Split(e, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					vals = append(vals, s)
				}
			}
			ps["enum"] = vals
		}
		def := f.Tag.Get("default")
		if def != "" {
			ps["default"] = parseDefault(f.Type, def)
		}
		props[name] = ps

		req := f.Tag.Get("required") == "true"
		if !req && def == "" && f.Type.Kind() != reflect.Ptr && !opts["omitempty"] {
			req = true
		}
		if req {
			required = append(required, name)
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func parseJSONTag(tag string) (string, map[string]bool) {
	opts := map[string]bool{}
	if tag == "" {
		return "", opts
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	for _, p := range parts[1:] {
		opts[p] = true
	}
	return name, opts
}

func parseDefault(t reflect.Type, s string) any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return s
	case reflect.Bool:
		v, err := strconv.ParseBool(s)
		if err == nil {
			return v
		}
		return s
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			return v
		}
		return s
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(s, 10, 64)
		if err == nil {
			return v
		}
		return s
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return v
		}
		return s
	}
	return s
}
