package service

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// Uygunluk bağlamının YERLEŞİK alan adları.
//
// Kural yazan yönetici bu adları kullanır; her biri sepetin bir OLGUSUDUR ve
// çağıranın gönderdiği serbest alanların ÜZERİNE yazılır (bkz.
// [Service.ListShippingOptionsFor]).
const (
	// AttrRegionID sepetin bölgesidir.
	AttrRegionID = "region_id"
	// AttrCountryCode teslimat ülkesidir (ISO 3166-1 alpha-2, büyük harf).
	AttrCountryCode = "country_code"
	// AttrCurrencyCode sepetin para birimidir (ISO 4217, büyük harf).
	AttrCurrencyCode = "currency_code"
	// AttrSubtotal sepetin ara toplamıdır (minor unit TAM SAYI). "Ücretsiz
	// kargo" kuralları bu alana bakar.
	AttrSubtotal = "subtotal"
	// AttrItemCount sepetteki toplam adettir.
	AttrItemCount = "item_count"
	// AttrTotalWeight gönderinin toplam ağırlığıdır (gram).
	AttrTotalWeight = "total_weight"
	// AttrIsReturn iade akışında olunup olunmadığıdır ("true"/"false").
	AttrIsReturn = "is_return"
)

// clientDeclarableFacts çağıranın SERBESTÇE İDDİA edebildiği sayısal sepet
// olgularıdır.
//
// Bu üç alan bir sepetin GİZLİ durumudur: bu modül onları hesaplayamaz
// (Prensip 2.1) ve doğrulayamaz. Bir kural onlara bağlandığında, olguyu
// uyduran bir çağıran kendine kapalı bir seçeneği açabilir. Güvenilmeyen
// yüzeylerde ([ListOptionsInput.TrustedFacts] false) bu alanlara bağlı
// kuralı olan seçenekler listeye HİÇ girmez.
//
// Bölge, ülke ve para birimi listede YOKTUR ve bu bilinçlidir: onlar bir
// ayrıcalık kapısı değil, isteğin KAPSAMIDIR — başka bir ülkenin seçeneklerini
// sormak vitrinde normal bir davranıştır ve teslimat adresi zaten ödeme
// adımında doğrulanır.
var clientDeclarableFacts = []string{AttrSubtotal, AttrItemCount, AttrTotalWeight}

// ListOptionsInput bir sepet bağlamı için uygun seçeneklerin sorgulanmasıdır.
//
// Alanların hepsi ÇAĞIRANIN bildiği olgulardır; bu modül hiçbirini kendi
// hesaplamaz (Prensip 2.1: sepet cart modülünün verisidir). Sayısal alanların
// ARALIĞI yine de doğrulanır: değerin doğruluğu bilinemese de, tek bir istek
// parametresiyle sağlayıcının aritmetiğini taşıracak büyüklükte olmadığı
// bilinebilir.
type ListOptionsInput struct {
	// RegionID sepetin bölgesidir. Bölgesi buna eşit olan VE bölgesi boş olan
	// seçenekler aday olur.
	RegionID string
	// CurrencyCode sepetin para birimidir (ISO 4217); zorunludur.
	//
	// Süzgeç olması şarttır: başka para biriminde fiyatlanmış bir kargo
	// seçeneğini sepete eklemek, iki para biriminin tutarlarını toplamak
	// demek olurdu.
	CurrencyCode string
	// CountryCode teslimat ülkesidir; sağlayıcıya iletilir ve kural bağlamına
	// girer. Boş olabilir.
	CountryCode string
	// ShippingProfileIDs sepetin ürünlerinin bağlı olduğu profillerdir.
	// BOŞ verilirse profil süzgeci uygulanmaz.
	ShippingProfileIDs []string
	// Subtotal sepetin ara toplamıdır (minor unit TAM SAYI);
	// 0 ile [models.MaxAmount] arasında olmalıdır.
	Subtotal int64
	// ItemCount sepetteki toplam adettir;
	// 0 ile [models.MaxItemCount] arasında olmalıdır.
	ItemCount int64
	// TotalWeight gönderinin toplam ağırlığıdır (gram); bilinmiyorsa sıfır.
	// 0 ile [models.MaxTotalWeight] arasında olmalıdır.
	TotalWeight int64
	// Attributes çağıranın eklediği serbest kural bağlamıdır
	// (örn. {"customer_group_id": "vip"}).
	Attributes map[string]string
	// TrustedFacts sayısal sepet olgularının ([ListOptionsInput.Subtotal],
	// [ListOptionsInput.ItemCount], [ListOptionsInput.TotalWeight]) SUNUCU
	// tarafında üretildiğini bildirir.
	//
	// Varsayılan false'tur ve bu bilinçlidir: bir yüzey bu bayrağı koymayı
	// unuttuğunda sonuç GÜVENLİ tarafa düşmelidir. false iken sayılar
	// doğrulanmamış bir İDDİADIR ve onlara bağlı kuralı olan seçenekler
	// listeye hiç girmez (bkz. [Service.ListShippingOptionsFor]).
	//
	// true veren tek taraflar: sepet olgularını kendi hesabından getiren
	// akışlar ([Interop.ListOptionsJSON]) ve yönetim yüzeyi (yönetici zaten
	// tüm kataloğu görebilir, bağlamı uydurması ona yeni bir şey açmaz).
	TrustedFacts bool
	// IncludeAdminOnly yalnızca YÖNETİM yüzeyinde true'dur.
	IncludeAdminOnly bool
	// IsReturn iade seçeneklerinin mi normal seçeneklerin mi istendiğini
	// bildirir.
	IsReturn bool
}

