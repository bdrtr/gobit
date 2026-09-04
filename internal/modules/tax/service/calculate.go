package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// ShippingLineID kargo satırının sonuçtaki sabit kimliğidir.
//
// Kargo bir kalem değildir ve çağıranın verdiği bir kimliği yoktur; sonuçta
// yine de adlandırılması gerekir ki JSON yüzeyi ([Interop.CalculateTaxJSON])
// kalemlerle aynı şekli kullanabilsin. Sabit bir ad, çağıranın kalem
// kimliklerinden ayırt edilebilir olmalıdır: alt çizgiyle başlaması, hiçbir
// modülün önekiyle çakışmamasını sağlar.
const ShippingLineID = "_shipping"

// TaxableItem vergilendirilecek tek bir kalemdir.
//
// # Taban İNDİRİM SONRASIDIR
//
// [TaxableItem.Amount] çağıranın hesapladığı, indirimi DÜŞÜLMÜŞ tabandır. Bu
// modül indirim hesaplamaz ve indirim verisini görmez; tabanı olduğu gibi
// vergiler. Karar sepet akışının bugünkü sözleşmesiyle birebir aynıdır
// (internal/workflows/cart, "Vergi sözleşmesi"): vergi fiilen ödenen bedeli
// izler, indirim öncesi tutarı vergilemek müşteriden hiç alınmayan bir paranın
// vergisini almak olurdu. Faz 7'de promotion modülü indirimi doldurduğunda
// tabanın TANIMI değişmez; yalnızca değeri küçülür.
type TaxableItem struct {
	// ID kalemin ÇAĞIRAN tarafındaki kimliğidir (örn. sepet satırı) ve
	// sonuçta aynen döner. Bu modül onu doğrulamaz ve saklamaz.
	ID string
	// ProductID kural eşleşmesi için ürün kimliğidir; boş bırakılabilir.
	ProductID string
	// ProductTypeID kural eşleşmesi için ürün tipi kimliğidir; boş
	// bırakılabilir.
	ProductTypeID string
	// Amount vergilendirilebilir tabandır (minor unit, İNDİRİM SONRASI).
	Amount int64
}

// ShippingInput kargo satırının hesap girdisidir.
type ShippingInput struct {
	// OptionID kargo seçeneğinin kimliğidir; kural eşleşmesi için kullanılır.
	OptionID string
	// Amount kargo tutarıdır (minor unit).
	Amount int64
	// Taxable kargonun vergilendirilip vergilendirilmeyeceğidir.
	//
	// Varsayılan FALSE'tur ve çağıran AÇIKÇA istemedikçe kargo tabana
	// GİRMEZ; gerekçe [Service.CalculateTax] godoc'undadır.
	Taxable bool
}

// CalculateTaxInput bir vergi hesabının girdisidir.
type CalculateTaxInput struct {
	// CountryCode ISO 3166-1 alpha-2 kodudur; zorunludur.
	CountryCode string
	// ProvinceCode eyalet/il kodudur; isteğe bağlıdır.
	ProvinceCode string
	// Items vergilendirilecek kalemlerdir; boş olabilir.
	Items []TaxableItem
	// Shipping kargo satırıdır.
	Shipping ShippingInput
}

// ItemTax tek bir kalemin hesaplanan vergisidir.
type ItemTax struct {
	// ID kalemin çağıran tarafındaki kimliğidir; kargo satırında
	// [ShippingLineID].
	ID string
	// RateID uygulanan oranın kimliğidir; oran bulunamadıysa boş.
	RateID string
	// RateBps uygulanan orandır (baz puan); oran bulunamadıysa sıfır.
	RateBps int32
	// TaxableAmount verginin hesaplandığı tabandır (minor unit).
	TaxableAmount int64
	// TaxAmount hesaplanan vergidir (minor unit).
	TaxAmount int64
}

