// Package api auth modülünün HTTP yüzeyidir.
//
// Modülün tüm uçları /admin/v1 altındadır; auth'un vitrin (/store/v1) ucu
// YOKTUR. Mağaza tarafındaki karşılığı bir uç değil, publishable anahtarı
// okuyan corehttp.RequireStore middleware'idir.
//
// # Uçlar
//
// Kimlik uçları (yetki İSTEMEZ, bkz. [Handler.Routes]):
//
//   - POST /admin/v1/auth/login — jeton üretir; TEK KORUMASIZ uçtur.
//   - GET /admin/v1/auth/me — doğrulanmış çağıranın kimliğini geri okur.
//   - POST /admin/v1/auth/logout — çağıranın TÜM oturumlarını düşürür; tek
//     cihaz seçilemez (bkz. [Handler.adminLogout]).
//
// Kaynak uçları ([ScopeRead] ya da [ScopeWrite] ister):
//
//   - /admin/v1/users, /admin/v1/users/{id}/password
//   - /admin/v1/api-keys, /admin/v1/api-keys/{id}/revoke ve kanal bağları
//   - /admin/v1/sales-channels
//
// # KORUMASIZ UÇ: POST /admin/v1/auth/login
//
// Giriş ucu doğası gereği KORUMASIZDIR: kimlik doğrulaması yapılacak olan
// istektir, kimliği daha yeni kuracaktır. Yönetim yüzeyine corehttp.RequireAdmin
// bağlanırken bu uç DIŞARIDA BIRAKILMALIDIR; korumaya alınırsa kimse giriş
// yapamaz ve sistem kilitlenir.
//
// Kimlik middleware'inin bağlanması bu modülün DEĞİL, router'ı kuran tarafın
// işidir; muafiyet de orada tanımlanır. YETKİ ise burada, uç uç zorlanır
// (bkz. [Handler.Routes] ve [ScopeRead], [ScopeWrite]).
//
// # Sırlar
//
// Bu paketten dışarı çıkan tek düz sır, anahtar oluşturma yanıtındaki
// [createAPIKeyResponse.Key] alanıdır ve bir kez döner. Parola hiçbir yanıtta
// GEÇMEZ; istek gövdesindeki parola alanı [secret] tipiyle taşınır ve o tip
// loglandığında maskelenir.
//
// Handler'lar status kodu SEÇMEZ: servis tipli hata döner, corehttp.WriteError
// onu status koduna çevirir (plan Bölüm 2.7).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// maxBodyBytes tek bir istek gövdesinin azami boyutudur. Sınırsız bir gövde,
// tek istekle belleği tüketmenin en ucuz yoludur.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidBody istek gövdesi ya da parametresi çözümlenemediğinde dönen
// hata kodudur.
const codeInvalidBody = "auth_invalid_body"

// Yol parametrelerinin adları.
const (
	paramID        = "id"
	paramChannelID = "sales_channel_id"
)

// secret istek gövdesindeki düz parolayı taşıyan dize tipidir.
//
// Tek işi KAZA ile loglanmayı engellemektir: bir istek yapısı "%v", "%+v" ya
// da slog ile kaydedildiğinde bu alan maskelenmiş görünür. Değerin kendisine
// ulaşmak için açık bir string dönüşümü gerekir ve o dönüşüm kodda göze
// batar — tam da istenen budur.
type secret string

// String maskelenmiş gösterimi döner.
func (s secret) String() string { return "REDACTED" }

// LogValue slog çıktısında maskelenmiş değeri döner.
func (s secret) LogValue() slog.Value { return slog.StringValue("REDACTED") }