// QuotedOption fiyatı belirlenmiş bir kargo seçeneğidir.
type QuotedOption struct {
	// Option seçeneğin kendisidir.
	Option models.ShippingOption
	// Amount seçeneğin bu sepet için ücretidir (minor unit TAM SAYI).
	Amount int64
	// CurrencyCode ücretin para birimidir (ISO 4217).
	CurrencyCode string
	// ProviderData sağlayıcının döndürdüğü ham veridir; yalnızca
	// "calculated" seçeneklerde doludur.
	//
	// SAĞLAYICININ İÇ VERİSİDİR ve mağaza yüzeyine ÇIKMAZ (bkz. api paketi).
	// Burada taşınmasının sebebi, gönderi açılırken aynı verinin sağlayıcıya
	// geri verilebilmesidir.
	ProviderData json.RawMessage
}

// ListShippingOptionsFor bir sepet bağlamı için UYGUN kargo seçeneklerini
// fiyatlarıyla döner.
//
// # Eleme sırası
//
//  1. Sütun düzeyinde ucuz elemeler VERİTABANINDA yapılır: bölge, para birimi,
//     iade işareti, profil kümesi ve admin_only. admin_only süzgecinin SQL'de
//     durması bilinçlidir — mağaza yüzeyine sızmaması gereken tek alan budur ve
//     satırın hiç okunmaması, okunup sonra atılmasından güvenlidir.
//  2. Kalan adayların TÜM kuralları bağlamla eşleşmelidir. Kuralsız seçenek
//     koşulsuzdur. Kuralın baktığı alan bağlamda yoksa kural eşleşmez ve
//     seçenek elenir — "ne" gibi olumsuz işleçlerde bile. Aksi hâlde bağlamı
//     boş bir istek tüm olumsuz kuralları sağlayarak kısıtlı seçenekleri
//     herkese açardı.
//  3. Ücret belirlenir: "flat" seçenekler kendi tutarını kullanır, "calculated"
//     seçenekler sağlayıcının Quote'unu çağırır.
//
// # Bağlam alanları
//
// Sepetin OLGULARI ([AttrRegionID], [AttrSubtotal], [AttrItemCount], …)
// çağıranın gönderdiği serbest alanların ÜZERİNE yazılır. Çağıran kendi
// "subtotal" değerini [ListOptionsInput.Attributes] içine koyup kuralı
// atlatamaz: olguyu bu metot kurar.
//
// # Güvenilmeyen bağlamda kurala bağlı seçenek LİSTELENMEZ
//
// Yukarıdaki ezme kuralı, olgunun DOĞRU olduğunu değil, yalnızca tek bir
// yerden geldiğini garanti eder. Sayıyı uyduran taraf çağıranın kendisiyse
// (vitrinden gelen bir sorgu parametresi) kural yine atlatılırdı: boş sepetle
// "subtotal=50000" gönderen bir müşteri, ücretsiz kargoyu ve fiyatını görürdü.
//
// Bu yüzden [ListOptionsInput.TrustedFacts] false iken [AttrSubtotal],
// [AttrItemCount] ya da [AttrTotalWeight] alanına bağlı kuralı olan seçenek,
// bağlam eşleşse bile listeye GİRMEZ. Bedeli açıktır ve kabul edilmiştir:
// "500 TL üzeri ücretsiz kargo" HTTP mağaza ucunda hiç görünmez; müşteriye
// sepet akışı üzerinden ([Interop.ListOptionsJSON], sunucu tarafı olgularla)
// gösterilir. Karar pricing modülüyle aynıdır: orada da kurala bağlı fiyatlar
// mağaza yüzeyinden hiç çıkmaz.
//
// # Sağlayıcı hatası seçeneği DÜŞÜRÜR, isteği düşürmez
//
// Bir "calculated" seçeneğin Quote'u patlarsa yalnızca O SEÇENEK listeden
// çıkar; istek hata dönmez. Gerekçe: bu metot sepet her güncellendiğinde
// çağrılır ve tek bir kargo firmasının erişilemez olması, ödeme adımının
// tamamını kapatmamalıdır — "flat" seçenekler ayakta kalır ve müşteri
// alışverişi tamamlayabilir.
//
// Bedeli açıktır ve kabul edilmiştir: YANLIŞ YAPILANDIRILMIŞ bir sağlayıcı
// "hiç seçenek yok" gibi görünür. Bu yüzden düşen her seçenek LOGLANIR;
// sağlayıcının hiç kayıtlı olmaması bir kurulum hatasıdır ve ERROR, geçici bir
// Quote hatası ise WARN seviyesinde yazılır.
//
// # Sıralama
//
// Sonuç ÖNCE ücrete (ucuz kazanır), eşitlikte kimliğe göre sıralanır. Sıra
// tamdır: aynı girdi her çağrıda aynı listeyi verir ve vitrindeki seçenek
// sırası isteğe göre oynamaz.
func (s *Service) ListShippingOptionsFor(
	ctx context.Context,
	in ListOptionsInput,
) ([]QuotedOption, error) {
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return nil, err
	}
	if err := checkTextLen("bölge kimliği", in.RegionID); err != nil {
		return nil, err
	}
	if err := requireAmount("ara toplam", in.Subtotal); err != nil {
		return nil, err
	}
	// Adet ve ağırlığın ÜST sınırı da denetlenir. Yalnızca negatifliğe bakan
	// bir kontrol, tek bir sorgu parametresiyle (total_weight=2^63-1)
	// sağlayıcının çarpımını taşırır: seçenek sessizce listeden düşer ve
	// sunucuda istemci girdisiyle tetiklenen ERROR gürültüsü oluşurdu.
	if err := requireRange("kalem adedi", in.ItemCount, models.MaxItemCount); err != nil {
		return nil, err
	}
	if err := requireRange("toplam ağırlık", in.TotalWeight, models.MaxTotalWeight); err != nil {
		return nil, err
	}

	countryCode := strings.ToUpper(strings.TrimSpace(in.CountryCode))
	if err := checkTextLen("ülke kodu", countryCode); err != nil {
		return nil, err
	}

	candidates, err := s.store.ListEligibleShippingOptions(ctx, models.EligibilityFilter{
		RegionID:         strings.TrimSpace(in.RegionID),
		CurrencyCode:     currency,
		ProfileIDs:       in.ShippingProfileIDs,
		IsReturn:         in.IsReturn,
		IncludeAdminOnly: in.IncludeAdminOnly,
	})
	if err != nil {
		return nil, err
	}

	attributes := s.eligibilityAttributes(in, currency, countryCode)

	out := make([]QuotedOption, 0, len(candidates))
	for i := range candidates {
		option := candidates[i]
		if !in.TrustedFacts && dependsOnDeclaredFacts(option.Rules) {
			continue
		}
		if !matchRules(option.Rules, attributes) {
			continue
		}
		quoted, ok := s.quote(ctx, option, in, currency, countryCode)
		if !ok {
			continue
		}
		out = append(out, quoted)
	}

	slices.SortFunc(out, func(a, b QuotedOption) int {
		if c := cmp.Compare(a.Amount, b.Amount); c != 0 {
			return c
		}
		return strings.Compare(a.Option.ID, b.Option.ID)
	})
	return out, nil
}

