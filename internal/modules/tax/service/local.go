package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/internal/modules/tax/models"

	"github.com/bdrtr/gobit/core/errors"
)

// LocalProvider vergiyi bu modülün KENDİ tablolarından hesaplar.
//
// Kutudan çıkan sağlayıcıdır ve bölge zincirindeki HİÇBİR halka sağlayıcı
// belirtmediğinde kullanılır; boş bir provider_id "yerel" değil "ebeveynimin
// sağlayıcısı" demektir (bkz. [Service.providerFor]).
// Dış bir vergi servisine (Avalara, TaxJar …) hiç gitmez; oranları ve kuralları
// [RateSource] üzerinden okur.
//
// # Oran seçimi
//
// Bir kaleme UYGULANAN ORAN TEK'tir; oranlar toplanmaz. Seçim, bölge
// zincirinde en ÖZELDEN genele yürüyerek yapılır (eyalet, sonra ülke) ve her
// bölgede şu sıra uygulanır:
//
//  1. Kalemle EŞLEŞEN kurallı oranlar arasında en BELİRGİN olan kazanır: tek
//     bir ürüne yazılmış kural, o ürünün tipine yazılmış kuralı yener
//     (bkz. [models.RuleReference.Specificity]).
//  2. Belirginlik eşitse KİMLİĞİ KÜÇÜK olan kazanır. Kimlikler zaman sıralı
//     olduğu için bu, "önce tanımlanan kazanır" demektir ve sonucu
//     BELİRLENİMCİ kılar; sıra kuralı olmasaydı aynı sepet iki çağrıda iki
//     farklı vergi üretebilirdi.
//  3. Hiçbir kural eşleşmezse bölgenin VARSAYILAN oranı uygulanır.
//  4. Bölge hiçbir oran vermiyorsa (ne eşleşen kural ne varsayılan) zincirin
//     bir ÜST halkasına geçilir.
//
// # Eyalet ülkeyi EZER, oranlar TOPLANMAZ
//
// Karar bilinçlidir. Toplama (eyalet + ülke) yalnızca ülke altı verginin
// gerçekten eklemeli olduğu yargı bölgelerinde doğrudur (ABD eyalet + ilçe
// satış vergisi). KDV/VAT ülkelerinde — Türkiye, AB — ülke oranı verginin
// TAMAMIDIR ve bir eyalet satırı eklendiği an her sepet iki kez vergilenirdi.
// Eklemeli bir yapı gerekiyorsa doğru ifade biçimi AYNI bölgede birleşik bir
// oran tanımlamaktır (örn. %6 eyalet + %2 ilçe için tek bir 800 baz puanlık
// oran); böylece uygulanan oran faturada tek satır olarak da okunur.
//
// # Üst halkaya geçiş neden var
//
// Bir eyalet bölgesi çoğu zaman TEK BİR İSTİSNA için açılır (örn. bir ürün
// grubuna indirimli oran). O bölgede varsayılan oran tanımlanmadıysa, kuralla
// eşleşmeyen kalemlerin vergisiz kalması yerine ülkenin oranına düşmesi
// beklenen davranıştır. Buna karşılık eyaletin VARSAYILAN oranı varsa ülkenin
// oranı hiç görünmez: eyalet, o coğrafyanın tam yanıtını vermiştir.
type LocalProvider struct {
	rates RateSource
}

// NewLocalProvider verilen oran kaynağı üzerinde çalışan yerel sağlayıcıyı
// üretir.
func NewLocalProvider(rates RateSource) *LocalProvider {
	return &LocalProvider{rates: rates}
}

// ID sağlayıcının kimliğini döner.
func (p *LocalProvider) ID() string { return LocalProviderID }

