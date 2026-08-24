package cart

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// Bu dosya sepet hesabının VERGİ ayağıdır (plan Faz 7).
//
// Faz 5'te vergi region'ın tek düz oranından geliyordu ve region'ın godoc'u
// bunu "tax modülü devralacak" diye geçici işaretlemişti; devralma burada
// olur. Region yolu SİLİNMEZ, geri düşüş yolu olarak kalır — hangisinin
// kullanıldığı [Totals.TaxSource] alanında GÖRÜNÜR.

// Verginin hangi kaynaktan geldiğini bildiren değerler ([Totals.TaxSource]).
//
// Alanın var olması bu turun en önemli kararıdır. İki vergi kaynağı arasında
// gidip gelmenin kabul edilebilir tek biçimi, hangisinin cevap verdiğinin
// SONUÇTA okunabilmesidir: aynı sepetin vergisinin iki kurulumda farklı
// çıkması normaldir, ama farkın NEREDEN geldiğinin anlaşılamaması değildir.
const (
	// TaxSourceTax vergiyi tax modülünün hesapladığını ve ülkenin vergi
	// bölgesinin BULUNDUĞUNU bildirir.
	TaxSourceTax = "tax"
	// TaxSourceTaxUnconfigured tax modülünün çağrıldığını ama ülkenin vergi
	// bölgesinin YAPILANDIRILMADIĞINI bildirir; vergi bu durumda sıfırdır.
	//
	// [TaxSourceTax] değerinden ayrı tutulur çünkü sıfır vergi iki farklı
	// sebepten doğar: oran gerçekten sıfırdır ya da o ülke için hiç
	// yapılandırma yoktur. Ayrımı yutan bir alan, eksik kurulumu "vergisiz
	// ülke" sanmaya davetiye olurdu (tax modülü de aynı ayrımı
	// "region_found" ile yapar).
	TaxSourceTaxUnconfigured = "tax_unconfigured"
	// TaxSourceRegion verginin region modülünün oranıyla, yani Faz 5 yoluyla
	// hesaplandığını bildirir.
	TaxSourceRegion = "region"
)

// codeProviderNotFound Query katmanının "bu entity'nin sağlayıcısı container'da
// kayıtlı değil" hata kodudur.
//
// Kod BURADA TEKRARLANIR çünkü core/query'deki karşılığı unexported'dır ve
// paketler arası tek taşınabilir bağ hata kodudur (aynı tekrar product
// modülünün vitrin listelemesinde de vardır). Değeri değişirse bölgenin ülkesi
// okunamadığında hesap, geri düşmek yerine HATA verir — sessizce daha
// hoşgörülü olmaktan yeğdir.
const codeProviderNotFound = "query_provider_not_found"

// taxRequest tax modülüne giden vergi hesabı isteğinin JSON şemasıdır.
//
// Alan adları tax'ın interop şemasıyla BİREBİR aynı olmak ZORUNDADIR: karşı
// taraf bilinmeyen alanları REDDEDER ve iki paket birbirini import edemediği
// için derleyici uyumu göremez (ADR 0006'nın kabul edilen bedeli).
type taxRequest struct {
	// CountryCode ISO 3166-1 alpha-2 kodudur; nereden geldiği
	// [Workflows.countryForRegion] godoc'undadır.
	CountryCode string `json:"country_code"`
	// ProvinceCode eyalet/il kodudur ve bu fazda DAİMA BOŞTUR: sepet
	// yalnızca bölge tutar, adres bu yüzeyden görünmez.
	ProvinceCode string `json:"province_code"`
	// Items vergilendirilecek satırlardır ve sepetteki SIRAYLA gider.
	Items []taxRequestItem `json:"items"`
	// Shipping kargo satırıdır; taxable DAİMA false gider.
	Shipping taxRequestShipping `json:"shipping"`
}

// taxRequestItem istekteki tek bir satırın şemasıdır.
type taxRequestItem struct {
	// ID sepet satırının kimliğidir; vergi aynı kimlikle geri döner.
	ID string `json:"id"`
	// ProductID kural eşleşmesi içindir ve bu fazda BOŞTUR: sepet satırı
	// varyantı bilir, ürünü bilmez.
	ProductID string `json:"product_id"`
	// ProductTypeID kural eşleşmesi içindir ve bu fazda BOŞTUR.
	ProductTypeID string `json:"product_type_id"`
	// Amount satırın İNDİRİM SONRASI vergilendirilebilir tabanıdır.
	Amount int64 `json:"amount"`
}

