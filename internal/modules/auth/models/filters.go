package models

// Bu dosyadaki tiplerin ortak kuralı şudur: alanların hepsi işaretçidir, nil
// "dokunma / süzme" demektir, dolu bir işaretçi ise değeri sıfır (boş dize,
// false) olsa bile GERÇEK bir istektir. İki durumu ayırmayan bir tasarımda
// "avatarı temizle" isteği sessizce "avatara dokunma"ya dönerdi.

// UserFilter kullanıcı listelemesine uygulanan süzgeçtir.
type UserFilter struct {
	// Email verilirse yalnızca bu e-postaya sahip kullanıcı döner.
	// Değer çağıran tarafından normalize edilmiş olmalıdır.
	Email *string
	// Scope verilirse yalnızca bu yetkiye sahip kullanıcılar döner.
	Scope *string
}

// UserPatch bir kullanıcının kısmi güncellemesidir.
//
// Metadata ve Scopes için nil aynı anlamı taşır: dilim/harita verilirse
// sütunun TAMAMI değiştirilir, birleştirme yapılmaz.
type UserPatch struct {
	// Email yeni e-postadır; çağıran tarafından normalize edilmiş olmalıdır.
	Email *string
	// FirstName yeni addır.
	FirstName *string
	// LastName yeni soyaddır.
	LastName *string
	// AvatarURL yeni avatar adresidir.
	AvatarURL *string
	// Scopes yeni yetki listesidir; sütunun tamamını değiştirir.
	Scopes []string
	// Metadata yeni metadata haritasıdır; sütunun tamamını değiştirir.
	Metadata map[string]any
}

// APIKeyFilter API anahtarı listelemesine uygulanan süzgeçtir.
type APIKeyFilter struct {
	// Type verilirse yalnızca bu türdeki anahtarlar döner.
	Type *APIKeyType
	// Revoked verilirse iptal edilmiş/edilmemiş ayrımına göre süzer.
	Revoked *bool
}

// SalesChannelFilter satış kanalı listelemesine uygulanan süzgeçtir.
type SalesChannelFilter struct {
	// Name verilirse yalnızca bu ada sahip kanal döner.
	Name *string
	// IsDisabled verilirse devre dışı/etkin ayrımına göre süzer.
	IsDisabled *bool
}

// SalesChannelPatch bir satış kanalının kısmi güncellemesidir.
type SalesChannelPatch struct {
	// Name kanalın yeni adıdır; canlı kanallar arasında benzersizdir.
	Name *string
	// Description kanalın yeni açıklamasıdır.
	Description *string
	// IsDisabled kanalın yeni etkinlik durumudur.
	IsDisabled *bool
	// Metadata yeni metadata haritasıdır; sütunun tamamını değiştirir.
	Metadata map[string]any
}
