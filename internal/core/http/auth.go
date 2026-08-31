package http

import (
	"context"
	"net/http"
	"strings"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// Auth kodları. İstemciler bunlara göre dallanır; mesajlar değişebilir.
const (
	// CodeUnauthenticated kimlik sunulmadı ya da geçersiz.
	CodeUnauthenticated = "unauthenticated"
	// CodeForbidden kimlik geçerli ama yetki yetmiyor.
	CodeForbidden = "forbidden"
)

// Principal doğrulanmış çağıranın kimliğidir.
//
// Çekirdek, kimliğin NASIL doğrulandığını bilmez: JWT, gizli API anahtarı ya
// da başka bir yöntem olabilir. Yalnızca yetki kararı için gereken alanları
// taşır.
type Principal struct {
	// ID çağıranın benzersiz kimliğidir (kullanıcı ya da API anahtarı).
	ID string
	// Kind kimliğin türüdür: "user" | "api_key".
	Kind string
	// Scopes çağıranın yetkileridir (örn. "admin", "orders:read").
	Scopes []string
	// SalesChannelIDs publishable anahtarın bağlı olduğu satış kanallarıdır;
	// store yüzeyinde katalog süzmesi buna dayanır.
	SalesChannelIDs []string
}

// HasScope çağıranın verilen yetkiye sahip olup olmadığını bildirir.
func (p Principal) HasScope(scope string) bool {
	for _, s := range p.Scopes {
		if s == scope || s == ScopeAdmin {
			return true
		}
	}
	return false
}

// ScopeAdmin tüm yetkileri kapsayan üst yetkidir.
const ScopeAdmin = "admin"

// Authenticator gelen isteğin kimliğini çözer.
//
// Bu arayüz TÜKETİCİ tarafında (çekirdekte) tanımlıdır ve auth modülü onu
// yapısal olarak karşılar (ADR 0001). Çekirdek modülleri tanımadığı için
// (Prensip 2.4) somut uygulama container'dan adla çözülüp buraya verilir.
type Authenticator interface {
	// AuthenticateAdmin admin yüzeyinin kimliğini çözer: Bearer JWT ya da
	// gizli API anahtarı. Kimlik geçersizse errors.Unauthorized döner.
	AuthenticateAdmin(ctx context.Context, scheme, credential string) (Principal, error)

	// AuthenticateStore mağaza yüzeyinin kimliğini çözer: publishable API
	// anahtarı. Anahtar geçersizse errors.Unauthorized döner.
	//
	// Publishable anahtar bir SIR DEĞİLDİR (tarayıcıda görünür); tek işi
	// isteği bir satış kanalına bağlamaktır.
	AuthenticateStore(ctx context.Context, key string) (Principal, error)
}

// principalKey context'te doğrulanmış kimliği taşıyan anahtardır.
type principalKey struct{}

// WithPrincipal doğrulanmış kimliği context'e koyar.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext context'teki doğrulanmış kimliği döner.
// İkinci dönüş değeri false ise istek doğrulanmamıştır.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// RequireAdmin admin yüzeyini korur.
//
// Authorization başlığını okur ve iki şemayı destekler:
//
//	Authorization: Bearer <jwt>
//	Authorization: Bearer <gizli api anahtarı>
//
// Şema ayrımını [Authenticator] yapar; çekirdek yalnızca başlığı ayrıştırır.
// auth nil ise middleware TÜM istekleri reddeder: korumasız bir admin yüzeyi,
// yanlış yapılandırmanın en pahalı hâlidir ve sessizce açık kalmamalıdır.
func RequireAdmin(auth Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if auth == nil {
				unauthorized(ctx, w, "kimlik doğrulama yapılandırılmamış")
				return
			}

			scheme, credential, ok := bearerCredential(r)
			if !ok {
				unauthorized(ctx, w, "kimlik doğrulama gerekli")
				return
			}

			principal, err := auth.AuthenticateAdmin(ctx, scheme, credential)
			if err != nil {
				// Sebep LOGLANIR, istemciye SIZDIRILMAZ: "kullanıcı yok" ile
				// "parola yanlış" arasındaki fark, kullanıcı sayımına yarar.
				LoggerFromContext(ctx).WarnContext(ctx, "admin kimlik doğrulama başarısız",
					"error", err, "request_id", RequestIDFromContext(ctx))
				unauthorized(ctx, w, "kimlik doğrulama gerekli")
				return
			}

			next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, principal)))
		})
	}
}

// RequireStore mağaza yüzeyini korur.
//
// Publishable anahtar "x-publishable-api-key" başlığından okunur. Anahtar bir
// SIR DEĞİLDİR; amacı isteği bir satış kanalına bağlamaktır, gizlilik değil.
func RequireStore(auth Authenticator, header string) func(http.Handler) http.Handler {
	if header == "" {
		header = PublishableKeyHeader
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if auth == nil {
				unauthorized(ctx, w, "kimlik doğrulama yapılandırılmamış")
				return
			}

			key := strings.TrimSpace(r.Header.Get(header))
			if key == "" {
				unauthorized(ctx, w, "publishable api anahtarı gerekli")
				return
			}

			principal, err := auth.AuthenticateStore(ctx, key)
			if err != nil {
				LoggerFromContext(ctx).WarnContext(ctx, "mağaza kimlik doğrulama başarısız",
					"error", err, "request_id", RequestIDFromContext(ctx))
				unauthorized(ctx, w, "publishable api anahtarı geçersiz")
				return
			}

			next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, principal)))
		})
	}
}

// PublishableKeyHeader publishable anahtarın okunduğu varsayılan başlıktır.
const PublishableKeyHeader = "x-publishable-api-key"

// RequireScope belirli bir yetki isteyen route'ları korur.
//
// [RequireAdmin] SONRASINDA kullanılır; kimlik yoksa 401, yetki yetmiyorsa
// 403 döner. İki durumu ayırmak bilinçlidir: 401 "kim olduğunu söyle",
// 403 "kim olduğunu biliyorum ama yetkin yok" demektir.
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			principal, ok := PrincipalFromContext(ctx)
			if !ok {
				unauthorized(ctx, w, "kimlik doğrulama gerekli")
				return
			}
			if !principal.HasScope(scope) {
				WriteError(ctx, w, coreerrors.Forbidden(CodeForbidden,
					"bu işlem için %q yetkisi gerekli", scope))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// bearerCredential Authorization başlığını şema ve kimlik bilgisine ayırır.
func bearerCredential(r *http.Request) (scheme, credential string, ok bool) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if raw == "" {
		return "", "", false
	}

	scheme, credential, found := strings.Cut(raw, " ")
	if !found {
		return "", "", false
	}
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return "", "", false
	}
	return strings.ToLower(scheme), credential, true
}

// unauthorized RFC 9110'a uygun 401 yanıtı yazar.
//
// WWW-Authenticate başlığı ZORUNLUDUR: istemci hangi şemanın beklendiğini
// bilmeden doğru kimlikle tekrar deneyemez.
func unauthorized(ctx context.Context, w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	WriteError(ctx, w, coreerrors.Unauthorized(CodeUnauthenticated, "%s", message))
}
