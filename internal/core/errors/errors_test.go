package errors_test

import (
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/bdrtr/gobit/internal/core/errors"
)

func TestConstructorsSetKind(t *testing.T) {
	tests := map[string]struct {
		err  *errors.Error
		kind errors.Kind
	}{
		"not found":    {errors.NotFound("product_not_found", "product %s not found", "prod_1"), errors.KindNotFound},
		"invalid":      {errors.Invalid("bad_qty", "the quantity must be positive"), errors.KindInvalid},
		"conflict":     {errors.Conflict("dup_sku", "the sku already exists"), errors.KindConflict},
		"unauthorized": {errors.Unauthorized("no_token", "no token"), errors.KindUnauthorized},
		"forbidden":    {errors.Forbidden("no_scope", "not permitted"), errors.KindForbidden},
		"unavailable":  {errors.Unavailable("db_down", "the database is unreachable"), errors.KindUnavailable},
		"internal":     {errors.Internal("boom", "unexpected"), errors.KindInternal},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.err.Kind != tt.kind {
				t.Errorf("Kind = %v, want %v", tt.err.Kind, tt.kind)
			}
			if errors.KindOf(tt.err) != tt.kind {
				t.Errorf("KindOf() = %v, want %v", errors.KindOf(tt.err), tt.kind)
			}
		})
	}
}

func TestFormatArguments(t *testing.T) {
	err := errors.NotFound("product_not_found", "product %s not found", "prod_01")
	if got, want := err.Message, "product prod_01 not found"; got != want {
		t.Errorf("Message = %q, want %q", got, want)
	}
	if got := err.Error(); got != "product_not_found: product prod_01 not found" {
		t.Errorf("Error() = %q", got)
	}
}

func TestWrapPreservesChain(t *testing.T) {
	sentinel := stderrors.New("connection refused")
	wrapped := errors.Wrap(sentinel, errors.KindUnavailable, "db_unreachable", "postgres could not be reached")

	if !stderrors.Is(wrapped, sentinel) {
		t.Error("errors.Is did not find the wrapped error")
	}
	if errors.KindOf(wrapped) != errors.KindUnavailable {
		t.Errorf("KindOf() = %v, want KindUnavailable", errors.KindOf(wrapped))
	}
	if got := wrapped.Error(); got != "db_unreachable: postgres could not be reached: connection refused" {
		t.Errorf("Error() = %q", got)
	}
}

func TestWrapNilReturnsNil(t *testing.T) {
	// So that the caller needs no separate nil check.
	if got := errors.Wrap(nil, errors.KindInternal, "x", "y"); got != nil {
		t.Errorf("Wrap(nil) = %v, want nil", got)
	}
}

func TestKindOfThroughDeepChain(t *testing.T) {
	// The class must still be findable when the typed error is wrapped with
	// fmt.Errorf.
	base := errors.NotFound("cart_not_found", "no such cart")
	outer := fmt.Errorf("the cart total could not be computed: %w", base)

	if !errors.IsNotFound(outer) {
		t.Error("IsNotFound did not find it deep in the chain")
	}
	if got, want := errors.CodeOf(outer), "cart_not_found"; got != want {
		t.Errorf("CodeOf() = %q, want %q", got, want)
	}
}

func TestUntypedErrorDefaultsToInternal(t *testing.T) {
	// The zero value being KindInternal is deliberate: an unclassified error
	// must not accidentally behave like a "not found" and return a 404.
	plain := stderrors.New("a plain error")
	if got := errors.KindOf(plain); got != errors.KindInternal {
		t.Errorf("KindOf(a plain error) = %v, want KindInternal", got)
	}
	if errors.CodeOf(plain) != "" {
		t.Errorf("CodeOf(a plain error) = %q, want empty", errors.CodeOf(plain))
	}
	if errors.IsNotFound(plain) {
		t.Error("a plain error came back as IsNotFound")
	}
}

func TestZeroKindIsInternal(t *testing.T) {
	var k errors.Kind
	if k != errors.KindInternal {
		t.Errorf("the zero Kind = %v, want KindInternal", k)
	}
	if k.String() != "internal" {
		t.Errorf("String() = %q, want %q", k.String(), "internal")
	}
}

func TestWithDetails(t *testing.T) {
	err := errors.Invalid("validation_failed", "the input is invalid").
		WithDetails(map[string]any{"field": "quantity"}).
		WithDetails(map[string]any{"reason": "negative"})

	if got := err.Details["field"]; got != "quantity" {
		t.Errorf("Details[field] = %v", got)
	}
	if got := err.Details["reason"]; got != "negative" {
		t.Errorf("Details[reason] = %v", got)
	}
}

func TestNilSafety(t *testing.T) {
	var e *errors.Error
	if got := e.Error(); got != "<nil>" {
		t.Errorf("nil Error() = %q", got)
	}
	if e.Unwrap() != nil {
		t.Error("nil Unwrap() did not return nil")
	}
	if e.WithDetails(map[string]any{"a": 1}) != nil {
		t.Error("nil WithDetails() did not return nil")
	}
}

func TestKindString(t *testing.T) {
	tests := map[errors.Kind]string{
		errors.KindNotFound:     "not_found",
		errors.KindInvalid:      "invalid",
		errors.KindConflict:     "conflict",
		errors.KindUnauthorized: "unauthorized",
		errors.KindForbidden:    "forbidden",
		errors.KindUnavailable:  "unavailable",
		errors.KindInternal:     "internal",
		errors.Kind(200):        "internal",
	}
	for k, want := range tests {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

func TestStdlibHelpersReExported(t *testing.T) {
	// core/errors must be importable in place of the stdlib errors.
	a := errors.New("a")
	b := errors.New("b")
	joined := errors.Join(a, b)

	if !errors.Is(joined, a) || !errors.Is(joined, b) {
		t.Error("Join/Is behaved as if they had not been re-exported")
	}

	var target *errors.Error
	typed := errors.Conflict("c", "a conflict")
	if !errors.As(error(typed), &target) {
		t.Error("As did not find the typed error")
	}
}
