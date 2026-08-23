package models

// CustomerFilter müşteri listelemesine uygulanan süzgeçtir.
//
// Alanların hepsi işaretçidir: nil "süzme" demektir, dolu bir işaretçi ise
// değeri sıfır (boş dize, false) olsa bile gerçek bir süzgeçtir. İki durumu
// ayırmayan bir tasarımda "hesabı olmayanları listele" isteği sessizce
// "hepsini listele"ye dönerdi.
type CustomerFilter struct {
	// Email verilirse yalnızca bu e-postaya sahip müşteriler döner.
	// Değer çağıran tarafından normalize edilmiş olmalıdır.
	Email *string
	// HasAccount verilirse misafir/kayıtlı ayrımına göre süzer.
	HasAccount *bool
	// GroupID verilirse yalnızca bu grubun üyeleri döner.
	GroupID *string
}

// CustomerPatch bir müşterinin kısmi güncellemesidir.
//
// nil alan "dokunma", dolu alan "bu değeri yaz" demektir; boş dize gerçek bir
// temizlemedir. Metadata için nil aynı anlamı taşır: harita verilirse sütunun
// TAMAMI değiştirilir, birleştirme yapılmaz.
type CustomerPatch struct {
	// Email yeni e-postadır; çağıran tarafından normalize edilmiş olmalıdır.
	Email *string
	// FirstName yeni addır.
	FirstName *string
	// LastName yeni soyaddır.
	LastName *string
	// Phone yeni telefondur.
	Phone *string
	// Metadata yeni metadata haritasıdır; sütunun tamamını değiştirir.
	Metadata map[string]any
}

// CustomerGroupPatch bir müşteri grubunun kısmi güncellemesidir.
//
// nil alan "dokunma", dolu alan "bu değeri yaz" demektir. Metadata haritası
// verilirse sütunun TAMAMI değiştirilir, birleştirme yapılmaz.
type CustomerGroupPatch struct {
	// Name grubun yeni adıdır; çağıran tarafından kırpılmış olmalıdır ve
	// canlı gruplar arasında benzersizdir.
	Name *string
	// Metadata yeni metadata haritasıdır; sütunun tamamını değiştirir.
	Metadata map[string]any
}

// AddressPatch bir adresin kısmi güncellemesidir.
//
// Varsayılan kargo/fatura işaretleri BİLİNÇLİ OLARAK burada yoktur: işaret
// değiştirmek, müşterinin diğer adreslerini de ilgilendiren bir işlemdir ve
// tek satırlık bir güncellemeyle yapılamaz (bkz. SetDefaultAddress).
type AddressPatch struct {
	// FirstName yeni addır.
	FirstName *string
	// LastName yeni soyaddır.
	LastName *string
	// Company yeni şirket adıdır.
	Company *string
	// Address1 adresin yeni ilk satırıdır.
	Address1 *string
	// Address2 adresin yeni ikinci satırıdır.
	Address2 *string
	// City yeni şehirdir.
	City *string
	// CountryCode yeni ülke kodudur; çağıran tarafından normalize edilmelidir.
	CountryCode *string
	// PostalCode yeni posta kodudur.
	PostalCode *string
	// Phone yeni telefondur.
	Phone *string
}

// DefaultKind bir adresin hangi tür varsayılan olarak işaretleneceğidir.
type DefaultKind uint8

// Varsayılan adresin türleri.
const (
	// DefaultShipping varsayılan kargo adresidir (sıfır değer).
	DefaultShipping DefaultKind = iota
	// DefaultBilling varsayılan fatura adresidir.
	DefaultBilling
)

// String türün okunabilir adını döner.
func (k DefaultKind) String() string {
	switch k {
	case DefaultBilling:
		return "billing"
	case DefaultShipping:
		return "shipping"
	default:
		return "shipping"
	}
}

// Valid türün tanımlı olup olmadığını bildirir.
//
// Tip dışa açıktır ve çağıran enum dışında bir değer kurabilir; böyle bir değer
// sessizce kargoya düşseydi, istemci fatura adresini işaretlediğini sanırken
// kargo adresini değiştirirdi.
func (k DefaultKind) Valid() bool {
	return k == DefaultShipping || k == DefaultBilling
}
