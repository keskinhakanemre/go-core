package httpserver

import (
	"context"
	"html"
	"net/http"
	"reflect"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/keskinhakanemre/go-core/openapi"
)

// APIInfo is the metadata shown at the top of the API documentation.
type APIInfo struct {
	Title       string
	Version     string
	Description string
}

// API registers typed routes and documents them in an OpenAPI document at
// the same time. Routes registered directly on Fiber keep working but are not
// documented.
//
//	api := httpserver.NewAPI(app, httpserver.APIInfo{Title: "Order Service", Version: "1.0.0"})
//	products := api.Group("/api/v1/products", "Products")
//	httpserver.Post(products, "", createHandler, httpserver.WithStatus(fiber.StatusCreated))
//	httpserver.Get(products, "/:id", getHandler, httpserver.WithErrors(fiber.StatusNotFound))
//	api.ServeDocs("/openapi.json", "/docs")
type API struct {
	router fiber.Router
	prefix string
	tags   []string
	shared *apiShared
}

type apiShared struct {
	root fiber.Router
	spec *openapi.Builder
}

// NewAPI wraps r (usually the *fiber.App) for documented route registration.
func NewAPI(r fiber.Router, info APIInfo) *API {
	spec := openapi.NewBuilder(openapi.Info{
		Title:       info.Title,
		Version:     info.Version,
		Description: info.Description,
	}, reflect.TypeFor[ErrorResponse]())
	return &API{router: r, shared: &apiShared{root: r, spec: spec}}
}

// Group returns a sub API under prefix. Operations registered on it are
// grouped under tags in the documentation (the parent's tags when none given).
func (a *API) Group(prefix string, tags ...string) *API {
	if len(tags) == 0 {
		tags = a.tags
	}
	return &API{router: a.router.Group(prefix), prefix: joinPath(a.prefix, prefix), tags: tags, shared: a.shared}
}

// Router returns the underlying Fiber router, e.g. to add group middleware.
func (a *API) Router() fiber.Router { return a.router }

// Spec returns the OpenAPI document builder.
func (a *API) Spec() *openapi.Builder { return a.shared.spec }

// DescribeTag adds a description to a tag shown in the documentation.
func (a *API) DescribeTag(tag, description string) { a.shared.spec.SetTagDescription(tag, description) }

// ServeDocs serves the OpenAPI document at specPath and an interactive
// Swagger UI at uiPath on the root router. Swagger UI assets are loaded from
// the jsDelivr CDN by the browser.
func (a *API) ServeDocs(specPath, uiPath string) {
	spec := a.shared.spec
	a.shared.root.Get(specPath, func(c *fiber.Ctx) error {
		b, err := spec.MarshalJSON()
		if err != nil {
			return err
		}
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
		return c.Send(b)
	})
	if uiPath == "" {
		return
	}
	title := "API"
	if doc, err := spec.Document(); err == nil && doc.Info.Title != "" {
		title = doc.Info.Title
	}
	page := []byte(strings.NewReplacer(
		"{{title}}", html.EscapeString(title),
		"{{spec}}", html.EscapeString(specPath),
	).Replace(swaggerUIPage))
	a.shared.root.Get(uiPath, func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
		return c.Send(page)
	})
}

// Route registers h for method and path and documents it.
func Route[Req any, Res any](a *API, method, path string, h Handler[Req, Res], opts ...HandleOption) {
	o := newHandleOptions(opts)
	a.router.Add(method, path, Handle(h, opts...))
	if o.hidden {
		return
	}

	tags := o.tags
	if len(tags) == 0 {
		tags = a.tags
	}
	opID := o.operationID
	if opID == "" {
		opID = handlerName(h)
	}
	a.shared.spec.Add(openapi.OperationSpec{
		Method:        method,
		Path:          joinPath(a.prefix, path),
		Request:       reflect.TypeFor[Req](),
		Response:      reflect.TypeFor[Res](),
		SuccessStatus: o.status,
		OperationID:   opID,
		Summary:       o.summary,
		Description:   o.description,
		Tags:          tags,
		ErrorStatuses: o.errors,
		Deprecated:    o.deprecated,
	})
}