// Auth handler'ların servisten ihtiyaç duyduğu yüzeydir.
//
// Dar tutulması testleri sadeleştirir: HTTP davranışı, gerçek bir veritabanı
// olmadan birkaç satırlık bir sahte ile doğrulanabilir.
type Auth interface {
	// Login e-posta ve parolayla oturum jetonu üretir.
	Login(ctx context.Context, email, password string) (string, time.Time, error)
	// Logout çağıranın TÜM oturumlarını düşürür ve iptal anını döner.
	//
	// Kimliğin TÜRÜ de geçirilir: bir API anahtarının oturumu yoktur ve o
	// çağrı tipli bir hata ile reddedilir (bkz. service.Service.Logout).
	Logout(ctx context.Context, principalID, principalKind string) (time.Time, error)

	// CreateUser yeni bir yönetim kullanıcısı oluşturur; password boş olabilir.
	CreateUser(ctx context.Context, in service.CreateUserInput, password string) (models.User, error)
	// GetUser kullanıcıyı kimliğiyle döner.
	GetUser(ctx context.Context, id string) (models.User, error)
	// ListUsers kullanıcıları süzer ve sayfalar.
	ListUsers(ctx context.Context, in service.ListUsersInput) (service.Page[models.User], error)
	// UpdateUser kullanıcının verilen alanlarını günceller.
	UpdateUser(ctx context.Context, id string, in service.UpdateUserInput) (models.User, error)
	// DeleteUser kullanıcıyı yumuşak siler.
	DeleteUser(ctx context.Context, id string) error
	// SetPassword kullanıcının parolasını belirler.
	SetPassword(ctx context.Context, userID, password string) error

	// CreateAPIKey yeni bir API anahtarı üretir ve düz metnini bir kez döner.
	CreateAPIKey(ctx context.Context, in service.CreateAPIKeyInput) (models.APIKey, string, error)
	// GetAPIKey anahtarı kimliğiyle döner.
	GetAPIKey(ctx context.Context, id string) (models.APIKey, error)
	// ListAPIKeys anahtarları süzer ve sayfalar.
	ListAPIKeys(ctx context.Context, in service.ListAPIKeysInput) (service.Page[models.APIKey], error)
	// RevokeAPIKey anahtarı iptal eder.
	RevokeAPIKey(ctx context.Context, id, revokedBy string) (models.APIKey, error)
	// DeleteAPIKey anahtarı yumuşak siler.
	DeleteAPIKey(ctx context.Context, id string) error
	// LinkSalesChannel publishable anahtarı bir satış kanalına bağlar.
	LinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error
	// UnlinkSalesChannel bağı kaldırır.
	UnlinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error
	// SalesChannelsOfAPIKey anahtarın bağlı olduğu kanalları döner.
	SalesChannelsOfAPIKey(ctx context.Context, apiKeyID string) ([]models.SalesChannel, error)

	// CreateSalesChannel yeni bir satış kanalı oluşturur.
	CreateSalesChannel(ctx context.Context, in service.SalesChannelInput) (models.SalesChannel, error)
	// GetSalesChannel kanalı kimliğiyle döner.
	GetSalesChannel(ctx context.Context, id string) (models.SalesChannel, error)
	// ListSalesChannels kanalları süzer ve sayfalar.
	ListSalesChannels(ctx context.Context, in service.ListSalesChannelsInput) (service.Page[models.SalesChannel], error)
	// UpdateSalesChannel kanalın verilen alanlarını günceller.
	UpdateSalesChannel(ctx context.Context, id string, in service.UpdateSalesChannelInput) (models.SalesChannel, error)
	// DeleteSalesChannel kanalı yumuşak siler.
	DeleteSalesChannel(ctx context.Context, id string) error
}

// Handler auth modülünün HTTP handler kümesidir.
type Handler struct {
	svc Auth
}

// New verilen servis üzerinde çalışan handler kümesini üretir.
func New(svc Auth) *Handler {
	return &Handler{svc: svc}
}

// LoginPath giriş ucunun tam yoludur.
//
// Sabit olarak yayımlanması, router'ı kuran tarafın KORUMASIZ bırakacağı yolu
// elle yazmak zorunda kalmaması içindir: yol değişirse istisna da onunla
// birlikte değişir ve bir gün sessizce korumaya alınıp sistemi kilitlemez.
const LoginPath = "/admin/v1/auth/login"

