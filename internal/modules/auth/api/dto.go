package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// DTO'lar domain modellerinden AYRI tutulur: JSON alan adları dış sözleşmedir
// ve modelde yapılan bir yeniden adlandırma istemciyi kırmamalıdır.
//
// Ayrımın bu modülde ikinci bir işi vardır: modelde bulunan ama DIŞARI
// ÇIKMAMASI gereken alanlar ([models.APIKey.TokenHash] gibi) DTO'da hiç yer
// almaz. Yanıt gövdesi modelden otomatik türeseydi, modele eklenen bir sır
// alanı sessizce API'ye sızardı.

// --- istek gövdeleri --------------------------------------------------------

// loginRequest giriş isteğinin gövdesidir.
type loginRequest struct {
	// Email kullanıcının e-postasıdır.
	Email string `json:"email"`
	// Password kullanıcının düz parolasıdır; [secret] tipiyle taşınır ve
	// loglandığında maskelenir.
	Password secret `json:"password"`
}

// createUserRequest kullanıcı oluşturma isteğinin gövdesidir.
type createUserRequest struct {
	// Email kullanıcının e-postasıdır; zorunludur.
	Email string `json:"email"`
	// FirstName kullanıcının adıdır.
	FirstName string `json:"first_name"`
	// LastName kullanıcının soyadıdır.
	LastName string `json:"last_name"`
	// AvatarURL profil görselinin adresidir.
	AvatarURL string `json:"avatar_url"`
	// Scopes kullanıcının yetkileridir; verilmezse "admin" uygulanır.
	//
	// Çağıranın KENDİSİNDE OLMAYAN bir yetki verilemez; böyle bir istek 403
	// döner (bkz. service.CreateUser). Alanın hiç verilmemesi de bir yetki
	// isteğidir: varsayılan tam yetkidir ve o da aynı denetimden geçer.
	Scopes []string `json:"scopes"`
	// Password kullanıcının ilk parolasıdır; boş bırakılabilir ve o zaman
	// parola sonradan atanır.
	Password secret `json:"password"`
	// Metadata serbest yapısal bağlamdır.
	Metadata map[string]any `json:"metadata"`
}

// updateUserRequest kullanıcı güncelleme isteğinin gövdesidir.
//
// Parola alanı BİLİNÇLİ OLARAK yoktur: parola ayrı bir uçtan değişir
// (POST /admin/v1/users/{id}/password). Aynı gövdede olsaydı, adını
// güncelleyen bir isteğin yanlışlıkla parolayı da değiştirmesi mümkün olurdu.
type updateUserRequest struct {
	// Email yeni e-postadır.
	Email *string `json:"email"`
	// FirstName yeni addır.
	FirstName *string `json:"first_name"`
	// LastName yeni soyaddır.
	LastName *string `json:"last_name"`
	// AvatarURL yeni avatar adresidir.
	AvatarURL *string `json:"avatar_url"`
	// Scopes yeni yetki listesidir; verilmezse dokunulmaz.
	//
	// Çağıranın KENDİSİNDE OLMAYAN bir yetki verilemez; böyle bir istek 403
	// döner (bkz. service.UpdateUser). Yetki kaldırmak serbesttir.
	Scopes []string `json:"scopes"`
	// Metadata yeni metadata haritasıdır.
	Metadata map[string]any `json:"metadata"`
}

// setPasswordRequest parola atama isteğinin gövdesidir.
type setPasswordRequest struct {
	// Password yeni düz paroladır.
	Password secret `json:"password"`
}

// createAPIKeyRequest anahtar oluşturma isteğinin gövdesidir.
type createAPIKeyRequest struct {
	// Type anahtarın türüdür: "publishable" ya da "secret".
	Type string `json:"type"`
	// Title anahtarın görünen adıdır.
	Title string `json:"title"`
	// Scopes gizli anahtarın yetkileridir; publishable anahtarda verilemez.
	//
	// Çağıranın KENDİSİNDE OLMAYAN bir yetki verilemez; böyle bir istek 403
	// döner (bkz. service.CreateAPIKey). Gizli anahtarda alanın hiç
	// verilmemesi "admin" demektir ve o da aynı denetimden geçer.
	Scopes []string `json:"scopes"`
	// SalesChannelIDs publishable anahtarın bağlanacağı kanallardır; gizli
	// anahtarda verilemez.
	SalesChannelIDs []string `json:"sales_channel_ids"`
}

// linkChannelRequest anahtar-kanal bağı isteğinin gövdesidir.
type linkChannelRequest struct {
	// SalesChannelID bağlanacak kanalın kimliğidir.
	SalesChannelID string `json:"sales_channel_id"`
}

