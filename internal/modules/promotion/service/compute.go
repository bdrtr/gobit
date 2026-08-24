package service

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// MaxComputeLines tek bir hesapta taşınabilecek azami satır sayısıdır.
//
// Sınırın var olması şarttır: her satır her promosyon için ayrı ayrı
// değerlendirilir ve sınırsız bir liste, tek istekle hesabı meşgul ederdi.
// Değer cömerttir — gerçek bir sepetin satır sayısı bunun çok altındadır ve
// sınır ancak bozuk bir istemciyi durdurmak için vardır.
const MaxComputeLines = 1000

// ComputeItem hesaba giren tek bir sepet kalemidir.
type ComputeItem struct {
	// ID kalemin kimliğidir; sonuçta indirim bu kimlikle geri döner ve aynı
	// listede TEKRAR EDEMEZ.
	ID string
	// Amount kalemin ara toplamıdır (birim × adet), minor unit.
	//
	// Birim fiyat DEĞİL satır tutarı taşınır: indirim satıra uygulanır ve
	// birim fiyattan satır tutarını yeniden hesaplamak, bölünmeyen adetlerde
	// çağıranınkinden farklı bir taban üretirdi.
	Amount int64
	// Quantity kalemin adedidir; "fixed" + "each" indiriminde kaç birime
	// uygulanacağını belirler.
	Quantity int64
	// Attributes hedef kurallarının bakacağı kalem öznitelikleridir
	// (örn. {"product_category_id": "cat_1"}). nil olabilir; o durumda hedef
	// kuralı olan bir promosyon bu kalemi seçemez.
	Attributes map[string]string
}

// ComputeShippingMethod hesaba giren tek bir kargo yöntemidir.
//
// Adet TAŞIMAZ: bir kargo yönteminin adedi yoktur ve "fixed" + "each"
// indiriminde bir birim sayılır.
type ComputeShippingMethod struct {
	// ID kargo yönteminin kimliğidir; aynı listede TEKRAR EDEMEZ.
	ID string
	// Amount kargo tutarıdır (minor unit).
	Amount int64
	// Attributes hedef kurallarının bakacağı özniteliklerdir; nil olabilir.
	Attributes map[string]string
}

// ComputeInput bir indirim hesabının bağlamıdır.
type ComputeInput struct {
	// CurrencyCode sepetin para birimidir (ISO 4217); ZORUNLUDUR.
	//
	// Sabit tutarlı indirimler yalnızca KENDİ para biriminde uygulanır; farklı
	// para birimindeki bir promosyon elenir. Kur çevirisi promotion'ın işi
	// değildir ve sessiz bir çeviri, 100 USD'lik bir indirimi 100 TRY olarak
	// uygulardı.
	CurrencyCode string
	// Context bağlam kurallarının bakacağı alanlardır (örn.
	// {"region_id": "reg_1", "customer_group_id": "vip"}). nil olabilir; o
	// durumda bağlam kuralı olan her promosyon elenir.
	Context map[string]string
	// Items sepet kalemleridir.
	Items []ComputeItem
	// ShippingMethods sepetin kargo yöntemleridir.
	ShippingMethods []ComputeShippingMethod
	// Codes uygulanacak kupon kodlarıdır; sıraları SONUCU ETKİLEMEZ
	// (bkz. [Service.ComputeDiscounts], "Sıra").
	Codes []string
	// At hesabın yapıldığı andır; sıfırsa "şimdi" kullanılır. Kampanyaların
	// tarih penceresi bu ana göre değerlendirilir.
	At time.Time
}

// LineDiscount tek bir satıra düşen indirimdir.
type LineDiscount struct {
	// ID satırın kimliğidir.
	ID string
	// Amount satıra düşen TOPLAM indirimdir (minor unit); satırın tutarını
	// ASLA aşmaz.
	Amount int64
}

// AppliedPromotion hesapta fiilen indirim üreten bir promosyondur.
type AppliedPromotion struct {
	// PromotionID promosyonun kimliğidir.
	PromotionID string
	// Code promosyonun kupon kodudur.
	Code string
	// IsAutomatic promosyonun kodsuz uygulanıp uygulanmadığını bildirir.
	IsAutomatic bool
	// Amount promosyonun FİİLEN uyguladığı toplam indirimdir; satır
	// sınırlarına takılan kısım BURAYA GİRMEZ.
	Amount int64
}

