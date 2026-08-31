package provider

import "context"

// Bildirim kanalları; [Notification.Channel] bu değerlerden birini taşır.
//
// Sabitler çekirdektedir çünkü değeri hem gönderen taraf (bildirim modülü) hem
// tüketen taraf (eklentideki sağlayıcı) yazar ve ikisi birbirini import EDEMEZ
// (Prensip 2.4). Elle tekrarlanan iki "sms" dizesi, sağlayıcının kanalı hiç
// tanımaması ve bildirimin sessizce düşmesi demek olurdu.
const (
	// ChannelEmail e-posta kanalıdır; To bir e-posta adresidir.
	ChannelEmail = "email"
	// ChannelSMS SMS kanalıdır; To bir telefon numarasıdır.
	ChannelSMS = "sms"
)

// Notification gönderilecek tek bir bildirimdir.
type Notification struct {
	// Channel gönderim kanalıdır: [ChannelEmail] ya da [ChannelSMS].
	// Sağlayıcı desteklemediği bir kanal görürse gönderim yapmaz, hata döner.
	Channel string
	// To alıcı adresidir; anlamı kanala göre değişir (e-posta adresi ya da
	// telefon numarası).
	To string
	// Template kullanılacak şablonun adıdır (örn. "order.placed").
	//
	// Şablon adının olay adıyla aynı seçilmesi bilinçlidir: bildirimin
	// tetikleyicisi bir olaydır ve iki ayrı ad kullanmak, "hangi olay hangi
	// şablonu tetikliyor" sorusunu koda bakarak yanıtlanır hâle getirirdi.
	// Metnin kendisi çekirdekte DEĞİLDİR; şablonu sağlayıcı çözer.
	Template string
	// Data şablona geçilecek değerlerdir; TÜM değerler DİZEDİR.
	//
	// map[string]any daha esnek görünür ama YANLIŞ olurdu ve gerekçe olay
	// yükününkiyle birebir aynıdır: bildirimin kaynağı "order.placed"
	// olayıdır, o olay üretimde Redis Streams'e JSON olarak yazılır ve JSON'un
	// tek sayı tipi vardır. int64 olarak konan bir alan aboneye float64 olarak
	// ulaşır, InMemory backend'inde ise int64 kalır — yani aynı alan
	// geliştirmede ve üretimde FARKLI Go tipiyle gelir. any taşınsaydı
	// sağlayıcı bu değeri biçimlendirmek zorunda kalır, büyük tutarlar
	// "1.2345678e+13" gibi basılır ve 2^53 minor unit üstü sessizce yuvarlanır
	// — para float üzerinden geçerdi (plan Bölüm 8: float ASLA).
	//
	// Dize her iki backend'de de AYNI Go tipini ve TAM değeri verir.
	// Biçimlendirme kararı böylece sağlayıcının tahminine değil, değeri üreten
	// çağırana kalır.
	Data map[string]string
}

// NotificationProvider bir bildirim sağlayıcısının çekirdeğe sunduğu
// sözleşmedir (plan Bölüm 5.6).
//
// # Neden tek metot
//
// [PaymentProvider] ve [FulfillmentProvider] çok adımlıdır çünkü sagaların
// geri alabileceği bir durum tutarlar. Bildirimde böyle bir durum yoktur:
// gönderilmiş bir e-posta geri alınamaz, dolayısıyla telafi (Cancel) yolu da
// olamaz. Sözleşmeye teslim durumu sorgulama eklemek, her sağlayıcının
// desteklemediği bir yeteneği taklit etmesini gerektirirdi.
//
// # İdempotency BEKLENMEZ
//
// Diğer sağlayıcıların aksine burada IdempotencyKey yoktur. Bildirim bir olay
// abonesinden tetiklenir ve [github.com/bdrtr/gobit/internal/core/eventbus]'ın
// Redis backend'i EN AZ BİR KEZ teslim eder; aynı bildirim iki kez
// gönderilebilir. Bu bilinçli olarak kabul edilir: tekrarı önlemek, sağlayıcı
// tarafında kalıcı bir "gönderildi" kaydı tutmayı gerektirirdi ve o kayıt
// çekirdeğin değil, gönderen modülün işidir. Bedel de simetrik değildir —
// mükerrer bir e-posta rahatsız eder, mükerrer bir tahsilat müşterinin
// parasını alır.
type NotificationProvider interface {
	Provider

	// Send bildirimi gönderir.
	//
	// Çağrı BLOKLAYICIDIR ve dış bir servise gider; çağıran ctx'e süre
	// sınırı koymalıdır. Hata dönmesi bildirimin gitmediği anlamına gelmez:
	// zaman aşımına uğrayan bir istek karşı tarafta işlenmiş olabilir.
	Send(ctx context.Context, n Notification) error
}