// salesChannelRequest kanal oluşturma isteğinin gövdesidir.
type salesChannelRequest struct {
	// Name kanalın adıdır; zorunludur.
	Name string `json:"name"`
	// Description kanalın açıklamasıdır.
	Description string `json:"description"`
	// IsDisabled kanalın devre dışı açılmasını sağlar.
	IsDisabled bool `json:"is_disabled"`
	// Metadata serbest yapısal bağlamdır.
	Metadata map[string]any `json:"metadata"`
}

// updateSalesChannelRequest kanal güncelleme isteğinin gövdesidir.
type updateSalesChannelRequest struct {
	// Name kanalın yeni adıdır.
	Name *string `json:"name"`
	// Description kanalın yeni açıklamasıdır.
	Description *string `json:"description"`
	// IsDisabled kanalın yeni etkinlik durumudur.
	IsDisabled *bool `json:"is_disabled"`
	// Metadata yeni metadata haritasıdır.
	Metadata map[string]any `json:"metadata"`
}

// --- yanıt gövdeleri --------------------------------------------------------

// loginResponse giriş yanıtının gövdesidir.
//
// Jeton bir SIRDIR: istemci onu saklar ve her istekte Authorization başlığında
// gönderir. Yanıt gövdesi dışında hiçbir yere (log, denetim kaydı, hata
// mesajı) yazılmaz.
type loginResponse struct {
	// Token imzalı oturum jetonudur (HS256 JWT).
	Token string `json:"token"`
	// ExpiresAt jetonun son kullanma anıdır (RFC3339, UTC).
	ExpiresAt time.Time `json:"expires_at"`
	// TokenType jetonun Authorization başlığında kullanılacağı şemadır.
	TokenType string `json:"token_type"`
}

// logoutResponse çıkış yanıtının gövdesidir.
//
// Gövde, status kodunun söyleyemediğini söyler: çıkışın ÇAĞIRANIN TÜMÜNÜ
// kapsadığını ve hangi ana dayandığını. Boş bir 204, "bu cihazdan çıktım"
// sanan istemciyi düzeltmezdi.
type logoutResponse struct {
	// AllSessions iptalin çağıranın TÜM oturumlarını kapsadığını bildirir.
	//
	// Alan bugün her zaman true'dur ve bu bir eksiklik değil, sözleşmenin
	// kendisidir: tek cihazı düşürmenin yolu yoktur (bkz.
	// service.Service.Logout). Sabit olduğu için atılabilirdi ama o zaman
	// istemcinin toptan iptali öğrenebileceği tek yer belgeler olurdu ve
	// yanıta bakan bir geliştirici yanlış varsayımıyla baş başa kalırdı.
	AllSessions bool `json:"all_sessions"`
	// RevokedAt iptalin dayandığı andır (RFC3339, UTC).
	//
	// Bu andan ÖNCE üretilmiş her oturum jetonu artık reddedilir; isteğin
	// kendisinde kullanılan jeton da buna dâhildir.
	RevokedAt time.Time `json:"revoked_at"`
}

// principalResponse doğrulanmış çağıranın kimliğidir.
type principalResponse struct {
	// ID çağıranın kimliğidir (kullanıcı ya da API anahtarı).
	ID string `json:"id"`
	// Kind kimliğin türüdür: "user" | "api_key".
	Kind string `json:"kind"`
	// Scopes çağıranın yetkileridir.
	Scopes []string `json:"scopes"`
	// SalesChannelIDs publishable anahtarın bağlı olduğu kanallardır; yönetim
	// kimliğinde boştur.
	SalesChannelIDs []string `json:"sales_channel_ids,omitempty"`
}