// taxRequestShipping istekteki kargo satırının şemasıdır.
type taxRequestShipping struct {
	// OptionID kargo seçeneğinin kimliğidir; kural eşleşmesi içindir.
	OptionID string `json:"option_id"`
	// Amount kargo tutarıdır (minor unit).
	Amount int64 `json:"amount"`
	// Taxable kargonun vergilendirilip vergilendirilmeyeceğidir.
	Taxable bool `json:"taxable"`
}

// taxResponse tax modülünden dönen hesap sonucunun JSON şemasıdır.
//
// Bilinmeyen alanlar SESSİZCE ATLANIR (isteğin tersine): tax şemasını
// büyüttüğünde bu paketin aynı turda güncellenmesi gerekmemelidir. Tanınan
// alanların taşıdığı değişmezler [applyTaxResponse] içinde tek tek
// DOĞRULANIR.
type taxResponse struct {
	// RegionID hesabın dayandığı en özel vergi bölgesidir; bölge yoksa boş.
	RegionID string `json:"region_id"`
	// RegionFound ülkeye ait bir vergi bölgesi bulunup bulunmadığıdır.
	RegionFound bool `json:"region_found"`
	// ProviderID hesabı yapan sağlayıcının kimliğidir.
	ProviderID string `json:"provider_id"`
	// TaxTotal toplam vergidir (minor unit).
	TaxTotal int64 `json:"tax_total"`
	// Items satır başına vergidir; İSTEKTEKİ SIRAYLA döner.
	Items []taxResponseLine `json:"items"`
	// Shipping kargo satırının vergisidir; kargo vergilenmediği için sıfır
	// beklenir.
	Shipping taxResponseLine `json:"shipping"`
}

// taxResponseLine yanıttaki tek bir satır vergisinin şemasıdır.
type taxResponseLine struct {
	// ID verginin ait olduğu satırdır.
	ID string `json:"id"`
	// RateID uygulanan oranın kimliğidir; oran bulunamadıysa boş.
	RateID string `json:"rate_id"`
	// RateBps uygulanan orandır (BAZ PUAN; 2000 = %20).
	RateBps int32 `json:"rate_bps"`
	// TaxableAmount verginin hesaplandığı tabandır (minor unit).
	TaxableAmount int64 `json:"taxable_amount"`
	// TaxAmount hesaplanan vergidir (minor unit).
	TaxAmount int64 `json:"tax_amount"`
}

// applyTaxes satırların vergisini hesaplar, satırlara YAZAR ve kullanılan
// KAYNAĞI döner.
//
// İndirim hesaplanmış olmalıdır: vergi tabanı satırın ara toplamı EKSİ satırın
// indirimidir (bkz. paket yorumu, "Vergi sözleşmesi"). Kargo tabana girmez ve
// isteğe taxable=false olarak gider — tax modülü kargoyu opsiyonel kılar, bu
// akış o seçeneği açmaz.
//
// # Otorite TEK'tir ve KURULUMDA seçilir
//
// Ladder üç basamaklıdır ve seçim sonuçta okunur:
//
//  1. tax yüzeyi kayıtlı DEĞİLSE → region'ın oranı ([TaxSourceRegion]).
//  2. Kayıtlıysa ama sepetin bölgesi TEK bir ülkeye çözülemiyorsa → yine
//     region'ın oranı ([TaxSourceRegion]); tax'a hiç sorulmaz.
//  3. Kayıtlı ve ülke belliyse → tax'ın cevabı ([TaxSourceTax] ya da ülkenin
//     yapılandırması yoksa [TaxSourceTaxUnconfigured]).
//
// Üçüncü basamakta tax'ın "bu ülkenin vergi bölgesi yok" cevabı OLDUĞU GİBİ
// kabul edilir ve region'a geri DÜŞÜLMEZ. Ayrım şudur: orada tax ÇAĞRILMIŞ ve
// yetkili bir cevap vermiştir; ikinci basamakta ise hiç çağrılamamıştır, çünkü
// hangi yargı bölgesinin sorulacağı bilinmiyordu — cevabı olmayan bir otorite
// önceki otoriteyi devirmez. Bunu veriye göre gidip gelmeye çevirmek (tax'ta
// yapılandırma varsa tax, yoksa region) verginin hangi ülkeye vergi bölgesi
// tanımlandığına göre sessizce değişmesi olurdu.
//
// # Neden vergi SIFIRA düşmüyor
//
// İndirim eksikse müşteri FAZLA öder ve bunu görür; vergi eksikse satıcı
// eksik tahsil eder, fark faturada hiç görünmez ve ancak mutabakatta ortaya
// çıkar. region'ın oranı da kaybolmuş bir veri değildir — Faz 5'in yetkilisi
// hâlâ oradadır ve devralma bir KABLOLAMA adımıdır, veri silme adımı değil.
// Bu yüzden geri düşüş sıfır değil, önceki yetkilidir.
func (w *Workflows) applyTaxes(
	ctx context.Context,
	snap Snapshot,
	shippingTotal int64,
	lines []LineTotals,
) (string, error) {
	if w.taxes == nil {
		return TaxSourceRegion, w.applyRegionTax(ctx, snap, lines)
	}

	country, reason, err := w.countryForRegion(ctx, snap.RegionID)
	if err != nil {
		return "", err
	}
	if reason != "" {
		w.log.WarnContext(ctx, "sepetin bölgesi tek bir ülkeye çözülemedi; vergi region oranıyla hesaplanıyor",
			slog.String("cart_id", snap.ID),
			slog.String("region_id", snap.RegionID),
			slog.String("sebep", reason),
			slog.String("tax_source", TaxSourceRegion),
		)
		return TaxSourceRegion, w.applyRegionTax(ctx, snap, lines)
	}
	return w.applyModuleTax(ctx, snap, country, shippingTotal, lines)
}