// eligibilityAttributes kural bağlamını kurar.
//
// Çağıranın serbest alanları önce konur, sepetin OLGULARI sonra yazılır;
// çakışmada olgu kazanır (bkz. [Service.ListShippingOptionsFor]).
func (s *Service) eligibilityAttributes(
	in ListOptionsInput,
	currency, countryCode string,
) map[string]string {
	attributes := make(map[string]string, len(in.Attributes)+7)
	maps.Copy(attributes, in.Attributes)

	attributes[AttrRegionID] = strings.TrimSpace(in.RegionID)
	attributes[AttrCountryCode] = countryCode
	attributes[AttrCurrencyCode] = currency
	attributes[AttrSubtotal] = strconv.FormatInt(in.Subtotal, 10)
	attributes[AttrItemCount] = strconv.FormatInt(in.ItemCount, 10)
	attributes[AttrTotalWeight] = strconv.FormatInt(in.TotalWeight, 10)
	attributes[AttrIsReturn] = strconv.FormatBool(in.IsReturn)
	return attributes
}

// quote tek bir seçeneğin ücretini belirler.
//
// İkinci dönüş değeri false ise seçenek listeden DÜŞER; sebep loglanmıştır.
// Hata dönmemesi bilinçlidir (bkz. [Service.ListShippingOptionsFor]).
func (s *Service) quote(
	ctx context.Context,
	option models.ShippingOption,
	in ListOptionsInput,
	currency, countryCode string,
) (QuotedOption, bool) {
	if option.PriceType == models.PriceFlat {
		return QuotedOption{
			Option:       option,
			Amount:       option.Amount,
			CurrencyCode: option.CurrencyCode,
		}, true
	}

	provider, err := s.providers.Get(option.ProviderID)
	if err != nil {
		// Kayıtlı olmayan sağlayıcı bir KURULUM hatasıdır: seçenek yaratılırken
		// kayıt vardı, şimdi yok. Sessizce geçmemeli, ama tek bir seçenek
		// yüzünden tüm liste de düşmemeli.
		s.log.ErrorContext(ctx, "kargo seçeneğinin sağlayıcısı kayıtlı değil, seçenek listeden düştü",
			"secenek", option.ID, "saglayici", option.ProviderID, "error", err)
		return QuotedOption{}, false
	}

	quote, err := provider.Quote(ctx, coreprovider.QuoteInput{
		OptionID:     option.ID,
		CurrencyCode: currency,
		CountryCode:  countryCode,
		TotalWeight:  in.TotalWeight,
		ItemCount:    in.ItemCount,
		Data:         option.Data,
	})
	if err != nil {
		s.log.WarnContext(ctx, "kargo sağlayıcısı fiyat veremedi, seçenek listeden düştü",
			"secenek", option.ID, "saglayici", option.ProviderID, "error", err)
		return QuotedOption{}, false
	}

	if err := validateQuote(quote, currency); err != nil {
		s.log.ErrorContext(ctx, "kargo sağlayıcısı sözleşme dışı fiyat döndü, seçenek listeden düştü",
			"secenek", option.ID, "saglayici", option.ProviderID, "error", err)
		return QuotedOption{}, false
	}

	return QuotedOption{
		Option:       option,
		Amount:       quote.Amount,
		CurrencyCode: strings.ToUpper(strings.TrimSpace(quote.CurrencyCode)),
		ProviderData: quote.Data,
	}, true
}

