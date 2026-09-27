package openapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// OperationSpec describes one route for Builder.Add.
type OperationSpec struct {
	Method        string       // GET, POST, ...
	Path          string       // Fiber style path: /products/:id
	Request       reflect.Type // request struct (params/query/reqHeader/json tags); may be nil
	Response      reflect.Type // response body type; nil for no body
	SuccessStatus int          // default 200
	OperationID   string       // default derived from method and path
	Summary       string       // default derived from OperationID
	Description   string
	Tags          []string
	ErrorStatuses []int // documented error statuses in addition to the automatic ones
	Deprecated    bool
}

// Builder accumulates operations into a Document. It is safe for concurrent use.
type Builder struct {
	mu        sync.Mutex
	doc       Document
	gen       *Generator
	errSchema *Schema
	opIDs     map[string]string // operationId -> method+path owning it
}

// NewBuilder creates a builder. errorType, if not nil, is the body type of
// every error response.
func NewBuilder(info Info, errorType reflect.Type) *Builder {
	b := &Builder{
		doc:   Document{OpenAPI: Version, Info: info, Paths: map[string]PathItem{}},
		gen:   NewGenerator(),
		opIDs: map[string]string{},
	}
	if errorType != nil {
		b.errSchema = b.gen.Schema(errorType)
	}
	return b
}

// Add registers an operation. Registering the same method and path again replaces it.
func (b *Builder) Add(spec OperationSpec) {
	b.mu.Lock()
	defer b.mu.Unlock()

	method := strings.ToUpper(spec.Method)
	path, pathParams := ConvertPath(spec.Path)
	key := method + " " + path

	op := &Operation{
		Summary:     spec.Summary,
		Description: spec.Description,
		Tags:        spec.Tags,
		Deprecated:  spec.Deprecated,
		Responses:   map[string]*Response{},
	}
	op.OperationID = b.uniqueID(spec.OperationID, method, path, key)
	if op.Summary == "" {
		op.Summary = humanize(op.OperationID)
	}

	hasInput, hasRules := b.addRequest(op, method, spec.Request, pathParams)
	b.addResponses(op, spec, hasInput, hasRules)

	for _, t := range spec.Tags {
		b.addTag(t)
	}
	item := b.doc.Paths[path]
	if item == nil {
		item = PathItem{}
		b.doc.Paths[path] = item
	}
	item[strings.ToLower(method)] = op
}

func (b *Builder) addRequest(op *Operation, method string, req reflect.Type, pathParams []string) (hasInput, hasRules bool) {
	declared := map[string]bool{}
	var bodyFields []reflect.StructField
	if req != nil {
		for _, f := range structFields(deref(req)) {
			if f.Tag.Get("validate") != "" {
				hasRules = true
			}
			in, name := paramLocation(f)
			if in == "" {
				if _, ok := jsonName(f); ok {
					bodyFields = append(bodyFields, f)
				}
				continue
			}
			schema, required := b.gen.FieldSchema(f)
			if schema == nil {
				continue
			}
			p := &Parameter{Name: name, In: in, Required: required || in == "path", Schema: schema}
			// Parameters carry description/example themselves.
			p.Description, schema.Description = schema.Description, ""
			p.Example, schema.Example = schema.Example, nil
			if in == "path" {
				declared[name] = true
			}
			op.Parameters = append(op.Parameters, p)
		}
	}
	// Path parameters the request struct does not bind are still part of the URL.
	for _, name := range pathParams {
		if !declared[name] {
			op.Parameters = append(op.Parameters, &Parameter{Name: name, In: "path", Required: true, Schema: &Schema{Type: "string"}})
		}
	}
	sortParameters(op.Parameters, pathParams)

	if len(bodyFields) > 0 && method != http.MethodGet && method != http.MethodHead {
		keep := map[string]bool{}
		for _, f := range bodyFields {
			keep[f.Name] = true
		}
		schema := b.bodySchema(deref(req), func(f reflect.StructField) bool { return keep[f.Name] })
		required := false
		for _, f := range bodyFields {
			required = required || isRequired(f.Tag.Get("validate"))
		}
		op.RequestBody = &RequestBody{
			Required: required,
			Content:  map[string]MediaType{"application/json": {Schema: schema}},
		}
	}
	return len(op.Parameters) > 0 || op.RequestBody != nil, hasRules
}

// bodySchema registers the request struct (body fields only) as a component.
func (b *Builder) bodySchema(t reflect.Type, keep func(reflect.StructField) bool) *Schema {
	if t.Name() == "" {
		return b.gen.structSchema(t, keep)
	}
	return b.gen.ref(t, keep)
}

func (b *Builder) addResponses(op *Operation, spec OperationSpec, hasInput, hasRules bool) {
	status := spec.SuccessStatus
	if status == 0 {
		status = http.StatusOK
	}
	resp := &Response{Description: http.StatusText(status)}
	if spec.Response != nil && status != http.StatusNoContent {
		if s := b.gen.Schema(spec.Response); s != nil {
			resp.Content = map[string]MediaType{"application/json": {Schema: s}}
		}
	}
	op.Responses[strconv.Itoa(status)] = resp

	errs := append([]int(nil), spec.ErrorStatuses...)
	if hasInput {
		errs = append(errs, http.StatusBadRequest)
	}
	if hasRules {
		errs = append(errs, http.StatusUnprocessableEntity)
	}
	errs = append(errs, http.StatusInternalServerError)
	for _, code := range errs {
		r := &Response{Description: http.StatusText(code)}
		if b.errSchema != nil {
			r.Content = map[string]MediaType{"application/json": {Schema: b.errSchema}}
		}
		op.Responses[strconv.Itoa(code)] = r
	}
}

