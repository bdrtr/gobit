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
// # Çapa KİMLİK başınadır, kullanıcı başına değil
//
// auth_identity tablosu sağlayıcı başına satır tutar, dolayısıyla bir
// kullanıcının birden çok çapası olabilir. İki uç da bu çokluğu görür ve AYNI
// kuralı uygular: çıkış hepsini birden ilerletir ([Service.Logout]), jeton
// doğrulaması ise en YENİ olanı okur ([latestAnchor], Repository.SessionAnchor).
// Yalnızca biri sağlayıcıya göre seçseydi öteki boşa çalışırdı — çıkışın
// yazdığı çapa hiç okunmaz ya da okunan çapa hiç yazılmaz olurdu.
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

// latestAnchor kimlik kümesinin EN YENİ oturum çapasını döner; küme boşsa sıfır
// zaman.
//
// EN YENİ olan seçilir, en eski değil. Çapalar sağlayıcı başına ilerler ve
// hepsi birlikte ilerlemez: parola değişimi yalnızca emailpass satırını yazar.
// En eskiyi almak, hiç ilerlemeyen tek bir satırın iptalin tamamını etkisiz
// bırakması demek olurdu. Aynı seçim okuma tarafında SQL'de yapılır
// (queries/identities.sql, GetSessionAnchor); iki uç aynı kuralı uygular.
//
// Boş küme çağıranın eleyeceği bir durumdur: kimliği olmayan kullanıcının
// çıkışı depo katmanında errors.NotFound ile reddedilir.
func latestAnchor(identities []models.AuthIdentity) time.Time {
	var latest time.Time
	// Dizin ile dolaşılır: [models.AuthIdentity] büyük bir yapıdır ve değerle
	// dolaşmak her turda gereksiz kopya üretirdi.
	for i := range identities {
		if anchor := sessionAnchor(identities[i]); anchor.After(latest) {
			latest = anchor
		}
	}
	return latest
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
// # TÜM sağlayıcılar düşer
//
// Çapa kullanıcının BÜTÜN kimlik satırlarında ilerletilir, yalnızca
// [models.ProviderEmailPass] olanda değil. Bugün gözlemlenebilir bir fark
// YOKTUR: tek sağlayıcı odur, dolayısıyla ilerletilen satır sayısı birdir ve
// uç aynı yanıtı verir. Kazanılan şey gelecekteki sessiz açığın kapanmasıdır —
// OAuth eklendiği gün tek bir sağlayıcı seçen bir çıkış, öteki sağlayıcıdan
// alınmış jetonları DÜŞÜRMEZ ve bunu haber vermeden yapardı: 204 alan
// kullanıcı hâlâ oturumda kalırdı.
//
// Zincirin öteki ucu da aynı kuralı uygular: jeton doğrulanırken çapa tek bir
// sağlayıcıdan değil, kullanıcının en yeni kimliğinden okunur
// (bkz. [Service.principalFromToken]). Yalnızca burası değişseydi yazılan
// fazladan çapa hiç okunmaz ve değişiklik hiçbir işe yaramazdı.
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

	// Hiç kimlik kaydı yoksa errors.NotFound döner. Bu yol normalde
	// imkânsızdır: çağıranın jetonu, çapası okunarak doğrulanmıştır
	// (bkz. [Service.principalFromToken]). Yine de sessizce başarılı DÖNÜLMEZ —
	// dönülseydi, çıkışın hiçbir şey yazmadığı bir arıza (silinmiş kimlik)
	// 204 yanıtının arkasında görünmez kalırdı.
	identities, err := s.repo.RevokeSessions(ctx, principalID, s.clock())
	if err != nil {
		return time.Time{}, err
	}

	revokedAt := latestAnchor(identities)
	s.log.InfoContext(ctx, "yönetim oturumları kapatıldı",
		slog.String("user_id", principalID),
		slog.Time("revoked_at", revokedAt),
		// Kaç kimliğin ilerletildiği loglanır: ikinci bir sağlayıcı eklendiği
		// gün çıkışın gerçekten hepsine dokunduğu ancak buradan görülür.
		slog.Int("identity_count", len(identities)),
	)
	return revokedAt, nil
}