// validateQuote sağlayıcının döndüğü fiyatı sözleşmeye göre denetler.
//
// İki şart var: para birimi İSTENENLE aynı olmalı ve tutar izin verilen
// aralıkta kalmalı. Para birimi denetimi ihmal edilirse, dolar cinsinden bir
// kargo ücreti lira sepetine sessizce eklenir ve fark ancak muhasebede
// görülürdü.
func validateQuote(quote coreprovider.ShippingQuote, currency string) error {
	quoted := strings.ToUpper(strings.TrimSpace(quote.CurrencyCode))
	if quoted != currency {
		return errors.Internal(CodeProviderContract,
			"sağlayıcı %q para biriminde fiyat döndü, istenen %q", quote.CurrencyCode, currency)
	}
	if quote.Amount < models.MinAmount || quote.Amount > models.MaxAmount {
		return errors.Internal(CodeProviderContract,
			"sağlayıcının döndüğü tutar %d ile %d arasında olmalı: %d",
			models.MinAmount, models.MaxAmount, quote.Amount)
	}
	return nil
}

// dependsOnDeclaredFacts seçeneğin kurallarından herhangi birinin, çağıranın
// serbestçe iddia edebildiği bir sepet olgusuna baktığını bildirir.
//
// Karar KURALIN ALANINA bakar, bağlamın değerine değil: "bu sepet için eşleşti
// mi" sorusunun cevabı zaten uydurulmuş bir sayıdan gelirdi. Kuralsız bir
// seçenek koşulsuzdur ve bu süzgeçten etkilenmez.
func dependsOnDeclaredFacts(rules []models.ShippingOptionRule) bool {
	for i := range rules {
		if slices.Contains(clientDeclarableFacts, rules[i].Attribute) {
			return true
		}
	}
	return false
}

