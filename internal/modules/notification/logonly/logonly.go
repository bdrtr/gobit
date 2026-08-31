// Package logonly bildirimi YALNIZCA loglayan, hiçbir yere GÖNDERMEYEN
// varsayılan bildirim sağlayıcısıdır (plan Bölüm 5.6).
//
// [Provider], internal/core/provider'daki NotificationProvider sözleşmesini
// karşılar ve kutudan çıkan tek sağlayıcıdır: gobit bir çerçevedir ve hangi
// e-posta/SMS servisinin kullanılacağını bilemez, ama bildirim yolunun ayakta
// olduğunu göstermek zorundadır.
//
// # Adı NEDEN "log"
//
// payment modülünün "manual" sağlayıcısı da gerçek bir kuruluşa gitmez, ama
// oradaki taklit ZARARSIZDIR: manuel ödeme gerçek bir iş modelidir ve
// sağlayıcı kendi defterinde tutarlı bir durum tutar. Burada öyle bir durum
// yoktur — gönderilmemiş bir e-posta hiçbir yerde "bekliyor" olarak durmaz.
// Bu yüzden ad, davranışı SÖYLER: "log", gönderim değil kayıt yapıldığını
// açıkça bildirir. "smtp", "default" ya da "noop" gibi bir ad seçilseydi
// kurulumun sahibi bildirimin gittiğini sanabilirdi ve o yanılgının bedeli
// somuttur: sipariş onayı almadığı için müşterinin siparişinin geçmediğini
// sanması — üstelik sistemde hiçbir hata görünmeden.
//
// Aynı sebeple gönderim BAŞARISIZ sayılmaz: hata dönmek, teslim günlüğünü
// gerçek bir arıza varmış gibi kırmızıya boyar ve yapılandırma hatası ile
// sağlayıcı arızasını ayırt edilemez hâle getirirdi. Sağlayıcı "isteği
// aldım" der; alınan isteğin nereye gitmediğini adı ve bu belge söyler.
//
// # ALICI ADRESİ LOGLANMAZ
//
// Log satırı ne e-posta ne telefon taşır (plan Bölüm 8: hassas veri
// loglanmaz). Log toplayıcısı, yönetim yüzeyinden çok daha geniş bir kitleye
// açıktır; bir adresin oraya düşmesi, teslim günlüğünde bilinçli olarak
// tutulmayan veriyi arka kapıdan kalıcı hâle getirirdi.
package logonly

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
)

// ID sağlayıcının kimliğidir; NOTIFICATION_PROVIDER varsayılanı budur.
const ID = "log"

// Provider bildirimi yalnızca loglayan sağlayıcıdır.
// Eşzamanlı kullanıma güvenlidir.
type Provider struct {
	log *slog.Logger
}

// Provider'ın çekirdek sözleşmesini karşıladığı derleme zamanında doğrulanır;
// imza kayması çalışma zamanına kalmaz.
var _ coreprovider.NotificationProvider = (*Provider)(nil)

// New bir log sağlayıcısı üretir. log nil verilirse loglar atılır.
func New(log *slog.Logger) *Provider {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Provider{log: log}
}

// ID sağlayıcının kimliğini döner.
func (p *Provider) ID() string { return ID }

// Send bildirimi loglar ve HİÇBİR YERE GÖNDERMEZ.
//
// Log seviyesi WARN'dır, INFO değil: bu satır normal bir işleyişin kaydı
// değil, "kurulum gerçek bir sağlayıcı seçmemiş" uyarısıdır. Üretimde her
// siparişte bir uyarı görmek istenmeyen bir gürültüdür ve tam olarak bu
// yüzden doğrudur — susturmanın yolu NOTIFICATION_PROVIDER'ı bir eklenti
// sağlayıcısına çevirmektir.
//
// Yükün DEĞERLERİ loglanmaz, yalnızca ANAHTARLARI yazılır. Bugünkü tek şablon
// ("order.placed") kişisel veri taşımaz, ama şablon verisi tanımı gereği
// serbesttir ve yarın bir müşteri adı taşıyabilir; anahtar listesi "şablon
// dolduruldu mu" sorusunu, değerleri sızdırmadan yanıtlar.
func (p *Provider) Send(ctx context.Context, n coreprovider.Notification) error {
	p.log.WarnContext(ctx, "bildirim GÖNDERİLMEDİ: 'log' sağlayıcısı yalnızca kaydeder",
		"saglayici", ID,
		"sablon", n.Template,
		"kanal", n.Channel,
		"veri_anahtarlari", slices.Sorted(maps.Keys(n.Data)))
	return nil
}
