// Package errors gobit'in tipli hata modelini sağlar.
//
// Servisler dışarıya bu paketin ürettiği *Error değerlerini döner; HTTP katmanı
// Kind alanını status koduna çevirir (plan Bölüm 2.7 ve Bölüm 8). Paket, stdlib
// errors'ın As/Is/Join/New/Unwrap yardımcılarını da yeniden dışa verir; böylece
// çağıran taraf iki ayrı errors paketi import etmek zorunda kalmaz.
package errors

import (
	stderrors "errors"
	"fmt"
)

// stdlib errors yardımcıları; bu paket onun yerine import edilebilsin diye.
var (
	As     = stderrors.As
	Is     = stderrors.Is
	Join   = stderrors.Join
	New    = stderrors.New
	Unwrap = stderrors.Unwrap
)

// Kind bir hatanın sınıfıdır. Sıfır değeri KindInternal'dır; böylece
// sınıflandırılmamış bir hata kazara "bulunamadı" gibi davranmaz.
type Kind uint8

// Hata sınıfları. HTTP eşlemesi için internal/core/http paketine bakın.
const (
	// KindInternal beklenmeyen sunucu hatasıdır (sıfır değer).
	KindInternal Kind = iota
	// KindNotFound istenen kaynağın bulunamadığını bildirir.
	KindNotFound
	// KindInvalid girdinin doğrulamadan geçmediğini bildirir.
	KindInvalid
	// KindConflict mevcut durumla çakışmayı bildirir (örn. tekrarlanan kayıt).
	KindConflict
	// KindUnauthorized kimlik doğrulamanın eksik veya geçersiz olduğunu bildirir.
	KindUnauthorized
	// KindForbidden kimliğin doğrulandığını ama yetkinin yetmediğini bildirir.
	KindForbidden
	// KindUnavailable bir alt sistemin geçici olarak erişilemez olduğunu bildirir.
	KindUnavailable
)

// String Kind'ın okunabilir adını döner.
func (k Kind) String() string {
	switch k {
	case KindNotFound:
		return "not_found"
	case KindInvalid:
		return "invalid"
	case KindConflict:
		return "conflict"
	case KindUnauthorized:
		return "unauthorized"
	case KindForbidden:
		return "forbidden"
	case KindUnavailable:
		return "unavailable"
	case KindInternal:
		return "internal"
	default:
		return "internal"
	}
}

// Error gobit'in tipli hatasıdır.
type Error struct {
	// Kind hatanın sınıfıdır; taşıma katmanı buna göre status kodu seçer.
	Kind Kind
	// Code makine tarafından okunabilen sabit koddur (örn. "product_not_found").
	// İstemciler buna göre dallanabilir; Message'ın aksine değişmez sayılır.
	Code string
	// Message insan tarafından okunabilen açıklamadır. Hassas veri içermemelidir.
	Message string
	// Details isteğe bağlı yapısal bağlamdır (örn. hangi alanın geçersiz olduğu).
	Details map[string]any
	// err sarmalanan alttaki hatadır; Unwrap ile erişilir.
	err error
}

// Error, error arayüzünü karşılar.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := e.Message
	if e.Code != "" {
		msg = e.Code + ": " + msg
	}
	if e.err != nil {
		return msg + ": " + e.err.Error()
	}
	return msg
}

// Unwrap sarmalanan hatayı döner; errors.Is/As zinciri için gereklidir.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// WithDetails hataya yapısal bağlam ekleyip aynı hatayı döner.
// Details nil ise oluşturulur; var olan anahtarlar ezilir.
func (e *Error) WithDetails(kv map[string]any) *Error {
	if e == nil {
		return nil
	}
	if e.Details == nil {
		e.Details = make(map[string]any, len(kv))
	}
	for k, v := range kv {
		e.Details[k] = v
	}
	return e
}

// newError ortak yapıcıdır.
func newError(kind Kind, code, format string, a ...any) *Error {
	return &Error{
		Kind:    kind,
		Code:    code,
		Message: fmt.Sprintf(format, a...),
	}
}

// NotFound istenen kaynağın bulunamadığını bildiren hata üretir.
func NotFound(code, format string, a ...any) *Error {
	return newError(KindNotFound, code, format, a...)
}

// Invalid girdinin doğrulamadan geçmediğini bildiren hata üretir.
func Invalid(code, format string, a ...any) *Error {
	return newError(KindInvalid, code, format, a...)
}

// Conflict mevcut durumla çakışmayı bildiren hata üretir.
func Conflict(code, format string, a ...any) *Error {
	return newError(KindConflict, code, format, a...)
}

// Unauthorized kimlik doğrulamanın eksik veya geçersiz olduğunu bildirir.
func Unauthorized(code, format string, a ...any) *Error {
	return newError(KindUnauthorized, code, format, a...)
}

// Forbidden kimliğin doğrulandığını ama yetkinin yetmediğini bildirir.
func Forbidden(code, format string, a ...any) *Error {
	return newError(KindForbidden, code, format, a...)
}

// Unavailable bir alt sistemin geçici olarak erişilemez olduğunu bildirir.
func Unavailable(code, format string, a ...any) *Error {
	return newError(KindUnavailable, code, format, a...)
}

// Internal beklenmeyen sunucu hatasını bildirir.
func Internal(code, format string, a ...any) *Error {
	return newError(KindInternal, code, format, a...)
}

// Wrap var olan bir hatayı tipli hataya sarar. err nil ise nil döner;
// böylece çağıran tarafta ayrıca nil kontrolü gerekmez.
func Wrap(err error, kind Kind, code, format string, a ...any) *Error {
	if err == nil {
		return nil
	}
	e := newError(kind, code, format, a...)
	e.err = err
	return e
}

// KindOf zincirdeki ilk *Error'ın Kind'ını döner.
// Zincirde tipli hata yoksa KindInternal döner (güvenli varsayılan).
func KindOf(err error) Kind {
	var e *Error
	if stderrors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

// CodeOf zincirdeki ilk *Error'ın Code'unu döner; yoksa boş dize.
func CodeOf(err error) string {
	var e *Error
	if stderrors.As(err, &e) {
		return e.Code
	}
	return ""
}

// HasKind hatanın (zincir boyunca) verilen sınıfta olup olmadığını bildirir.
// stdlib Is ile karışmaması için ayrı adlandırılmıştır.
func HasKind(err error, kind Kind) bool {
	return KindOf(err) == kind
}

// IsNotFound hatanın KindNotFound sınıfında olup olmadığını bildirir.
func IsNotFound(err error) bool { return HasKind(err, KindNotFound) }

// IsInvalid hatanın KindInvalid sınıfında olup olmadığını bildirir.
func IsInvalid(err error) bool { return HasKind(err, KindInvalid) }

// IsConflict hatanın KindConflict sınıfında olup olmadığını bildirir.
func IsConflict(err error) bool { return HasKind(err, KindConflict) }

// IsUnauthorized hatanın KindUnauthorized sınıfında olup olmadığını bildirir.
func IsUnauthorized(err error) bool { return HasKind(err, KindUnauthorized) }

// IsForbidden hatanın KindForbidden sınıfında olup olmadığını bildirir.
func IsForbidden(err error) bool { return HasKind(err, KindForbidden) }
