package service

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// Bu dosya pricing'in MODÜLLER ARASI yüzeyidir (ADR 0001).
//
// Buradaki imzalar YALNIZCA ilkel ve stdlib tipleri kullanır. Sebebi Go'nun
// yapısal uyum kuralıdır: tüketici modül pricing'i import edemediği için
// imzasında [models.PriceSet] gibi bir tipi adlandıramaz; adlandırdığı an o,
// kendi paketinde tanımlı BAŞKA bir tip olur ve somut servis tüketicinin
// arayüzünü karşılamaz. İlkel tiplerle yazılmış bir imza ise tüketicinin kendi
// paketinde birebir tekrarlanabilir ve container'dan adla çözülür.
//
// Modül içi zengin yüzey (models tipleriyle) service.go ve calculate.go'dadır;
// onu yalnızca pricing'in kendi API katmanı ve query sağlayıcısı çağırır.

// CreateEmptyPriceSet fiyatsız bir price set oluşturur ve KİMLİĞİNİ döner.
//
// product modülü bir varyant yaratırken bunu çağırır ve dönen kimliği
// "product_variant_price_set" linkine yazar; pricing o linki hiç görmez ve
// varyantın varlığından haberdar olmaz (Prensip 2.1/2.3).
//
// Tüketici tarafındaki karşılığı:
//
//	type PriceSetCreator interface {
//	    CreateEmptyPriceSet(ctx context.Context) (string, error)
//	}
func (s *Service) CreateEmptyPriceSet(ctx context.Context) (string, error) {
	set, err := s.CreatePriceSet(ctx, nil)
	if err != nil {
		return "", err
	}
	return set.ID, nil
}

// SetBasePrices bir kabın TABAN fiyatlarını para birimi -> tutar eşlemesiyle
// topluca yazar.
//
// "Taban" demek listesiz ve kuralsız demektir; kampanya ve segment fiyatları bu
// yüzeyden yazılamaz (onlar pricing'in kendi admin API'sinin işidir). Yazma
// [Service.SetPrices] gibi YERİNE KOYMADIR: eşlemede olmayan para birimlerinin
// fiyatları silinir.
//
// Eşlemenin dolaşım sırası rastgele olduğu için para birimleri SIRALANIR; aynı
// girdi her çağrıda aynı sırada yazılır ve hata mesajındaki indeks anlamlı olur.
//
// Tüketici tarafındaki karşılığı:
//
//	type BasePriceWriter interface {
//	    SetBasePrices(ctx context.Context, priceSetID string, amountsByCurrency map[string]int64) error
//	}
func (s *Service) SetBasePrices(ctx context.Context, priceSetID string, amountsByCurrency map[string]int64) error {
	inputs := make([]PriceInput, 0, len(amountsByCurrency))
	for _, currency := range slices.Sorted(maps.Keys(amountsByCurrency)) {
		inputs = append(inputs, PriceInput{
			CurrencyCode: currency,
			Amount:       amountsByCurrency[currency],
			MinQuantity:  models.MinQuantity,
		})
	}

	_, err := s.SetPrices(ctx, priceSetID, inputs)
	return err
}

// CalculateAmount seçilen fiyatın BİRİM tutarını minor unit olarak döner.
//
// Seçim kuralı [Service.CalculatePrice] ile birebir aynıdır; bu yalnızca
// modüller arası geçebilen dar bir imzadır. Hesaplama anı "şimdi"dir: tüketici
// modülün geçmişe dönük fiyat sorması bir rapor ihtiyacıdır ve pricing'in kendi
// API'sinden yapılır.
//
// quantity 0 verilirse 1 kabul edilir. attributes nil olabilir; o durumda
// kurallı fiyatlar elenir ve taban fiyat seçilir.
//
// Tüketici tarafındaki karşılığı (Faz 5'te cart bunu tanımlayacaktır):
//
//	type PriceCalculator interface {
//	    CalculateAmount(ctx context.Context, priceSetID, currencyCode string,
//	        quantity int32, attributes map[string]string) (int64, error)
//	}
func (s *Service) CalculateAmount(
	ctx context.Context,
	priceSetID, currencyCode string,
	quantity int32,
	attributes map[string]string,
) (int64, error) {
	calculated, err := s.CalculatePrice(ctx, priceSetID, CalculateParams{
		CurrencyCode: currencyCode,
		Quantity:     quantity,
		Attributes:   attributes,
	})
	if err != nil {
		return 0, err
	}
	return calculated.Amount, nil
}