// Calculate kalemlerin ve (istenmişse) kargonun vergisini hesaplar.
//
// Bölge zinciri boşsa hiçbir sorgu yapılmaz ve tüm vergiler sıfır döner;
// vergisi yapılandırılmamış bir ülke hata değil sıfır üretir
// (bkz. [Service.CalculateTax]).
func (p *LocalProvider) Calculate(ctx context.Context, in ProviderInput) (ProviderResult, error) {
	out := ProviderResult{
		Items:    make([]ProviderItemTax, 0, len(in.Items)),
		Shipping: ProviderItemTax{ID: ShippingLineID},
	}
	if len(in.RegionIDs) == 0 || (len(in.Items) == 0 && !in.Shipping.Taxable) {
		for i := range in.Items {
			out.Items = append(out.Items,
				ProviderItemTax{ID: in.Items[i].ID, TaxableAmount: in.Items[i].Amount})
		}
		return out, nil
	}

	table, err := p.loadRates(ctx, in.RegionIDs)
	if err != nil {
		return ProviderResult{}, err
	}

	for i := range in.Items {
		tax, err := table.applyTo(
			itemKeys(in.Items[i]), in.Items[i].ID, in.Items[i].Amount, in.PricesIncludeTax)
		if err != nil {
			return ProviderResult{}, err
		}
		out.Items = append(out.Items, tax)
	}

	if in.Shipping.Taxable {
		tax, err := table.applyTo(
			shippingKeys(in.Shipping), ShippingLineID, in.Shipping.Amount, in.PricesIncludeTax)
		if err != nil {
			return ProviderResult{}, err
		}
		out.Shipping = tax
	}
	return out, nil
}

// loadRates bölge zincirinin oranlarını ve kurallarını İKİ sorguda okur.
//
// Sorgu sayısı kalem sayısından ve zincir uzunluğundan BAĞIMSIZDIR: bir kalem
// de bin kalem de aynı iki turu yapar. Kalem başına oran sorgusu, hesabı
// sepetin boyuyla büyüyen bir N+1'e çevirirdi.
func (p *LocalProvider) loadRates(ctx context.Context, regionIDs []string) (rateTable, error) {
	rates, err := p.rates.ListTaxRatesByRegions(ctx, regionIDs)
	if err != nil {
		return rateTable{}, err
	}

	ruledIDs := make([]string, 0, len(rates))
	for i := range rates {
		if !rates[i].IsDefault {
			ruledIDs = append(ruledIDs, rates[i].ID)
		}
	}

	var rules []models.TaxRateRule
	if len(ruledIDs) > 0 {
		rules, err = p.rates.ListTaxRateRulesByRates(ctx, ruledIDs)
		if err != nil {
			return rateTable{}, err
		}
	}
	return newRateTable(regionIDs, rates, rules), nil
}

// matchKey bir kalemin kurallarla eşleştirilecek tek bir özelliğidir.
type matchKey struct {
	reference   models.RuleReference
	referenceID string
}

// itemKeys bir kalemin eşleşme anahtarlarını üretir.
//
// Boş kimlikler ATLANIR: boş bir ürün tipi, "tipi olmayan ürün" demektir ve
// referenceID'si boş olan bir kural zaten yazılamaz (CHECK kısıtı), yani boş
// anahtarın eşleşme şansı yoktur — listeye konması yalnızca gereksiz
// karşılaştırma üretirdi.
func itemKeys(item TaxableItem) []matchKey {
	keys := make([]matchKey, 0, 3)
	if item.ProductID != "" {
		keys = append(keys, matchKey{models.ReferenceProduct, item.ProductID})
	}
	if item.TaxClassID != "" {
		keys = append(keys, matchKey{models.ReferenceTaxClass, item.TaxClassID})
	}
	if item.ProductTypeID != "" {
		keys = append(keys, matchKey{models.ReferenceProductType, item.ProductTypeID})
	}
	return keys
}

// shippingKeys kargo satırının eşleşme anahtarlarını üretir.
func shippingKeys(shipping ShippingInput) []matchKey {
	if shipping.OptionID == "" {
		return nil
	}
	return []matchKey{{models.ReferenceShippingOption, shipping.OptionID}}
}

// ruledRate kurallı bir oranın kurallarıyla birlikte hâlidir.
type ruledRate struct {
	rate  models.TaxRate
	rules []models.TaxRateRule
}

// rateTable bölge zincirindeki oranların hesap için hazırlanmış hâlidir.
//
// SAF bir veri yapısıdır: veritabanına, saate ve loglamaya dokunmaz. Seçim
// kuralının tek başına, gerçek bir sağlayıcı ya da havuz olmadan sınanabilmesi
// bu ayrım sayesindedir.
type rateTable struct {
	// chain bölge kimlikleridir; en ÖZELDEN genele sıralıdır.
	chain []string
	// ruled bölge kimliğinden o bölgenin kurallı oranlarına eşlemedir;
	// her dilim oran kimliğine göre ARTAN sıralıdır.
	ruled map[string][]ruledRate
	// fallback bölge kimliğinden o bölgenin varsayılan oranına eşlemedir.
	fallback map[string]models.TaxRate
	// standing bir oranın kimliğinden, ONUN ÜSTÜNDE duran orana eşlemedir.
	//
	// Yığın bir LİSTEDİR: her tabanın üstünde en fazla bir oran durur (kısmi
	// benzersiz indeks), yani bu harita yığını sırasıyla yürümeye yeter ve
	// hiçbir konum kolonu gerekmez.
	standing map[string]models.TaxRate
}

