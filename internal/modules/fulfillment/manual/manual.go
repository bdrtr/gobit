// Package manual gerçek bir ağ çağrısı yapmayan test/manuel kargo
// sağlayıcısıdır (plan Faz 7).
//
// [Provider], internal/core/provider'daki FulfillmentProvider sözleşmesini
// karşılar ve o sözleşmenin godoc'unda yazılı şartları yerine getirir:
//
//   - [Provider.Quote] YAN ETKİSİZDİR: hiçbir şey yazmaz, defteri okumaz ve
//     aynı girdi için daima aynı ücreti döner. Sepet toplamı hesaplanırken
//     defalarca çağrılabilir.
//   - Aynı IdempotencyKey ile ikinci [Provider.Create] YENİ gönderi açmaz,
//     mevcut gönderiyi döner.
//   - [Provider.Cancel] saga telafisidir ve İDEMPOTENTTİR: iki kez iptal
//     edilen bir gönderi ikinci çağrıda hata vermez.
//
// # Durum neden VERİTABANINDA tutulur
//
// Karar payment modülündeki manuel sağlayıcıyla AYNIDIR ve aynı gerekçelere
// dayanır. Bellekte tutulan bir defter, sürecin her yeniden başlatılışında
// sıfırlanırdı; bedeli üç yerde ödenirdi:
//
//   - e2e akışları (internal/e2e) ve Faz 9 yük testi, süreç yeniden
//     başladığında AÇILMIŞ bir gönderiyi bulabilmelidir; aksi hâlde kargo
//     adımı "gönderi bulunamadı" ile düşer.
//   - Saga telafisi tam da sürecin düştüğü senaryoda çalışmalıdır. Belleğe
//     dayanan bir sağlayıcıda Cancel, yeniden başlatma sonrası hiçbir zaman
//     çalışamaz ve basılmış bir kargo etiketi sonsuza kadar açık kalırdı.
//   - Birden çok süreç (ya da yatay ölçek) aynı gönderiyi görmezdi; sağlayıcı
//     yalnızca tek örnekli çalışan bir sunucuda doğru davranırdı.
//
// Gerçek bir kargo firmasının durumu da kendi sistemindedir ve süreç yeniden
// başlatmalarından etkilenmez; taklit bu yüzden kalıcı olmalıdır.
//
// [Provider.Quote] BU KURALIN DIŞINDADIR ve hiçbir şey saklamaz — saklasaydı
// yan etkisiz olmazdı. Fiyat, seçeneğin yapılandırmasından ve sepet
// bağlamından SAF olarak hesaplanır.
//
// # Defterin ayrılığı
//
// Sağlayıcının durumu fulfillment_manual_shipments tablosundadır ve
// fulfillment servisinin tablolarından AYRIDIR. Servis bu tabloya hiç
// dokunmaz; sağlayıcıya yalnızca FulfillmentProvider arayüzünden ulaşır.
// Ayrım, modülün kazara sağlayıcının iç durumunu okumasını yapısal olarak
// engeller — gerçek bir sağlayıcıda da böyle bir okuma mümkün değildir.
//
// # Test için başarısızlık enjeksiyonu
//
// Saga testleri kargo adımını PATLATABİLMELİDİR. Davranış, kargo seçeneğinin
// yapılandırmasından ([coreprovider.QuoteInput.Data]) ve gönderi açılırken
// verilen Data alanından okunur; gönderi davranışı gönderiyle birlikte kalıcı
// olarak saklanır, böylece süreç yeniden başlasa da aynı gönderi aynı biçimde
// davranır. Bkz. [DataKeyOutcome], [DataKeyQuoteAmount] ve fiyat anahtarları.
package manual

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// ID sağlayıcının kimliğidir; kargo seçenekleri bu adla açılır.
const ID = "manual"