// CalculateTaxResult bir vergi hesabının sonucudur.
//
// Kimlik daima sağlanır: TaxTotal = Σ(Items[i].TaxAmount) + Shipping.TaxAmount.
type CalculateTaxResult struct {
	// RegionID hesabın dayandığı EN ÖZEL bölgedir (eyalet varsa o, yoksa
	// ülke kökü); bölge bulunamadıysa boş.
	RegionID string
	// RegionFound ülkeye ait bir vergi bölgesi bulunup bulunmadığıdır.
	//
	// Alan ZORUNLUDUR: sıfır vergi iki farklı sebepten doğabilir — oran
	// gerçekten sıfırdır ya da o ülke için hiç yapılandırma yoktur. İkisini
	// ayırt edemeyen bir çağıran, yapılandırma eksiğini "vergisiz ülke" sanıp
	// sessizce satış yapardı.
	RegionFound bool
	// ProviderID hesabı yapan sağlayıcının kimliğidir; bölge yoksa boş.
	ProviderID string
	// Items kalem başına vergidir; girdideki SIRAYLA döner.
	Items []ItemTax
	// Shipping kargo satırının vergisidir; vergilendirilmediyse sıfır.
	Shipping ItemTax
	// TaxTotal toplam vergidir.
	TaxTotal int64
}