// Get registers a documented GET route.
func Get[Req any, Res any](a *API, path string, h Handler[Req, Res], opts ...HandleOption) {
	Route(a, http.MethodGet, path, h, opts...)
}

// Post registers a documented POST route.
func Post[Req any, Res any](a *API, path string, h Handler[Req, Res], opts ...HandleOption) {
	Route(a, http.MethodPost, path, h, opts...)
}

// Put registers a documented PUT route.
func Put[Req any, Res any](a *API, path string, h Handler[Req, Res], opts ...HandleOption) {
	Route(a, http.MethodPut, path, h, opts...)
}

// Patch registers a documented PATCH route.
func Patch[Req any, Res any](a *API, path string, h Handler[Req, Res], opts ...HandleOption) {
	Route(a, http.MethodPatch, path, h, opts...)
}

// Delete registers a documented DELETE route.
func Delete[Req any, Res any](a *API, path string, h Handler[Req, Res], opts ...HandleOption) {
	Route(a, http.MethodDelete, path, h, opts...)
}

// Func adapts a plain function to Handler with inferred type parameters:
//
//	httpserver.Get(api, "/ping", httpserver.Func(func(ctx context.Context, _ *struct{}) (*Pong, error) { ... }))
func Func[Req any, Res any](fn func(ctx context.Context, req *Req) (*Res, error)) HandlerFunc[Req, Res] {
	return fn
}

// WithSummary sets the one line summary shown in the documentation.
// Default: derived from the handler type (CreateProductHandler -> "Create product").
func WithSummary(s string) HandleOption { return func(o *handleOptions) { o.summary = s } }

// WithDescription sets a longer, Markdown capable description.
func WithDescription(s string) HandleOption { return func(o *handleOptions) { o.description = s } }

// WithTags overrides the documentation tags of the route.
func WithTags(tags ...string) HandleOption { return func(o *handleOptions) { o.tags = tags } }

// WithErrors documents additional error statuses the route can return, e.g.
// 404 or 409. 400, 422 (when the request has validate rules) and 500 are
// documented automatically.
func WithErrors(statuses ...int) HandleOption {
	return func(o *handleOptions) { o.errors = append(o.errors, statuses...) }
}

// WithOperationID sets the operationId used by client generators.
// Default: the handler type name without the "Handler" suffix.
func WithOperationID(id string) HandleOption { return func(o *handleOptions) { o.operationID = id } }

// WithDeprecated marks the route as deprecated in the documentation.
func WithDeprecated() HandleOption { return func(o *handleOptions) { o.deprecated = true } }

// WithoutDocs registers the route without documenting it.
func WithoutDocs() HandleOption { return func(o *handleOptions) { o.hidden = true } }

// handlerName derives an operationId from a named handler type:
// *product.CreateProductHandler -> "CreateProduct". Function handlers yield "".
func handlerName(h any) string {
	t := reflect.TypeOf(h)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Name() == "" || strings.Contains(t.Name(), "[") {
		return ""
	}
	return strings.TrimSuffix(t.Name(), "Handler")
}

func joinPath(prefix, path string) string {
	switch {
	case path == "" || path == "/":
		if prefix == "" {
			return "/"
		}
		return prefix
	case prefix == "":
		return path
	default:
		return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(path, "/")
	}
}

const swaggerUIPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{title}} - API docs</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
<style>body{margin:0}</style>
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
<script>
window.ui = SwaggerUIBundle({
  url: "{{spec}}",
  dom_id: "#swagger-ui",
  deepLinking: true,
  tryItOutEnabled: true,
  displayRequestDuration: true,
  persistAuthorization: true
});
</script>
</body>
</html>
`
