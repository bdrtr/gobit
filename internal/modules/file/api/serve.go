package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// onbellekDenetimi sunulan dosyaların Cache-Control değeridir.
//
// # Neden "immutable" DEĞİL
//
// Anahtar tekrar kullanılmaz (80 bit rastgelelik), yani bir adresin içeriği
// DEĞİŞMEZ — buraya kadar "immutable" doğru görünür. Ama içerik değişmese de
// SİLİNEBİLİR ve fark tam orada ortaya çıkar: bu uç kimliksizdir, dolayısıyla
// PAYLAŞILAN önbellekler (CDN, ters vekil) yanıtı meşru biçimde saklar.
// "immutable" onlara "yeniden doğrulama" demeyi bırakmalarını söyler; sonuç,
// DELETE /admin/v1/uploads/{id} çağrıldıktan sonra da aynı adresin bir YIL
// boyunca sunulmaya devam etmesidir. Silme, orijinde çalışır ama erişimi geri
// almaz.
//
// Bir saat, iki isteği dengeler: görsel trafiği hâlâ önbellekten karşılanır ve
// bir silme kararı en geç bir saatte her yere ulaşır. Daha uzun bir süre isteyen
// kurulum, silmeyi önbellek temizlemeyle (purge) birlikte yapmalıdır — o zaman
// süreyi uzatmak da güvenlidir.
const onbellekDenetimi = "public, max-age=3600"

// serveFile GET /files/{key} handler'ıdır.
//
// # İki kural her yanıtta geçerlidir
//
//  1. Content-Type SAKLANAN tipten yazılır — istemcinin yükleme sırasında
//     bildirdiği tipten DEĞİL. Saklanan tip, yükleme anında dosyanın
//     İÇERİĞİNDEN tespit edilmiştir; istemcinin iddiası hiçbir yerde
//     saklanmaz ki buraya sızabilsin.
//  2. X-Content-Type-Options: nosniff HER yanıtta bulunur — hata yanıtları
//     dâhil, bu yüzden başlık daha ilk satırda yazılır. Bu başlık olmadan
//     tarayıcı, gönderdiğimiz tipe rağmen içeriğe bakıp kendi tahminini yapar:
//     "image/png" olarak saklanmış ama HTML'e benzeyen bir dosya HTML gibi
//     çalıştırılabilirdi. Yani tespit ve izin listesi doğru çalışsa bile
//     sunum aşaması onları geçersiz kılardı.
//
// # Content-Disposition YAZILMAZ
//
// İstemcinin bildirdiği dosya adı kayıtta durur ama BAŞLIĞA konmaz: içeriğine
// güvenilmeyen bir dizeyi başlık dilbilgisinin içine koymak, ayrı bir sınıf
// açık üretir. Ad, JSON gövdesinde döner ve orada kodlaması güvenlidir.
//
// # Yanıtın gövdesini net/http yazar
//
// [net/http.ServeContent] koşullu istekleri (If-Modified-Since) ve aralık
// (Range) isteklerini karşılar. Elle io.Copy yazmak, her ikisini de kaybetmek
// olurdu — büyük bir görselin yeniden yüklenmesi ya da kısmi indirilmesi
// sıradan bir tarayıcı davranışıdır. Dosya adı BOŞ geçilir: ServeContent, adı
// yalnızca Content-Type'ı TAHMİN ETMEK için kullanır ve biz onu zaten
// kayıttan yazdık; ad verilseydi tahmin yolu açık kalırdı.
func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	w.Header().Set(headerContentTypeOptions, nosniff)

	acilan, err := h.svc.OpenByKey(ctx, chi.URLParam(r, paramKey))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	defer func() { _ = acilan.Content.Close() }()

	w.Header().Set("Content-Type", acilan.Upload.ContentType)
	w.Header().Set("Cache-Control", onbellekDenetimi)

	// ÇOK ARALIKLI Range reddedilir; başlık silinerek tam gövdeye düşülür.
	//
	// [net/http.ServeContent] çok aralıklı isteği multipart/byteranges ile
	// karşılar ve yalnızca aralıkların TOPLAM BAYTININ dosya boyutunu aşmasını
	// engeller — aralık SAYISINI sınırlamaz. Her aralık kendi sınır dizesini
	// ve başlık bloğunu taşıdığı için, tek baytlık yüzlerce aralık isteyen bir
	// istemci gövdeden kat kat büyük bir yanıt ürettirir. Bu uç KİMLİKSİZDİR
	// (vitrindeki <img> başlık gönderemez), yani büyütme doğrudan bant genişliği
	// saldırısına dönüşür.
	//
	// Tek aralıklı Range KORUNUR: tarayıcıların ve video oynatıcıların
	// gerçekten kullandığı biçim odur. 416 dönmek yerine başlığı silmek
	// bilinçlidir — istemci hatasız ve tam içerikle devam eder, yalnızca
	// aralık optimizasyonunu kaybeder.
	if strings.Contains(r.Header.Get("Range"), ",") {
		r.Header.Del("Range")
	}

	http.ServeContent(w, r, "", acilan.ModTime, acilan.Content)
}
