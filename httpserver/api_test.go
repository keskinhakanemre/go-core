package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gofiber/fiber/v2"

	"github.com/keskinhakanemre/go-core/config"
	"github.com/keskinhakanemre/go-core/httpserver"
)

type CreateItemHandler struct{}

func (CreateItemHandler) Handle(_ context.Context, r *createItemReq) (*item, error) {
	return &item{ID: knownID, Name: r.Name}, nil
}

func newDocumentedApp(t *testing.T) (*fiber.App, *httpserver.API) {
	t.Helper()
	app := httpserver.New(config.HTTPConfig{})
	api := httpserver.NewAPI(app, httpserver.APIInfo{Title: "Items API", Version: "1.2.3"})
	api.DescribeTag("Items", "Item management")

	items := api.Group("/api/v1/items", "Items")
	httpserver.Post(items, "", CreateItemHandler{}, httpserver.WithStatus(http.StatusCreated))
	httpserver.Get(items, "/:id", httpserver.Func(func(_ context.Context, r *getItemReq) (*item, error) {
		if r.ID != knownID {
			return nil, errItemNotFound
		}
		return &item{ID: r.ID}, nil
	}), httpserver.WithSummary("Fetch an item"), httpserver.WithErrors(http.StatusNotFound))
	httpserver.Delete(items, "/:id", httpserver.Func(func(context.Context, *getItemReq) (*struct{}, error) {
		return nil, nil
	}), httpserver.WithStatus(http.StatusNoContent), httpserver.WithDeprecated())
	httpserver.Get(api, "/internal/ping", httpserver.Func(func(context.Context, *struct{}) (*item, error) {
		return &item{Name: "pong"}, nil
	}), httpserver.WithoutDocs())

	api.ServeDocs("/openapi.json", "/docs")
	return app, api
}

func TestAPIRoutesServeTraffic(t *testing.T) {
	app, _ := newDocumentedApp(t)

	resp, body := do(t, app, jsonReq(http.MethodPost, "/api/v1/items", `{"name":"pen"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	resp, _ = do(t, app, httptest.NewRequest(http.MethodGet, "/api/v1/items/"+missingID, nil))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get missing: %d", resp.StatusCode)
	}
	resp, _ = do(t, app, httptest.NewRequest(http.MethodDelete, "/api/v1/items/"+knownID, nil))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	resp, _ = do(t, app, httptest.NewRequest(http.MethodGet, "/internal/ping", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hidden route must still work: %d", resp.StatusCode)
	}
}

func TestAPIServesValidSpec(t *testing.T) {
	app, _ := newDocumentedApp(t)
	resp, body := do(t, app, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("spec: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	doc, err := openapi3.NewLoader().LoadFromData([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("invalid spec: %v\n%s", err, body)
	}

	if doc.Info.Title != "Items API" || doc.Info.Version != "1.2.3" {
		t.Fatalf("info: %+v", doc.Info)
	}
	create := doc.Paths.Find("/api/v1/items").Post
	if create == nil || create.OperationID != "CreateItem" || create.Summary != "Create item" ||
		create.Responses.Status(http.StatusCreated) == nil || len(create.Tags) != 1 || create.Tags[0] != "Items" {
		t.Fatalf("create operation: %+v", create)
	}
	getOp := doc.Paths.Find("/api/v1/items/{id}").Get
	if getOp == nil || getOp.Summary != "Fetch an item" || getOp.Responses.Status(http.StatusNotFound) == nil {
		t.Fatalf("get operation: %+v", getOp)
	}
	if del := doc.Paths.Find("/api/v1/items/{id}").Delete; del == nil || !del.Deprecated {
		t.Fatal("delete must be documented as deprecated")
	}
	if doc.Paths.Find("/internal/ping") != nil {
		t.Fatal("WithoutDocs routes must not be documented")
	}
	if doc.Paths.Find("/openapi.json") != nil || doc.Paths.Find("/docs") != nil {
		t.Fatal("documentation endpoints must not document themselves")
	}
	var tagDesc string
	for _, tag := range doc.Tags {
		if tag.Name == "Items" {
			tagDesc = tag.Description
		}
	}
	if tagDesc != "Item management" {
		t.Fatalf("tag description: %q", tagDesc)
	}
}

func TestAPIServesSwaggerUI(t *testing.T) {
	app, _ := newDocumentedApp(t)
	resp, body := do(t, app, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("docs: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{`url: "/openapi.json"`, "swagger-ui-bundle.js", "Items API"} {
		if !strings.Contains(body, want) {
			t.Errorf("docs page missing %q", want)
		}
	}
}

func TestAPISpecMatchesRequestStruct(t *testing.T) {
	_, api := newDocumentedApp(t)
	raw, err := json.Marshal(api.Spec())
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name   string `json:"name"`
				In     string `json:"in"`
				Schema struct {
					Format string `json:"format"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	params := m.Paths["/api/v1/items/{id}"]["get"].Parameters
	got := map[string]string{}
	for _, p := range params {
		got[p.In+":"+p.Name] = p.Schema.Format
	}
	// getItemReq: params:"id" validate uuid4, query:"expand", reqHeader:"X-Tenant"
	if f, ok := got["path:id"]; !ok || f != "uuid" {
		t.Fatalf("path param id: %v", got)
	}
	if _, ok := got["query:expand"]; !ok {
		t.Fatalf("query param expand: %v", got)
	}
	if _, ok := got["header:X-Tenant"]; !ok {
		t.Fatalf("header param X-Tenant: %v", got)
	}
}