// applyRegionTax vergiyi region'ın düz oranıyla hesaplar (Faz 5 yolu).
func (w *Workflows) applyRegionTax(ctx context.Context, snap Snapshot, lines []LineTotals) error {
	rateBps, err := w.taxRate(ctx, snap.RegionID)
	if err != nil {
		return err
	}

	for i := range lines {
		tax, taxErr := taxOf(lines[i].Subtotal-lines[i].DiscountTotal, rateBps)
		if taxErr != nil {
			return taxErr
		}
		lines[i].TaxTotal = tax
	}
	return nil
}

// taxRate bölgenin uygulanacak vergi oranını baz puan olarak döner.
//
// Vergi OTOMATİK değilse oran sıfırdır: bölge, vergiyi kendi hesaplamak yerine
// dışarıda bırakmayı seçmiştir ve oranı yine de uygulamak o seçimi sessizce
// tersine çevirirdi.
func (w *Workflows) taxRate(ctx context.Context, regionID string) (int32, error) {
	rateBps, automatic, err := w.regions.RegionTax(ctx, regionID)
	if err != nil {
		return 0, err
	}
	if !automatic {
		return 0, nil
	}
	if rateBps < 0 || rateBps > MaxTaxRateBps {
		return 0, errors.Internal(CodeTaxRateInvalid,
			"%s bölgesi sözleşme dışı vergi oranı bildirdi: %d baz puan ([0, %d] beklenir)",
			regionID, rateBps, MaxTaxRateBps)
	}
	return rateBps, nil
}

// applyModuleTax vergiyi tax modülünden alır ve satırlara yazar.
func (w *Workflows) applyModuleTax(
	ctx context.Context,
	snap Snapshot,
	countryCode string,
	shippingTotal int64,
	lines []LineTotals,
) (string, error) {
	items := make([]taxRequestItem, 0, len(lines))
	for i := range lines {
		items = append(items, taxRequestItem{
			ID:     lines[i].LineItemID,
			Amount: lines[i].Subtotal - lines[i].DiscountTotal,
		})
	}

	payload, err := json.Marshal(taxRequest{
		CountryCode: countryCode,
		Items:       items,
		// Kargo tabana GİRMEZ; gerekçe paket yorumundaki "Vergi sözleşmesi"
		// başlığındadır. Tutar yine de bildirilir ki tax modülü ileride
		// kargoyu vergilemeye açıldığında istek şeması değişmesin.
		Shipping: taxRequestShipping{Amount: shippingTotal, Taxable: false},
	})
	if err != nil {
		return "", errors.Wrap(err, errors.KindInternal, CodeTaxFailed,
			"vergi isteği JSON'a çevrilemedi: %s", snap.ID)
	}

	raw, err := w.taxes.CalculateTaxJSON(ctx, payload)
	if err != nil {
		// Sınıf KORUNUR: geçersiz bir ülke kodu Invalid, veritabanı kesintisi
		// Unavailable kalmalıdır; hepsini Internal'a çevirmek düzeltilebilir
		// bir kurulum hatasını sunucu arızası gibi gösterirdi.
		return "", errors.Wrap(err, errors.KindOf(err), CodeTaxFailed,
			"sepet vergisi hesaplanamadı: %s (%q, %d satır)", snap.ID, countryCode, len(lines))
	}

	var resp taxResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", errors.Wrap(err, errors.KindInternal, CodeTaxInvalid,
			"vergi sonucu çözülemedi: %s", snap.ID)
	}
	if err := applyTaxResponse(snap, lines, resp); err != nil {
		return "", err
	}

	if !resp.RegionFound {
		w.log.WarnContext(ctx, "ülkenin vergi bölgesi yapılandırılmamış; vergi sıfır hesaplandı",
			slog.String("cart_id", snap.ID),
			slog.String("country_code", countryCode),
			slog.String("tax_source", TaxSourceTaxUnconfigured),
		)
		return TaxSourceTaxUnconfigured, nil
	}
	return TaxSourceTax, nil
}