// newRateTable oranları ve kuralları hesap tablosuna çevirir.
//
// Varsayılan oranların kuralları BİLİNÇLİ OLARAK yok sayılır: servis ve depo
// katmanı varsayılan bir orana kural yazılmasını zaten reddeder, ama elle
// (doğrudan SQL ile) yazılmış bir kural burada sessizce oranın kapsamını
// daraltmamalıdır. Varsayılan oran, adı gereği, eşleşme aranmadan uygulanan
// orandır.
func newRateTable(chain []string, rates []models.TaxRate, rules []models.TaxRateRule) rateTable {
	byRate := make(map[string][]models.TaxRateRule, len(rules))
	for i := range rules {
		byRate[rules[i].TaxRateID] = append(byRate[rules[i].TaxRateID], rules[i])
	}

	table := rateTable{
		chain:    slices.Clone(chain),
		ruled:    make(map[string][]ruledRate, len(chain)),
		fallback: make(map[string]models.TaxRate, len(chain)),
		standing: make(map[string]models.TaxRate, len(rates)),
	}
	for i := range rates {
		rate := rates[i]
		if rate.StacksOnID != nil {
			// Bir oranın ÜSTÜNDE duran oran hiçbir zaman SEÇİLMEZ: seçilen
			// oranın genişletilmesiyle ulaşılır. Yazma zamanında varsayılan
			// olması ve kurallı bir oranın üstüne kurulması reddediliyor;
			// burada adaylardan çıkarmak o reddin ikinci savunma hattıdır,
			// aynı satırın varsayılan bir oranın elle yazılmış kurallarını
			// düşürmesi gibi.
			//
			// En fazla bir oran bir tabanın üstünde durabilir (kısmi benzersiz
			// indeks). İkincisi yine de gelirse KİMLİĞİ KÜÇÜK olan korunur;
			// sessizce sonuncuyu almak, vergiyi satır sırasına bağlardı.
			if existing, ok := table.standing[*rate.StacksOnID]; !ok || rate.ID < existing.ID {
				table.standing[*rate.StacksOnID] = rate
			}
			continue
		}
		if rate.IsDefault {
			// Bölgede en fazla bir varsayılan oran olabilir (kısmi benzersiz
			// indeks). İkincisi yine de gelirse KİMLİĞİ KÜÇÜK olan korunur;
			// sessizce sonuncuyu almak, sonucu satır sırasına bağlardı.
			if existing, ok := table.fallback[rate.TaxRegionID]; !ok || rate.ID < existing.ID {
				table.fallback[rate.TaxRegionID] = rate
			}
			continue
		}
		table.ruled[rate.TaxRegionID] = append(table.ruled[rate.TaxRegionID], ruledRate{
			rate:  rate,
			rules: byRate[rate.ID],
		})
	}

	for regionID := range table.ruled {
		slices.SortFunc(table.ruled[regionID], func(a, b ruledRate) int {
			return compareStrings(a.rate.ID, b.rate.ID)
		})
	}
	return table
}

// compareStrings iki dizeyi sözlüksel olarak karşılaştırır.
//
// strings.Compare yerine küçük bir yardımcı kullanılması, sıralama ölçütünün
// (kimlik artan) çağrı yerinde okunabilir kalmasını sağlar.
func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// selectRate verilen anahtarlar için uygulanacak oranı seçer.
//
// Sıra ve gerekçeler [LocalProvider] godoc'undadır. Hiçbir oran bulunamazsa
// ikinci dönüş değeri false olur ve vergi sıfırdır.
func (t rateTable) selectRate(keys []matchKey) (models.TaxRate, bool) {
	for _, regionID := range t.chain {
		best, bestSpec, found := models.TaxRate{}, 0, false
		candidates := t.ruled[regionID]
		for i := range candidates {
			spec := matchSpecificity(candidates[i].rules, keys)
			// Kesin büyüklük (>) kullanılması eşitlik hâlinde İLK adayı
			// korur; dilim kimliğe göre sıralı olduğu için bu "en küçük
			// kimlik kazanır" demektir.
			if spec > bestSpec {
				best, bestSpec, found = candidates[i].rate, spec, true
			}
		}
		if found {
			return best, true
		}
		if fallback, ok := t.fallback[regionID]; ok {
			return fallback, true
		}
	}
	return models.TaxRate{}, false
}

