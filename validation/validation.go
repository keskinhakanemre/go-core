// Package validation wraps go-playground/validator and converts its errors
// into apperror validation errors with client friendly field names.
package validation

import (
	"errors"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"

	"github.com/hekanemre/go-core/apperror"
)

var (
	once sync.Once
	v    *validator.Validate
)

// Validator returns the shared validator instance. Register custom rules on
// it during startup (before serving traffic):
//
//	validation.Validator().RegisterValidation("sku", isSKU)
func Validator() *validator.Validate {
	once.Do(func() {
		v = validator.New(validator.WithRequiredStructEnabled())
		v.RegisterTagNameFunc(fieldName)
	})
	return v
}

// fieldName reports fields by the name the client used: json, then params,
// query and reqHeader tags, falling back to the Go field name.
func fieldName(f reflect.StructField) string {
	for _, tag := range []string{"json", "params", "query", "reqHeader", "form"} {
		name := strings.Split(f.Tag.Get(tag), ",")[0]
		if name == "-" {
			continue
		}
		if name != "" {
			return name
		}
	}
	return f.Name
}

// FieldError describes a single failed rule.
type FieldError struct {
	Field string `json:"field"`
	Rule  string `json:"rule"`
	Param string `json:"param,omitempty"`
}

// ErrValidation is the sentinel for validation failures; use errors.Is.
var ErrValidation = apperror.Validation("validation_failed", "request validation failed")

// Struct validates s. On failure it returns ErrValidation with []FieldError details.
func Struct(s any) error {
	err := Validator().Struct(s)
	if err == nil {
		return nil
	}
	var invalid *validator.InvalidValidationError
	if errors.As(err, &invalid) {
		// Non struct input (e.g. a map or nil) - nothing to validate.
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return apperror.Internal(err)
	}
	fields := make([]FieldError, 0, len(verrs))
	for _, fe := range verrs {
		fields = append(fields, FieldError{Field: fieldPath(fe), Rule: fe.Tag(), Param: fe.Param()})
	}
	return ErrValidation.WithDetails(fields)
}

// fieldPath drops the top level struct name: "CreateReq.items[0].name" -> "items[0].name".
func fieldPath(fe validator.FieldError) string {
	ns := fe.Namespace()
	if i := strings.IndexByte(ns, '.'); i >= 0 {
		return ns[i+1:]
	}
	return fe.Field()
}