// CalculateTax verilen ülke/eyalet ve kalemler için vergiyi hesaplar.
//
// BU METOT MODÜLÜN KALBİDİR. Aldığı kararlar ve gerekçeleri:
//
// # 1. Vergi tabanı İNDİRİM SONRASIDIR
//
// Kalemin [TaxableItem.Amount] alanı, çağıranın indirimi düşerek hesapladığı
// tabandır; bu modül indirimi görmez. Karar sepet akışının bugünkü
// sözleşmesiyle aynıdır (internal/workflows/cart paket yorumu): vergi fiilen
// ödenen bedeli izler.
//
// # 2. YUVARLAMA: kalem başına, AŞAĞI
//
// Vergi her kalem için AYRI hesaplanır ve tam sayı bölmesiyle AŞAĞI yuvarlanır
// (bkz. [TaxOf]). Toplam vergi, YUVARLANMIŞ kalem vergilerinin TOPLAMIDIR —
// kalem tabanlarının toplamı üzerinden yeniden hesaplanmaz.
//
// Bu ayrım bir FARK üretir ve farkın nerede kaldığı açıkça belgelenmelidir:
// Σ(floor(tabanᵢ × oran)) ≤ floor(Σtabanᵢ × oran) olduğu için kalem başına
// hesap, sepetin tamamı üzerinden yapılan hesaptan en fazla (kalem sayısı - 1)
// minor unit AZ çıkar. Fark MÜŞTERİ LEHİNE kalır; satıcı hiçbir durumda fazla
// tahsil etmez.
//
// Kalem başına hesaplamanın seçilmesinin iki sebebi vardır: (a) faturada her
// satırın vergisi tek tek açıklanabilir olmalıdır, (b) satırlara FARKLI
// oranlar uygulanabildiği için sepet tabanını tek seferde vergilemek zaten
// mümkün değildir.
//
// # 3. KARGO varsayılan olarak VERGİLENMEZ
//
// [ShippingInput.Taxable] açıkça true verilmedikçe kargo tabana girmez ve
// sonuçtaki kargo vergisi sıfırdır. Sepet akışı bugün kargoyu tabana KATMIYOR
// ve bu modül o davranışı değiştirmez; kargonun vergilenip vergilenmediği
// yargı bölgesine göre değişir ve "malla aynıdır" varsaymak sessiz bir
// tahmindir. Vergilendirme açıldığında kargo satırı da kendi oranını seçer:
// "shipping_option" kuralıyla eşleşen bir oran varsa o, yoksa bölgenin
// varsayılan oranı uygulanır.
//
// # 4. EYALET ÜLKEYİ EZER; oranlar TOPLANMAZ
//
// Eyalet bölgesi varsa oranları önce denenir; yalnızca eyalet hiçbir oran
// vermediğinde (ne eşleşen kural ne varsayılan) ülke oranına düşülür. Toplama
// yapılmaz — gerekçesi ve reddedilen alternatif [LocalProvider] godoc'undadır.
//
// # 5. VERGİ BÖLGESİ YOKSA: sıfır vergi, HATA DEĞİL
//
// Ülkeye ait kök bölge bulunamazsa tüm vergiler sıfır döner,
// [CalculateTaxResult.RegionFound] false olur ve durum UYARI olarak loglanır.
// Hata dönmek, vergisi henüz yapılandırılmamış bir ülkedeki her sepetin hiç
// açılamaması demek olurdu; sepet hesabı bu metodu her turda çağırır. Buna
// karşılık sessizlik de kabul edilemez: RegionFound alanı ve log kaydı,
// yapılandırma eksiğini çağıran için GÖRÜNÜR kılar ve çağıran (örn. sepet
// akışı) isterse reddetmeyi seçebilir.
//
// Ülke kodunun BİÇİMİ geçersizse errors.Invalid döner; "geçersiz kod" ile
// "yapılandırılmamış ülke" ayrı durumlardır.
//
// # 6. Sağlayıcı zincirden DEVRALINIR, sonucu DOĞRULANIR
//
// Hesabı [TaxProvider] yapar; hangi sağlayıcının çağrılacağını zincirdeki EN
// ÖZEL DOLU provider_id söyler. Eyaletin alanı boşsa ülkenin sağlayıcısı
// devralınır — gerekçe [Service.providerFor] godoc'undadır — ve hiçbiri dolu
// değilse yerel hesaplama uygulanır. Dönen sonuç körü körüne kabul EDİLMEZ:
// her kalem kimliği girdide bulunmalı, hiçbir kalem eksik ya da tekrar
// olmamalı, oran [0, %100] ve vergi [0, taban] aralığında kalmalıdır. Toplam
// da sağlayıcıdan alınmaz, BURADA yeniden toplanır; böylece kimlik
// (TaxTotal = Σ kalem + kargo) sağlayıcının doğruluğuna bağlı olmaz.
func (s *Service) CalculateTax(ctx context.Context, in CalculateTaxInput) (CalculateTaxResult, error) {
	if err := s.ready(); err != nil {
		return CalculateTaxResult{}, err
	}

	normalized, err := s.normalizeCalculateInput(in)
	if err != nil {
		return CalculateTaxResult{}, err
	}

	chain, err := s.repo.ResolveTaxRegions(ctx, normalized.CountryCode, normalized.ProvinceCode)
	if err != nil {
		return CalculateTaxResult{}, err
	}
	if len(chain) == 0 {
		s.log.WarnContext(ctx, "vergi bölgesi yapılandırılmamış, vergi sıfır hesaplandı",
			slog.String("country_code", normalized.CountryCode),
			slog.String("province_code", normalized.ProvinceCode),
			slog.Int("item_count", len(normalized.Items)),
		)
		return zeroResult(normalized), nil
	}

	regionIDs := make([]string, 0, len(chain))
	for i := range chain {
		regionIDs = append(regionIDs, chain[i].ID)
	}

	// Sağlayıcıyı zincirdeki EN ÖZEL DOLU provider_id belirler: eyalet kendi
	// vergi otoritesini seçebilir, seçmediyse ülkeninkini DEVRALIR.
	provider, err := s.providerFor(chain)
	if err != nil {
		return CalculateTaxResult{}, err
	}

	raw, err := provider.Calculate(ctx, ProviderInput{
		RegionIDs:    regionIDs,
		CountryCode:  normalized.CountryCode,
		ProvinceCode: normalized.ProvinceCode,
		Items:        normalized.Items,
		Shipping:     normalized.Shipping,
	})
	if err != nil {
		return CalculateTaxResult{}, err
	}

	result, err := assembleResult(normalized, raw, provider.ID())
	if err != nil {
		return CalculateTaxResult{}, err
	}
	result.RegionID = chain[0].ID
	result.RegionFound = true
	return result, nil
}