// applyTaxResponse yanıtı DOĞRULAR ve satırlara yazar.
//
// tax modülü sağlayıcısının çıktısını zaten doğrular; buradaki ikinci
// doğrulama onun kopyası değildir, SINIRIN bu tarafına ait değişmezleri
// denetler: satır kimlikleri bu sepetin satırlarıdır, sıra korunmuştur, taban
// bizim gönderdiğimizdir ve toplam satırların toplamıdır. Sınırı derleyici
// denetlemediği için (ADR 0006) tek koruma budur.
//
// Yazma, doğrulamanın TAMAMI geçtikten sonra yapılır: yarı yazılmış satırlar,
// hata dönse bile çağıranın elinde tutarsız bir dilim bırakırdı.
func applyTaxResponse(snap Snapshot, lines []LineTotals, resp taxResponse) error {
	if len(resp.Items) != len(lines) {
		return errors.Internal(CodeTaxInvalid,
			"vergi sonucu %d satır için %d kayıt döndürdü (%s)",
			len(lines), len(resp.Items), snap.ID)
	}
	if resp.Shipping.TaxAmount != 0 {
		return errors.Internal(CodeTaxInvalid,
			"kargo vergilenmemesi istendiği hâlde kargo vergisi döndü: %d (%s)",
			resp.Shipping.TaxAmount, snap.ID)
	}

	var sum int64
	for i := range resp.Items {
		line := resp.Items[i]
		base := lines[i].Subtotal - lines[i].DiscountTotal

		if line.ID != lines[i].LineItemID {
			return errors.Internal(CodeTaxInvalid,
				"vergi sonucu istekteki sırayı korumadı: %d. kayıt %q, beklenen %q (%s)",
				i, line.ID, lines[i].LineItemID, snap.ID)
		}
		if line.TaxableAmount != base {
			return errors.Internal(CodeTaxInvalid,
				"vergi tabanı gönderilenden farklı: %q -> %d, gönderilen %d (%s)",
				line.ID, line.TaxableAmount, base, snap.ID)
		}
		// Üst sınırın TABAN olması bilinçlidir: oran en fazla %100 olabildiğine
		// göre vergi hiçbir koşulda tabanı aşamaz ve aşan bir değer, kuruş ile
		// birimin karıştırıldığının en olası göstergesidir.
		if line.TaxAmount < 0 || line.TaxAmount > base {
			return errors.Internal(CodeTaxInvalid,
				"satır vergisi [0, %d] aralığında olmalı: %q -> %d (%s)",
				base, line.ID, line.TaxAmount, snap.ID)
		}

		var err error
		if sum, err = addAmount(sum, line.TaxAmount); err != nil {
			return err
		}
	}

	// Sepetin vergisi Σ satır vergisidir. tax aynı kimliği kendi toplamıyla da
	// bildirir; ikisinin ayrışması, satırlara yazılan vergiyle sepete yazılanın
	// farklı olması demektir.
	if sum != resp.TaxTotal {
		return errors.Internal(CodeTaxInvalid,
			"vergi toplamı satır vergileriyle uyuşmuyor: Σ=%d, bildirilen=%d (%s)",
			sum, resp.TaxTotal, snap.ID)
	}

	for i := range resp.Items {
		lines[i].TaxTotal = resp.Items[i].TaxAmount
	}
	return nil
}