// MaxCalculateItems tek bir toplu fiyat isteğinin taşıyabileceği kalem
// sayısıdır; aşılırsa istek errors.Invalid ile REDDEDİLİR ve hiçbir kalem
// fiyatlanmaz.
//
// Sınır SESSİZ DEĞİLDİR: kırpma yoktur, hata mesajı hem sınırı hem gelen kalem
// sayısını yazar. Kırpmak, çağıranın sepetinin bir kısmını fiyatlanmamış
// bırakıp sonucu "başarılı" göstermek olurdu.
//
// Değer, tek tüketicisi olan sepet hesabının kendi tavanının (workflows/cart
// içindeki MaxLineItems, bugün 100) ON KATIDIR. İkisinin eşit olmaması
// bilinçlidir: sepet tavanı KONMADAN ÖNCE açılmış ve o tavanın üstünde satır
// taşıyan bir sepetin hesabı yine de yapılabilmelidir — hesabın reddedilmesi,
// müşterinin var olan sepetini ödenemez hâle getirirdi. Aradaki boşluk o eski
// sepetleri kapsar; 1000 satırın da üstü, tek istekte 1000 kabın fiyat
// adayını belleğe alan bir okumadır ve orada durmak gerekir.
//
// # Tavan ile planın DÖNDÜĞÜ nokta aynı yer değildir
//
// 1000 yasal üst sınırdır, UCUZ olanın sınırı değildir: kimlik dizisi
// büyüdükçe planlayıcı bir yerde kısmi indeksi bırakır ve price tablosunu
// baştan tarar. Ölçüldü (gobit_load, 58.000 fiyat satırı, aynı sorgu, ısınmış,
// beşin en iyisi):
//
//	kimlik sayısı   plan                 süre
//	          280   Bitmap Index Scan   0,73 ms
//	          300   Seq Scan on price   4,69 ms
//	        1 000   Seq Scan on price   5,30 ms
//
// Dönüş 280 ile 300 arasındadır, yani tavanın ÜÇ KAT altında. Bugün erişilemez
// (satır açan tek yol sepetin kendi 100'lük tavanına tabidir) ve düzeltilecek
// bir şey de değildir: taramaya düşen tek sorgu, aynı kapları tek tek sormanın
// (300 × ~0,1 ms) yine çok altındadır. Buraya yazılmasının sebebi tavanı
// büyütecek olanın BEKLENTİSİDİR — 1000'e kadar maliyet doğrusal değildir ve
// tavanı büyütmek bu sıçramayı görmeden yapılmamalıdır.
const MaxCalculateItems = 1_000

// calculateAmountsRequest toplu fiyat isteğinin gövdesidir.
//
// Para birimi ve kural bağlamı kalem BAŞINA değil istek başına taşınır: bir
// sepetin tüm satırları aynı para biriminde ve aynı bölgededir, alanı kalem
// başına tekrarlamak iki satırın farklı bağlamla fiyatlanabildiği izlenimi
// verirdi.
type calculateAmountsRequest struct {
	// CurrencyCode istenen para birimidir (ISO 4217); zorunludur.
	CurrencyCode string `json:"currency_code"`
	// Attributes kural bağlamıdır (örn. {"region_id": "reg_1"}); boş olabilir.
	Attributes map[string]string `json:"attributes"`
	// Items fiyatlanacak kalemlerdir; SIRA korunur ve yanıt aynı sıradadır.
	Items []calculateAmountsItem `json:"items"`
}

// calculateAmountsItem toplu istekteki tek bir kalemdir.
type calculateAmountsItem struct {
	// PriceSetID fiyatı sorulan kaptır.
	PriceSetID string `json:"price_set_id"`
	// Quantity satın alınmak istenen adettir; 0 verilirse 1 kabul edilir.
	Quantity int32 `json:"quantity"`
}