// providerFor bölge ZİNCİRİNİN sağlayıcısını çözer; zincir en özelden geneledir.
//
// # Boş provider_id DEVRALIR
//
// Zincirde en özelden genele yürünür ve İLK DOLU provider_id kazanır; hiçbiri
// dolu değilse [LocalProviderID] uygulanır. Yani boş bir alan "yerel" değil
// "ebeveynimin otoritesi" demektir.
//
// Karar bir para hatasını kapatır. Bir eyalet bölgesi çoğu zaman TEK BİR
// İSTİSNA için açılır (bkz. [LocalProvider]) ve o satıra sağlayıcı yazmak
// akla gelmez; boş değer yerel sayılsaydı, ülkesi dış bir otoriteye (Avalara,
// TaxJar …) bağlı bir kurulumda o eyaletteki HER sepet sessizce yerel tablodan
// vergilenirdi. Yanlış otoriteyle kesilmiş fatura, hatanın hiç fark
// edilmemesi demektir.
//
// Reddedilen alternatif: eyalet oluşturulurken ebeveynin sağlayıcısı doluyken
// çocuğun boş bırakılmasını YASAKLAMAK. Yasak, boş dizeyi "yerel" anlamında
// bırakacağı için veri modelinde "ebeveynimin sağlayıcısını kullan" ifadesini
// hâlâ imkânsız kılardı ve doğrudan SQL ile yazılan satırları hiç kapsamazdı.
// Devralmada her niyet ifade edilebilir: boş = devral, "local" = açıkça yerel,
// başka bir kimlik = o sağlayıcı.
//
// # Kayıtlı olmayan kimlik
//
// KURULUM hatasıdır ve KindInternal'a çevrilir. Kayıt katmanının NotFound'u
// olduğu gibi geçseydi, sepet toplamı isteyen istemci 404 alır ve sepetinin ya
// da ürününün kaybolduğunu sanırdı. Sessizce yerele düşmek ise daha kötüdür:
// yanlış otoritenin oranıyla hesaplanmış bir fatura, hatanın hiç fark
// edilmemesi demektir.
func (s *Service) providerFor(chain []models.TaxRegion) (TaxProvider, error) {
	if s.providers == nil {
		return nil, errors.Internal(CodeProviderMisconfigured,
			"the tax provider registry is not configured")
	}

	regionID, providerID := "", LocalProviderID
	if len(chain) > 0 {
		regionID = chain[0].ID
	}
	for i := range chain {
		if id := strings.TrimSpace(chain[i].ProviderID); id != "" {
			regionID, providerID = chain[i].ID, id
			break
		}
	}

	provider, err := s.providers.Get(providerID)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeProviderMisconfigured,
			"%s bölgesi kayıtlı olmayan %q vergi sağlayıcısına işaret ediyor",
			regionID, providerID)
	}
	return provider, nil
}

// normalizeCalculateInput hesap girdisini doğrular ve normalleştirir.
//
// Doğrulama VERİTABANINA GİTMEDEN yapılır: sonucu baştan belli bir istek için
// bölge sorgusu çalıştırmak, hatalı bir istemcinin veritabanını meşgul etmesi
// demektir.
func (s *Service) normalizeCalculateInput(in CalculateTaxInput) (CalculateTaxInput, error) {
	country, err := NormalizeCountryCode(in.CountryCode)
	if err != nil {
		return CalculateTaxInput{}, err
	}
	province, err := NormalizeProvinceCode(in.ProvinceCode)
	if err != nil {
		return CalculateTaxInput{}, err
	}
	if len(in.Items) > MaxItems {
		return CalculateTaxInput{}, errors.Invalid(CodeInvalidInput,
			"tek hesapta en fazla %d kalem olabilir, %d verildi", MaxItems, len(in.Items))
	}

	items := make([]TaxableItem, 0, len(in.Items))
	seen := make(map[string]struct{}, len(in.Items))
	for i := range in.Items {
		item := in.Items[i]
		if item.ID == "" {
			return CalculateTaxInput{}, errors.Invalid(CodeInvalidInput,
				"%d. kalemin kimliği boş", i)
		}
		if item.ID == ShippingLineID {
			// Kargo satırının kimliği ayrılmıştır; bir kalem onu kullanırsa
			// sonuçtaki iki satır ayırt edilemez hâle gelirdi.
			return CalculateTaxInput{}, errors.Invalid(CodeInvalidInput,
				"%q kimliği kargo satırına ayrılmıştır ve kalem kimliği olamaz", ShippingLineID)
		}
		if _, dup := seen[item.ID]; dup {
			return CalculateTaxInput{}, errors.Invalid(CodeInvalidInput,
				"%q kalem kimliği birden çok kez verildi", item.ID)
		}
		seen[item.ID] = struct{}{}

		if err := checkTaxableAmount("kalem vergi tabanı", item.Amount); err != nil {
			return CalculateTaxInput{}, err
		}
		items = append(items, item)
	}

	if err := checkTaxableAmount("kargo tutarı", in.Shipping.Amount); err != nil {
		return CalculateTaxInput{}, err
	}

	return CalculateTaxInput{
		CountryCode:  country,
		ProvinceCode: province,
		Items:        items,
		Shipping:     in.Shipping,
	}, nil
}

