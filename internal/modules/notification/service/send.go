package service

import (
	"context"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
)

// sendTimeout tek bir sağlayıcı çağrısına tanınan süredir.
//
// Sınır ZORUNLUDUR ve çağırandan devralınamaz: bu servisin tek çağıranı bir
// olay işleyicisidir ve [eventbus.Handler] sözleşmesine göre işleyicinin ctx'i
// isteğin iptalini DEVRALMAZ, yani süresizdir. Sınırsız bir SMTP/HTTP çağrısı,
// Redis backend'inde aynı stream'in tüm olaylarını arkasında kuyruklar
// (searchpg/events.go'daki aynı gerekçe) ve InMemory backend'inde goroutine
// biriktirir. On beş saniye, yavaş bir e-posta ağ geçidine yetecek kadar uzun,
// bir olay akışını kilitlemeyecek kadar kısadır.
const sendTimeout = 15 * time.Second

// maxErrorLen günlüğe yazılan hata metninin üst sınırıdır.
//
// Sağlayıcının döndüğü hata dıştan gelen bir metindir ve uzunluğu bu modülün
// denetiminde değildir; sınırsız yazmak, tek bir arızanın tabloyu şişirmesi
// demekti.
const maxErrorLen = 512

// mesajAdresYok adressiz atlanan kayda yazılan açıklamadır.
//
// Error sütunu yalnızca "failed" durumunda hata taşır; burada bir hata
// DEĞİLDİR ama kaydı okuyan kişi "neden atlandı" sorusunu tek satırdan
// yanıtlayabilmelidir. Metin ALICI ADRESİ İÇERMEZ — zaten yoktur.
const mesajAdresYok = "gönderilecek adres yok; sağlayıcıya gidilmedi"

// NotifyInput tek bir bildirim gönderiminin girdisidir.
//
// TÜM alanlar İLKELDİR ve Data'nın değerleri de dizedir; gerekçe çekirdekteki
// [coreprovider.Notification] belgesindedir (özet: yükün kaynağı bir olaydır,
// olay üretimde JSON'a çevrilir ve JSON'un tek sayı tipi vardır — any taşınsa
// para float üzerinden geçerdi).
type NotifyInput struct {
	// Template kullanılacak şablonun adıdır (örn. "order.placed").
	// İdempotency anahtarının ilk yarısıdır.
	Template string
	// Channel gönderim kanalıdır ([coreprovider.ChannelEmail] ya da
	// [coreprovider.ChannelSMS]).
	Channel string
	// Reference bildirimin bağlı olduğu kaydın kimliğidir (sipariş).
	// İdempotency anahtarının ikinci yarısıdır.
	Reference string
	// To alıcı adresidir. GÜNLÜĞE YAZILMAZ ve loglanmaz; yalnızca sağlayıcıya
	// geçirilir. Boş olabilir; bkz. [Service.Notify].
	To string
	// Data şablona geçilecek değerlerdir.
	Data map[string]string
}

// normalize girdiyi doğrular ve boşlukları temizler.
func (in NotifyInput) normalize() (NotifyInput, error) {
	in.Template = strings.TrimSpace(in.Template)
	in.Channel = strings.TrimSpace(in.Channel)
	in.Reference = strings.TrimSpace(in.Reference)
	in.To = strings.TrimSpace(in.To)

	if in.Template == "" {
		return NotifyInput{}, errors.Invalid(CodeInvalidInput, "şablon adı zorunludur")
	}
	if in.Channel == "" {
		return NotifyInput{}, errors.Invalid(CodeInvalidInput, "kanal zorunludur")
	}
	if in.Reference == "" {
		return NotifyInput{}, errors.Invalid(CodeInvalidInput, "referans zorunludur")
	}
	return in, nil
}