// Sağlayıcının davranışını yönlendiren Data anahtarları.
//
// Fiyat anahtarları kargo SEÇENEĞİNİN yapılandırmasından
// (shipping_options.data) gelir ve Quote'a olduğu gibi geçirilir. Davranış
// anahtarları ([DataKeyOutcome]) hem fiyat sorgusunda hem gönderi açılışında
// okunur; gönderi açılışındakiler gönderiyle birlikte SAKLANIR, çünkü iptal
// başka bir istekte (hatta başka bir süreçte) yapılır ve o çağrının elinde
// yalnızca gönderi kimliği vardır.
const (
	// DataKeyOutcome çağrının sonucunu belirler; değerleri [OutcomeOK] ve
	// [OutcomeError]'dır. Verilmezse [OutcomeOK] varsayılır.
	DataKeyOutcome = "manual_outcome"
	// DataKeyQuoteAmount fiyatı DOĞRUDAN belirler; verilirse aşağıdaki
	// bileşenler hiç hesaplanmaz.
	DataKeyQuoteAmount = "manual_quote_amount"
	// DataKeyBaseAmount gönderi başına sabit ücrettir (minor unit).
	DataKeyBaseAmount = "manual_base_amount"
	// DataKeyPerItemAmount kalem başına ücrettir (minor unit).
	DataKeyPerItemAmount = "manual_per_item_amount"
	// DataKeyPerKilogramAmount başlanan her kilogram için ücrettir
	// (minor unit); yuvarlama YUKARI doğrudur (bkz. [Provider.Quote]).
	DataKeyPerKilogramAmount = "manual_per_kilogram_amount"
	// DataKeyTrackingNumber gönderiye yazılacak takip numarasıdır.
	DataKeyTrackingNumber = "manual_tracking_number"
	// DataKeyTrackingURL gönderiye yazılacak takip adresidir.
	DataKeyTrackingURL = "manual_tracking_url"
)

// Çağrı sonuçları ([DataKeyOutcome] değerleri).
const (
	// OutcomeOK çağrının başarılı olmasını sağlar; varsayılan davranıştır.
	OutcomeOK = "ok"
	// OutcomeError sağlayıcının ERİŞİLEMEDİĞİNİ taklit eder: metot hata döner
	// ve defterde hiçbir şey değişmez. Saga'nın "adım patladı" dalını sınamak
	// içindir ve yeniden denenebilir bir hatadır (errors.Unavailable).
	OutcomeError = "error"
)

// gramsPerKilogram bir kilogramın gram karşılığıdır.
const gramsPerKilogram int64 = 1000

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "fulfillment_manual_invalid_input"
	// CodeInvalidState gönderinin durumunda geçersiz bir geçiş denendiğini
	// bildirir.
	CodeInvalidState = "fulfillment_manual_invalid_state"
	// CodeIdempotencyMismatch aynı anahtarın FARKLI bir gövdeyle yeniden
	// kullanıldığını bildirir.
	CodeIdempotencyMismatch = "fulfillment_manual_idempotency_mismatch"
	// CodeSimulatedFailure test için enjekte edilmiş başarısızlığı bildirir.
	CodeSimulatedFailure = "fulfillment_manual_simulated_failure"
	// CodeDataInvalid gönderi verisinin çözümlenemediğini bildirir.
	CodeDataInvalid = "fulfillment_manual_data_invalid"
)

// Store sağlayıcının ihtiyaç duyduğu kalıcılık yüzeyidir.
//
// Arayüz TÜKETEN tarafta, yani burada tanımlıdır (ADR 0001'in örüntüsü).
// Sağlayıcı repository paketini import ETMEZ; somut depo bu imzaları yapısal
// olarak karşılar ve bağlantı module.go'da kurulur. Böylece sağlayıcının
// idempotency davranışı gerçek bir veritabanı olmadan, birkaç satırlık bir
// sahte depo ile sınanabilir.
//
// Kilit alan metot ([Store.LockManualShipment]) yalnızca [Store.WithTx] içinde
// çağrılabilir: işlemsiz bir FOR UPDATE kilidi hiçbir şeyi korumaz.
//
// Fiyat sorgusunun burada karşılığı YOKTUR ve bu bilinçlidir: Quote yan
// etkisizdir ve deftere hiç dokunmaz.
type Store interface {
	// WithTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem geri alınır.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// InsertManualShipmentIfAbsent gönderiyi yalnızca idempotency anahtarı
	// henüz kullanılmamışsa yazar. İkinci dönüş değeri satırın yazılıp
	// yazılmadığıdır; çakışma HATA DEĞİLDİR.
	InsertManualShipmentIfAbsent(ctx context.Context, shipment models.ManualShipment) (models.ManualShipment, bool, error)
	// ManualShipmentByIdempotencyKey gönderiyi anahtarıyla döner; yoksa NotFound.
	ManualShipmentByIdempotencyKey(ctx context.Context, key string) (models.ManualShipment, error)
	// ManualShipment gönderiyi kimliğiyle döner; yoksa NotFound.
	ManualShipment(ctx context.Context, id string) (models.ManualShipment, error)
	// LockManualShipment gönderiyi işlem boyunca kilitler ve güncel hâlini döner.
	LockManualShipment(ctx context.Context, id string) (models.ManualShipment, error)
	// UpdateManualShipmentState durumu ve takip bilgisini MUTLAK değerlerle yazar.
	UpdateManualShipmentState(
		ctx context.Context,
		id string,
		status models.FulfillmentStatus,
		trackingNumber, trackingURL string,
	) (models.ManualShipment, error)
}