// Yetki sözlüğü: auth'un yönetim uçlarının istediği yetkiler.
//
// Sözlük BİLİNÇLİ OLARAK iki girdiden ibarettir. Kaynak başına ayrı yetki
// ("users:read", "api_keys:write" …) tanımlamak listeyi büyütür ama bugün
// verilebilecek hiçbir yeni kararı mümkün kılmaz: yetkiyi dağıtan tek yer bu
// modüldür ve dağıtılmayan bir yetki adı, ilk kez verildiği gün ne işe
// yaradığı kimsenin bilmediği bir addır. Ayrım gerçekten gerektiğinde
// eklenir; şimdiden eklenirse yalnızca yanlış bir kesinlik hissi verir.
const (
	// ScopeRead auth'un yönetim yüzeyindeki OKUMA uçlarının istediği yetkidir.
	//
	// Kullanıcı kayıtlarını, API anahtarlarının MASKELENMİŞ gösterimlerini ve
	// satış kanallarını okumaya yeter; hiçbir yazma ucunu açmaz. Ayrıca
	// verilmesi tam yetkili kimlikler için gerekmez: corehttp.ScopeAdmin
	// taşıyan bir çağıran bunu da karşılar (bkz. corehttp.Principal.HasScope).
	ScopeRead = "auth:read"

	// ScopeWrite auth'un yönetim yüzeyindeki YAZMA uçlarının istediği
	// yetkidir; corehttp.ScopeAdmin'in ta kendisidir.
	//
	// Daha dar bir "auth:write" TANIMLANMAMIŞTIR ve bu bir eksiklik değildir:
	// bu uçlarda yazılan şeyin kendisi yetkidir — kullanıcının yetkisi,
	// anahtarın yetkisi, anahtarın göreceği satış kanalı. Yetki yazabilen bir
	// kimlik tek istekte kendini admin yapabileceği için zaten admindir; ayrı
	// bir ad, gerçekte var olmayan bir sınırı varmış gibi gösterirdi.
	ScopeWrite = corehttp.ScopeAdmin
)

