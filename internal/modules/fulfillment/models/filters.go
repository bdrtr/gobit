package models

// Bu dosyadaki süzgeçlerde işaretçi alanlar "verilmedi" ile "boş verildi"
// ayrımını korur: nil bir RegionID süzgeç uygulanmadığı, boş dizeye işaret
// eden bir RegionID ise bölgesi BOŞ (yani her bölgede geçerli) seçeneklerin
// istendiği anlamına gelir. Değer tipi kullanılsaydı ikisi ayırt edilemezdi.

// ProfileFilter kargo profili listelemesinin süzgeç ve sayfalama
// parametreleridir.
type ProfileFilter struct {
	// Type verilirse yalnızca o türdeki profiller döner.
	Type *string
	// Limit döndürülecek azami satır sayısıdır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}

// OptionFilter kargo seçeneği listelemesinin süzgeç ve sayfalama
// parametreleridir.
type OptionFilter struct {
	// RegionID verilirse yalnızca o bölgeye ait seçenekler döner.
	RegionID *string
	// ProfileID verilirse yalnızca o profile bağlı seçenekler döner.
	ProfileID *string
	// ProviderID verilirse yalnızca o sağlayıcının seçenekleri döner.
	ProviderID *string
	// PriceType verilirse yalnızca o fiyat türündeki seçenekler döner.
	PriceType *string
	// Limit döndürülecek azami satır sayısıdır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}

// EligibilityFilter bir sepet bağlamı için ADAY seçeneklerin sorgulanmasıdır.
//
// Yalnızca sütun düzeyinde ucuz olan elemeler burada durur; kural eşleşmesi
// servis katmanındaki saf fonksiyonda yapılır.
type EligibilityFilter struct {
	// RegionID sepetin bölgesidir. Bölgesi bu değere eşit olan VE bölgesi boş
	// olan seçenekler aday olur.
	RegionID string
	// CurrencyCode sepetin para birimidir (ISO 4217, büyük harf).
	CurrencyCode string
	// ProfileIDs sepetin ürünlerinin bağlı olduğu profillerdir. BOŞ verilirse
	// profil süzgeci uygulanmaz.
	ProfileIDs []string
	// IsReturn iade seçeneklerinin mi normal seçeneklerin mi istendiğini
	// bildirir.
	IsReturn bool
	// IncludeAdminOnly yalnızca yönetim yüzeyinde true'dur.
	IncludeAdminOnly bool
}

// FulfillmentFilter gönderi listelemesinin süzgeç ve sayfalama
// parametreleridir.
type FulfillmentFilter struct {
	// Reference verilirse yalnızca o referansa ait gönderiler döner.
	Reference *string
	// Status verilirse yalnızca o durumdaki gönderiler döner.
	Status *string
	// Limit döndürülecek azami satır sayısıdır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}

// LocationFilter depo seçim politikalarının listelenmesidir.
//
// Süzgeç alanı YOKTUR ve bu bilinçlidir: tablo kurulumdaki depo sayısı kadar
// satır taşır (onlarca, milyonlarca değil) ve bir bölgeye göre süzmek isteyen
// yönetim yüzeyi, dönen kayıtların bölge listesine zaten bakabilir. Süzgeç
// eklendiği gün listeleme ile SAYMA sorgusunun aynı koşulu taşıması gerekir.
type LocationFilter struct {
	// Limit döndürülecek azami satır sayısıdır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}
