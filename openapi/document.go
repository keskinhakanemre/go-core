// Package openapi builds an OpenAPI 3.0 document from Go request and response
// types by reflection, so API documentation never drifts from the code.
//
// Request structs use the same tags as httpserver.Bind:
//
//	params:"id"         path parameter
//	query:"limit"       query parameter
//	reqHeader:"X-Key"   header parameter
//	json:"name"         request body property (any other exported field too)
//
// Constraints are read from go-playground/validator `validate` tags
// (required, min, max, len, gte, lte, gt, lt, oneof, email, url, uuid, ...).
// Optional `doc:"..."` and `example:"..."` tags add descriptions and examples.
package openapi

// Version is the OpenAPI specification version produced by this package.
const Version = "3.0.3"

type Document struct {
	OpenAPI    string              `json:"openapi"`
	Info       Info                `json:"info"`
	Servers    []Server            `json:"servers,omitempty"`
	Tags       []Tag               `json:"tags,omitempty"`
	Paths      map[string]PathItem `json:"paths"`
	Components *Components         `json:"components,omitempty"`
}

type Info struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

type Tag struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// PathItem maps a lower case HTTP method to its operation.
type PathItem map[string]*Operation

type Operation struct {
	OperationID string               `json:"operationId,omitempty"`
	Summary     string               `json:"summary,omitempty"`
	Description string               `json:"description,omitempty"`
	Tags        []string             `json:"tags,omitempty"`
	Parameters  []*Parameter         `json:"parameters,omitempty"`
	RequestBody *RequestBody         `json:"requestBody,omitempty"`
	Responses   map[string]*Response `json:"responses"`
	Deprecated  bool                 `json:"deprecated,omitempty"`
}

type Parameter struct {
	Name        string  `json:"name"`
	In          string  `json:"in"` // path | query | header
	Description string  `json:"description,omitempty"`
	Required    bool    `json:"required,omitempty"`
	Schema      *Schema `json:"schema"`
	Example     any     `json:"example,omitempty"`
}

type RequestBody struct {
	Required bool                 `json:"required,omitempty"`
	Content  map[string]MediaType `json:"content"`
}

type MediaType struct {
	Schema *Schema `json:"schema"`
}

type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

type Components struct {
	Schemas map[string]*Schema `json:"schemas,omitempty"`
}

// Schema is the subset of the OpenAPI 3.0 schema object used by this package.
type Schema struct {
	Ref                  string             `json:"$ref,omitempty"`
	Type                 string             `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Description          string             `json:"description,omitempty"`
	Nullable             bool               `json:"nullable,omitempty"`
	Enum                 []any              `json:"enum,omitempty"`
	Pattern              string             `json:"pattern,omitempty"`
	Minimum              *float64           `json:"minimum,omitempty"`
	Maximum              *float64           `json:"maximum,omitempty"`
	ExclusiveMinimum     bool               `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum     bool               `json:"exclusiveMaximum,omitempty"`
	MinLength            *int               `json:"minLength,omitempty"`
	MaxLength            *int               `json:"maxLength,omitempty"`
	MinItems             *int               `json:"minItems,omitempty"`
	MaxItems             *int               `json:"maxItems,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	AdditionalProperties *Schema            `json:"additionalProperties,omitempty"`
	AllOf                []*Schema          `json:"allOf,omitempty"`
	Example              any                `json:"example,omitempty"`
}