// zeroResult vergisiz bir sonuç üretir; kalemler girdideki sırayla döner.
func zeroResult(in CalculateTaxInput) CalculateTaxResult {
	items := make([]ItemTax, 0, len(in.Items))
	for i := range in.Items {
		items = append(items, ItemTax{ID: in.Items[i].ID, TaxableAmount: in.Items[i].Amount})
	}

	shipping := ItemTax{ID: ShippingLineID}
	if in.Shipping.Taxable {
		shipping.TaxableAmount = in.Shipping.Amount
	}
	return CalculateTaxResult{Items: items, Shipping: shipping}
}

// assembleResult sağlayıcının çıktısını DOĞRULAR ve sonuca çevirir.
//
// Doğrulamanın sağlayıcıya değil buraya ait olması bilinçlidir: sağlayıcı
// üçüncü taraf olabilir ve kendi çıktısını denetlemesi beklenemez. Toplam da
// burada, yuvarlanmış kalem vergileri üzerinden toplanır — kimlik
// (TaxTotal = Σ kalem + kargo) sağlayıcının aritmetiğine bağlı kalmaz.
func assembleResult(in CalculateTaxInput, raw ProviderResult, providerID string) (CalculateTaxResult, error) {
	byID := make(map[string]ProviderItemTax, len(raw.Items))
	for i := range raw.Items {
		line := raw.Items[i]
		if _, dup := byID[line.ID]; dup {
			return CalculateTaxResult{}, errors.Internal(CodeProviderInvalidResult,
				"%q sağlayıcısı %q kalemi için iki sonuç döndürdü", providerID, line.ID)
		}
		byID[line.ID] = line
	}
	if len(byID) != len(in.Items) {
		return CalculateTaxResult{}, errors.Internal(CodeProviderInvalidResult,
			"%q sağlayıcısı %d kalem için %d sonuç döndürdü",
			providerID, len(in.Items), len(byID))
	}

	out := CalculateTaxResult{
		ProviderID: providerID,
		Items:      make([]ItemTax, 0, len(in.Items)),
	}

	var total int64
	for i := range in.Items {
		line, ok := byID[in.Items[i].ID]
		if !ok {
			return CalculateTaxResult{}, errors.Internal(CodeProviderInvalidResult,
				"%q sağlayıcısı %q kalemi için sonuç döndürmedi", providerID, in.Items[i].ID)
		}
		item, err := validateLine(providerID, line, in.Items[i].ID, in.Items[i].Amount)
		if err != nil {
			return CalculateTaxResult{}, err
		}

		total, err = addAmount(total, item.TaxAmount)
		if err != nil {
			return CalculateTaxResult{}, err
		}
		out.Items = append(out.Items, item)
	}

	shippingBase := int64(0)
	if in.Shipping.Taxable {
		shippingBase = in.Shipping.Amount
	}
	shipping, err := validateLine(providerID, raw.Shipping, ShippingLineID, shippingBase)
	if err != nil {
		return CalculateTaxResult{}, err
	}
	total, err = addAmount(total, shipping.TaxAmount)
	if err != nil {
		return CalculateTaxResult{}, err
	}

	out.Shipping = shipping
	out.TaxTotal = total
	return out, nil
}

