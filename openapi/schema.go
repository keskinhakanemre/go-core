package openapi

import (
	"encoding"
	"encoding/json"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	timeType          = reflect.TypeFor[time.Time]()
	durationType      = reflect.TypeFor[time.Duration]()
	rawMessageType    = reflect.TypeFor[json.RawMessage]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
)

// Generator converts Go types to schemas. Named struct types become reusable
// components referenced with $ref.
type Generator struct {
	schemas map[string]*Schema
	names   map[reflect.Type]string
	taken   map[string]reflect.Type
}

func NewGenerator() *Generator {
	return &Generator{
		schemas: map[string]*Schema{},
		names:   map[reflect.Type]string{},
		taken:   map[string]reflect.Type{},
	}
}

// Components returns the component schemas generated so far.
func (g *Generator) Components() map[string]*Schema { return g.schemas }

// Schema returns the schema for t. It returns nil for types that cannot be
// represented in JSON (functions, channels).
func (g *Generator) Schema(t reflect.Type) *Schema {
	switch t {
	case timeType:
		return &Schema{Type: "string", Format: "date-time"}
	case durationType:
		return &Schema{Type: "integer", Format: "int64", Description: "duration in nanoseconds"}
	case rawMessageType:
		return &Schema{}
	}
	if t.Kind() == reflect.Pointer {
		s := g.Schema(t.Elem())
		if s != nil && s.Ref == "" {
			s.Nullable = true
		}
		return s
	}
	if t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType) {
		return &Schema{Type: "string"}
	}
	if t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType) {
		return &Schema{} // custom JSON: shape unknown
	}

	switch t.Kind() {
	case reflect.Bool:
		return &Schema{Type: "boolean"}
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint64, reflect.Uintptr:
		return &Schema{Type: "integer", Format: "int64"}
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return &Schema{Type: "integer", Format: "int32"}
	case reflect.Float32:
		return &Schema{Type: "number", Format: "float"}
	case reflect.Float64:
		return &Schema{Type: "number", Format: "double"}
	case reflect.String:
		return &Schema{Type: "string"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 && t.Kind() == reflect.Slice {
			return &Schema{Type: "string", Format: "byte"}
		}
		items := g.Schema(t.Elem())
		if items == nil {
			return nil
		}
		return &Schema{Type: "array", Items: items}
	case reflect.Map:
		val := g.Schema(t.Elem())
		if val == nil {
			return nil
		}
		return &Schema{Type: "object", AdditionalProperties: val}
	case reflect.Interface:
		return &Schema{}
	case reflect.Struct:
		if t.Name() == "" {
			return g.structSchema(t, nil)
		}
		return g.ref(t, nil)
	default:
		return nil
	}
}

// ref registers t (optionally filtered) as a component and returns a $ref to it.
func (g *Generator) ref(t reflect.Type, keep func(reflect.StructField) bool) *Schema {
	name, ok := g.names[t]
	if !ok {
		name = g.nameFor(t)
		g.names[t] = name
		g.taken[name] = t
		g.schemas[name] = &Schema{Type: "object"} // placeholder for recursive types
		g.schemas[name] = g.structSchema(t, keep)
	}
	return &Schema{Ref: "#/components/schemas/" + name}
}

func (g *Generator) structSchema(t reflect.Type, keep func(reflect.StructField) bool) *Schema {
	s := &Schema{Type: "object", Properties: map[string]*Schema{}}
	for _, f := range structFields(t) {
		if keep != nil && !keep(f) {
			continue
		}
		name, ok := jsonName(f)
		if !ok {
			continue
		}
		prop, required := g.FieldSchema(f)
		if prop == nil {
			continue
		}
		s.Properties[name] = prop
		if required {
			s.Required = append(s.Required, name)
		}
	}
	if len(s.Properties) == 0 {
		s.Properties = nil
	}
	return s
}

// FieldSchema returns the schema of a struct field with its validate, doc
// and example tags applied, and whether the field is required.
func (g *Generator) FieldSchema(f reflect.StructField) (*Schema, bool) {
	s := g.Schema(f.Type)
	if s == nil {
		return nil, false
	}
	required := applyValidate(s, f.Type, f.Tag.Get("validate"))
	desc := f.Tag.Get("doc")
	example, hasExample := f.Tag.Lookup("example")
	if s.Ref != "" && (desc != "" || hasExample) {
		// Siblings of $ref are ignored in OpenAPI 3.0; wrap it.
		s = &Schema{AllOf: []*Schema{s}}
	}
	if desc != "" {
		s.Description = desc
	}
	if hasExample {
		s.Example = parseScalar(example, f.Type)
	}
	return s, required
}

// structFields returns exported fields, flattening embedded structs the way
// encoding/json does.
func structFields(t reflect.Type) []reflect.StructField {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []reflect.StructField
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if tag, _, _ := strings.Cut(f.Tag.Get("json"), ","); tag == "" && ft.Kind() == reflect.Struct {
				out = append(out, structFields(ft)...)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		out = append(out, f)
	}
	return out
}