func (b *Builder) uniqueID(want, method, path, key string) string {
	id := want
	if id == "" {
		id = defaultOperationID(method, path)
	}
	// Free the id this route used before, if it is re-registered.
	for k, owner := range b.opIDs {
		if owner == key {
			delete(b.opIDs, k)
		}
	}
	base := id
	for i := 2; ; i++ {
		if owner, ok := b.opIDs[id]; !ok || owner == key {
			break
		}
		id = base + strconv.Itoa(i)
	}
	b.opIDs[id] = key
	return id
}

func (b *Builder) addTag(name string) {
	for _, t := range b.doc.Tags {
		if t.Name == name {
			return
		}
	}
	b.doc.Tags = append(b.doc.Tags, Tag{Name: name})
}

// SetTagDescription documents a tag (group of operations).
func (b *Builder) SetTagDescription(name, desc string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.addTag(name)
	for i := range b.doc.Tags {
		if b.doc.Tags[i].Name == name {
			b.doc.Tags[i].Description = desc
		}
	}
}

// MarshalJSON renders the current document.
func (b *Builder) MarshalJSON() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	doc := b.doc
	if comps := b.gen.Components(); len(comps) > 0 {
		doc.Components = &Components{Schemas: comps}
	}
	return json.Marshal(doc)
}

// Document returns a copy of the current document.
func (b *Builder) Document() (*Document, error) {
	raw, err := b.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var d Document
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// ConvertPath converts a Fiber route path to an OpenAPI path and returns its
// parameter names: /products/:id<int> -> /products/{id}, [id].
func ConvertPath(p string) (string, []string) {
	if p == "" {
		return "/", nil
	}
	segs := strings.Split(p, "/")
	var params []string
	wild := 0
	for i, s := range segs {
		switch {
		case strings.HasPrefix(s, ":"):
			name := strings.TrimPrefix(s, ":")
			if j := strings.IndexAny(name, "<?"); j >= 0 {
				name = name[:j]
			}
			segs[i] = "{" + name + "}"
			params = append(params, name)
		case s == "*" || s == "+":
			wild++
			name := "wildcard"
			if wild > 1 {
				name += strconv.Itoa(wild)
			}
			segs[i] = "{" + name + "}"
			params = append(params, name)
		}
	}
	out := strings.Join(segs, "/")
	if len(out) > 1 {
		out = strings.TrimSuffix(out, "/")
	}
	if !strings.HasPrefix(out, "/") {
		out = "/" + out
	}
	return out, params
}

func paramLocation(f reflect.StructField) (in, name string) {
	for _, loc := range [...]struct{ tag, in string }{{"params", "path"}, {"query", "query"}, {"reqHeader", "header"}} {
		if v, ok := f.Tag.Lookup(loc.tag); ok {
			name, _, _ = strings.Cut(v, ",")
			if name == "" || name == "-" {
				continue
			}
			return loc.in, name
		}
	}
	return "", ""
}

// sortParameters orders path parameters as they appear in the path, then query, then header.
func sortParameters(ps []*Parameter, pathOrder []string) {
	rank := func(p *Parameter) int {
		switch p.In {
		case "path":
			for i, n := range pathOrder {
				if n == p.Name {
					return i
				}
			}
			return len(pathOrder)
		case "query":
			return 1000
		default:
			return 2000
		}
	}
	for i := 1; i < len(ps); i++ { // stable insertion sort, lists are tiny
		for j := i; j > 0 && rank(ps[j]) < rank(ps[j-1]); j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}

// defaultOperationID builds e.g. "getApiV1ProductsById" from GET /api/v1/products/{id}.
func defaultOperationID(method, path string) string {
	var sb strings.Builder
	sb.WriteString(strings.ToLower(method))
	for _, seg := range strings.Split(path, "/") {
		if seg == "" {
			continue
		}
		if strings.HasPrefix(seg, "{") {
			sb.WriteString("By")
			seg = strings.Trim(seg, "{}")
		}
		for _, part := range strings.FieldsFunc(seg, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
			r := []rune(part)
			r[0] = unicode.ToUpper(r[0])
			sb.WriteString(string(r))
		}
	}
	return sb.String()
}

// isRequired reports whether a validate tag marks the field itself (not its elements) as required.
func isRequired(tag string) bool {
	for _, rule := range strings.Split(tag, ",") {
		switch rule {
		case "required":
			return true
		case "dive":
			return false
		}
	}
	return false
}

// humanize turns "CreateProduct" or "getApiV1Products" into "Create product" / "Get api v1 products".
func humanize(id string) string {
	if id == "" {
		return ""
	}
	var words []string
	var cur []rune
	rs := []rune(id)
	for i, r := range rs {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(rs[i-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))) {
			words = append(words, string(cur))
			cur = nil
		}
		cur = append(cur, r)
	}
	words = append(words, string(cur))
	for i, w := range words {
		if i == 0 {
			r := []rune(w)
			r[0] = unicode.ToUpper(r[0])
			words[i] = string(r)
			continue
		}
		if strings.ToUpper(w) != w { // keep acronyms like ID, URL
			words[i] = strings.ToLower(w)
		}
	}
	return strings.Join(words, " ")
}