// validateLine tek bir sağlayıcı satırını doğrular ve sonuç satırına çevirir.
//
// Üst sınırın TABAN olması bilinçlidir: oran en fazla %100 olabildiğine göre
// vergi hiçbir koşulda tabanı aşamaz. Aşan bir değer, sağlayıcının kuruş ile
// birim karıştırdığının (ya da bir para birimi çevrimini atladığının) en olası
// göstergesidir ve sessizce geçseydi müşteriye iki kat fatura çıkardı.
func validateLine(providerID string, line ProviderItemTax, wantID string, base int64) (ItemTax, error) {
	if line.RateBps < models.MinRateBps || line.RateBps > models.MaxRateBps {
		return ItemTax{}, errors.Internal(CodeProviderInvalidResult,
			"%q sağlayıcısı %q kalemi için sözleşme dışı oran döndürdü: %d baz puan ([%d, %d] beklenir)",
			providerID, wantID, line.RateBps, models.MinRateBps, models.MaxRateBps)
	}
	if line.TaxAmount < 0 || line.TaxAmount > base {
		return ItemTax{}, errors.Internal(CodeProviderInvalidResult,
			"%q sağlayıcısı %q kalemi için sözleşme dışı vergi döndürdü: %d ([0, %d] beklenir)",
			providerID, wantID, line.TaxAmount, base)
	}
	return ItemTax{
		ID:            wantID,
		RateID:        line.RateID,
		RateBps:       line.RateBps,
		TaxableAmount: base,
		TaxAmount:     line.TaxAmount,
	}, nil
}

// DefaultRateForCountry bir ülke kökünün VARSAYILAN oranını baz puan olarak
// döner.
//
// Sepet akışının en sade yoludur ve region modülünün GEÇİCİ RegionTax
// metodunun karşılığıdır; modüller arası ilkel imzası [Interop.RateForCountry]
// olarak yayımlanır.
//
// # Neyi DEĞERLENDİRMEZ
//
// Eyalet bölgeleri, kurallar ve kargo bu yolda hiç bakılmaz; bunlara ihtiyaç
// duyan çağıran [Service.CalculateTax] kullanmalıdır. Bölgenin SAĞLAYICISI da
// çağrılmaz: dış bir vergi servisine yalnızca "bu ülkenin oranı nedir" diye
// sormak, sepetin her turunda ağ çağrısı demek olurdu ve dış servislerin
// yanıtı zaten kaleme bağlıdır.
//
// # İki durumun ayrımı
//
// found false ise ülkenin kök vergi bölgesi ya hiç yoktur ya da varsayılan
// oranı yoktur; oran o hâlde daima sıfırdır. Ayrım olmadan çağıran,
// yapılandırma eksiğini "vergisiz ülke" sanardı.
func (s *Service) DefaultRateForCountry(ctx context.Context, countryCode string) (rateBps int32, found bool, err error) {
	if err := s.ready(); err != nil {
		return 0, false, err
	}

	country, err := NormalizeCountryCode(countryCode)
	if err != nil {
		return 0, false, err
	}

	chain, err := s.repo.ResolveTaxRegions(ctx, country, "")
	if err != nil {
		return 0, false, err
	}

	var root models.TaxRegion
	for i := range chain {
		if chain[i].IsRoot() {
			root = chain[i]
			break
		}
	}
	if root.ID == "" {
		return 0, false, nil
	}

	rates, err := s.repo.ListTaxRates(ctx, root.ID)
	if err != nil {
		return 0, false, err
	}
	for i := range rates {
		if !rates[i].IsDefault {
			continue
		}
		// Sözleşme dışı bir oran (elle SQL ile yazılmış olabilir) sessizce
		// geçmez: çağıran onu doğrudan tutarla çarpar ve %1000'lik bir oran
		// sepeti on katına çıkarırdı.
		if rates[i].RateBps < models.MinRateBps || rates[i].RateBps > models.MaxRateBps {
			return 0, false, errors.Internal(CodeRateOutOfRange,
				"%s oranı sözleşme dışı: %d baz puan ([%d, %d] beklenir)",
				rates[i].ID, rates[i].RateBps, models.MinRateBps, models.MaxRateBps)
		}
		return rates[i].RateBps, true, nil
	}
	return 0, false, nil
}