// userDTO bir yönetim kullanıcısının yanıt gövdesidir.
//
// Parola ya da parola hash'i BURADA YOKTUR ve hiçbir zaman olmayacaktır.
type userDTO struct {
	// ID kullanıcının kimliğidir.
	ID string `json:"id"`
	// Email kullanıcının e-postasıdır (küçük harfe normalize edilmiş).
	Email string `json:"email"`
	// FirstName kullanıcının adıdır.
	FirstName string `json:"first_name"`
	// LastName kullanıcının soyadıdır.
	LastName string `json:"last_name"`
	// AvatarURL profil görselinin adresidir.
	AvatarURL string `json:"avatar_url"`
	// Scopes kullanıcının yetkileridir.
	Scopes []string `json:"scopes"`
	// Metadata serbest yapısal bağlamdır; boşsa gövdede görünmez.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt oluşturulma anıdır (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// apiKeyDTO bir API anahtarının yanıt gövdesidir.
//
// [models.APIKey.TokenHash] alanı BURADA YOKTUR: özet bir sır olmasa da
// dışarıya verilmesi gereksizdir ve verildiğinde "anahtarı bulduk" sanılmasına
// yol açardı. Düz metin ise yalnızca oluşturma yanıtında bulunur
// ([createAPIKeyResponse]).
type apiKeyDTO struct {
	// ID anahtarın kimliğidir.
	ID string `json:"id"`
	// Type anahtarın türüdür: "publishable" | "secret".
	Type string `json:"type"`
	// Title anahtarın görünen adıdır.
	Title string `json:"title"`
	// Redacted maskelenmiş gösterimdir (örn. "pk_...a1b2"); onunla kimlik
	// doğrulanamaz.
	Redacted string `json:"redacted"`
	// Scopes anahtarın yetkileridir; publishable anahtarda boştur.
	Scopes []string `json:"scopes"`
	// CreatedBy anahtarı üretenin kimliğidir.
	CreatedBy string `json:"created_by"`
	// LastUsedAt son kullanım anıdır; YAKLAŞIKTIR ve hiç kullanılmamışsa
	// gövdede görünmez.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// RevokedAt iptal anıdır; iptal edilmemişse gövdede görünmez.
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	// RevokedBy iptali yapanın kimliğidir; boşsa gövdede görünmez.
	RevokedBy string `json:"revoked_by,omitempty"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır.
	UpdatedAt time.Time `json:"updated_at"`
}

// createAPIKeyResponse anahtar oluşturma yanıtının gövdesidir.
//
// DÜZ ANAHTAR YALNIZCA BURADA DÖNER. Değer bir daha hiçbir uçtan okunamaz;
// istemci onu şimdi saklamazsa anahtar kaybolur ve tek çare iptal edip
// yenisini üretmektir. Bu, saklamanın yalnızca özet üzerinden yapılmasının
// doğrudan sonucudur (bkz. [models.APIKey]).
type createAPIKeyResponse struct {
	// APIKey anahtarın kaydıdır.
	APIKey apiKeyDTO `json:"api_key"`
	// Key anahtarın DÜZ metnidir; bir daha gösterilmez.
	Key string `json:"key"`
}

// salesChannelDTO bir satış kanalının yanıt gövdesidir.
type salesChannelDTO struct {
	// ID kanalın kimliğidir.
	ID string `json:"id"`
	// Name kanalın adıdır.
	Name string `json:"name"`
	// Description kanalın açıklamasıdır.
	Description string `json:"description"`
	// IsDisabled kanalın devre dışı olduğunu bildirir.
	IsDisabled bool `json:"is_disabled"`
	// Metadata serbest yapısal bağlamdır; boşsa gövdede görünmez.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır.
	UpdatedAt time.Time `json:"updated_at"`
}

// --- dönüşümler -------------------------------------------------------------

// toUserDTO domain kullanıcısını yanıt gövdesine çevirir.
func toUserDTO(u models.User) userDTO {
	return userDTO{
		ID:        u.ID,
		Email:     u.Email,
		FirstName: u.FirstName,
		LastName:  u.LastName,
		AvatarURL: u.AvatarURL,
		Scopes:    orEmpty(u.Scopes),
		Metadata:  u.Metadata,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// toAPIKeyDTO domain anahtarını yanıt gövdesine çevirir.
func toAPIKeyDTO(k models.APIKey) apiKeyDTO {
	return apiKeyDTO{
		ID:         k.ID,
		Type:       k.Type.String(),
		Title:      k.Title,
		Redacted:   k.Redacted,
		Scopes:     orEmpty(k.Scopes),
		CreatedBy:  k.CreatedBy,
		LastUsedAt: k.LastUsedAt,
		RevokedAt:  k.RevokedAt,
		RevokedBy:  k.RevokedBy,
		CreatedAt:  k.CreatedAt,
		UpdatedAt:  k.UpdatedAt,
	}
}

// toSalesChannelDTO domain kanalını yanıt gövdesine çevirir.
func toSalesChannelDTO(c models.SalesChannel) salesChannelDTO {
	return salesChannelDTO{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		IsDisabled:  c.IsDisabled,
		Metadata:    c.Metadata,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

// toCreateUserInput istek gövdesini servis girdisine çevirir.
//
// Parola BU DÖNÜŞÜMDE TAŞINMAZ: servis onu ayrı bir parametre olarak alır ve
// hiçbir yapıya konmaz.
func toCreateUserInput(req createUserRequest) service.CreateUserInput {
	return service.CreateUserInput{
		Email:     req.Email,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		AvatarURL: req.AvatarURL,
		Scopes:    req.Scopes,
		Metadata:  req.Metadata,
	}
}

// toUpdateUserInput istek gövdesini servis girdisine çevirir.
func toUpdateUserInput(req updateUserRequest) service.UpdateUserInput {
	return service.UpdateUserInput{
		Email:     req.Email,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		AvatarURL: req.AvatarURL,
		Scopes:    req.Scopes,
		Metadata:  req.Metadata,
	}
}

// orEmpty nil dilimi boş dilime çevirir.
//
// JSON'da "scopes": null yerine [] görünmesi tüketici için tek biçimli bir
// yüzeydir; null gören istemci dizide döngüye girmeden önce ek kontrol yazmak
// zorunda kalırdı.
func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