// countryForRegion sepetin bölgesinin ÜLKE kodunu Query katmanından okur.
//
// İkinci dönüş değeri boş DEĞİLSE ülke çözülememiştir ve değeri sebebi
// söyler; o durumda birinci değer anlamsızdır.
//
// # Ülke neden sepetin adresinden değil bölgeden geliyor
//
// Vergi teslim edilen yargı bölgesini izler, yani doğru kaynak sepetin kargo
// adresidir. Ama o adres bu sınırdan GÖRÜNMÜYOR: sepet modülünün anlık görüntü
// şeması ([Snapshot]) adres taşımaz ve bu paket cart'ı import edip şemayı
// büyütemez (ADR 0006). Görünse bile hesabın her turunda dolu olmazdı — sepete
// adres girilmeden önce de toplam hesaplanır ve o turların vergisiz kalması,
// müşteriye ödeyeceğinden azını göstermek olurdu.
//
// Bölge ise DAİMA doludur ([Snapshot.validate] boş bölgeyi reddeder) ve zaten
// sepetin para biriminin, fiyat bağlamının ve Faz 5'te verginin kaynağıdır.
// Adres yüzeye çıktığı gün doğru sıra "önce adresin ülkesi, yoksa bölge" olur
// ve bağlanacağı yer burasıdır.
//
// # Neden TEK ülke şartı var
//
// Bir bölge birden çok ülke taşıyabilir ("Avrupa"). O durumda hangi ülkenin
// vergisinin uygulanacağı sepetin verisinden çıkarılamaz ve haritadan birini
// seçmek, vergiyi sıralama tesadüfüne bağlamak olurdu. Bölge tek ülkeye
// bağlıysa belirsizlik yoktur; değilse ülke ÇÖZÜLEMEMİŞ sayılır ve hesap
// region'ın bölge başına tek oranına düşer — sistemin çok ülkeli bir bölge için
// zaten verdiği cevap odur.
//
// # Query kayıtlı değilse
//
// Bölge sağlayıcısı container'da yoksa (region modülü Query'ye açılmamışsa)
// ülke çözülememiş sayılır. Düşüş HATA SINIFINA göre değil KODA göre
// daraltılmıştır: kayıtlı bir sağlayıcının kendi içinde ürettiği NotFound ya da
// bir veritabanı kesintisi buradan GEÇMEZ ve çağırana hata olarak döner —
// geçici bir arıza yüzünden verginin sessizce başka bir otoriteye kayması,
// aranan en kötü sonuçtur.
func (w *Workflows) countryForRegion(ctx context.Context, regionID string) (code, reason string, err error) {
	records, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityRegion,
		Fields:  []string{query.IDField, FieldCountries},
		Filters: map[string]any{query.IDField: regionID},
		Limit:   1,
	})
	if err != nil {
		if errors.CodeOf(err) == codeProviderNotFound {
			return "", "bölge sağlayıcısı Query katmanında kayıtlı değil", nil
		}
		return "", "", errors.Wrap(err, errors.KindOf(err), CodeRegionReadFailed,
			"%s bölgesi Query katmanından okunamadı", regionID)
	}
	if len(records) == 0 {
		return "", "bölge Query katmanında bulunamadı", nil
	}

	codes := countryCodes(records[0][FieldCountries])
	switch len(codes) {
	case 1:
		return codes[0], "", nil
	case 0:
		return "", "bölgeye bağlı ülke yok", nil
	default:
		return "", "bölge birden çok ülkeye bağlı", nil
	}
}

// countryCodes bölge kaydındaki ülke alt kayıtlarının ISO kodlarını çıkarır.
//
// Üç şekil de kabul edilir: region sağlayıcısı []map[string]any yazar, Query
// kayıtları query.Record taşıyabilir ve bir JSON turundan geçen değer []any
// olur. Tek bir tip iddiası, kodu sessizce yutup bölgeyi "ülkesiz" gösterirdi;
// aynı hoşgörü product modülünün genişletme okumasında da vardır.
//
// Kod okunamayan bir alt kayıt ATLANIR, hata verilmez: sonuçta kalan kod sayısı
// zaten kararı belirler ve eksik bir kod, çoğul bir bölgeyi yanlışlıkla tekil
// göstermez — yalnızca tekil bir bölgeyi çözülemez yapar ki bu güvenli yöndür.
func countryCodes(value any) []string {
	records := make([]query.Record, 0, 4)
	switch typed := value.(type) {
	case []map[string]any:
		for i := range typed {
			records = append(records, typed[i])
		}
	case []query.Record:
		records = append(records, typed...)
	case []any:
		for i := range typed {
			if record := asCountryRecord(typed[i]); record != nil {
				records = append(records, record)
			}
		}
	default:
		return nil
	}

	out := make([]string, 0, len(records))
	for _, record := range records {
		if code, ok := record[FieldCode].(string); ok && code != "" {
			out = append(out, code)
		}
	}
	return out
}

// asCountryRecord tek bir ülke alt kaydını çözer; çözülemezse nil döner.
func asCountryRecord(value any) query.Record {
	switch typed := value.(type) {
	case query.Record:
		return typed
	case map[string]any:
		return typed
	default:
		return nil
	}
}
