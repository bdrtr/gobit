// Package models promotion modülünün domain modellerini tanımlar.
//
// Buradaki tipler veritabanı tiplerinden ARINDIRILMIŞTIR: pgtype bu pakete
// girmez, dönüşüm repository sarmalayıcısında yapılır. Böylece servis ve API
// katmanları depolama ayrıntısına bağlanmaz.
//
// Para daima TAM SAYI minor unit'tir (kuruş/cent) ve para birimi ayrı alanda
// durur (plan Bölüm 8); float hiçbir yerde kullanılmaz. ORANLAR baz puandır
// (2000 = %20) ve yuvarlama yönü [BasisPointDenominator] yanında belgelidir.
// Zamanlar UTC'dir.
package models

import "time"

// Tutar, oran ve adet sınırları.
//
// Sınırlar keyfi değildir, TAŞMA korumasıdır. En büyük ara çarpım
// MaxAmount × BasisPointDenominator = 10^12 × 10^4 = 10^16'dır ve int64'ün
// üst sınırı olan 9.22×10^18'in çok altındadır; yüzde hesabı bu yüzden
// yapısal olarak taşamaz. Aynı sınırlar migration'daki CHECK kısıtlarında da
// tekrarlanır: servis doğrulaması atlansa bile veritabanı ikinci kapıdır.
const (
	// MinAmount izin verilen en küçük tutardır.
	MinAmount int64 = 0
	// MaxAmount izin verilen en büyük tutardır (minor unit). Hem tek bir
	// kalemin tutarı hem de bir hesabın ara toplamı bu sınırla bağlıdır.
	MaxAmount int64 = 1_000_000_000_000
	// MinQuantity bir kalemin en küçük adedidir.
	MinQuantity int64 = 1
	// MaxQuantity bir kalemin en büyük adedidir.
	MaxQuantity int64 = 1_000_000
	// BasisPointDenominator baz puan paydasıdır: 10000 baz puan = %100.
	//
	// # Yuvarlama yönü
	//
	// Yüzde indirim `tutar * bps / BasisPointDenominator` biçiminde TAM SAYI
	// aritmetiğiyle hesaplanır ve Go'nun tam sayı bölmesi sıfıra doğru kırptığı
	// için sonuç AŞAĞI yuvarlanır. Yön bilinçlidir:
	//
	//   - İndirim, vaat edilen yüzdeyi hiçbir zaman AŞMAZ. Yukarı yuvarlama,
	//     "%20 indirim" yazan bir kampanyanın kuruş düzeyinde %20'den fazlasını
	//     vermesi demek olurdu ve kampanya bütçesi tam olarak vaat edilenle
	//     sınırlı kalmazdı.
	//   - Hata satır başına en fazla BİR minor unit'tir.
	//   - "across" tahsisinde toplam BİR KEZ yuvarlanır ve kuruş artığı
	//     kalemlere dağıtılır (bkz. service paketindeki tahsis kuralı), yani
	//     sepet düzeyinde kayıp yine en fazla bir minor unit'tir.
	BasisPointDenominator int64 = 10_000
)

// CampaignBudgetType bir kampanya bütçesinin ölçü birimidir.
type CampaignBudgetType string

// Kampanya bütçe türleri.
const (
	// BudgetNone bütçesiz kampanyadır; kullanım sınırsızdır.
	BudgetNone CampaignBudgetType = "none"
	// BudgetSpend bütçeyi PARA olarak ölçer; her kullanım, kullanılan indirim
	// tutarı kadar bütçe tüketir ve bütçenin para birimi zorunludur.
	BudgetSpend CampaignBudgetType = "spend"
	// BudgetUsage bütçeyi ADET olarak ölçer; her kullanım bütçeyi bir tüketir.
	BudgetUsage CampaignBudgetType = "usage"
)

// Valid türün tanımlı olup olmadığını bildirir.
func (t CampaignBudgetType) Valid() bool {
	return t == BudgetNone || t == BudgetSpend || t == BudgetUsage
}

