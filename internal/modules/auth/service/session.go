package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// Bu dosya bir oturumun ne zaman ve NASIL düştüğünü tanımlar.
//
// # Oturum kaydı YOKTUR
//
// Jeton durum tutmaz: sunucuda "açık oturumlar" diye bir tablo yoktur ve
// üretilmiş TEK bir jetonu geçersizleştirmenin yolu da yoktur. Bunun için
// jetona bir "jti" konması ve her isteğin okuduğu, süresi dolan kayıtları
// düzenli temizlenen bir kara liste deposu tutulması gerekirdi — jetonun
// durumsuz olmasından vazgeçmek demek olan bu bedel, bugün karşılığında
// hiçbir yeni yetenek vermiyor (bkz. [Service.Logout]).
//
// Onun yerine kimlik başına TEK bir zaman damgası tutulur: [sessionAnchor].
// Bir jeton, çapadan önce üretildiyse reddedilir. Bu yüzden iptal her zaman
// TOPTANDIR — çapa ilerlediğinde kullanıcının bütün cihazlarındaki jetonlar
// aynı anda düşer.
//
// # Çapayı yalnızca SAHİBİNİN İRADESİ ilerletir
//
// İki iş çapayı ilerletir ve ikisi de hesap sahibinin bilerek yaptığı
// işlerdir: parola değişimi ([Service.SetPassword]) ve çıkış
// ([Service.Logout]). Giriş sayaçları çapaya DOKUNMAZ; dokunsalardı tek bir
// hatalı giriş denemesi kurbanın tüm oturumlarını düşüren hedefli bir hizmet
// dışı bırakma aracı olurdu (gerekçe queries/identities.sql).

// sessionAnchor kimliğin oturum çapasını döner: bu andan ÖNCE üretilmiş
// jetonlar geçersizdir.
//
// # Neden updated_at
//
// Tabloda ayrı bir "sessions_revoked_at" sütunu YOKTUR ve gerekmez: bu tabloda
// updated_at zaten yalnızca çapayı ilerletmesi gereken iki yazmada
// (UpdatePasswordHash, RevokeSessions) hareket eder. Giriş sayaçlarını yazan
// sorgular ona bilerek dokunmaz.
//
// Fonksiyon tek satırdır ama bir SÖZLEŞMEYİ adlandırır: sütun seçimi ileride
// değişirse (örneğin gerçekten ayrı bir sütun eklenirse) dokunulacak tek yer
// burasıdır.
func sessionAnchor(identity models.AuthIdentity) time.Time {
	return identity.UpdatedAt
}

// Logout çağıranın oturumlarını kapatır ve iptalin dayandığı anı döner.
//
// # TÜM oturumlar düşer; tek cihaz seçilemez
//
// Bu uç bir cihazı DEĞİL, çağıranın bütün oturumlarını kapatır: telefondan
// çıkış yapan yönetici, dizüstündeki oturumunu da kapatmış olur. Sınır
// gerçektir ve gizlenmemelidir — "çıkış yaptım" sanan kullanıcının aslında ne
// yaptığını bilmesi gerekir.
//
// Tek cihazı düşürmek EKLENMEMİŞTİR çünkü jeton durumsuzdur: "şu jetonu
// düşür" demek, jti bazlı bir kara liste, yani her istekte okunan ve süresi
// dolmuş kayıtları temizlenen YENİ BİR DEPO demektir. Bugünkü ihtiyaç —
// "cihazımı kaybettim, her yerden çıkış yap" — toptan iptalle zaten
// karşılanıyor; ayrım gerçekten gerektiğinde eklenir (bkz. dosya başı).
//
// # Yalnızca KİMLİK ister
//
// Kendi oturumunu kapatmak bir ayrıcalık değildir: yetkisi hiç olmayan bir
// kullanıcı da çıkış yapabilmelidir. Uca yetki konsaydı, yetkisi geri alınmış
// bir yöneticinin elindeki jeton süresi dolana kadar kapatılamaz hâle
// gelirdi — yani yetkiyi kaybetmek oturumu da kapatılamaz yapardı.
//
// # API anahtarı çıkış YAPAMAZ
//
// Çağıran bir API anahtarıysa istek tipli bir hata ile reddedilir
// ([CodeNoSession]). Anahtarın oturumu yoktur: jetonla değil, kalıcı bir sırla
// gelir ve o sır bu çağrıdan sonra da çalışmaya devam ederdi. Sessizce
// başarılı dönmek, çağırana anahtarın kapatıldığı YANILGISINI bırakırdı;
// anahtarı kapatmanın yolu POST /admin/v1/api-keys/{id}/revoke ucudur.
//
// # Sınır durumu
//
// Karşılaştırma saniye çözünürlüklüdür: çıkışla AYNI saniyede üretilmiş bir
// jeton hayatta kalır (gerekçe [parsedToken.issuedBefore]). Ters tercih,
// çıkıştan hemen sonra yeniden giriş yapan kullanıcının TAZE jetonunu
// düşürürdü. Kabul edilen bedel, çıkışın etkisinin en fazla o saniyenin sonuna
// kadar gecikmesidir; 12 saatlik jeton ömrünün yanında ölçülemez.
//
// Dönen an, kimliğe YAZILAN çapadır: istemci elindeki bir jetonun bu andan
// önce mi üretildiğini kendisi görebilir.
func (s *Service) Logout(ctx context.Context, principalID, principalKind string) (time.Time, error) {
	if err := s.ready(); err != nil {
		return time.Time{}, err
	}
	if principalKind != PrincipalKindUser {
		return time.Time{}, errors.Invalid(CodeNoSession,
			"%q türündeki çağıranın kapatılabilecek bir oturumu yok; "+
				"api anahtarı POST /admin/v1/api-keys/{id}/revoke ucundan iptal edilir",
			principalKind)
	}
	if err := requireID(principalID, models.UserIDPrefix, "kullanıcı kimliği"); err != nil {
		return time.Time{}, err
	}

	// Kimlik kaydı yoksa errors.NotFound döner. Bu yol normalde imkânsızdır:
	// çağıranın jetonu, kimlik kaydı okunarak doğrulanmıştır
	// (bkz. [Service.principalFromToken]). Yine de sessizce başarılı DÖNÜLMEZ —
	// dönülseydi, çıkışın hiçbir şey yazmadığı bir arıza (yanlış sağlayıcı adı,
	// silinmiş kimlik) 200 yanıtının arkasında görünmez kalırdı.
	identity, err := s.repo.RevokeSessions(
		ctx, principalID, models.ProviderEmailPass, s.clock(),
	)
	if err != nil {
		return time.Time{}, err
	}

	revokedAt := sessionAnchor(identity)
	s.log.InfoContext(ctx, "yönetim oturumları kapatıldı",
		slog.String("user_id", principalID),
		slog.Time("revoked_at", revokedAt),
	)
	return revokedAt, nil
}
