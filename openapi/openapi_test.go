package openapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/keskinhakanemre/go-core/openapi"
)

type Money struct {
	Amount   int64  `json:"amount" validate:"gte=0"`
	Currency string `json:"currency" validate:"required,len=3" example:"TRY"`
}

type Page[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

type Category struct {
	Name     string      `json:"name"`
	Children []*Category `json:"children,omitempty"` // recursive
}

type Audit struct {
	CreatedAt time.Time `json:"created_at"`
}

type Product struct {
	Audit                      // embedded: flattened
	ID       string            `json:"id" doc:"Product identifier" example:"0b8a4b6e-0f3e-4a8b-9a55-6b3c8e1f0a11"`
	Name     string            `json:"name"`
	Price    Money             `json:"price" doc:"Unit price"`
	Tags     []string          `json:"tags,omitempty"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Category *Category         `json:"category,omitempty"`
	Note     *string           `json:"note,omitempty"`
	Secret   string            `json:"-"`
	Raw      json.RawMessage   `json:"raw,omitempty"`
}

type CreateProductRequest struct {
	Tenant string   `reqHeader:"X-Tenant" validate:"required" doc:"Tenant id"`
	DryRun bool     `query:"dry_run"`
	Name   string   `json:"name" validate:"required,min=2,max=120" example:"Kalem"`
	Price  Money    `json:"price" validate:"required"`
	Status string   `json:"status" validate:"omitempty,oneof=draft active"`
	Email  string   `json:"email" validate:"omitempty,email"`
	Tags   []string `json:"tags" validate:"max=5,dive,min=2"`
	Rating float64  `json:"rating" validate:"gt=0,lte=5"`
}

type GetProductRequest struct {
	ID     string `params:"id" validate:"required,uuid4"`
	Expand bool   `query:"expand" doc:"Include category"`
}

type ListRequest struct {
	Limit  int    `query:"limit" validate:"omitempty,min=1,max=100" example:"20"`
	Offset int    `query:"offset" validate:"gte=0"`
	Sort   string `query:"sort" validate:"omitempty,oneof=name price"`
}

type ErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	} `json:"error"`
}

func build(t *testing.T) (*openapi.Builder, map[string]any) {
	t.Helper()
	b := openapi.NewBuilder(openapi.Info{Title: "Test API", Version: "1.0.0"}, reflect.TypeFor[ErrorResponse]())
	b.Add(openapi.OperationSpec{
		Method: http.MethodPost, Path: "/api/v1/products", Tags: []string{"Products"},
		Request: reflect.TypeFor[CreateProductRequest](), Response: reflect.TypeFor[Product](),
		SuccessStatus: http.StatusCreated, OperationID: "CreateProduct", ErrorStatuses: []int{http.StatusConflict},
	})
	b.Add(openapi.OperationSpec{
		Method: http.MethodGet, Path: "/api/v1/products/:id", Tags: []string{"Products"},
		Request: reflect.TypeFor[GetProductRequest](), Response: reflect.TypeFor[Product](),
		ErrorStatuses: []int{http.StatusNotFound},
	})
	b.Add(openapi.OperationSpec{
		Method: http.MethodGet, Path: "/api/v1/products", Tags: []string{"Products"},
		Request: reflect.TypeFor[ListRequest](), Response: reflect.TypeFor[Page[Product]](),
	})
	b.Add(openapi.OperationSpec{
		Method: http.MethodDelete, Path: "/api/v1/products/:id",
		Request: reflect.TypeFor[GetProductRequest](), SuccessStatus: http.StatusNoContent,
	})
	b.Add(openapi.OperationSpec{
		Method: http.MethodGet, Path: "/files/:bucket/*", Response: reflect.TypeFor[map[string]any](),
	})

	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return b, m
}

func TestDocumentIsValidOpenAPI(t *testing.T) {
	b, _ := build(t)
	raw, _ := json.Marshal(b)
	doc, err := openapi3.NewLoader().LoadFromData(raw)
	if err != nil {
		t.Fatalf("load: %v\n%s", err, raw)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("invalid OpenAPI document: %v\n%s", err, raw)
	}
}

// get walks a decoded JSON document: get(m, "paths", "/x", "get").
func get(t *testing.T, v any, keys ...any) any {
	t.Helper()
	for _, k := range keys {
		switch key := k.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("expected object at %v", k)
			}
			v = m[key]
		case int:
			a, ok := v.([]any)
			if !ok || key >= len(a) {
				t.Fatalf("expected array with index %d", key)
			}
			v = a[key]
		}
	}
	return v
}

func TestRequestMapping(t *testing.T) {
	_, m := build(t)
	create := get(t, m, "paths", "/api/v1/products", "post")

	if get(t, create, "operationId") != "CreateProduct" || get(t, create, "summary") != "Create product" {
		t.Fatalf("unexpected id/summary: %v", create)
	}
	params := get(t, create, "parameters").([]any)
	if len(params) != 2 {
		t.Fatalf("expected query + header params, got %v", params)
	}
	if get(t, params[0], "in") != "query" || get(t, params[1], "in") != "header" ||
		get(t, params[1], "required") != true || get(t, params[1], "description") != "Tenant id" {
		t.Fatalf("unexpected params: %v", params)
	}

	if get(t, create, "requestBody", "required") != true {
		t.Fatal("body with required fields must be required")
	}
	body := get(t, m, "components", "schemas", "CreateProductRequest")
	props := get(t, body, "properties").(map[string]any)
	for _, excluded := range []string{"Tenant", "DryRun", "X-Tenant", "dry_run"} {
		if _, ok := props[excluded]; ok {
			t.Fatalf("parameter %s leaked into body schema", excluded)
		}
	}
	name := props["name"]
	if get(t, name, "minLength") != 2.0 || get(t, name, "maxLength") != 120.0 || get(t, name, "example") != "Kalem" {
		t.Fatalf("name constraints: %v", name)
	}
	if enum := get(t, props["status"], "enum").([]any); len(enum) != 2 || enum[0] != "draft" {
		t.Fatalf("status enum: %v", enum)
	}
	if get(t, props["email"], "format") != "email" {
		t.Fatal("email format")
	}
	if get(t, props["tags"], "maxItems") != 5.0 || get(t, props["tags"], "items", "minLength") != 2.0 {
		t.Fatalf("tags constraints: %v", props["tags"])
	}
	rating := props["rating"]
	if get(t, rating, "minimum") != 0.0 || get(t, rating, "exclusiveMinimum") != true || get(t, rating, "maximum") != 5.0 {
		t.Fatalf("rating constraints: %v", rating)
	}
	required := get(t, body, "required").([]any)
	if len(required) != 2 || required[0] != "name" || required[1] != "price" {
		t.Fatalf("required = %v", required)
	}
}

func TestPathParamsAndResponses(t *testing.T) {
	_, m := build(t)
	getOp := get(t, m, "paths", "/api/v1/products/{id}", "get")
	p0 := get(t, getOp, "parameters", 0)
	if get(t, p0, "name") != "id" || get(t, p0, "in") != "path" || get(t, p0, "required") != true ||
		get(t, p0, "schema", "format") != "uuid" {
		t.Fatalf("path param: %v", p0)
	}
	responses := get(t, getOp, "responses").(map[string]any)
	for _, code := range []string{"200", "400", "404", "422", "500"} {
		if _, ok := responses[code]; !ok {
			t.Errorf("missing response %s in %v", code, responses)
		}
	}
	if get(t, responses["404"], "content", "application/json", "schema", "$ref") != "#/components/schemas/ErrorResponse" {
		t.Fatal("error responses must reference the error schema")
	}

	del := get(t, m, "paths", "/api/v1/products/{id}", "delete")
	if _, ok := get(t, del, "responses", "204").(map[string]any)["content"]; ok {
		t.Fatal("204 must not have content")
	}
	if get(t, del, "requestBody") != nil {
		t.Fatal("DELETE without body fields must not have a request body")
	}

	files := get(t, m, "paths", "/files/{bucket}/{wildcard}", "get")
	if len(get(t, files, "parameters").([]any)) != 2 {
		t.Fatalf("undeclared path params must be documented: %v", files)
	}
	if _, ok := get(t, files, "responses").(map[string]any)["400"]; !ok {
		t.Fatal("routes with parameters document 400")
	}
}

func TestSchemaGeneration(t *testing.T) {
	_, m := build(t)
	schemas := get(t, m, "components", "schemas").(map[string]any)
	for _, name := range []string{"Product", "Money", "Category", "Page_Product", "ErrorResponse"} {
		if _, ok := schemas[name]; !ok {
			t.Errorf("missing component %s (have %v)", name, keys(schemas))
		}
	}
	props := get(t, schemas["Product"], "properties").(map[string]any)
	if get(t, props["created_at"], "format") != "date-time" {
		t.Fatal("embedded struct must be flattened and time must be date-time")
	}
	if _, ok := props["Secret"]; ok {
		t.Fatal(`json:"-" fields must be skipped`)
	}
	if get(t, props["price"], "description") != "Unit price" ||
		get(t, props["price"], "allOf", 0, "$ref") != "#/components/schemas/Money" {
		t.Fatalf("described refs must be wrapped in allOf: %v", props["price"])
	}
	if get(t, props["note"], "nullable") != true {
		t.Fatal("pointer to scalar must be nullable")
	}
	if get(t, props["attrs"], "additionalProperties", "type") != "string" {
		t.Fatal("maps must use additionalProperties")
	}
	if get(t, schemas["Category"], "properties", "children", "items", "$ref") != "#/components/schemas/Category" {
		t.Fatal("recursive types must reference themselves")
	}
	if get(t, schemas["Money"], "properties", "currency", "minLength") != 3.0 {
		t.Fatal("len rule on string")
	}
}

func TestReRegistrationReplaces(t *testing.T) {
	b, _ := build(t)
	b.Add(openapi.OperationSpec{Method: http.MethodPost, Path: "/api/v1/products", OperationID: "CreateProduct", Summary: "New"})
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	if op := doc.Paths["/api/v1/products"]["post"]; op.Summary != "New" || op.OperationID != "CreateProduct" {
		t.Fatalf("re-registration: %+v", op)
	}
}

func TestDuplicateOperationIDsAreMadeUnique(t *testing.T) {
	b := openapi.NewBuilder(openapi.Info{Title: "x", Version: "1"}, nil)
	b.Add(openapi.OperationSpec{Method: "GET", Path: "/a", OperationID: "Get"})
	b.Add(openapi.OperationSpec{Method: "GET", Path: "/b", OperationID: "Get"})
	doc, _ := b.Document()
	if doc.Paths["/a"]["get"].OperationID == doc.Paths["/b"]["get"].OperationID {
		t.Fatal("operation ids must be unique")
	}
}

func TestConvertPath(t *testing.T) {
	cases := map[string]string{
		"":                   "/",
		"/":                  "/",
		"/products/":         "/products",
		"/products/:id<int>": "/products/{id}",
		"/users/:name?":      "/users/{name}",
		"/static/*":          "/static/{wildcard}",
		"/a/:x/b/:y":         "/a/{x}/b/{y}",
	}
	for in, want := range cases {
		if got, _ := openapi.ConvertPath(in); got != want {
			t.Errorf("ConvertPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