// Notify bildirimi seçili sağlayıcıya gönderir ve denemeyi günlüğe yazar.
//
// # Sıra: önce KAYIT, sonra gönderim
//
// Kayıt (şablon, referans) çifti üzerinde benzersizdir ve sağlayıcıya
// gidilmeden ÖNCE açılır. Ters sıra — önce gönder, sonra kaydet — mükerrer
// bildirimi hiç engellemezdi: eşzamanlı iki işleyici de sağlayıcıya gider,
// benzersizlik ihlali ancak İKİ e-posta gittikten sonra görünürdü.
//
// # Kayıt zaten varsa gönderim ATLANIR; durumu ne olursa olsun
//
// İkinci çağrı hata DÖNMEZ, sessizce atlar ve bunu bilgi seviyesinde loglar.
// Atlama BAŞARISIZ bir kayıt için de geçerlidir ve bu bilinçlidir: çekirdek
// sözleşmesi (bkz. [coreprovider.NotificationProvider.Send]) hata dönmenin
// bildirimin gitmediği ANLAMINA GELMEDİĞİNİ söyler — zaman aşımına uğrayan bir
// istek karşı tarafta işlenmiş olabilir. "Başarısızsa yeniden dene" kuralı bu
// yüzden mükerrer e-posta üretebilir ve gerçek bir yeniden deneme politikası
// (deneme sayacı, geri çekilme, ölü mektup kuyruğu) ister; o politika bu
// modülün kapsamında değildir. Yeniden gönderim, günlüğe bakan bir insanın
// KASITLI kararı olmalıdır.
//
// # Adressiz bildirim HATA DEĞİLDİR
//
// To boşsa sağlayıcıya HİÇ gidilmez, kayıt [models.DeliverySkipped] olarak
// kapatılır ve nil dönülür. Hata dönmek yanlış olurdu: çağıran bir olay
// işleyicisidir ve onun için "adres yok" KALICI bir durumdur — yeniden
// denenecek bir arızadan ayırt edilemezse ya sonsuza dek denenir ya da gerçek
// arızalar da yutulur (aynı gerekçe order modülünün OrderContactJSON
// belgesinde de yazılıdır).
//
// # Dönen hata
//
// Sağlayıcının hatası ÇAĞIRANA GERİ VERİLİR (kayda yazıldıktan sonra): olay
// işleyicisi onu veri yoluna döndürür ve veri yolu hatayı olay adı, olay
// kimliği ve hata zinciriyle ERROR seviyesinde loglar. Yutulsaydı, bildirimin
// gitmediği yalnızca tabloya bakan birine görünürdü.
func (s *Service) Notify(ctx context.Context, in NotifyInput) error {
	in, err := in.normalize()
	if err != nil {
		return err
	}

	// Sağlayıcı kayıttan ÖNCE çözülür: bilinmeyen bir sağlayıcı adıyla açılan
	// kayıt, hiç gönderilmemiş bir bildirimin idempotency anahtarını tüketir
	// ve yapılandırma düzeltildikten sonra bildirim bir daha hiç gönderilemezdi.
	provider, err := s.providers.Get(s.providerID)
	if err != nil {
		return err
	}

	kayit, yeni, err := s.store.ClaimDelivery(ctx, models.Delivery{
		ID:         models.NewDeliveryID(time.Now()),
		Template:   in.Template,
		Channel:    in.Channel,
		Reference:  in.Reference,
		ProviderID: provider.ID(),
		Status:     models.DeliveryPending,
	})
	if err != nil {
		return err
	}
	if !yeni {
		s.log.InfoContext(ctx, "bildirim zaten gönderilmiş; atlandı",
			"sablon", in.Template, "referans", in.Reference)
		return nil
	}

	if in.To == "" {
		s.finish(ctx, kayit, models.DeliverySkipped, mesajAdresYok)
		s.log.InfoContext(ctx, "bildirim atlandı: adres yok",
			"kayit", kayit.ID, "sablon", in.Template, "referans", in.Reference)
		return nil
	}

	sendErr := s.send(ctx, provider, in)

	durum, mesaj := models.DeliverySent, ""
	if sendErr != nil {
		durum, mesaj = models.DeliveryFailed, kisalt(sendErr.Error(), maxErrorLen)
	}
	s.finish(ctx, kayit, durum, mesaj)

	if sendErr != nil {
		return errors.Wrap(sendErr, errors.KindOf(sendErr), CodeSendFailed,
			"%q bildirimi gönderilemedi (%s, referans %s)",
			in.Template, provider.ID(), in.Reference)
	}

	s.log.InfoContext(ctx, "bildirim gönderildi",
		"kayit", kayit.ID,
		"sablon", in.Template,
		"kanal", in.Channel,
		"referans", in.Reference,
		"saglayici", provider.ID())
	return nil
}

// send sağlayıcıyı SÜRE SINIRLI bir bağlamla çağırır.
//
// Ayrı bir metot olmasının sebebi, süre sınırının tek bir yerde ve gönderim
// dışındaki hiçbir adımı kapsamadan kurulmasıdır: kayıt yazma da aynı sınırın
// altında kalsaydı, yavaş bir sağlayıcıdan sonra SONUCU yazacak çağrı da
// süresi dolmuş bir bağlamla yapılır ve kayıt "pending" kalırdı.
func (s *Service) send(
	ctx context.Context,
	provider coreprovider.NotificationProvider,
	in NotifyInput,
) error {
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	return provider.Send(sendCtx, coreprovider.Notification{
		Channel:  in.Channel,
		To:       in.To,
		Template: in.Template,
		Data:     in.Data,
	})
}

// finish denemenin sonucunu günlüğe yazar; yazamazsa ERROR loglar.
//
// Yazma hatası ÇAĞIRANA DÖNMEZ ve bu bilinçlidir: bu noktada sağlayıcıya çoktan
// gidilmiştir, yani asıl olay olmuştur. Hata dönmek, gönderilmiş bir bildirimi
// "başarısız" gibi göstermek olurdu; oysa gerçek durum "gönderildi ama sonucu
// yazılamadı"dır ve kaydın "pending" kalması tam olarak bunu anlatır
// (bkz. [models.DeliveryPending]).
//
// Bağlam İPTALDEN ARINDIRILIR: gönderim süresi dolduğunda ya da olay işleme
// bağlamı iptal edildiğinde sonucun yazılamaması, kaydı kalıcı olarak
// "pending" bırakırdı — yani en çok bilgiye ihtiyaç duyulan durumda günlük en
// sessiz hâlini alırdı.
func (s *Service) finish(
	ctx context.Context,
	kayit models.Delivery,
	durum models.DeliveryStatus,
	mesaj string,
) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()

	if _, err := s.store.FinishDelivery(writeCtx, kayit.ID, durum, mesaj); err != nil {
		s.log.ErrorContext(ctx, "bildirim sonucu günlüğe yazılamadı; kayıt 'pending' kaldı",
			"kayit", kayit.ID,
			"sablon", kayit.Template,
			"referans", kayit.Reference,
			"sonuc", durum.String(),
			"error", err)
	}
}

// kisalt metni verilen uzunluğa kırpar.
//
// Kırpma RUNE sınırında yapılır: bayt sınırında kesmek, çok baytlı bir
// karakterin ortasında bölerek geçersiz UTF-8 üretir ve o metin JSON'a
// kodlanırken sessizce bozulurdu.
func kisalt(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