// ComputeResult bir indirim hesabının sonucudur.
//
// Kimlik her zaman sağlanır:
//
//	DiscountTotal = ItemsDiscountTotal + ShippingDiscountTotal
//	              = Σ Items[i].Amount + Σ ShippingMethods[i].Amount
//	              = Σ Applied[i].Amount
type ComputeResult struct {
	// CurrencyCode hesabın para birimidir (BÜYÜK harf).
	CurrencyCode string
	// Items kalem başına indirimlerdir; girdideki HER kalem için bir kayıt
	// içerir (indirimi sıfır olanlar dâhil) ve girdiyle AYNI sıradadır.
	Items []LineDiscount
	// ShippingMethods kargo yöntemi başına indirimlerdir; aynı kural geçerlidir.
	ShippingMethods []LineDiscount
	// ItemsDiscountTotal kalemlere düşen toplam indirimdir.
	ItemsDiscountTotal int64
	// ShippingDiscountTotal kargo yöntemlerine düşen toplam indirimdir.
	ShippingDiscountTotal int64
	// DiscountTotal toplam indirimdir.
	DiscountTotal int64
	// Applied fiilen indirim üreten promosyonlardır, UYGULAMA SIRASINDA.
	Applied []AppliedPromotion
	// UnmatchedCodes uygulanabilir bir promosyona bağlanamayan kupon kodlarıdır.
	//
	// Kod yanlış olabilir, promosyon taslak/pasif olabilir, kampanyası bitmiş
	// ya da bütçesi tükenmiş olabilir; AYRIM YAPILMAZ. Sebep sızıntıdır:
	// "bu kod var ama kampanyası henüz başlamadı" cevabı, yayınlanmamış bir
	// kampanyanın varlığını ele verirdi.
	UnmatchedCodes []string
}

// ComputeDiscounts verilen sepet bağlamı için indirimleri hesaplar.
//
// BU MODÜLÜN KALBİDİR ve HİÇBİR ŞEY YAZMAZ: kupon sayacı ve kampanya bütçesi
// yalnızca [Service.RedeemPromotion] ile değişir. Ayrım zorunludur — sepet
// toplamı her değişiklikte yeniden hesaplanır ve her hesabın bir kuponu
// tüketmesi, sepete bakmakla kuponu harcamayı aynı şey yapardı.
//
// # 1. Eleme
//
// Bir promosyon şu koşulların HEPSİNİ sağlamıyorsa hesaba hiç girmez:
//
//   - Durumu "active"dir. Taslak ve pasif promosyonlar indirim üretmez.
//   - Türü "standard"dır. "buyget" mekaniği bu fazda yoktur (bkz.
//     [models.PromotionBuyGet]); yapısal olarak etkinleştirilemez, buradaki
//     eleme ikinci savunmadır.
//   - Bir uygulama yöntemi vardır. Yöntemsiz promosyon indirimin NASIL
//     uygulanacağını söylemez ve atlanır.
//   - Kullanım sınırı DOLMAMIŞTIR.
//   - Kampanyası varsa: kampanya SİLİNMEMİŞ, tarih penceresi anı KAPSIYOR ve
//     bütçesi TÜKENMEMİŞ olmalıdır.
//   - Kampanyasının bütçesi PARA ölçülüyse bütçenin para birimi sepetinkiyle
//     AYNIDIR. Aksi hâlde indirim sepette görünür ama [Service.RedeemPromotion]
//     onu reddederdi (bkz. [campaignBudgetCurrencyMatches]).
//   - Sabit tutarlı indirimse para birimi sepetinkiyle AYNIDIR.
//   - TÜM bağlam kuralları sepet bağlamıyla eşleşir.
//
// Kupon kodları için ek koşul: kodun sahibi promosyon yalnızca kod verildiğinde
// hesaba girer. Otomatik promosyonlar kodsuz girer.
//
// # 2. Sıra — ÖNCE KUPONLAR, SONRA OTOMATİKLER
//
// Uygulama sırası şudur ve çağıranın kod sırasından BAĞIMSIZDIR:
//
//  1. Kupon kodlu promosyonlar, kimliğe göre artan sırada.
//  2. Otomatik promosyonlar, kimliğe göre artan sırada.
//
// Kimlikler zaman sıralı olduğu için ikinci ölçüt "önce tanımlanan önce
// uygulanır" demektir ve sonuç BELİRLENİMCİDİR.
//
// Kuponların önce gelmesi bir tercih değil, açıklanabilirlik kararıdır: sıra
// yalnızca bir satırın indirimi tutarına DAYANDIĞINDA görünür hâle gelir ve o
// anda kırpılan promosyon sonuncusudur. Müşteri, kendi yazdığı kuponun tam
// uygulandığını görmelidir; hiç adını duymadığı bir otomatik indirimin sessizce
// kırpılması ise sorulmayan bir sorudur.
//
// # 3. Yüzdeler BİRBİRİNİN ÜZERİNE BİNMEZ (bileşik değil)
//
// Her yüzde indirim, satırın ORİJİNAL tutarı üzerinden hesaplanır; önceki
// indirimlerden ARTA KALAN tutar üzerinden değil. %10 ve %20 birlikte
// uygulandığında toplam indirim %30'dur, %28 değil.
//
// Gerekçe üç katlıdır:
//
//   - Açıklanabilirlik: müşteri iki indirimi toplar, çarpmaz. Bileşik hesap
//     her zaman söz verilenden azını verir ve destek talebine dönüşür.
//   - Sıra bağımsızlığı: bileşik hesapta sonuç uygulama sırasına bağlıdır;
//     bileşik olmayan hesapta sıra yalnızca üst sınır bağladığında görünür.
//   - Üst sınır zaten güvencededir (aşağıya bakınız), yani bileşik hesabın
//     koruduğu tek şeyi (indirimin tutarı aşmaması) başka bir kural sağlar.
//
// # 4. Üst sınırlar
//
// İki değişmez her koşulda korunur:
//
//   - Bir satırın TOPLAM indirimi, satırın tutarını AŞAMAZ. Aşan kısım düşer
//     ve promosyonun [AppliedPromotion.Amount] değerine GİRMEZ.
//   - Toplam indirim ara toplamı aşamaz; bu, satır sınırının doğal sonucudur
//     (Σ satır indirimi ≤ Σ satır tutarı).
//
// Sınırın kırpılan kısmı BAŞKA bir satıra taşınmaz: taşımak, o satıra
// promosyonun vaat ettiğinden fazlasını vermek olurdu.
//
// # 5. Yuvarlama
//
// Yüzde hesabı tam sayıdır ve AŞAĞI yuvarlar (bkz.
// [models.BasisPointDenominator]). "across" tahsisinde toplam BİR KEZ
// yuvarlanır, sonra satırlara birebir dağıtılır — kuruş artığının kime gittiği
// [allocateAcross] godoc'unda tanımlıdır.
//
// # 6. Kampanya bütçesi
//
// Bütçesi TÜKENMİŞ bir kampanyanın promosyonu hiç uygulanmaz; KISMİ uygulama
// yapılmaz. Sebep, bu çağrının yan etkisiz olmasıdır: kalan bütçeyi burada
// paylaştırmak, hesapla kullanım arasında değişebilen bir sayıyı müşteriye
// kesinmiş gibi göstermek olurdu. Bütçenin gerçek hakemi
// [Service.RedeemPromotion]'dır ve orada sınır aşılırsa errors.Conflict döner.
func (s *Service) ComputeDiscounts(ctx context.Context, in ComputeInput) (ComputeResult, error) {
	if err := s.ready(); err != nil {
		return ComputeResult{}, err
	}

	normalized, err := normalizeComputeInput(in, s.clock())
	if err != nil {
		return ComputeResult{}, err
	}

	candidates, err := s.repo.ListCandidates(ctx, normalized.Codes)
	if err != nil {
		return ComputeResult{}, err
	}
	return computeDiscounts(candidates, normalized), nil
}