// applyTo seçilen oranı verilen tutara uygular.
//
// İki hesap var ve hangisinin koştuğunu `included` belirler. Kapsayıcı
// olmayan pazarda tutar NET'tir, vergi üstüne eklenir ve taban tutarın
// kendisidir. Kapsayıcı pazarda tutar BRÜT'tür, vergi içinden ayıklanır ve
// taban geriye kalandır — böylece taban ile vergi toplandığında müşterinin
// gördüğü rakam çıkar. İkisi birbirinin yerine kullanılamaz; gerekçe ve ölçüm
// [TaxIncludedIn]'de.
func (t rateTable) applyTo(
	keys []matchKey, lineID string, amount int64, included bool,
) (ProviderItemTax, error) {
	rate, ok := t.selectRate(keys)
	if !ok {
		// Oran bulunamadığında vergi sıfırdır ve taban tutarın kendisidir —
		// kapsayıcı pazarda da öyle, çünkü içinden ayıklanacak bir vergi yok.
		return ProviderItemTax{ID: lineID, TaxableAmount: amount}, nil
	}

	stack, err := t.stackFrom(rate)
	if err != nil {
		return ProviderItemTax{}, err
	}

	if included {
		// KAPSAYICI FİYAT + YIĞIN REDDEDİLİR. TaxIncludedIn'in böleni TEK bir
		// oran için 10000+oran'dır (money.go); n bileşen, yuvarlama artığını
		// bir bileşene FERMANLA yükleyen bir ters soyma isterdi ve ADR 0086'nın
		// ölçümü bunun ne kadar kolay yanlış gittiğinin kaydıdır.
		//
		// Yazma zamanında da reddediliyor (oran yazımında ve bölge yazımında),
		// yani buraya düşen bir yapılandırma o kapılardan birinin delindiği
		// anlamına gelir: sessizce yanlış bir sayı üretmek yerine durur.
		if len(stack) > 1 {
			return ProviderItemTax{}, errors.Internal(CodeInconsistentConfig,
				"fiyatların vergi dahil olduğu bölgede %s oranı bir YIĞIN başlatıyor "+
					"(%d bileşen); kapsayıcı fiyatta ters hesap tek orana göre tanımlıdır",
				rate.ID, len(stack))
		}

		tax, taxErr := TaxIncludedIn(amount, rate.RateBps)
		if taxErr != nil {
			return ProviderItemTax{}, taxErr
		}

		return ProviderItemTax{
			ID:            lineID,
			RateID:        rate.ID,
			RateBps:       rate.RateBps,
			TaxAmount:     tax,
			TaxableAmount: amount - tax,
		}, nil
	}

	// Vergi HARİÇ yığın: her bileşen KENDİ tabanı üzerinden hesaplanır ve AYRI
	// AYRI aşağı yuvarlanır. Tek bir yuvarlama sonda yapılsaydı, bileşen başına
	// yazılan sayılar toplamlarına eşit olmazdı — ve bir fatura her bileşeni
	// ayrı basar.
	var total int64
	// Bileşenler YALNIZCA yığınlı satırda taşınır: tek oranlı satırda liste,
	// satırın kendi RateID/RateBps'inin söylediğinden fazlasını söylemez ve
	// "liste var" ibaresi "bu satırı bir yığın vergiledi" anlamını yitirirdi
	// (sözleşme [validateComponents]'te).
	var components []TaxComponent
	if len(stack) > 1 {
		components = make([]TaxComponent, 0, len(stack))
	}
	for i := range stack {
		base := amount
		if stack[i].Compound {
			// Bileşik bileşenin tabanı ALTINDAKİLERİN vergisini de içerir; o
			// vergiler zaten yuvarlanmıştır, yani artık yukarı taşınmaz.
			base, err = addAmount(amount, total)
			if err != nil {
				return ProviderItemTax{}, err
			}
		}

		tax, taxErr := TaxOf(base, stack[i].RateBps)
		if taxErr != nil {
			return ProviderItemTax{}, taxErr
		}
		total, err = addAmount(total, tax)
		if err != nil {
			return ProviderItemTax{}, err
		}

		if components != nil {
			components = append(components, TaxComponent{
				RateID:        stack[i].ID,
				RateBps:       stack[i].RateBps,
				Compound:      stack[i].Compound,
				TaxableAmount: base,
				TaxAmount:     tax,
			})
		}
	}

	return ProviderItemTax{
		ID:      lineID,
		RateID:  stack[0].ID,
		RateBps: stack[0].RateBps,
		// Satırın taşıdığı ORAN yığının TABANIDIR ve tutar toplamdır: taban,
		// gerçekten uygulanmış bir orandır ve gerçekten kaydedilmiş bir tutarın
		// üzerindedir. Yığınlı bir satırda EKSİKTİR — bileşenlerin tamamını
		// taşımak ayrı bir dilim — ama YANLIŞ değildir.
		TaxAmount:     total,
		TaxableAmount: amount,
		// Bileşenler satırın oranını TAMAMLAR, değiştirmez: satır yığının
		// tabanını taşımaya devam eder, bileşenler de her oranın kendi
		// tabanında ne aldığını yazar. Faturanın basacağı rakamlar bunlardır
		// (ADR 0096).
		Components: components,
	}, nil
}