// Provider manuel/test kargo sağlayıcısıdır. Eşzamanlı kullanıma güvenlidir.
type Provider struct {
	store Store
	log   *slog.Logger
}

// Provider'ın çekirdek sözleşmesini karşıladığı derleme zamanında doğrulanır;
// imza kayması çalışma zamanına kalmaz.
var _ coreprovider.FulfillmentProvider = (*Provider)(nil)

// New verilen depo üzerinde çalışan bir manuel sağlayıcı üretir.
// log nil verilirse loglar atılır.
func New(store Store, log *slog.Logger) *Provider {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Provider{store: store, log: log}
}

// ID sağlayıcının kimliğini döner.
func (p *Provider) ID() string { return ID }

// Quote verilen seçenek için kargo ücretini döner. YAN ETKİSİZDİR.
//
// Hesap SAF'tır: veritabanına dokunmaz, saate bakmaz ve aynı girdi için daima
// aynı sonucu verir. Sepet toplamı her güncellendiğinde çağrılabileceği için
// ucuz olması şarttır.
//
// # Formül
//
//	ücret = taban + (kalem başına × kalem adedi) + (kilogram başına × ⌈gram/1000⌉)
//
// Bileşenler kargo seçeneğinin yapılandırmasından okunur ([DataKeyBaseAmount],
// [DataKeyPerItemAmount], [DataKeyPerKilogramAmount]); verilmeyen bileşen
// SIFIRDIR. [DataKeyQuoteAmount] verilirse formül hiç çalışmaz ve o tutar
// döner.
//
// # Yuvarlama YUKARI doğrudur
//
// Ağırlık gramdır ve kilogram başına ücret BAŞLANAN her kilogram için alınır:
// 1200 gram İKİ kilogram sayılır. Yön bilinçlidir ve gerçek taşıyıcıların
// desi/kilogram kademelerini izler; aşağı yuvarlamak, 1999 gramlık bir paketi
// bir kilogram ücretine taşımak demek olurdu. Hesap TAM SAYI aritmetiğiyle
// yapılır; para hiçbir aşamada kayan noktaya uğramaz (plan Bölüm 8).
//
// # Taşma bir HATADIR, negatif fiyat değil
//
// Kalem adedi ve ağırlık ÇAĞIRANDAN gelir ve bu sağlayıcı onların üst sınırını
// bilmez (sınırı koyan taraf servistir; bkz. [models.MaxItemCount] ve
// [models.MaxTotalWeight]). Bu yüzden aritmetiğin kendisi savunmalıdır: hem
// kilograma yuvarlama hem çarpım/toplam taşmasız yazılmıştır ve hesap
// [models.MaxAmount]'u aşarsa errors.Invalid döner. Sağlayıcı HİÇBİR girdi
// için negatif bir ücret dönmez — dönseydi, düşürülmesi çağıranın son
// savunmasına kalırdı.
//
// Yapılandırılmamış bir seçenek SIFIR ücret döner ve bu geçerlidir:
// "ücretsiz kargo" gerçek bir iş kararıdır ve taklit sağlayıcının fiyat
// UYDURMASI sınanan davranışı belirsiz hâle getirirdi.
func (p *Provider) Quote(
	_ context.Context,
	in coreprovider.QuoteInput,
) (coreprovider.ShippingQuote, error) {
	optionID := strings.TrimSpace(in.OptionID)
	if optionID == "" {
		return coreprovider.ShippingQuote{}, errors.Invalid(CodeInvalidInput,
			"kargo seçeneği kimliği zorunludur")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.CurrencyCode))
	if len(currency) != currencyCodeLength {
		return coreprovider.ShippingQuote{}, errors.Invalid(CodeInvalidInput,
			"para birimi üç harfli ISO 4217 kodu olmalı: %q", in.CurrencyCode)
	}
	if in.ItemCount < 0 {
		return coreprovider.ShippingQuote{}, errors.Invalid(CodeInvalidInput,
			"kalem adedi negatif olamaz: %d", in.ItemCount)
	}
	if in.TotalWeight < 0 {
		return coreprovider.ShippingQuote{}, errors.Invalid(CodeInvalidInput,
			"toplam ağırlık negatif olamaz: %d", in.TotalWeight)
	}

	config, err := parseData(in.Data)
	if err != nil {
		return coreprovider.ShippingQuote{}, err
	}
	if config.Outcome == OutcomeError {
		return coreprovider.ShippingQuote{}, errors.Unavailable(CodeSimulatedFailure,
			"manuel kargo sağlayıcısına ulaşılamadı (test için enjekte edilmiş hata): %s", optionID)
	}

	amount, err := quoteAmount(config, in.ItemCount, in.TotalWeight)
	if err != nil {
		return coreprovider.ShippingQuote{}, err
	}

	return coreprovider.ShippingQuote{
		OptionID:     optionID,
		Amount:       amount,
		CurrencyCode: currency,
	}, nil
}