// normalizeComputeInput girdiyi doğrular ve normalleştirilmiş bir KOPYASINI
// döner.
//
// Kopya şarttır: kodlar büyük harfe çevrilip tekilleştirilir ve çağıranın
// dilimini yerinde değiştirmek, isteği gönderene ait bir veriyi bozmak olurdu.
func normalizeComputeInput(in ComputeInput, now time.Time) (ComputeInput, error) {
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return ComputeInput{}, err
	}

	if len(in.Items) > MaxComputeLines {
		return ComputeInput{}, errors.Invalid(CodeInvalidInput,
			"hesap en fazla %d kalem taşıyabilir, %d verildi", MaxComputeLines, len(in.Items))
	}
	if len(in.ShippingMethods) > MaxComputeLines {
		return ComputeInput{}, errors.Invalid(CodeInvalidInput,
			"hesap en fazla %d kargo yöntemi taşıyabilir, %d verildi",
			MaxComputeLines, len(in.ShippingMethods))
	}

	items := make([]ComputeItem, 0, len(in.Items))
	seen := make(map[string]struct{}, len(in.Items))
	var itemsSubtotal int64
	for i := range in.Items {
		item := in.Items[i]
		if err := validateLineID("kalem kimliği", item.ID, seen); err != nil {
			return ComputeInput{}, withIndex(err, detailItemIndex, i)
		}
		if err := validateAmount("kalem tutarı", item.Amount); err != nil {
			return ComputeInput{}, withIndex(err, detailItemIndex, i)
		}
		if err := validateQuantity("kalem adedi", item.Quantity); err != nil {
			return ComputeInput{}, withIndex(err, detailItemIndex, i)
		}
		itemsSubtotal += item.Amount
		if itemsSubtotal > models.MaxAmount {
			return ComputeInput{}, errors.Invalid(CodeInvalidInput,
				"kalem ara toplamı en fazla %d olabilir (minor unit)", models.MaxAmount)
		}
		item.Attributes = maps.Clone(item.Attributes)
		items = append(items, item)
	}

	shipping := make([]ComputeShippingMethod, 0, len(in.ShippingMethods))
	seenShipping := make(map[string]struct{}, len(in.ShippingMethods))
	var shippingSubtotal int64
	for i := range in.ShippingMethods {
		method := in.ShippingMethods[i]
		if err := validateLineID("kargo yöntemi kimliği", method.ID, seenShipping); err != nil {
			return ComputeInput{}, withIndex(err, detailShippingIndex, i)
		}
		if err := validateAmount("kargo tutarı", method.Amount); err != nil {
			return ComputeInput{}, withIndex(err, detailShippingIndex, i)
		}
		shippingSubtotal += method.Amount
		if shippingSubtotal > models.MaxAmount {
			return ComputeInput{}, errors.Invalid(CodeInvalidInput,
				"kargo ara toplamı en fazla %d olabilir (minor unit)", models.MaxAmount)
		}
		method.Attributes = maps.Clone(method.Attributes)
		shipping = append(shipping, method)
	}

	codes, err := normalizeCodes(in.Codes)
	if err != nil {
		return ComputeInput{}, err
	}

	at := in.At
	if at.IsZero() {
		at = now
	} else {
		at = at.UTC()
	}

	return ComputeInput{
		CurrencyCode:    currency,
		Context:         maps.Clone(in.Context),
		Items:           items,
		ShippingMethods: shipping,
		Codes:           codes,
		At:              at,
	}, nil
}

