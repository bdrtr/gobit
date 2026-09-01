package models

// CompanyFilter şirket listelemesine uygulanan süzgeçtir.
//
// Alanlar işaretçidir: nil "süzme" demektir, dolu bir işaretçi ise değeri boş
// olsa bile gerçek bir süzgeçtir. İki durumu ayırmayan bir tasarımda boş bir
// süzgeç değeri sessizce "hepsini listele"ye dönerdi.
type CompanyFilter struct {
	// Email verilirse yalnızca bu e-postaya sahip şirketler döner. Değer
	// çağıran tarafından normalize edilmiş olmalıdır ve BİRDEN ÇOK kayıt
	// dönebilir: şirket e-postası benzersiz değildir.
	Email *string
}

// EmployeeFilter çalışan listelemesine uygulanan süzgeçtir.
type EmployeeFilter struct {
	// CompanyID verilirse yalnızca bu şirketin çalışanları döner.
	CompanyID *string
	// IsCompanyAdmin verilirse yönetici/yönetici olmayan ayrımına göre süzer.
	IsCompanyAdmin *bool
}

// CompanyPatch bir şirketin kısmi güncellemesidir.
//
// nil alan "dokunma", dolu alan "bu değeri yaz" demektir; boş dize adres
// alanlarında gerçek bir temizlemedir (taşınan bir şirketin eski posta kodu
// silinebilmelidir).
type CompanyPatch struct {
	// Name şirketin yeni unvanıdır; verilirse boş olamaz.
	Name *string
	// Email yeni e-postadır; çağıran tarafından normalize edilmiş olmalıdır.
	Email *string
	// Phone yeni telefondur.
	Phone *string
	// Address adresin yeni sokak satırıdır.
	Address *string
	// City yeni şehirdir.
	City *string
	// PostalCode yeni posta kodudur.
	PostalCode *string
	// CountryCode yeni ülke kodudur; çağıran tarafından normalize edilmelidir.
	CountryCode *string
	// CurrencyCode yeni para birimi kodudur; verilirse boş olamaz.
	CurrencyCode *string
	// SpendingLimitResetPeriod yeni sıfırlama aralığıdır.
	SpendingLimitResetPeriod *SpendingResetPeriod
}

// EmployeePatch bir çalışanın kısmi güncellemesidir.
//
// ŞİRKET DEĞİŞTİRME BİLİNÇLİ OLARAK YOKTUR: bir çalışanın şirketi değişiyorsa
// bu, aynı kaydın güncellenmesi değil, eski kaydın kapanıp yenisinin açılması
// demektir — harcama geçmişi eski şirkete aittir ve kaydı taşımak o geçmişi
// sessizce yeni şirkete devrederdi.
type EmployeePatch struct {
	// SpendingLimit yeni harcama limitidir (minor unit).
	//
	// nil "dokunma" demektir; limiti SINIRSIZA çekmek için [EmployeePatch.ClearSpendingLimit]
	// kullanılır. İki alan gerekir çünkü alanın kendisi de nil olabilir ve tek
	// bir işaretçi "dokunma" ile "sınırsız yap"ı ayıramaz.
	SpendingLimit *int64
	// ClearSpendingLimit doğruysa limit kaldırılır (çalışan sınırsız olur).
	// SpendingLimit ile birlikte verilmesi anlamsızdır; servis reddeder.
	ClearSpendingLimit bool
	// IsCompanyAdmin yönetici işaretinin yeni değeridir.
	IsCompanyAdmin *bool
}