// matchRules seçeneğin TÜM kurallarının bağlamla eşleştiğini bildirir.
// Kuralsız seçenek koşulsuzdur ve daima eşleşir.
func matchRules(rules []models.ShippingOptionRule, attributes map[string]string) bool {
	for i := range rules {
		if !matchRule(rules[i], attributes) {
			return false
		}
	}
	return true
}

// matchRule tek bir kuralın bağlamla eşleştiğini bildirir.
//
// Kuralın baktığı alan bağlamda YOKSA kural eşleşmez — "ne" (eşit değil) gibi
// olumsuz işleçlerde bile. Aksi hâlde bağlamı boş bir istek, tüm olumsuz
// kuralları sağlayarak kısıtlı seçenekleri herkese açardı.
//
// DEĞERSİZ kural da eşleşmez ve PANİK ÜRETMEZ. Böyle bir kaydı servis
// doğrulaması üretmez, ama uygunluk hesabı veritabanından okuduğu her satıra
// dayanıklı olmalıdır: doğrudan SQL çalıştıran bir bakım betiği ya da kısmi
// bir geri yükleme değerleri boş bırakabilir. Gerekçe tanınmayan işleçtekiyle
// aynıdır — okunamayan bir koşul, kuralı sessizce devre dışı bırakıp seçeneği
// herkese AÇMAMALIDIR.
func matchRule(rule models.ShippingOptionRule, attributes map[string]string) bool {
	if len(rule.Values) == 0 {
		return false
	}

	value, ok := attributes[rule.Attribute]
	if !ok {
		return false
	}

	switch rule.Operator {
	case models.OpEq:
		return value == rule.Values[0]
	case models.OpNe:
		return value != rule.Values[0]
	case models.OpIn:
		return slices.Contains(rule.Values, value)
	case models.OpNin:
		return !slices.Contains(rule.Values, value)
	case models.OpGt, models.OpGte, models.OpLt, models.OpLte:
		return matchNumeric(rule.Operator, value, rule.Values[0])
	default:
		return false
	}
}

// matchNumeric sayısal işleçleri TAM SAYI üzerinde değerlendirir.
//
// İki taraf da tam sayıya çevrilemiyorsa kural EŞLEŞMEZ, hata dönmez: bağlam
// dışarıdan gelir ve tek bir bozuk alan tüm kargo listesini düşürmemelidir.
//
// Karşılaştırma SAYISALDIR, dizgesel değil: "9" ile "50000" dizge olarak
// karşılaştırılsaydı 9 büyük çıkar ve ücretsiz kargo eşiği tersine dönerdi.
// Çevirinin TAM SAYIYA yapılması ise ondalıklı bir eşiği (örn. "500.5")
// sessizce kabul etmek yerine kuralı eşleşmez yapar; para minor unit tam
// sayıdır ve kuralın eşiği de öyle olmalıdır (plan Bölüm 8).
func matchNumeric(operator models.RuleOperator, left, right string) bool {
	lhs, err := strconv.ParseInt(strings.TrimSpace(left), 10, 64)
	if err != nil {
		return false
	}
	rhs, err := strconv.ParseInt(strings.TrimSpace(right), 10, 64)
	if err != nil {
		return false
	}

	switch operator {
	case models.OpGt:
		return lhs > rhs
	case models.OpGte:
		return lhs >= rhs
	case models.OpLt:
		return lhs < rhs
	case models.OpLte:
		return lhs <= rhs
	case models.OpEq, models.OpNe, models.OpIn, models.OpNin:
		return false
	default:
		return false
	}
}