func jsonName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		name = f.Name
	}
	return name, true
}

var (
	pkgPathRe   = regexp.MustCompile(`[\w./-]*\.`)
	nonNameChar = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
)

func (g *Generator) nameFor(t reflect.Type) string {
	// Generic instantiations look like Page[github.com/acme/x.Item]; keep only type names.
	base := pkgPathRe.ReplaceAllString(t.Name(), "")
	base = strings.Trim(nonNameChar.ReplaceAllString(base, "_"), "_")
	if base == "" {
		base = "Object"
	}
	if other, ok := g.taken[base]; !ok || other == t {
		return base
	}
	pkg := t.PkgPath()
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		pkg = pkg[i+1:]
	}
	name := nonNameChar.ReplaceAllString(pkg, "_") + "." + base
	for i := 2; ; i++ {
		if other, ok := g.taken[name]; !ok || other == t {
			return name
		}
		name = base + strconv.Itoa(i)
	}
}

// applyValidate maps validator rules to schema constraints and reports
// whether the field is required.
func applyValidate(s *Schema, t reflect.Type, tag string) (required bool) {
	if tag == "" || tag == "-" {
		return false
	}
	t = deref(t)
	target, kind := s, t.Kind()
	if len(s.AllOf) > 0 || s.Ref != "" {
		target = nil // constraints cannot be attached to a reference
	}
	afterDive := false
	for _, rule := range strings.Split(tag, ",") {
		if strings.Contains(rule, "|") {
			continue // alternatives cannot be expressed simply
		}
		name, param, _ := strings.Cut(rule, "=")
		switch name {
		case "required":
			if !afterDive {
				required = true
			}
			continue
		case "dive":
			// Following rules apply to the elements.
			if target == nil || target.Items == nil || target.Items.Ref != "" ||
				(kind != reflect.Slice && kind != reflect.Array) {
				return required
			}
			t = deref(t.Elem())
			target, kind, afterDive = target.Items, t.Kind(), true
			continue
		}
		if target != nil {
			applyRule(target, kind, name, param)
		}
	}
	return required
}

func applyRule(s *Schema, kind reflect.Kind, name, param string) {
	switch name {
	case "min", "gte":
		setBound(s, kind, param, true, 0)
	case "max", "lte":
		setBound(s, kind, param, false, 0)
	case "gt":
		setBound(s, kind, param, true, 1)
	case "lt":
		setBound(s, kind, param, false, 1)
	case "len":
		setBound(s, kind, param, true, 0)
		setBound(s, kind, param, false, 0)
	case "oneof":
		for _, v := range strings.Fields(param) {
			s.Enum = append(s.Enum, parseKind(v, kind))
		}
	case "email":
		s.Format = "email"
	case "url", "uri", "http_url", "https_url":
		s.Format = "uri"
	case "uuid", "uuid3", "uuid4", "uuid5", "uuid_rfc4122", "uuid4_rfc4122":
		s.Format = "uuid"
	case "ipv4":
		s.Format = "ipv4"
	case "ipv6":
		s.Format = "ipv6"
	case "hostname", "hostname_rfc1123":
		s.Format = "hostname"
	case "alpha":
		s.Pattern = "^[a-zA-Z]+$"
	case "alphanum":
		s.Pattern = "^[a-zA-Z0-9]+$"
	case "numeric":
		s.Pattern = `^[-+]?[0-9]+(\.[0-9]+)?$`
	case "e164":
		s.Pattern = `^\+[1-9][0-9]{1,14}$`
	}
}

// setBound applies a lower (lower=true) or upper bound. offset turns gt/lt into
// strict bounds: exclusive for numbers, +/-1 for lengths and counts.
func setBound(s *Schema, kind reflect.Kind, param string, lower bool, offset int) {
	switch kind {
	case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
		n, err := strconv.Atoi(param)
		if err != nil {
			return
		}
		if lower {
			n += offset
		} else {
			n -= offset
		}
		p := &n
		switch {
		case kind == reflect.String && lower:
			s.MinLength = p
		case kind == reflect.String:
			s.MaxLength = p
		case lower:
			s.MinItems = p
		default:
			s.MaxItems = p
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(param, 64)
		if err != nil {
			return
		}
		if lower {
			s.Minimum, s.ExclusiveMinimum = &f, offset > 0
		} else {
			s.Maximum, s.ExclusiveMaximum = &f, offset > 0
		}
	}
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func parseScalar(v string, t reflect.Type) any {
	t = deref(t)
	if t == timeType || t.Implements(textMarshalerType) {
		return v
	}
	return parseKind(v, t.Kind())
}

func parseKind(v string, kind reflect.Kind) any {
	switch kind {
	case reflect.Bool:
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return v
}