// Campaign promosyonların kabıdır: ortak bir tarih penceresi ve ortak bir bütçe
// taşır.
//
// Kap, bir promosyonun kendi durumunun YERİNE geçmez: bir promosyon hem kendi
// durumu hem kampanyasının penceresi/bütçesi uygunken uygulanır.
type Campaign struct {
	// ID "camp_" önekli, zaman sıralı kimliktir.
	ID string
	// Name kampanyanın görünen adıdır; boş olamaz.
	Name string
	// CampaignIdentifier operatörün verdiği BENZERSİZ iş kimliğidir (örn.
	// "BLACKFRIDAY-2026"). Kimlikten ayrıdır: dış sistemler kampanyayı bu adla
	// tanır ve ad, kimliğin aksine okunabilir olmalıdır.
	CampaignIdentifier string
	// Description isteğe bağlı açıklamadır.
	Description string
	// StartsAt geçerlilik penceresinin başıdır; nil ise alt sınır yoktur.
	StartsAt *time.Time
	// EndsAt geçerlilik penceresinin sonudur; nil ise üst sınır yoktur.
	EndsAt *time.Time
	// BudgetType bütçenin ölçü birimidir.
	BudgetType CampaignBudgetType
	// BudgetLimit bütçenin üst sınırıdır; nil ise sınır yoktur. Birimi
	// [BudgetType]'a göre minor unit ya da adettir.
	BudgetLimit *int64
	// BudgetUsed bütçenin TÜKETİLEN kısmıdır; [BudgetLimit] ile aynı birimdedir.
	BudgetUsed int64
	// BudgetCurrencyCode "spend" bütçesinin para birimidir (ISO 4217, BÜYÜK
	// harf); diğer türlerde boştur.
	BudgetCurrencyCode string
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// WindowContains verilen anın kampanyanın penceresinde olup olmadığını bildirir.
//
// Pencere uçları KAPSAYICIDIR (nil uç = sınırsız): bir kampanyanın bitiş anında
// hâlâ geçerli olması, saniye hassasiyetinde bir sınırın müşteriye "kampanya
// bitti" demesinden yeğdir.
func (c Campaign) WindowContains(at time.Time) bool {
	if c.StartsAt != nil && at.Before(*c.StartsAt) {
		return false
	}
	if c.EndsAt != nil && at.After(*c.EndsAt) {
		return false
	}
	return true
}

// BudgetExhausted bütçenin TÜKENMİŞ olup olmadığını bildirir.
//
// Sınırsız bütçe (limit nil ya da tür "none") asla tükenmez. Sınır varsa
// tükenme koşulu `kullanılan >= sınır`dır: sınıra TAM oturmuş bir bütçede
// yeni bir kullanım sınırı aşardı.
func (c Campaign) BudgetExhausted() bool {
	if c.BudgetType == BudgetNone || c.BudgetLimit == nil {
		return false
	}
	return c.BudgetUsed >= *c.BudgetLimit
}

// BudgetDeltaFor bir kullanımın bütçeden ne kadar tüketeceğini bildirir.
//
// Birim [BudgetType]'a bağlıdır: "spend" bütçesi PARA tüketir (uygulanan
// indirim tutarı kadar), "usage" bütçesi ADET tüketir (kullanım başına bir),
// bütçesiz kampanya hiçbir şey tüketmez.
//
// Kural burada, domain'de durur; kullanım akışı onu yalnızca ÇAĞIRIR. Repo
// katmanında kopyalansaydı, bütçe türü eklendiğinde iki yerin ayrışması
// sessiz bir muhasebe hatası olurdu.
func (c Campaign) BudgetDeltaFor(amount int64) int64 {
	switch c.BudgetType {
	case BudgetSpend:
		return amount
	case BudgetUsage:
		return 1
	case BudgetNone:
		return 0
	default:
		// Tanınmayan bir tür bütçe TÜKETMEZ. Alternatif (tutarı düşmek) bilinmeyen
		// birimde bir sayacı bozardı; tüketmemek, bütçenin fazla harcanmasına
		// değil yalnızca eksik sayılmasına yol açar ve durum yönetim yüzeyinde
		// görünür kalır.
		return 0
	}
}

// PromotionType bir promosyonun mekaniğidir.
type PromotionType string

// Promosyon türleri.
const (
	// PromotionStandard doğrudan indirim uygulayan promosyondur.
	PromotionStandard PromotionType = "standard"
	// PromotionBuyGet "N al M öde" mekaniğidir.
	//
	// # Bu fazda ETKİNLEŞTİRİLEMEZ
	//
	// Mekanik, "hangi kalemler ALIŞ koşulunu sağlıyor" ve "indirim hangi
	// kalemlerin kaç ADEDİNE uygulanacak" sorularını gerektirir; ikincisi
	// kalemin BİRİM fiyatını ister ve [ComputeInput]'un taşıdığı satır tutarı
	// (birim × adet) bölünmeden birim fiyata çevrilemez — bölme, adede tam
	// bölünmeyen bir satırda sessiz bir yuvarlama hatası üretirdi.
	//
	// Eksiği SESSİZ bırakmamak için tür YAPISAL olarak kapatılmıştır: buyget
	// bir promosyon oluşturulabilir ama "active" duruma alınamaz (bkz. servis
	// doğrulaması) ve hesaplama da güvenlik ağı olarak onu atlar. Böylece
	// "kurulmuş ama hiçbir şey yapmayan aktif promosyon" durumu oluşamaz.
	PromotionBuyGet PromotionType = "buyget"
)

// Valid türün tanımlı olup olmadığını bildirir.
func (t PromotionType) Valid() bool {
	return t == PromotionStandard || t == PromotionBuyGet
}

// PromotionStatus bir promosyonun yayın durumudur.
type PromotionStatus string

// Promosyon durumları.
const (
	// PromotionDraft henüz yayına alınmamış promosyondur; hesaba KATILMAZ ve
	// müşteri yüzeyinde GÖRÜNMEZ.
	PromotionDraft PromotionStatus = "draft"
	// PromotionActive yayındaki promosyondur.
	PromotionActive PromotionStatus = "active"
	// PromotionInactive elle durdurulmuş promosyondur; hesaba KATILMAZ.
	PromotionInactive PromotionStatus = "inactive"
)

// Valid durumun tanımlı olup olmadığını bildirir.
func (s PromotionStatus) Valid() bool {
	return s == PromotionDraft || s == PromotionActive || s == PromotionInactive
}

// Promotion tek bir indirim tanımıdır.
//
// Kupon kodu ([Code]) ile ya da kodsuz ([IsAutomatic]) uygulanır. İkisi birden
// olabilir: otomatik bir promosyonun da kodu vardır, çünkü kod aynı zamanda
// operatörün promosyonu andığı addır ve benzersizdir.
type Promotion struct {
	// ID "promo_" önekli kimliktir.
	ID string
	// Code kupon kodudur; BENZERSİZDİR ve daima BÜYÜK harf saklanır.
	Code string
	// IsAutomatic promosyonun kod girilmeden uygulanıp uygulanmadığını bildirir.
	IsAutomatic bool
	// Type promosyonun mekaniğidir.
	Type PromotionType
	// CampaignID promosyonu bir kampanyaya bağlar; nil ise promosyon
	// kampanyasızdır ve yalnızca kendi kurallarıyla sınırlıdır.
	CampaignID *string
	// Status promosyonun yayın durumudur.
	Status PromotionStatus
	// UsageLimit promosyonun kaç kez kullanılabileceğidir; nil ise sınırsız.
	UsageLimit *int64
	// UsageCount promosyonun KULLANILMIŞ sayısıdır (bkz. [Redemption]).
	UsageCount int64
	// Metadata operatörün serbest anahtar/değer notudur; iş kuralına girmez.
	Metadata map[string]string
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// UsageExhausted kullanım sınırının dolup dolmadığını bildirir.
//
// Sınırsız promosyon asla tükenmez. Sınır varsa tükenme koşulu
// `kullanılan >= sınır`dır.
func (p Promotion) UsageExhausted() bool {
	if p.UsageLimit == nil {
		return false
	}
	return p.UsageCount >= *p.UsageLimit
}

// ApplicationMethodType indirimin nasıl ölçüldüğünü bildirir.
type ApplicationMethodType string

// Uygulama yöntemi türleri.
const (
	// MethodFixed SABİT TUTAR indirimidir; değeri minor unit'tir ve para birimi
	// ZORUNLUDUR.
	MethodFixed ApplicationMethodType = "fixed"
	// MethodPercentage YÜZDE indirimidir; değeri BAZ PUANDIR (2000 = %20) ve
	// para birimi taşımaz.
	MethodPercentage ApplicationMethodType = "percentage"
)

// Valid türün tanımlı olup olmadığını bildirir.
func (t ApplicationMethodType) Valid() bool {
	return t == MethodFixed || t == MethodPercentage
}

// ApplicationTargetType indirimin NEYE uygulanacağını bildirir.
type ApplicationTargetType string

// Uygulama hedefleri.
const (
	// TargetItems indirimi sepet KALEMLERİNE uygular.
	TargetItems ApplicationTargetType = "items"
	// TargetShippingMethods indirimi KARGO yöntemlerine uygular.
	TargetShippingMethods ApplicationTargetType = "shipping_methods"
	// TargetOrder indirimi SİPARİŞİN tamamına uygular; sonuç yine kalemlere
	// tahsis edilir (bkz. service paketindeki tahsis kuralı), çünkü sepet
	// toplamı satır başına indirim bekler.
	TargetOrder ApplicationTargetType = "order"
)

// Valid hedefin tanımlı olup olmadığını bildirir.
func (t ApplicationTargetType) Valid() bool {
	return t == TargetItems || t == TargetShippingMethods || t == TargetOrder
}

// Allocation indirimin hedef satırlar arasında nasıl dağıtılacağını bildirir.
type Allocation string

// Tahsis biçimleri.
const (
	// AllocationEach indirimi HER hedef satıra AYRI AYRI uygular: yüzde her
	// satırın kendi tutarına, sabit tutar her satırın her adedine işler.
	AllocationEach Allocation = "each"
	// AllocationAcross indirimi TEK bir tutar olarak hesaplayıp hedef satırlara
	// tutarlarıyla ORANTILI dağıtır.
	AllocationAcross Allocation = "across"
)

// Valid tahsis biçiminin tanımlı olup olmadığını bildirir.
func (a Allocation) Valid() bool {
	return a == AllocationEach || a == AllocationAcross
}

// ApplicationMethod bir promosyonun indirimi NASIL uygulayacağıdır.
//
// Bir promosyonun EN FAZLA BİR uygulama yöntemi vardır; yöntemsiz bir promosyon
// hiçbir indirim üretmez ve hesapta atlanır.
type ApplicationMethod struct {
	// ID "appm_" önekli kimliktir.
	ID string
	// PromotionID yöntemin bağlı olduğu promosyondur.
	PromotionID string
	// Type indirimin ölçüsüdür (fixed | percentage).
	Type ApplicationMethodType
	// TargetType indirimin hedefidir (items | shipping_methods | order).
	TargetType ApplicationTargetType
	// Allocation dağıtım biçimidir (each | across).
	Allocation Allocation
	// Value sabit tutar (minor unit) ya da baz puandır ([Type]'a göre).
	Value int64
	// MaxQuantity sabit tutarın uygulanacağı azami ADETTİR ve YALNIZCA
	// "fixed" + "each" bileşiminde anlamlıdır; nil ise sınır yoktur.
	//
	// Diğer bileşimlerde YOK SAYILIR. Sebep: yüzde indirim satırın TUTARINA
	// işler ve adedi sınırlamak, satır tutarını birim fiyata bölmeyi
	// gerektirirdi — adede tam bölünmeyen bir satırda bu sessiz bir yuvarlama
	// hatası olurdu. "across"ta ise dağıtılan tek bir toplam vardır ve adet
	// kavramı zaten devre dışıdır.
	MaxQuantity *int64
	// CurrencyCode "fixed" indirimin para birimidir (ISO 4217, BÜYÜK harf);
	// "percentage"ta boştur.
	CurrencyCode string
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
}

// RuleOperator bir promosyon kuralının karşılaştırma işlecidir.
type RuleOperator string

// Desteklenen işleçler.
//
// eq/ne/in/nin DİZGE karşılaştırmasıdır; gt/gte/lt/lte ise iki tarafı da tam
// sayıya çevirip SAYISAL karşılaştırır. Sayıya çevrilemeyen bir bağlam değeri
// kuralı EŞLEŞMEZ yapar, hata üretmez: bağlam dışarıdan gelir ve tek bir bozuk
// alan tüm indirim hesabını düşürmemelidir.
//
// pricing modülündeki PriceRule ile AYNI kavramdır. O paket import EDİLEMEZ
// (Prensip 2.4 / ADR 0001) ve tip burada yeniden tanımlanır; bu, izolasyonun
// ADR 0001'de açıkça kabul edilmiş bedelidir.
const (
	// OpEq değerin kuralın tek değerine eşit olmasını ister.
	OpEq RuleOperator = "eq"
	// OpNe değerin kuralın tek değerinden farklı olmasını ister.
	OpNe RuleOperator = "ne"
	// OpIn değerin kural kümesinde bulunmasını ister.
	OpIn RuleOperator = "in"
	// OpNin değerin kural kümesinde BULUNMAMASINI ister.
	OpNin RuleOperator = "nin"
	// OpGt sayısal olarak büyüklük ister.
	OpGt RuleOperator = "gt"
	// OpGte sayısal olarak büyük veya eşitlik ister.
	OpGte RuleOperator = "gte"
	// OpLt sayısal olarak küçüklük ister.
	OpLt RuleOperator = "lt"
	// OpLte sayısal olarak küçük veya eşitlik ister.
	OpLte RuleOperator = "lte"
)

// Valid işlecin tanımlı olup olmadığını bildirir.
func (o RuleOperator) Valid() bool {
	switch o {
	case OpEq, OpNe, OpIn, OpNin, OpGt, OpGte, OpLt, OpLte:
		return true
	default:
		return false
	}
}

// Numeric işlecin sayısal karşılaştırma yapıp yapmadığını bildirir.
func (o RuleOperator) Numeric() bool {
	switch o {
	case OpGt, OpGte, OpLt, OpLte:
		return true
	case OpEq, OpNe, OpIn, OpNin:
		return false
	default:
		return false
	}
}

// MultiValue işlecin birden çok değer alıp alamayacağını bildirir.
// Diğer tüm işleçler TEK değer ister.
func (o RuleOperator) MultiValue() bool {
	return o == OpIn || o == OpNin
}

// RuleType bir kuralın NEYE bakacağını bildirir.
type RuleType string

// Kural türleri.
const (
	// RuleContext SEPET BAĞLAMINA bakar (para birimi, bölge, müşteri grubu …).
	// Bağlam kuralı sağlanmazsa promosyonun tamamı uygulanmaz.
	RuleContext RuleType = "context"
	// RuleTarget KALEM özniteliklerine bakar ve indirimin hangi kalemlere
	// ineceğini süzer. Hedefi "items" ya da "order" olan promosyonlarda
	// anlamlıdır; kargo hedefinde kargo yönteminin öznitelikleri süzülür.
	RuleTarget RuleType = "target"
)

// Valid kural türünün tanımlı olup olmadığını bildirir.
func (t RuleType) Valid() bool {
	return t == RuleContext || t == RuleTarget
}

// PromotionRule bir promosyonun uygulanma koşuludur.
//
// Örnek: {RuleType: RuleContext, Attribute: "customer_group_id",
// Operator: OpIn, Values: []string{"vip", "b2b"}}.
type PromotionRule struct {
	// ID "prule_" önekli kimliktir.
	ID string
	// PromotionID kuralın bağlı olduğu promosyondur.
	PromotionID string
	// RuleType kuralın neye baktığıdır.
	RuleType RuleType
	// Attribute bağlamda ya da kalemde bakılacak alan adıdır.
	Attribute string
	// Operator karşılaştırma işlecidir.
	Operator RuleOperator
	// Values karşılaştırmanın sağ tarafıdır; en az bir eleman içerir.
	Values []string
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
}

// Redemption bir promosyonun TEK bir referans için kullanıldığının kaydıdır.
//
// Sayacın kendisi [Promotion.UsageCount] ve [Campaign.BudgetUsed] sütunlarıdır;
// bu kayıt onların DEFTERİDİR ve iki şeyi mümkün kılar:
//
//   - İdempotency: aynı referans için ikinci kullanım sayacı ikinci kez
//     artırmaz (bkz. servis katmanındaki RedeemPromotion).
//   - Geri alma: serbest bırakma, sayaca ne kadar eklendiğini TAHMİN ETMEZ;
//     eklenen değer [BudgetDelta] alanında saklıdır ve aynen düşülür. Kampanya
//     bütçesinin türü arada değişse bile defter tutarlı kalır.
type Redemption struct {
	// ID "predeem_" önekli kimliktir.
	ID string
	// PromotionID kullanılan promosyondur.
	PromotionID string
	// CampaignID kullanım anında promosyonun bağlı olduğu kampanyadır; nil ise
	// promosyon kampanyasızdı.
	CampaignID *string
	// Reference kullanımın hangi iş kaydına ait olduğudur (örn. sipariş
	// kimliği). SERBEST metindir ve foreign key DEĞİLDİR (Prensip 2.2).
	Reference string
	// Amount kullanımda fiilen uygulanan indirim tutarıdır (minor unit).
	Amount int64
	// CurrencyCode indirimin para birimidir (ISO 4217, BÜYÜK harf).
	CurrencyCode string
	// BudgetDelta kampanya bütçesine EKLENEN değerdir; serbest bırakmada aynen
	// düşülür. Kampanyasız ya da bütçesiz promosyonda sıfırdır.
	BudgetDelta int64
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// ReleasedAt serbest bırakılma anıdır; nil ise kullanım hâlâ geçerlidir.
	ReleasedAt *time.Time
}

// Released kullanımın serbest bırakılmış olup olmadığını bildirir.
func (r Redemption) Released() bool { return r.ReleasedAt != nil }

// PromotionCandidate hesaplamaya giren tek bir promosyon ve bağlamıdır.
//
// Promosyonun kendisi tek başına yetmez: indirimin ölçüsü [ApplicationMethod]'da,
// uygulanma koşulları [PromotionRule]'da, tarih penceresi ve bütçe ise
// [Campaign]'dedir. Dördü BİRLİKTE taşınır ki hesaplama, aday başına ek sorgu
// yapmak zorunda kalmasın (N+1 yoktur).
type PromotionCandidate struct {
	// Promotion promosyonun kendisidir.
	Promotion Promotion
	// Campaign promosyonun kampanyasıdır; nil ise promosyon kampanyasızdır.
	//
	// Promotion.CampaignID dolu ama bu alan nil ise kampanya SİLİNMİŞTİR ve
	// promosyon hesaba katılmaz (bkz. servis katmanındaki eleme kuralı).
	Campaign *Campaign
	// Method indirimin uygulama yöntemidir; nil ise promosyon indirim üretmez.
	Method *ApplicationMethod
	// Rules promosyonun TÜM kurallarıdır (bağlam ve hedef birlikte).
	Rules []PromotionRule
}

// ContextRules adayın BAĞLAM kurallarını döner.
func (c PromotionCandidate) ContextRules() []PromotionRule {
	return c.rulesOfType(RuleContext)
}

// TargetRules adayın HEDEF kurallarını döner.
func (c PromotionCandidate) TargetRules() []PromotionRule {
	return c.rulesOfType(RuleTarget)
}

// rulesOfType verilen türdeki kuralları süzer.
func (c PromotionCandidate) rulesOfType(t RuleType) []PromotionRule {
	out := make([]PromotionRule, 0, len(c.Rules))
	for i := range c.Rules {
		if c.Rules[i].RuleType == t {
			out = append(out, c.Rules[i])
		}
	}
	return out
}