// calculateAmountsResponse toplu fiyat yanıtının gövdesidir.
type calculateAmountsResponse struct {
	// Items istekteki kalemlerle AYNI SIRADA ve AYNI UZUNLUKTA sonuçlardır.
	Items []calculatedAmount `json:"items"`
}

// calculatedAmount tek bir kalemin sonucudur.
type calculatedAmount struct {
	// Amount seçilen fiyatın birim tutarıdır (minor unit); Priced false ise
	// anlamsızdır ve sıfırdır.
	Amount int64 `json:"amount"`
	// Priced kalem için geçerli bir fiyat BULUNUP bulunmadığını bildirir.
	//
	// Ayrı bir bayrak ŞARTTIR: sıfır GEÇERLİ bir fiyattır (price tablosunun
	// kısıtı amount >= 0'dır ve bedava kalem gerçek bir senaryodur), dolayısıyla
	// "tutar 0" ile "fiyat yok" tutarın kendisinden ayırt edilemez. Bayrak
	// olmasaydı fiyatı olmayan bir varyant sepete BEDAVA girerdi.
	Priced bool `json:"priced"`
}

// CalculateAmountsJSON birden çok kabın birim tutarını TEK turda döner.
//
// [Service.CalculateAmount] tek kap içindir ve kap başına iki sorgu açar (fiyat
// adayları + kuralları). Bu metot aynı işi kap sayısından BAĞIMSIZ olarak iki
// sorguda yapar; toplu okumanın kendisi zaten vardı ([Repository] üzerindeki
// ListPriceCandidatesBySets) ve buraya kadar taşınmamıştı.
//
// Ölçüm (gobit_load, 54.000 kap, localhost TCP, yedi turun en iyisi): 50 kap
// için kap başına yol 4,93 ms, toplu yol 0,25 ms (20 kat); 100 kap için
// 9,88 ms ve 0,33 ms (30 kat). Tek kapta toplu yolun bir üstünlüğü YOKTUR —
// aday sorgusunun kendisi 500 turun medyanıyla 66 µs'ye karşı 77 µs'dir — bu
// yüzden tekil metot kalır ve tek fiyat soran çağıran onu kullanır. Fark plan
// farkı değildir; EXPLAIN, tek kimlikli dizide de aynı kısmi indeksin
// (price_set_id_idx) tarandığını gösterir, dizili sorgu üstüne bir sıralama
// adımı ekler. Elli kimlikte plan bitmap taramaya geçer ve sunucu tarafı
// tek sorgu için 0,35 ms ölçülür; aynı elli kabın tek tek sorulması sunucuda
// 50 × 0,17 ms eder.
//
// # Seçim kuralı AYNIDIR
//
// Kazanan fiyatı yine [selectPrice] seçer — aynı saf fonksiyon, aynı eleme ve
// sıralama ölçütleri. İki yolun gördüğü aday satırları da aynıdır: iki SQL
// sorgusu aynı sütunları, aynı LEFT JOIN'i ve aynı deleted_at koşulunu taşır,
// toplu olan yalnızca kap kimliğini ANY(...) ile arar. Kap içi sıra da aynıdır
// (p.id) ama sonuç zaten sıradan bağımsızdır: [better] son ölçüt olarak fiyat
// KİMLİĞİNE bakar ve kimlik birincil anahtardır, yani sıralama tamdır.
//
// Tek fark saatin KAÇ KEZ okunduğudur: kap başına yolda her çağrı kendi anını
// alır, burada tüm kalemler TEK an ile değerlendirilir. Fark toplu yolun
// LEHİNEDİR — tam o sırada biten bir kampanya, aynı sepetin iki satırını farklı
// dünyalardan fiyatlayamaz.
//
// # Fiyatı olmayan kalem HATA DEĞİLDİR
//
// Kap için geçerli fiyat yoksa o kalem Priced=false ile döner ve istek başarılı
// sayılır. Tekil metot bu durumda errors.NotFound döner; ayrım bilinçlidir,
// çünkü tek bir fiyatsız satır yüzünden tüm sepetin fiyatını atmak, çağıranın
// hangi satırın sorunlu olduğunu öğrenmek için kalem başına yola dönmesi
// demek olurdu. Hangi satırın reddedileceğine çağıran karar verir.
//
// Bu yüzden kabın HİÇ OLMADIĞI durum da ayrıca sorulmaz (tekil yol boş aday
// görünce [Repository] üzerinden GetPriceSet ile "kap yok" ile "kap boş"u
// ayırır): iki durum da "bu kalemin fiyatı yok"tur ve ikisi de aynı bayrağa
// düşer.
func (s *Service) CalculateAmountsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	req, err := decodeCalculateAmounts(request)
	if err != nil {
		return nil, err
	}
	currency, err := normalizeCurrency(req.CurrencyCode)
	if err != nil {
		return nil, err
	}

	quantities := make([]int32, len(req.Items))
	setIDs := make([]string, 0, len(req.Items))
	seen := make(map[string]struct{}, len(req.Items))
	for i := range req.Items {
		item := req.Items[i]
		if err := requireID(item.PriceSetID, models.PriceSetIDPrefix, itemLabel(i)); err != nil {
			return nil, err
		}
		quantity, err := normalizeQuantity(item.Quantity)
		if err != nil {
			// Sınıf ve KOD korunur; eklenen tek şey hangi kalemin
			// reddedildiğidir. Kodu yeniden yazmak, çağıranın adet
			// doğrulamasına göre dallanmasını bozardı.
			return nil, errors.Wrap(err, errors.KindOf(err), errors.CodeOf(err),
				"%s reddedildi", itemLabel(i))
		}
		quantities[i] = quantity

		if _, dup := seen[item.PriceSetID]; !dup {
			seen[item.PriceSetID] = struct{}{}
			setIDs = append(setIDs, item.PriceSetID)
		}
	}

	candidatesBySet, err := s.repo.ListPriceCandidatesBySets(ctx, setIDs)
	if err != nil {
		return nil, err
	}

	// Saat BİR KEZ okunur; gerekçe godoc'taki "Seçim kuralı AYNIDIR"
	// başlığındadır.
	at := s.clock()

	out := calculateAmountsResponse{Items: make([]calculatedAmount, 0, len(req.Items))}
	for i := range req.Items {
		selected, ok := selectPrice(
			candidatesBySet[req.Items[i].PriceSetID], currency, quantities[i], req.Attributes, at)
		if !ok {
			out.Items = append(out.Items, calculatedAmount{})
			continue
		}
		out.Items = append(out.Items, calculatedAmount{Amount: selected.Amount, Priced: true})
	}

	payload, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"toplu fiyat yanıtı JSON'a çevrilemedi")
	}
	return payload, nil
}

// decodeCalculateAmounts toplu istek gövdesini çözer ve BOYUNU denetler.
func decodeCalculateAmounts(request json.RawMessage) (calculateAmountsRequest, error) {
	if len(request) == 0 {
		return calculateAmountsRequest{}, errors.Invalid(CodeInvalidInput,
			"toplu fiyat isteği boş olamaz")
	}

	var req calculateAmountsRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return calculateAmountsRequest{}, errors.Wrap(err, errors.KindInvalid, CodeInvalidInput,
			"toplu fiyat isteği çözülemedi")
	}
	if len(req.Items) > MaxCalculateItems {
		return calculateAmountsRequest{}, errors.Invalid(CodeInvalidInput,
			"toplu fiyat isteği en fazla %d kalem taşıyabilir, %d verildi",
			MaxCalculateItems, len(req.Items))
	}
	return req, nil
}

// itemLabel toplu istekteki bir kalemin hata mesajlarındaki adıdır.
//
// İndeks yazılır çünkü toplu istekte kalemi ayırt eden başka bir şey yoktur:
// aynı kap iki kez, farklı adetlerle sorulabilir.
func itemLabel(index int) string {
	return "toplu fiyat isteğinin " + strconv.Itoa(index) + ". kalemi"
}