// maxStackDepth bir yığının taşıyabileceği en fazla bileşen sayısıdır.
//
// Dört, bilinen en derin gerçek yığından (federal + eyalet + belediye + ek
// harç) bir fazlasıdır. Sınır bir başarım önlemi değil, YAPILANDIRMANIN
// okunabilirliğinin sınırıdır: beş kademeli bir vergi zincirini kimse gözle
// doğrulayamaz, ve sınırsız bir zincir tek satırda keyfi büyüklükte bir hesap
// açardı.
const maxStackDepth = 4

// stackFrom seçilen orandan başlayarak yığını UYGULAMA SIRASIYLA döner.
//
// Taban her zaman ilk sıradadır. Yürüyüş BELLEKTEKİ oranlar üzerindedir:
// bölgenin bütün oranları zaten yüklüdür (bkz. [LocalProvider.loadRates]), yani
// genişletme hiçbir ek sorgu yapmaz.
//
// # Neden DÖNGÜ koruması yok
//
// Yürüyüş SEÇİLEN orandan başlar ve seçilen bir oran hiçbir şeyin üstünde
// durmaz — üstte duranlar aday havuzuna hiç girmiyor (bkz. [newRateTable]).
// Her adım `standing` üzerinden ilerler ve o harita bir oranın TEK
// stacks_on_id'sinden türer; dolayısıyla zincirin başlangıcına dönebilmesi
// için başlangıcın da bir tabanı olması gerekirdi, ki o zaman seçilmezdi.
//
// Bu yüzden burada görülen kimlikleri saymanın bir karşılığı yok: yazıldı ve
// hiçbir yapılandırmayla tetiklenemediği ölçüldü, sonra silindi. Yürüyüşü yine
// de derinlik SINIRLIYOR — elle yazılmış (servisi atlayan) beş kademeli bir
// zincir mümkün, ve sessizce dört bileşen üretmek yerine durur.
func (t rateTable) stackFrom(base models.TaxRate) ([]models.TaxRate, error) {
	stack := make([]models.TaxRate, 0, maxStackDepth)

	current := base
	for {
		stack = append(stack, current)

		next, ok := t.standing[current.ID]
		if !ok {
			return stack, nil
		}
		if len(stack) >= maxStackDepth {
			return nil, errors.Internal(CodeInconsistentConfig,
				"vergi oranı yığını %d bileşeni aşıyor (%s)", maxStackDepth, base.ID)
		}
		current = next
	}
}

// matchSpecificity kuralların anahtarlarla EN BELİRGİN eşleşmesini döner;
// eşleşme yoksa sıfır.
func matchSpecificity(rules []models.TaxRateRule, keys []matchKey) int {
	best := 0
	for i := range rules {
		for _, key := range keys {
			if rules[i].Reference != key.reference || rules[i].ReferenceID != key.referenceID {
				continue
			}
			if spec := rules[i].Reference.Specificity(); spec > best {
				best = spec
			}
		}
	}
	return best
}