// Routes auth'un yönetim route'larını router'a bağlar.
//
// Route'lar chi'nin Route/Mount yardımcılarıyla DEĞİL, tam yollarla kaydedilir:
// /admin/v1 önekini birden çok modül paylaşır ve aynı öneki iki kez Mount etmek
// chi'de panik üretirdi. Tam yol kaydı aynı ağaca yan yana yazar.
//
// # KORUMA
//
// İki katman vardır ve ikisi de gereklidir:
//
//  1. KİMLİK — [LoginPath] DIŞINDAKİ tüm uçlar corehttp.RequireAdmin ile
//     korunur. O middleware bu modülde değil, router'ı kuran tarafta takılır
//     (bkz. corehttp.APIGuards); muafiyet listesi de oradadır.
//  2. YETKİ — uçlar BURADA, uç uç corehttp.RequireScope ile işaretlenir:
//     okuma uçları [ScopeRead], yazma uçları [ScopeWrite] ister.
//
// İkinci katman olmasaydı kimlik doğrulama yetkilendirmenin yerine geçerdi.
// Somut bedeli şudur: yalnızca "orders:read" taşıyan bir gizli anahtar ya da
// yetkisi hiç olmayan bir kullanıcı POST /admin/v1/api-keys çağırıp kendine
// tam yetkili bir anahtar üretebilirdi — tek istekte yetki yükseltme. Aynı
// yükseltme servis katmanında ikinci kez engellenir (bkz.
// service.CreateAPIKey), çünkü buradaki harita bir gün gevşetilebilir.
//
// Kimlik uçları yetki İSTEMEZ: [LoginPath] kimliği daha yeni kuracaktır,
// GET /admin/v1/auth/me kurulmuş kimliğin kendisini geri okur,
// POST /admin/v1/auth/logout ise onu sonlandırır. Kimlik ucuna yetki koymak,
// yetkisiz bir çağıranın kim olduğunu bile öğrenememesi demek olurdu ve bu,
// hiçbir şeyi korumadan hata ayıklamayı imkânsızlaştırırdı. Çıkış ucuna yetki
// koymak ise daha kötüsünü yapardı: yetkisi geri alınmış bir yöneticinin
// elindeki jeton, süresi dolana kadar kapatılamaz hâle gelirdi.
func (h *Handler) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))
	yazma := r.With(corehttp.RequireScope(ScopeWrite))

	// --- kimlik (login KORUMASIZ, /me ve /logout yalnızca KİMLİK ister) ---
	r.Post(LoginPath, h.adminLogin)
	r.Get("/admin/v1/auth/me", h.adminWhoami)
	r.Post("/admin/v1/auth/logout", h.adminLogout)

	// --- kullanıcılar ---
	yazma.Post("/admin/v1/users", h.adminCreateUser)
	okuma.Get("/admin/v1/users", h.adminListUsers)
	okuma.Get("/admin/v1/users/{id}", h.adminGetUser)
	yazma.Put("/admin/v1/users/{id}", h.adminUpdateUser)
	yazma.Delete("/admin/v1/users/{id}", h.adminDeleteUser)
	yazma.Post("/admin/v1/users/{id}/password", h.adminSetPassword)

	// --- api anahtarları ---
	yazma.Post("/admin/v1/api-keys", h.adminCreateAPIKey)
	okuma.Get("/admin/v1/api-keys", h.adminListAPIKeys)
	okuma.Get("/admin/v1/api-keys/{id}", h.adminGetAPIKey)
	yazma.Delete("/admin/v1/api-keys/{id}", h.adminDeleteAPIKey)
	yazma.Post("/admin/v1/api-keys/{id}/revoke", h.adminRevokeAPIKey)
	okuma.Get("/admin/v1/api-keys/{id}/sales-channels", h.adminListKeyChannels)
	yazma.Post("/admin/v1/api-keys/{id}/sales-channels", h.adminLinkKeyChannel)
	yazma.Delete("/admin/v1/api-keys/{id}/sales-channels/{sales_channel_id}", h.adminUnlinkKeyChannel)

	// --- satış kanalları ---
	//
	// Kanal kaydı katalog verisi gibi görünür ama yetki verisidir: bir
	// publishable anahtarın hangi kataloğu göreceğini kanal bağı belirler.
	// Bu yüzden yazma tarafı da [ScopeWrite] ister.
	yazma.Post("/admin/v1/sales-channels", h.adminCreateSalesChannel)
	okuma.Get("/admin/v1/sales-channels", h.adminListSalesChannels)
	okuma.Get("/admin/v1/sales-channels/{id}", h.adminGetSalesChannel)
	yazma.Put("/admin/v1/sales-channels/{id}", h.adminUpdateSalesChannel)
	yazma.Delete("/admin/v1/sales-channels/{id}", h.adminDeleteSalesChannel)
}

// itemEnvelope tekil yanıtların zarfıdır (plan Bölüm 8).
type itemEnvelope struct {
	// Data tek kaydın gövdesidir.
	Data any `json:"data"`
}

// listEnvelope liste yanıtlarının zarfıdır (plan Bölüm 8).
type listEnvelope struct {
	// Data geçerli sayfadaki kayıtlardır.
	Data any `json:"data"`
	// Count filtreye uyan TOPLAM kayıt sayısıdır.
	Count int64 `json:"count"`
	// Offset uygulanan atlama sayısıdır.
	Offset int64 `json:"offset"`
	// Limit uygulanan sayfa boyudur.
	Limit int64 `json:"limit"`
}