// Create sağlayıcının defterinde bir gönderi açar.
//
// Aynı IdempotencyKey ile ikinci çağrı YENİ gönderi açmaz, mevcut gönderiyi
// döner (çekirdek sözleşmesinin şartı). Anahtar aynı ama referans ya da
// seçenek FARKLIYSA errors.Conflict döner: idempotency "aynı isteği
// tekrarlamak" demektir, "farklı bir isteği eski anahtarla göndermek" değil —
// ikincisini sessizce kabul etmek, çağıranın açtığını sandığı gönderinin hiç
// açılmaması demek olurdu.
func (p *Provider) Create(
	ctx context.Context,
	in coreprovider.CreateFulfillmentInput,
) (coreprovider.Fulfillment, error) {
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		return coreprovider.Fulfillment{}, errors.Invalid(CodeInvalidInput,
			"idempotency anahtarı zorunludur")
	}
	reference := strings.TrimSpace(in.Reference)
	if reference == "" {
		return coreprovider.Fulfillment{}, errors.Invalid(CodeInvalidInput, "reference zorunludur")
	}
	optionID := strings.TrimSpace(in.OptionID)
	if optionID == "" {
		return coreprovider.Fulfillment{}, errors.Invalid(CodeInvalidInput,
			"kargo seçeneği kimliği zorunludur")
	}

	raw, err := json.Marshal(in.Data)
	if err != nil {
		return coreprovider.Fulfillment{}, errors.Wrap(err, errors.KindInvalid, CodeDataInvalid,
			"gönderi verisi kodlanamadı")
	}
	// Veri erken doğrulanır ve enjekte edilmiş hata BURADA patlar: bozuk bir
	// davranış anahtarı, gönderi açılırken söylenmelidir; sonraki bir çağrıda
	// patlaması teşhisi zorlaştırırdı.
	config, err := parseData(in.Data)
	if err != nil {
		return coreprovider.Fulfillment{}, err
	}
	if config.Outcome == OutcomeError {
		return coreprovider.Fulfillment{}, errors.Unavailable(CodeSimulatedFailure,
			"manuel kargo sağlayıcısına ulaşılamadı (test için enjekte edilmiş hata): %s", reference)
	}

	created, inserted, err := p.store.InsertManualShipmentIfAbsent(ctx, models.ManualShipment{
		ID:             models.NewManualShipmentID(),
		IdempotencyKey: key,
		Reference:      reference,
		OptionID:       optionID,
		Status:         models.StatusPending,
		TrackingNumber: config.TrackingNumber,
		TrackingURL:    config.TrackingURL,
		Data:           raw,
	})
	if err != nil {
		return coreprovider.Fulfillment{}, err
	}
	if inserted {
		return toProviderFulfillment(created), nil
	}

	existing, err := p.store.ManualShipmentByIdempotencyKey(ctx, key)
	if err != nil {
		return coreprovider.Fulfillment{}, err
	}
	if existing.Reference != reference || existing.OptionID != optionID {
		return coreprovider.Fulfillment{}, errors.Conflict(CodeIdempotencyMismatch,
			"aynı idempotency anahtarı farklı bir gönderi için kullanıldı: mevcut %s/%s, istenen %s/%s",
			existing.Reference, existing.OptionID, reference, optionID)
	}
	p.log.DebugContext(ctx, "manuel sağlayıcı mevcut gönderiyi döndürdü",
		"gonderi", existing.ID, "anahtar", key)
	return toProviderFulfillment(existing), nil
}

