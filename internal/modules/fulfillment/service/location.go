package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// SelectLocation gönderinin çıkacağı TEK lokasyonu adaylar arasından seçer.
//
// # Karar BURADA durur, bugün basit olduğu için değil
//
// Hangi depodan gönderileceği bir KARGO kararıdır; kuralları kargo bölgesine,
// taşıyıcının kapsama alanına ve teslim süresine bakar. "Hangi lokasyonlarda
// yeterli stok var" ise bir STOK OLGUSUDUR ve stok modülünün yüzeyinden gelir.
// İş bölümü bilinçlidir: iki yarıyı tek modülde toplamak, stok sorgusunu kargo
// politikasına ya da kargo politikasını stok şemasına bağımlı kılardı.
//
// Sepet akışı bu yüzden lokasyonu KENDİ seçmez: adayları stoktan alır, seçimi
// buraya sorar. Dikişin yeri, politikanın bugünkü sadeliğinden bağımsızdır.
//
// # Bugünkü politika: kimliği en küçük aday
//
// Politika BİLİNÇLİ OLARAK sadedir, çünkü bu modülün bir LOKASYON MODELİ
// yoktur: depoların nerede olduğunu, hangi kargo bölgesine ya da taşıyıcıya
// bağlı olduğunu bilmez. Yakınlık, maliyet ve stoğun dağılımı gibi kurallar bu
// veri olmadan İFADE EDİLEMEZ; yazılsalardı dayandıkları bilgi uydurma olurdu.
// Modül bir lokasyon modeli kazandığında (depo ↔ kargo bölgesi bağı) politika
// bu metodun İÇİNDE zenginleşir; çağıranın gördüğü imza değişmez.
//
// # Seçim DETERMİNİSTİKTİR
//
// Aynı adaylarla ikinci çağrı aynı lokasyonu döner ve sonuç adayların GELİŞ
// SIRASINDAN da bağımsızdır: kimliği en küçük olan kazanır. "İlk adayı al" da
// tek satırdır ama determinizmi çağıranın sıralamasına devrederdi; stok
// tarafının sıralaması stok hareketleriyle değişebilir ve saga'nın yeniden
// denemesi BAŞKA bir depodan ayırırdı — ilk denemenin rezervasyonu yetim
// kalırdı.
//
// Aday dilimi sıralanmaz, TARANIR: sıralamak çağıranın dilimini yerinde
// değiştirmek olurdu ve bir seçim yüzeyi kendisine verilen veriyi bozamaz.
//
// # Boş aday listesi Conflict'tir
//
// Hata errors.Invalid DEĞİLDİR: isteğin biçiminde düzeltilecek bir şey yoktur.
// Eksik olan DÜNYANIN durumudur — hiçbir lokasyonda yeterli stok yoktur — ve
// çağıran bunu, stok modülünün yetersiz stok için döndüğü hatayla AYNI dalda
// ("sipariş verilemez") karşılamalıdır. Sessizce boş dize dönmek en kötüsü
// olurdu: çağıran boş bir lokasyonla stok ayırmaya kalkar ve hata, sebebinden
// bir modül uzakta patlardı.
//
// Aday listesindeki BOŞ bir kimlik aynı sebeple reddedilir (errors.Invalid);
// "en küçük kimlik" kuralı onu seçerdi ve sonuç yine boş lokasyonla yapılan
// bir stok ayırma olurdu.
//
// ctx bugün kullanılmaz; imzada durmasının sebebi, politikanın bir lokasyon
// modeli geldiğinde veritabanına bakacak olmasıdır. Projede tüm servis
// metotları context alır ve o gün imza değişmek zorunda kalmamalıdır.
func (s *Service) SelectLocation(_ context.Context, candidateLocationIDs []string) (string, error) {
	if len(candidateLocationIDs) == 0 {
		return "", errors.Conflict(CodeNoShippingLocation,
			"gönderi yapılabilecek lokasyon yok")
	}

	selected := ""
	for i, candidate := range candidateLocationIDs {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			return "", errors.Invalid(CodeInvalidInput,
				"aday lokasyon kimliği boş olamaz (%d. aday)", i+1)
		}
		if err := checkTextLen("aday lokasyon kimliği", trimmed); err != nil {
			return "", err
		}
		if selected == "" || trimmed < selected {
			selected = trimmed
		}
	}
	return selected, nil
}
