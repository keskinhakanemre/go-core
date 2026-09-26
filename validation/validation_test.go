package validation_test

import (
	"errors"
	"testing"

	"github.com/keskinhakanemre/go-core/apperror"
	"github.com/keskinhakanemre/go-core/validation"
)

type item struct {
	Name string `json:"name" validate:"required"`
}

type request struct {
	ID    string `params:"id" validate:"required,uuid4"`
	Limit int    `query:"limit" validate:"lte=100"`
	Items []item `json:"items" validate:"dive"`
}

func TestStructOK(t *testing.T) {
	err := validation.Struct(&request{ID: "0b8a4b6e-0f3e-4a8b-9a55-6b3c8e1f0a11", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStructErrors(t *testing.T) {
	err := validation.Struct(&request{ID: "nope", Limit: 500, Items: []item{{}}})
	if !errors.Is(err, validation.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
	ae := apperror.From(err)
	if ae.Kind != apperror.KindValidation {
		t.Fatalf("kind = %s", ae.Kind)
	}
	fields := ae.Details.([]validation.FieldError)
	got := map[string]string{}
	for _, f := range fields {
		got[f.Field] = f.Rule
	}
	want := map[string]string{"id": "uuid4", "limit": "lte", "items[0].name": "required"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("field %s: got rule %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

func TestNonStructIsIgnored(t *testing.T) {
	if err := validation.Struct(map[string]string{}); err != nil {
		t.Fatal(err)
	}
}