// Cancel gönderiyi iptal eder.
//
// SAGA TELAFİSİ BUDUR ve İDEMPOTENTTİR: zaten iptal edilmiş bir gönderi için
// hata dönmez ve defterde ikinci kez değişiklik yapılmaz. TESLİM EDİLMİŞ bir
// gönderi iptal EDİLEMEZ (errors.Conflict); paket alıcıdadır ve geri almanın
// yolu iadedir. Kargoya verilmiş bir gönderi ise iptal EDİLEBİLİR: taşıyıcı
// yoldaki paketi geri çağırabilir (bkz.
// [models.FulfillmentStatus.CancelAction]).
//
// Bilinmeyen bir kimlik için errors.NotFound döner: idempotentlik "her şeyi
// sessizce yut" demek değildir. İki kez iptal edilen GERÇEK bir gönderi ile
// hiç var olmamış bir kimlik farklı durumlardır ve ikincisi çağıran tarafta
// bir hatadır. Gönderi kaydı silinmediği (yalnızca durumu değiştiği) için ilk
// durum her zaman ayırt edilebilir.
func (p *Provider) Cancel(ctx context.Context, fulfillmentID string) error {
	if strings.TrimSpace(fulfillmentID) == "" {
		return errors.Invalid(CodeInvalidInput, "gönderi kimliği zorunludur")
	}

	return p.store.WithTx(ctx, func(ctx context.Context) error {
		shipment, err := p.store.LockManualShipment(ctx, fulfillmentID)
		if err != nil {
			return err
		}

		switch shipment.Status.CancelAction() {
		case models.ActionNoop:
			p.log.DebugContext(ctx, "manuel sağlayıcı gönderisi zaten iptal edilmiş",
				"gonderi", fulfillmentID)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidState,
				"%q durumundaki gönderi iptal edilemez; iade kullanın: %s",
				shipment.Status, fulfillmentID)
		case models.ActionProceed:
			// Aşağıda ele alınır.
		}

		// Takip bilgisi KORUNUR: iptal edilen bir gönderinin hangi etiketle
		// açıldığı teşhis için hâlâ okunabilir olmalıdır.
		_, err = p.store.UpdateManualShipmentState(ctx, shipment.ID, models.StatusCanceled,
			shipment.TrackingNumber, shipment.TrackingURL)
		return err
	})
}

// GetShipment sağlayıcının defterindeki gönderiyi döner; yoksa
// errors.NotFound.
//
// Çekirdek sözleşmesinde YOKTUR ve fulfillment servisi bunu ÇAĞIRMAZ. Yalnızca
// entegrasyon testleri ve teşhis içindir: bir gönderinin sağlayıcı tarafındaki
// durumunu modülün kendi kaydına bakmadan doğrulamak gerekir — iki defterin
// ayrıştığı bir hata ancak böyle görülebilir.
func (p *Provider) GetShipment(ctx context.Context, id string) (models.ManualShipment, error) {
	if strings.TrimSpace(id) == "" {
		return models.ManualShipment{}, errors.Invalid(CodeInvalidInput, "gönderi kimliği zorunludur")
	}
	return p.store.ManualShipment(ctx, id)
}

// toProviderFulfillment defter kaydını çekirdek sözleşmesinin gönderi tipine
// çevirir.
func toProviderFulfillment(shipment models.ManualShipment) coreprovider.Fulfillment {
	return coreprovider.Fulfillment{
		ID:             shipment.ID,
		Status:         coreprovider.FulfillmentStatus(shipment.Status),
		TrackingNumber: shipment.TrackingNumber,
		TrackingURL:    shipment.TrackingURL,
		Data:           shipment.Data,
	}
}