// writeItem tekil yanıtı zarfıyla yazar.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// writeItems sayfalanmamış bir listeyi zarfıyla yazar.
//
// Limit, dönen kayıt sayısına EŞİTTİR ve [service.MaxLimit] ile KIRPILMAZ:
// burada sayfa yoktur, tek sayfa tüm kayıtlardır. Kırpılsaydı istemci sayfa
// boyunu yanlış sanıp sayfalama döngüsüne girerdi.
func writeItems[T any](w http.ResponseWriter, r *http.Request, items []T) {
	if items == nil {
		items = []T{}
	}
	count := int64(len(items))
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  count,
		Offset: 0,
		Limit:  count,
	})
}

// writePage servis sayfasını liste zarfıyla yazar.
func writePage[S any, T any](w http.ResponseWriter, r *http.Request, page service.Page[S], convert func(S) T) {
	items := make([]T, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, convert(item))
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  page.Count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// convertAll bir dilimi DTO dilimine çevirir; nil dilim boş dilime döner.
func convertAll[S any, T any](items []S, convert func(S) T) []T {
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, convert(item))
	}
	return out
}

// decodeBody istek gövdesini hedefe çözer.
//
// Bilinmeyen alanlar REDDEDİLİR: sessizce yok sayılan bir alan, istemcinin
// gönderdiğini sandığı bir değerin hiç yazılmaması demektir. Gövde boyutu da
// sınırlıdır; aşılırsa çözümleme hatası olarak döner.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(reader)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidBody, "istek gövdesi boş olamaz")
		}
		// Çözümleme hatasının metni GÖVDEDEN ALINTI İÇEREBİLİR ve giriş
		// isteğinin gövdesinde parola vardır. Bu yüzden alttaki hata
		// sarmalanır ama mesajı YAZILMAZ; ayrıntı yalnızca log'a düşer.
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidBody,
			"istek gövdesi çözümlenemedi")
	}

	// Tek bir JSON belgesi beklenir; arkasından gelen ikinci belge sessizce
	// yok sayılırsa istemci gönderdiğinin işlendiğini sanırdı.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidBody, "istek gövdesi tek bir JSON belgesi olmalı")
	}
	return nil
}

// pathParam yol parametresini okur.
func pathParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// actorID isteği yapan kimliği döner; kimlik yoksa boş dize.
//
// Denetim alanlarını (created_by, revoked_by) doldurur. Kimlik ÇEKİRDEKTEN
// gelir (corehttp.RequireAdmin onu context'e koyar); istemcinin gövdede
// bildirdiği bir değer KULLANILMAZ — kullanılsaydı denetim kaydını istemci
// yazardı.
func actorID(ctx context.Context) string {
	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok {
		return ""
	}
	return principal.ID
}

// pageParams sorgu dizesinden sayfalama parametrelerini okur.
//
// Eksik parametre sıfır döner ve servis varsayılanı uygular; SAYIYA
// ÇEVRİLEMEYEN bir değer ise hata döner — sessizce sıfıra düşmek, istemcinin
// istediği sayfa yerine ilk sayfayı almasına yol açardı.
func pageParams(r *http.Request) (limit, offset int64, err error) {
	limit, err = intParam(r, "limit")
	if err != nil {
		return 0, 0, err
	}
	offset, err = intParam(r, "offset")
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

// intParam tek bir sayısal sorgu parametresini okur; yoksa sıfır döner.
func intParam(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidBody,
			"%q parametresi tam sayı olmalı, %q verildi", name, raw)
	}
	return value, nil
}

// boolParam bir mantıksal sorgu parametresini okur; yoksa nil döner.
//
// nil ile false arasındaki fark burada anlamlıdır: "is_disabled=false" etkin
// kanalları süzer, parametrenin hiç verilmemesi ise süzmez.
func boolParam(r *http.Request, name string) (*bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, coreerrors.Invalid(codeInvalidBody,
			"%q parametresi mantıksal (true/false) olmalı, %q verildi", name, raw)
	}
	return &value, nil
}

// stringParam bir metin sorgu parametresini okur; yoksa nil döner.
func stringParam(r *http.Request, name string) *string {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil
	}
	return &raw
}
