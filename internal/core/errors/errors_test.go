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
		"not found":    {errors.NotFound("product_not_found", "ürün %s bulunamadı", "prod_1"), errors.KindNotFound},
		"invalid":      {errors.Invalid("bad_qty", "adet pozitif olmalı"), errors.KindInvalid},
		"conflict":     {errors.Conflict("dup_sku", "sku zaten var"), errors.KindConflict},
		"unauthorized": {errors.Unauthorized("no_token", "token yok"), errors.KindUnauthorized},
		"forbidden":    {errors.Forbidden("no_scope", "yetki yok"), errors.KindForbidden},
		"unavailable":  {errors.Unavailable("db_down", "veritabanı erişilemez"), errors.KindUnavailable},
		"internal":     {errors.Internal("boom", "beklenmedik"), errors.KindInternal},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.err.Kind != tt.kind {
				t.Errorf("Kind = %v, beklenen %v", tt.err.Kind, tt.kind)
			}
			if errors.KindOf(tt.err) != tt.kind {
				t.Errorf("KindOf() = %v, beklenen %v", errors.KindOf(tt.err), tt.kind)
			}
		})
	}
}

func TestFormatArguments(t *testing.T) {
	err := errors.NotFound("product_not_found", "ürün %s bulunamadı", "prod_01")
	if got, want := err.Message, "ürün prod_01 bulunamadı"; got != want {
		t.Errorf("Message = %q, beklenen %q", got, want)
	}
	if got := err.Error(); got != "product_not_found: ürün prod_01 bulunamadı" {
		t.Errorf("Error() = %q", got)
	}
}

func TestWrapPreservesChain(t *testing.T) {
	sentinel := stderrors.New("bağlantı reddedildi")
	wrapped := errors.Wrap(sentinel, errors.KindUnavailable, "db_unreachable", "postgres'e bağlanılamadı")

	if !stderrors.Is(wrapped, sentinel) {
		t.Error("errors.Is sarmalanan hatayı bulamadı")
	}
	if errors.KindOf(wrapped) != errors.KindUnavailable {
		t.Errorf("KindOf() = %v, beklenen KindUnavailable", errors.KindOf(wrapped))
	}
	if got := wrapped.Error(); got != "db_unreachable: postgres'e bağlanılamadı: bağlantı reddedildi" {
		t.Errorf("Error() = %q", got)
	}
}

func TestWrapNilReturnsNil(t *testing.T) {
	// Çağıran tarafta ayrıca nil kontrolü gerekmesin diye.
	if got := errors.Wrap(nil, errors.KindInternal, "x", "y"); got != nil {
		t.Errorf("Wrap(nil) = %v, beklenen nil", got)
	}
}

func TestKindOfThroughDeepChain(t *testing.T) {
	// Tipli hata fmt.Errorf ile sarıldığında da sınıf bulunabilmeli.
	base := errors.NotFound("cart_not_found", "sepet yok")
	outer := fmt.Errorf("sepet toplamı hesaplanamadı: %w", base)

	if !errors.IsNotFound(outer) {
		t.Error("IsNotFound derin zincirde bulamadı")
	}
	if got, want := errors.CodeOf(outer), "cart_not_found"; got != want {
		t.Errorf("CodeOf() = %q, beklenen %q", got, want)
	}
}

func TestUntypedErrorDefaultsToInternal(t *testing.T) {
	// Sıfır değerin KindInternal olması bilinçli: sınıflandırılmamış bir hata
	// kazara "bulunamadı" gibi davranıp 404 dönmemeli.
	plain := stderrors.New("düz hata")
	if got := errors.KindOf(plain); got != errors.KindInternal {
		t.Errorf("KindOf(düz hata) = %v, beklenen KindInternal", got)
	}
	if errors.CodeOf(plain) != "" {
		t.Errorf("CodeOf(düz hata) = %q, beklenen boş", errors.CodeOf(plain))
	}
	if errors.IsNotFound(plain) {
		t.Error("düz hata IsNotFound oldu")
	}
}

func TestZeroKindIsInternal(t *testing.T) {
	var k errors.Kind
	if k != errors.KindInternal {
		t.Errorf("sıfır Kind = %v, beklenen KindInternal", k)
	}
	if k.String() != "internal" {
		t.Errorf("String() = %q, beklenen %q", k.String(), "internal")
	}
}

func TestWithDetails(t *testing.T) {
	err := errors.Invalid("validation_failed", "girdi geçersiz").
		WithDetails(map[string]any{"field": "quantity"}).
		WithDetails(map[string]any{"reason": "negatif"})

	if got := err.Details["field"]; got != "quantity" {
		t.Errorf("Details[field] = %v", got)
	}
	if got := err.Details["reason"]; got != "negatif" {
		t.Errorf("Details[reason] = %v", got)
	}
}

func TestNilSafety(t *testing.T) {
	var e *errors.Error
	if got := e.Error(); got != "<nil>" {
		t.Errorf("nil Error() = %q", got)
	}
	if e.Unwrap() != nil {
		t.Error("nil Unwrap() nil dönmedi")
	}
	if e.WithDetails(map[string]any{"a": 1}) != nil {
		t.Error("nil WithDetails() nil dönmedi")
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
			t.Errorf("Kind(%d).String() = %q, beklenen %q", k, got, want)
		}
	}
}

func TestStdlibHelpersReExported(t *testing.T) {
	// core/errors, stdlib errors'ın yerine import edilebilmeli.
	a := errors.New("a")
	b := errors.New("b")
	joined := errors.Join(a, b)

	if !errors.Is(joined, a) || !errors.Is(joined, b) {
		t.Error("Join/Is yeniden dışa verilmemiş gibi davrandı")
	}

	var target *errors.Error
	typed := errors.Conflict("c", "çakışma")
	if !errors.As(error(typed), &target) {
		t.Error("As tipli hatayı bulamadı")
	}
}