// normalizeCodes kupon kodlarını doğrular, BÜYÜK harfe çevirir ve
// TEKİLLEŞTİRİR.
//
// Tekilleştirme şarttır: aynı kod iki kez verilseydi promosyon iki kez
// uygulanır ve indirim ikiye katlanırdı. Sıra korunur ki hata mesajındaki
// indeks anlamlı olsun; uygulama sırası zaten koddan bağımsızdır.
func normalizeCodes(codes []string) ([]string, error) {
	if len(codes) == 0 {
		return []string{}, nil
	}
	if len(codes) > MaxCodesPerCompute {
		return nil, errors.Invalid(CodeInvalidInput,
			"tek hesapta en fazla %d kupon kodu verilebilir, %d verildi",
			MaxCodesPerCompute, len(codes))
	}

	out := make([]string, 0, len(codes))
	seen := make(map[string]struct{}, len(codes))
	for i, raw := range codes {
		code, err := normalizeCode(raw)
		if err != nil {
			return nil, withIndex(err, detailCodeIndex, i)
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out, nil
}

// validateLineID bir satır kimliğinin dolu ve TEKİL olduğunu doğrular.
//
// Tekillik şarttır: sonuç satır kimliğiyle geri döner ve aynı kimlik iki kez
// geçseydi çağıran hangi satırın hangi indirimi aldığını ayırt edemezdi.
func validateLineID(label, id string, seen map[string]struct{}) error {
	if err := validateText(label, id, 1, maxIDLen); err != nil {
		return err
	}
	if _, dup := seen[id]; dup {
		return errors.Invalid(CodeInvalidInput, "%s tekrar ediyor: %q", label, id)
	}
	seen[id] = struct{}{}
	return nil
}

// lineState hesap boyunca tek bir satırın değişen durumudur.
type lineState struct {
	// id satırın kimliğidir.
	id string
	// amount satırın ORİJİNAL tutarıdır; hesap boyunca değişmez ve yüzde
	// indirimlerin tabanıdır (bileşik olmama kararı).
	amount int64
	// quantity satırın adedidir; kargo yönteminde birdir.
	quantity int64
	// attributes hedef kurallarının bakacağı özniteliklerdir.
	attributes map[string]string
	// discount satıra o ana kadar uygulanmış TOPLAM indirimdir.
	discount int64
}

// remaining satıra daha ne kadar indirim uygulanabileceğini döner.
func (l *lineState) remaining() int64 {
	if l.discount >= l.amount {
		return 0
	}
	return l.amount - l.discount
}

// charge satıra indirim uygular ve FİİLEN uygulananı döner.
//
// İstenen tutar satırın kalanını aşarsa kırpılır; kırpılan kısım kaybolur ve
// başka bir satıra taşınmaz (bkz. [Service.ComputeDiscounts], "Üst sınırlar").
func (l *lineState) charge(want int64) int64 {
	if want <= 0 {
		return 0
	}
	if limit := l.remaining(); want > limit {
		want = limit
	}
	l.discount += want
	return want
}

// computeDiscounts adaylardan sonucu üretir; SAF fonksiyondur.
//
// Veritabanına, saate ve loglamaya dokunmaz. Hesabın her dalı bu yüzden
// veritabanı olmadan birim testiyle kanıtlanabilir — modülün en kritik kararı
// olan indirim aritmetiği için bu bir gerekliliktir.
func computeDiscounts(candidates []models.PromotionCandidate, in ComputeInput) ComputeResult {
	items := make([]lineState, 0, len(in.Items))
	for i := range in.Items {
		items = append(items, lineState{
			id:         in.Items[i].ID,
			amount:     in.Items[i].Amount,
			quantity:   in.Items[i].Quantity,
			attributes: in.Items[i].Attributes,
		})
	}
	shipping := make([]lineState, 0, len(in.ShippingMethods))
	for i := range in.ShippingMethods {
		shipping = append(shipping, lineState{
			id:       in.ShippingMethods[i].ID,
			amount:   in.ShippingMethods[i].Amount,
			quantity: 1,
			// Kargo yönteminin adedi yoktur; "fixed" + "each" indiriminde bir
			// birim sayılır.
			attributes: in.ShippingMethods[i].Attributes,
		})
	}

	eligible := eligibleCandidates(candidates, in)
	applied := make([]AppliedPromotion, 0, len(eligible))
	for i := range eligible {
		amount := applyPromotion(eligible[i], items, shipping)
		if amount <= 0 {
			continue
		}
		applied = append(applied, AppliedPromotion{
			PromotionID: eligible[i].Promotion.ID,
			Code:        eligible[i].Promotion.Code,
			IsAutomatic: eligible[i].Promotion.IsAutomatic,
			Amount:      amount,
		})
	}

	result := ComputeResult{
		CurrencyCode:    in.CurrencyCode,
		Items:           make([]LineDiscount, 0, len(items)),
		ShippingMethods: make([]LineDiscount, 0, len(shipping)),
		Applied:         applied,
		UnmatchedCodes:  unmatchedCodes(in.Codes, eligible),
	}
	for i := range items {
		result.Items = append(result.Items, LineDiscount{ID: items[i].id, Amount: items[i].discount})
		result.ItemsDiscountTotal += items[i].discount
	}
	for i := range shipping {
		result.ShippingMethods = append(result.ShippingMethods,
			LineDiscount{ID: shipping[i].id, Amount: shipping[i].discount})
		result.ShippingDiscountTotal += shipping[i].discount
	}
	result.DiscountTotal = result.ItemsDiscountTotal + result.ShippingDiscountTotal
	return result
}

// eligibleCandidates uygulanabilir adayları süzer ve UYGULAMA SIRASINA dizer.
//
// Sıra kuralı [Service.ComputeDiscounts] godoc'unda tanımlıdır: önce kuponlar,
// sonra otomatikler; her grup içinde kimliğe göre artan.
func eligibleCandidates(candidates []models.PromotionCandidate, in ComputeInput) []models.PromotionCandidate {
	out := make([]models.PromotionCandidate, 0, len(candidates))
	for i := range candidates {
		if eligible(candidates[i], in) {
			out = append(out, candidates[i])
		}
	}

	slices.SortFunc(out, func(a, b models.PromotionCandidate) int {
		// Kuponlar (otomatik olmayanlar) önce gelir; bool sıralaması yerine
		// açık bir ölçüt kullanılır ki niyet okunabilir olsun.
		if a.Promotion.IsAutomatic != b.Promotion.IsAutomatic {
			if a.Promotion.IsAutomatic {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.Promotion.ID, b.Promotion.ID)
	})
	return out
}

// eligible bir adayın hesaba girip giremeyeceğini bildirir (bkz.
// [Service.ComputeDiscounts] godoc'undaki "Eleme").
func eligible(candidate models.PromotionCandidate, in ComputeInput) bool {
	promo := candidate.Promotion
	if promo.Status != models.PromotionActive {
		return false
	}
	if promo.Type != models.PromotionStandard {
		return false
	}
	if candidate.Method == nil {
		return false
	}
	if promo.UsageExhausted() {
		return false
	}
	if !promo.IsAutomatic && !slices.Contains(in.Codes, promo.Code) {
		return false
	}
	if !campaignUsable(candidate, in.At) {
		return false
	}
	if !campaignBudgetCurrencyMatches(candidate, in.CurrencyCode) {
		return false
	}
	if candidate.Method.Type == models.MethodFixed && candidate.Method.CurrencyCode != in.CurrencyCode {
		return false
	}
	return matchRules(candidate.ContextRules(), in.Context)
}

// campaignBudgetCurrencyMatches kampanyanın PARA ölçülü bütçesinin sepetin para
// birimiyle aynı olduğunu bildirir.
//
// Kampanyasız, bütçesiz ve ADET ölçülü bütçeli promosyonlar daima geçer: adet
// sayan bir bütçenin para birimi yoktur ve sepetinkiyle karşılaştırılamaz.
//
// Kontrolün BURADA da olması şarttır. Kullanım anında repository.Redeem aynı
// koşulu kampanya satırı kilitliyken zorlar ve uymayan kullanımı errors.Conflict
// ile reddeder. Eleme hesapta yapılmasaydı müşteri indirimi sepette görür,
// sipariş tamamlamada 409 alırdı — ya da saga tüm siparişi telafi ederdi. Kur
// çevirisi yapılmaz; gerekçesi [ComputeInput.CurrencyCode] godoc'undadır.
func campaignBudgetCurrencyMatches(candidate models.PromotionCandidate, currencyCode string) bool {
	campaign := candidate.Campaign
	if campaign == nil || campaign.BudgetType != models.BudgetSpend {
		return true
	}
	return campaign.BudgetCurrencyCode == currencyCode
}

// campaignUsable adayın kampanyasının verilen anda indirim sunmaya uygun
// olduğunu bildirir; kampanyasız promosyon daima uygundur.
//
// Kampanya kimliği dolu ama üstverisi yoksa kampanya SİLİNMİŞTİR; promosyon
// sahipsiz kalır ve hesaba katılmaz. Sessizce kampanyasız saymak, bütçesi ve
// tarihi olan bir indirimi sınırsız hâle getirirdi.
func campaignUsable(candidate models.PromotionCandidate, at time.Time) bool {
	if candidate.Promotion.CampaignID == nil {
		return true
	}
	if candidate.Campaign == nil {
		return false
	}
	return candidate.Campaign.WindowContains(at) && !candidate.Campaign.BudgetExhausted()
}

// unmatchedCodes uygulanabilir bir promosyona bağlanamayan kodları döner.
//
// Bir kod, sahibi promosyon ELEMEYİ GEÇTİYSE eşleşmiş sayılır — indirim
// üretmemiş olsa bile. Ayrım bilinçlidir: hedefine uyan kalemi olmayan geçerli
// bir kupon "geçersiz kod" değildir, yalnızca bu sepette işe yaramamıştır.
func unmatchedCodes(codes []string, eligible []models.PromotionCandidate) []string {
	if len(codes) == 0 {
		return []string{}
	}

	matched := make(map[string]struct{}, len(eligible))
	for i := range eligible {
		matched[eligible[i].Promotion.Code] = struct{}{}
	}

	out := make([]string, 0, len(codes))
	for _, code := range codes {
		if _, ok := matched[code]; !ok {
			out = append(out, code)
		}
	}
	return out
}

// applyPromotion tek bir promosyonu satırlara uygular ve FİİLEN uygulanan
// toplam indirimi döner.
//
// Satır durumlarını YERİNDE değiştirir; dönen değer, satır sınırlarına takılan
// kısım DÜŞÜLDÜKTEN sonraki gerçek toplamdır.
func applyPromotion(candidate models.PromotionCandidate, items, shipping []lineState) int64 {
	method := candidate.Method
	targets := selectTargets(candidate, items, shipping)
	if len(targets) == 0 {
		return 0
	}

	// Sipariş hedefi TEK bir toplamı kalemlere dağıtır; tahsis biçimi yazma
	// sırasında zaten "across"a zorlanır, buradaki zorlama elle yazılmış bir
	// kayda karşı ikinci savunmadır.
	allocation := method.Allocation
	if method.TargetType == models.TargetOrder {
		allocation = models.AllocationAcross
	}

	if allocation == models.AllocationEach {
		var applied int64
		for _, line := range targets {
			applied += line.charge(eachDiscount(*method, *line))
		}
		return applied
	}

	total := acrossTotal(*method, targets)
	lines := make([]allocLine, 0, len(targets))
	for _, line := range targets {
		lines = append(lines, allocLine{ID: line.id, Amount: line.amount})
	}

	var applied int64
	for i, share := range allocateAcross(total, lines) {
		applied += targets[i].charge(share)
	}
	return applied
}

// selectTargets promosyonun indirimini alacak satırları seçer.
//
// Hedef kuralları YALNIZCA "items" ve "shipping_methods" hedeflerinde
// uygulanır. "order" hedefi siparişin TAMAMINI indirir; orada bir alt küme
// süzmek, hedefin adıyla çelişirdi — bir alt küme isteniyorsa hedef "items"
// olmalıdır.
func selectTargets(candidate models.PromotionCandidate, items, shipping []lineState) []*lineState {
	switch candidate.Method.TargetType {
	case models.TargetItems:
		return filterLines(items, candidate.TargetRules())
	case models.TargetShippingMethods:
		return filterLines(shipping, candidate.TargetRules())
	case models.TargetOrder:
		return filterLines(items, nil)
	default:
		// Tanınmayan hedef indirim ÜRETMEZ: veritabanına sonradan sızmış bir
		// değer, indirimi rastgele bir satır kümesine uygulamamalıdır.
		return nil
	}
}

// filterLines hedef kurallarına uyan satırların işaretçilerini döner.
//
// Kuralsız süzgeç TÜM satırları seçer. İşaretçi dönmesi bilinçlidir: indirim
// satırın durumuna YAZILIR ve kopya üzerinde çalışmak yazmayı kaybederdi.
func filterLines(lines []lineState, rules []models.PromotionRule) []*lineState {
	out := make([]*lineState, 0, len(lines))
	for i := range lines {
		if len(rules) > 0 && !matchRules(rules, lines[i].attributes) {
			continue
		}
		out = append(out, &lines[i])
	}
	return out
}

// eachDiscount "each" tahsisinde tek bir satırın HAM indirimini hesaplar.
//
// Ham demek, satırın kalan tutarına henüz kırpılmamış demektir; kırpma
// [lineState.charge] içindedir.
//
// Sabit tutarda indirim satırın HER BİRİMİNE uygulanır ve
// [models.ApplicationMethod.MaxQuantity] birim sayısını sınırlar. Yüzdede
// MaxQuantity YOK SAYILIR; gerekçe o alanın godoc'undadır.
func eachDiscount(method models.ApplicationMethod, line lineState) int64 {
	switch method.Type {
	case models.MethodFixed:
		units := line.quantity
		if method.MaxQuantity != nil && *method.MaxQuantity < units {
			units = *method.MaxQuantity
		}
		if units <= 0 {
			return 0
		}
		return method.Value * units
	case models.MethodPercentage:
		return percentageOf(line.amount, method.Value)
	default:
		return 0
	}
}

// acrossTotal "across" tahsisinde dağıtılacak TOPLAM indirimi hesaplar.
//
// Yüzde, hedeflerin tutar toplamı üzerinden BİR KEZ hesaplanır: satır başına
// hesaplayıp toplamak, her satırda bir kuruşa kadar aşağı yuvarlar ve
// promosyonun vaat ettiğinden gözle görülür biçimde az verirdi.
//
// Taban, hedeflerin ORİJİNAL tutarlarının toplamıdır — KALAN tutarları DEĞİL.
//
// Ayrım bir kuruş meselesi değildir. Kalana kırpmak, önceki bir promosyonun
// doldurduğu satırı İKİ KEZ cezalandırırdı: dağıtılacak havuz o satırın kalanı
// kadar küçülür, ama [allocateAcross] payları yine satırların ORİJİNAL tutarına
// göre dağıttığı için dolu satır payını almaya devam eder ve o pay
// [lineState.charge] içinde kırpılıp KAYBOLUR. Boş satıra vaat edilenin yarısı
// verilirdi ve bu, [Service.ComputeDiscounts] godoc'undaki "yüzdeler birbirinin
// üzerine BİNMEZ" kararını arka kapıdan bileşik hesaba çevirirdi.
//
// Sonuç burada AYRICA kırpılmaz: bir tahsisin dağıttığı tabandan fazlasını
// dağıtamayacağı kuralının tek sahibi [allocateAcross]'tır ve satır sınırını
// [lineState.charge] korur. Aynı kırpmayı burada tekrarlamak, hiçbir davranışı
// değiştirmeyen — dolayısıyla hiçbir testin koruyamayacağı — bir satır bırakırdı.
func acrossTotal(method models.ApplicationMethod, targets []*lineState) int64 {
	var base int64
	for _, line := range targets {
		base += line.amount
	}

	switch method.Type {
	case models.MethodFixed:
		return method.Value
	case models.MethodPercentage:
		return percentageOf(base, method.Value)
	default:
		return 0
	}
}

// percentageOf bir tutarın baz puan karşılığını AŞAĞI yuvarlayarak döner.
//
// Çarpım int64'e sığar: tutar en fazla [models.MaxAmount] (10^12), baz puan en
// fazla [models.BasisPointDenominator] (10^4) olduğu için ara sonuç 10^16'yı
// aşmaz. Yuvarlama yönünün gerekçesi [models.BasisPointDenominator]
// godoc'undadır.
func percentageOf(amount, basisPoints int64) int64 {
	if amount <= 0 || basisPoints <= 0 {
		return 0
	}
	if amount > models.MaxAmount {
		amount = models.MaxAmount
	}
	if basisPoints > models.BasisPointDenominator {
		basisPoints = models.BasisPointDenominator
	}
	return amount * basisPoints / models.BasisPointDenominator
}

// Hata ayrıntısındaki indeks anahtarları.
const (
	// detailItemIndex kaçıncı KALEMİN reddedildiğini bildirir.
	detailItemIndex = "item_index"
	// detailShippingIndex kaçıncı KARGO YÖNTEMİNİN reddedildiğini bildirir.
	detailShippingIndex = "shipping_index"
	// detailCodeIndex kaçıncı KUPON KODUNUN reddedildiğini bildirir.
	detailCodeIndex = "code_index"
)

// withIndex bir doğrulama hatasına kaçıncı girdide oluştuğunu ekler.
//
// Toplu bir hesapta hangi satırın reddedildiğini bilmek, hatayı kullanılabilir
// kılan tek bilgidir. Anahtar çağırandan gelir çünkü kalem, kargo ve kod
// listeleri AYRIDIR ve tek bir "index" anahtarı hangisinin kastedildiğini
// söylemezdi.
func withIndex(err error, key string, index int) error {
	var typed *errors.Error
	if errors.As(err, &typed) && typed != nil {
		return typed.WithDetails(map[string]any{key: index})
	}
	return err
}

// storeCouponVisible bir promosyonun MÜŞTERİYE gösterilebilir olduğunu bildirir.
//
// Yönetim yüzeyinden farkı budur: taslak ve pasif promosyonlar, penceresi
// kapanmış ya da bütçesi tükenmiş kampanyalar ve kullanım hakkı bitmiş kuponlar
// müşteriye "yok" görünür.
//
// Ayrıca KURAL KOŞULLARI hiçbir zaman dışarı çıkmaz: bir kuralın sağ tarafı
// (örn. bir müşteri grubunun kimliği) iş bilgisidir. Bu yüzden kurallar burada
// DEĞERLENDİRİLMEZ de: sepet bağlamı olmadan değerlendirilemezler ve
// "koşullarını sağlamıyorsun" cevabı koşulun varlığını ele verirdi. Kuponun o
// sepette gerçekten indirim üretip üretmediğini [Service.ComputeDiscounts]
// söyler.
func storeCouponVisible(candidate models.PromotionCandidate, at time.Time) bool {
	promo := candidate.Promotion
	if promo.Status != models.PromotionActive || promo.Type != models.PromotionStandard {
		return false
	}
	if promo.UsageExhausted() {
		return false
	}
	return campaignUsable(candidate, at)
}

// StoreCoupon müşteriye gösterilebilen kupon bilgisidir.
//
// Bilinçli olarak DARDIR: durum, kullanım sayacı, kampanya bütçesi, üstveri ve
// kural koşulları BULUNMAZ. Müşterinin görmesi gereken tek şey kuponun geçerli
// olduğu ve ne tür bir indirim verdiğidir.
type StoreCoupon struct {
	// Code kupon kodudur (BÜYÜK harf).
	Code string
	// MethodType indirimin ölçüsüdür (fixed | percentage).
	MethodType models.ApplicationMethodType
	// TargetType indirimin hedefidir (items | shipping_methods | order).
	TargetType models.ApplicationTargetType
	// Value sabit tutar (minor unit) ya da baz puandır.
	Value int64
	// CurrencyCode sabit tutarlı indirimin para birimidir; yüzdede boştur.
	CurrencyCode string
}

// LookupStoreCoupon bir kupon kodunun MÜŞTERİYE gösterilebilir bilgisini döner.
//
// Kod yoksa, promosyon taslak/pasif ise, kampanyasının penceresi kapalıysa,
// bütçesi tükenmişse ya da kullanım hakkı bittiyse AYNI hata döner:
// errors.NotFound (kod: [CodePromotionNotUsable]). Ayrım yapılmaması
// bilinçlidir — "bu kod var ama kampanyası henüz başlamadı" cevabı,
// yayınlanmamış bir kampanyanın varlığını ele verir ve kod tahmin eden birine
// bir kampanya takvimi çıkarma imkânı tanırdı.
func (s *Service) LookupStoreCoupon(ctx context.Context, code string) (StoreCoupon, error) {
	if err := s.ready(); err != nil {
		return StoreCoupon{}, err
	}
	normalized, err := normalizeCode(code)
	if err != nil {
		// Biçimsel olarak geçersiz bir kod da "yok" sayılır: kodun biçimini
		// doğrulayan bir hata, geçerli biçimleri deneyerek arama alanını
		// daraltmaya yarardı.
		return StoreCoupon{}, notUsable(code)
	}

	candidate, err := s.storeCandidate(ctx, normalized)
	if err != nil {
		if errors.IsNotFound(err) {
			return StoreCoupon{}, notUsable(normalized)
		}
		// Altyapı arızası "kupon yok"a çevrilmez: müşteriye geçici bir hatayı
		// kalıcı bir cevap gibi göstermek, geçerli bir kuponu sessizce
		// reddetmek olurdu.
		return StoreCoupon{}, err
	}

	if !storeCouponVisible(candidate, s.clock()) || candidate.Method == nil {
		return StoreCoupon{}, notUsable(normalized)
	}

	return StoreCoupon{
		Code:         candidate.Promotion.Code,
		MethodType:   candidate.Method.Type,
		TargetType:   candidate.Method.TargetType,
		Value:        candidate.Method.Value,
		CurrencyCode: candidate.Method.CurrencyCode,
	}, nil
}

// storeCandidate kupon doğrulaması için adayı SÜZGEÇSİZ okur.
//
// [Service.ComputeDiscounts]'un kullandığı aday listesi yalnızca AKTİF
// promosyonları döner. Bu yüzey ondan okusaydı "müşteriye ne görünür" kuralı
// İKİ yere bölünürdü — biri [storeCouponVisible], biri o sorgunun WHERE'i — ve
// buradaki kontrol sessizce ölü kalırdı: durum süzgecini kaldıran bir
// değişiklik hiçbir testi düşürmezdi.
//
// Aday bu yüzden süzgeçsiz kurulur ve görünürlük kararının TEK sahibi
// [storeCouponVisible] olur. Bedeli üç sorgudur; bu, müşterinin kupon
// yazdığında yaptığı TEK bir işlemdir ve sepet hesabı gibi her turda
// koşmaz.
//
// Uygulama yöntemi ya da kampanya bulunamazsa hata DEĞİL, eksik alan dönülür:
// ikisi de "kupon kullanılamaz" anlamına gelir ve kararı [storeCouponVisible]
// verir.
func (s *Service) storeCandidate(ctx context.Context, code string) (models.PromotionCandidate, error) {
	promo, err := s.repo.GetPromotionByCode(ctx, code)
	if err != nil {
		return models.PromotionCandidate{}, err
	}
	candidate := models.PromotionCandidate{Promotion: promo}

	method, err := s.repo.GetApplicationMethod(ctx, promo.ID)
	switch {
	case err == nil:
		candidate.Method = &method
	case !errors.IsNotFound(err):
		return models.PromotionCandidate{}, err
	}

	if promo.CampaignID != nil {
		campaign, campErr := s.repo.GetCampaign(ctx, *promo.CampaignID)
		switch {
		case campErr == nil:
			candidate.Campaign = &campaign
		case !errors.IsNotFound(campErr):
			return models.PromotionCandidate{}, campErr
		}
	}
	return candidate, nil
}

// notUsable müşteriye dönen tek biçimli "kupon yok" hatasını üretir.
//
// Mesaj kodun kendisini tekrar eder ama BAŞKA hiçbir şey söylemez; ayrımsız
// olmasının gerekçesi [Service.LookupStoreCoupon] godoc'undadır.
func notUsable(code string) error {
	return errors.NotFound(CodePromotionNotUsable,
		"kupon kullanılabilir değil: %s", strings.TrimSpace(code))
}
