package service

import (
	"context"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// CalculateParams bir fiyat hesaplamasının bağlamıdır.
type CalculateParams struct {
	// CurrencyCode istenen para birimidir (ISO 4217); zorunludur.
	CurrencyCode string
	// Quantity satın alınmak istenen adettir; 0 verilirse 1 kabul edilir.
	Quantity int32
	// Attributes kural bağlamıdır (örn. {"region_id": "reg_1"}).
	// Kuralın baktığı alan burada YOKSA kural eşleşmez.
	Attributes map[string]string
	// At hesaplamanın yapıldığı andır; sıfırsa "şimdi" kullanılır. Fiyat
	// listelerinin tarih penceresi bu ana göre değerlendirilir.
	At time.Time
}

// CalculatePrice bir price set'in verilen bağlamdaki GEÇERLİ fiyatını seçer.
//
// Bu fonksiyon Faz 5'te sepet toplamının dayanacağı seçim noktasıdır; kuralı
// bu yüzden burada, tek yerde ve açıkça tanımlıdır.
//
// # 1. Eleme
//
// Bir fiyat şu koşulların HEPSİNİ sağlamıyorsa yarışa hiç girmez:
//
//   - Para birimi istenenle birebir aynıdır (karşılaştırma BÜYÜK harf üzerinden).
//   - Adet, fiyatın [MinQuantity, MaxQuantity] aralığındadır (üst sınır nil ise sınırsız).
//   - Fiyat bir listeye bağlıysa liste KULLANILABİLİR olmalıdır: durumu active
//     ve an, listenin tarih penceresindedir. Listesi silinmiş bir fiyat da elenir.
//   - Fiyatın TÜM kuralları bağlamla eşleşir. Kuralın baktığı alan bağlamda yoksa
//     kural eşleşmez, dolayısıyla fiyat elenir.
//
// # 2. Sıralama
//
// Ayakta kalanlar arasında sırasıyla şu ölçütlere bakılır; ilk FARK kazananı
// belirler:
//
//  1. Liste önceliği (büyük kazanır): override (2) > sale (1) > taban fiyat (0).
//     Sözleşmeli/B2B fiyat kampanyayı, kampanya da taban fiyatı ezer.
//  2. Eşleşen kural sayısı (çok kazanır): daha çok koşul sağlayan fiyat daha
//     BELİRGİNDİR; "TR bölgesi + VIP grubu" fiyatı yalnızca "TR bölgesi"
//     fiyatını yener.
//  3. Adet aralığı genişliği (dar kazanır): 10-20 aralığı, 1-sınırsız aralığını
//     yener. Toptan kademesi bu sayede çalışır.
//  4. Tutar (küçük kazanır): eşdeğer belirginlikte MÜŞTERİ LEHİNE karar verilir.
//  5. Kimlik (küçük kazanır): kalan her durumda sonuç BELİRLENİMCİDİR. Kimlikler
//     zaman sıralı olduğu için bu, "önce yazılan kazanır" demektir.
//
// Hiçbir aday kalmazsa errors.NotFound (kod: [CodeNotCalculable]) döner; bu,
// "bu para biriminde/adette fiyat yok" demektir ve price set'in yokluğundan
// AYRI bir durumdur (o da NotFound'dur ama kodu farklıdır).
func (s *Service) CalculatePrice(
	ctx context.Context,
	priceSetID string,
	params CalculateParams,
) (models.CalculatedPrice, error) {
	if err := s.ready(); err != nil {
		return models.CalculatedPrice{}, err
	}
	if err := requireID(priceSetID, models.PriceSetIDPrefix, "price set id"); err != nil {
		return models.CalculatedPrice{}, err
	}

	currency, err := normalizeCurrency(params.CurrencyCode)
	if err != nil {
		return models.CalculatedPrice{}, err
	}
	quantity, err := normalizeQuantity(params.Quantity)
	if err != nil {
		return models.CalculatedPrice{}, err
	}

	at := params.At
	if at.IsZero() {
		at = s.clock()
	} else {
		at = at.UTC()
	}

	candidates, err := s.repo.ListPriceCandidates(ctx, priceSetID)
	if err != nil {
		return models.CalculatedPrice{}, err
	}
	if len(candidates) == 0 {
		// Kabın hiç fiyatı yoksa iki durum ayırt edilir: kap yok (404,
		// price_set_not_found) ya da kap var ama boş (404, price_not_calculable).
		// Ek sorgu YALNIZCA bu yolda yapılır; mutlu yol tek gidiş dönüştür.
		if _, err := s.repo.GetPriceSet(ctx, priceSetID); err != nil {
			return models.CalculatedPrice{}, err
		}
	}

	selected, ok := selectPrice(candidates, currency, quantity, params.Attributes, at)
	if !ok {
		return models.CalculatedPrice{}, errors.NotFound(CodeNotCalculable,
			"%s için %s para biriminde ve %d adette geçerli fiyat yok",
			priceSetID, currency, quantity).
			WithDetails(map[string]any{
				"price_set_id":  priceSetID,
				"currency_code": currency,
				"quantity":      quantity,
			})
	}
	return selected, nil
}

// normalizeQuantity adet parametresini doğrular ve varsayılanı uygular.
func normalizeQuantity(quantity int32) (int32, error) {
	if quantity == 0 {
		return models.MinQuantity, nil
	}
	if quantity < models.MinQuantity {
		return 0, errors.Invalid(CodeInvalidInput,
			"adet en az %d olmalı, %d verildi", models.MinQuantity, quantity)
	}
	if quantity > models.MaxQuantity {
		return 0, errors.Invalid(CodeInvalidInput,
			"adet en fazla %d olabilir, %d verildi", models.MaxQuantity, quantity)
	}
	return quantity, nil
}

// scored sıralamaya giren tek bir adayın ölçütleridir.
//
// Ölçütler adaydan BİR KEZ türetilir; karşılaştırma sırasında yeniden
// hesaplanmaz. Bu, sıralama kuralının tek bir yerde okunabilir kalmasını sağlar.
type scored struct {
	candidate models.PriceCandidate
	// tier liste önceliğidir (override 2 > sale 1 > taban 0).
	tier int
	// rules eşleşen kural sayısıdır; belirginlik ölçüsüdür.
	rules int
	// span adet aralığının genişliğidir; üst sınırsız aralık için azami değer.
	span int64
}

// selectPrice uygun adaylar arasından kazananı seçer.
//
// SAF fonksiyondur: veritabanına, saate ve loglamaya dokunmaz. Seçim kuralının
// her dalı bu yüzden veritabanı olmadan birim testiyle kanıtlanabilir.
// İkinci dönüş değeri false ise hiçbir aday uygun değildir.
func selectPrice(
	candidates []models.PriceCandidate,
	currency string,
	quantity int32,
	attributes map[string]string,
	at time.Time,
) (models.CalculatedPrice, bool) {
	var best scored
	found := false

	for i := range candidates {
		candidate := candidates[i]
		if !eligible(candidate, currency, quantity, attributes, at) {
			continue
		}
		current := score(candidate)
		if !found || better(current, best) {
			best, found = current, true
		}
	}
	if !found {
		return models.CalculatedPrice{}, false
	}
	return result(best, quantity), true
}

// eligible bir adayın yarışa girip giremeyeceğini bildirir (bkz. CalculatePrice
// godoc'undaki "Eleme").
func eligible(
	candidate models.PriceCandidate,
	currency string,
	quantity int32,
	attributes map[string]string,
	at time.Time,
) bool {
	price := candidate.Price
	if price.CurrencyCode != currency {
		return false
	}
	if quantity < price.MinQuantity {
		return false
	}
	if price.MaxQuantity != nil && quantity > *price.MaxQuantity {
		return false
	}
	if !listAvailable(candidate, at) {
		return false
	}
	return matchRules(price.Rules, attributes)
}

// listAvailable adayın bağlı olduğu listenin verilen anda fiyat sunabildiğini
// bildirir; listesiz (taban) fiyat daima uygundur.
//
// Liste kimliği dolu ama üstverisi yoksa liste SİLİNMİŞTİR; fiyat sahipsiz
// kalır ve hesaba katılmaz.
//
// Ayrı bir fonksiyon olması bilinçlidir: aynı süzgeci [QueryProvider] de
// uygular. Kural tek yerde kalmazsa modülün hesapladığı fiyat ile vitrine
// gösterdiği fiyat ayrışır.
func listAvailable(candidate models.PriceCandidate, at time.Time) bool {
	if candidate.Price.PriceListID == nil {
		return true
	}
	return candidate.List != nil && candidate.List.Usable(at)
}

// score adayın sıralama ölçütlerini türetir.
func score(candidate models.PriceCandidate) scored {
	tier := 0
	if candidate.Price.PriceListID != nil && candidate.List != nil {
		tier = candidate.List.Type.Priority()
	}
	return scored{
		candidate: candidate,
		tier:      tier,
		rules:     len(candidate.Price.Rules),
		span:      quantitySpan(candidate.Price),
	}
}

// quantitySpan adet aralığının genişliğidir; üst sınır yoksa azami değer.
//
// Sınırsız aralığın azami genişlik sayılması, "dar olan kazanır" kuralının
// doğal sonucudur: sınırsız aralık her zaman en genel adaydır.
func quantitySpan(price models.Price) int64 {
	if price.MaxQuantity == nil {
		return math.MaxInt64
	}
	return int64(*price.MaxQuantity) - int64(price.MinQuantity)
}

// better a'nın b'yi yenip yenmediğini bildirir.
//
// Ölçüt sırası CalculatePrice godoc'unda tanımlıdır; ilk FARK kazananı belirler.
// Son ölçüt kimliktir ve asla eşit çıkmaz (birincil anahtar), yani sıralama
// TAMDIR: sonuç adayların geliş sırasından bağımsızdır.
func better(a, b scored) bool {
	if a.tier != b.tier {
		return a.tier > b.tier
	}
	if a.rules != b.rules {
		return a.rules > b.rules
	}
	if a.span != b.span {
		return a.span < b.span
	}
	if a.candidate.Price.Amount != b.candidate.Price.Amount {
		return a.candidate.Price.Amount < b.candidate.Price.Amount
	}
	return a.candidate.Price.ID < b.candidate.Price.ID
}

// result kazanan adayı sonuç modeline çevirir.
func result(best scored, quantity int32) models.CalculatedPrice {
	price := best.candidate.Price

	var listType models.PriceListType
	if price.PriceListID != nil && best.candidate.List != nil {
		listType = best.candidate.List.Type
	}

	maxQty := price.MaxQuantity
	if maxQty != nil {
		limit := *maxQty
		maxQty = &limit
	}

	return models.CalculatedPrice{
		PriceID:       price.ID,
		PriceSetID:    price.PriceSetID,
		CurrencyCode:  price.CurrencyCode,
		Amount:        price.Amount,
		Quantity:      quantity,
		Total:         price.Amount * int64(quantity),
		MinQuantity:   price.MinQuantity,
		MaxQuantity:   maxQty,
		PriceListID:   price.PriceListID,
		PriceListType: listType,
		MatchedRules:  best.rules,
	}
}

// matchRules fiyatın TÜM kurallarının bağlamla eşleştiğini bildirir.
// Kuralsız fiyat koşulsuzdur ve daima eşleşir.
func matchRules(rules []models.PriceRule, attributes map[string]string) bool {
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
// kuralları sağlayarak segment fiyatlarını herkese açardı.
//
// DEĞERSİZ kural da eşleşmez ve PANİK ÜRETMEZ. Böyle bir kaydı servis
// doğrulaması üretmez, ama hesaplama veritabanından okuduğu her satıra
// dayanıklı olmalıdır: doğrudan SQL çalıştıran bir bakım betiği ya da kısmi
// bir geri yükleme değerleri boş bırakabilir. Gerekçe tanınmayan işleçtekiyle
// aynıdır — okunamayan bir koşul, kuralı sessizce devre dışı bırakıp fiyatı
// herkese AÇMAMALIDIR.
func matchRule(rule models.PriceRule, attributes map[string]string) bool {
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
		return matchNumeric(rule, value)
	default:
		// Tanınmayan işleç EŞLEŞMEZ: veritabanına sonradan sızmış bir değer,
		// kuralı sessizce devre dışı bırakıp fiyatı herkese açık hâle
		// getirmemelidir.
		return false
	}
}

// matchNumeric sayısal işleçleri değerlendirir.
//
// İki taraf da tam sayıya çevrilebilmelidir; çevrilemeyen bir bağlam değeri
// kuralı eşleşmez yapar (hata üretmez): bağlam dışarıdan gelir ve tek bir bozuk
// alan tüm fiyat hesabını düşürmemelidir.
//
// YALNIZCA matchRule'dan çağrılır ve kuralın en az bir değeri olduğu orada
// güvence altına alınmıştır; ilk değer bu yüzden doğrudan okunur.
func matchNumeric(rule models.PriceRule, value string) bool {
	left, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return false
	}
	right, err := strconv.ParseInt(rule.Values[0], 10, 64)
	if err != nil {
		return false
	}

	switch rule.Operator {
	case models.OpGt:
		return left > right
	case models.OpGte:
		return left >= right
	case models.OpLt:
		return left < right
	case models.OpLte:
		return left <= right
	case models.OpEq, models.OpNe, models.OpIn, models.OpNin:
		return false
	default:
		return false
	}
}
