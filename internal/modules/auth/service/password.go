package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// Bu dosya modülün parola kararlarını taşır.
//
// # Neden bcrypt (argon2id değil)
//
// İkisi de kabul edilebilir seçimlerdir; bcrypt üç somut nedenle seçilmiştir:
//
//  1. Maliyet parametresi hash'in İÇİNDE kodludur. Donanım hızlandıkça
//     maliyet artırılır ve eski hash'ler geçersiz OLMAZ — her hash kendi
//     maliyetiyle doğrulanır, kullanıcı bir sonraki girişinde sessizce yeni
//     maliyete taşınabilir. argon2id'nin x/crypto'daki API'si ham bayt döner;
//     tuz ve üç parametre (bellek, tur, paralellik) ELLE kodlanıp ayrıştırılmak
//     zorundadır. O kodlama bu modülün yazacağı ve bir daha hiç
//     değiştiremeyeceği bir biçim olurdu; hatası da sessiz olurdu.
//  2. Doğrulama tek çağrıdır ve tuz yönetimi kütüphanededir; elle tuz üreten
//     her satır, bir gün sabit tuz yazma riskidir.
//  3. bcrypt'in bilinen sınırı (72 bayt) AÇIKÇA uygulanır (bkz.
//     [MaxPasswordLen]); argon2id'nin böyle bir sınırı yoktur ama onun yerine
//     bellek parametresini yanlış seçmenin sessiz riski gelir.
//
// argon2id'nin GPU'ya karşı üstünlüğü gerçektir; bu modül için belirleyici
// olmamasının nedeni, parolaların yalnızca YÖNETİM kullanıcılarına ait olması
// ve sayılarının onlarla ölçülmesidir. Karar ileride değiştirilirse
// [Service.SetPassword] ile [Service.Login] tek dokunma noktasıdır.
//
// # Neden kilit var (ve neden kısa)
//
// Art arda başarısız denemede kimlik [Options.LoginLockDuration] süresince
// kilitlenir. Karar ikili bir tercihtir:
//
//   - Hiç sayaç olmasaydı saldırgan, bilinen bir admin e-postasına karşı
//     yalnızca bcrypt hızıyla sınırlı olurdu; maliyet 12'de çekirdek başına
//     ~4 deneme/saniye, günde yüz binlerce deneme eder. Zayıf bir parola için
//     bu yeterlidir.
//   - Kalıcı kilit ise hedefli bir hizmet dışı bırakma aracıdır: saldırgan
//     admin'in e-postasını bilirse hesabı süresiz kapatabilirdi.
//
// Bu yüzden kilit KISA ve KENDİLİĞİNDEN AÇILIR; yönetici müdahalesi
// gerektirmez. Kilit hesap başınadır; IP başına hız sınırı BU MODÜLÜN İŞİ
// DEĞİLDİR ve Faz 9'un "rate limiting" middleware'ine bırakılmıştır — ikisi
// birbirinin yerine geçmez, tamamlar.
//
// Kilitli hesap, kilitsiz bir yanlış paroladan AYIRT EDİLEMEZ: aynı hata, aynı
// süre. "Hesabınız kilitli" demek, hesabın var olduğunu doğrulamak olurdu.
// Yönetici durumu log'dan görür.

// dummyPassword zamanlama eşitliği için kullanılan sabit kukla parolasıdır.
//
// Bu bir kimlik bilgisi DEĞİLDİR: hiçbir hesaba ait değildir, hiçbir yere
// yazılmaz ve tek işi bcrypt karşılaştırmasının süresini üretmektir.
const dummyPassword = "gobit-kukla-parola-zamanlama-esitligi-icin" //nolint:gosec // G101: hesaba ait olmayan, yalnızca zamanlama için kullanılan sabit

// newDummyHash verilen maliyette bir kukla hash üreticisi kurar.
//
// Üretim TEMBELDİR (sync.OnceValue): maliyet 12'de bir bcrypt ~250 ms sürer ve
// bunu her servis kurulumunda ödemek, açılışı ve her birim testini yavaşlatırdı.
// Bedeli, ilk "kullanıcı yok" denemesinin bir kez daha yavaş olmasıdır; tek
// seferlik bu sapma hiçbir hesabın varlığını ele vermez.
func newDummyHash(cost int) func() []byte {
	return sync.OnceValue(func() []byte {
		hash, err := bcrypt.GenerateFromPassword([]byte(dummyPassword), cost)
		if err != nil {
			// [New] maliyeti aralık içine çektiği için buraya düşülmez;
			// düşülürse [Service.equalizeTiming] eşdeğer maliyetli bir yola
			// geçer, sessizce hızlanmaz.
			return nil
		}
		return hash
	})
}

// equalizeTiming kimlik bulunamadığında da bir bcrypt turu çalıştırır.
//
// Zamanlama eşitliğinin tamamı buradadır: "kullanıcı yok", "kimlik yok",
// "parola atanmamış" ve "hesap kilitli" dallarının hepsi bu çağrıyı yapar,
// böylece dört durum da gerçek bir parola karşılaştırması kadar sürer.
// Yapılmasaydı, yanıt süresi "bu e-posta kayıtlı mı?" sorusunun cevabı olur ve
// saldırgan yönetici e-postalarını sayabilirdi.
//
// Sonuç bilinçli olarak YOK SAYILIR; çağıranın dalı zaten bellidir.
func (s *Service) equalizeTiming(password string) {
	if hash := s.dummyHash(); len(hash) > 0 {
		_ = bcrypt.CompareHashAndPassword(hash, []byte(password))
		return
	}
	// Kukla hash üretilemediyse eşdeğer maliyetli ikinci yol: üretim de
	// doğrulama da aynı sayıda bcrypt turu çalıştırır.
	_, _ = bcrypt.GenerateFromPassword([]byte(dummyPassword), s.cost)
}

// hashPassword parolayı yapılandırılmış maliyette bcrypt ile hash'ler.
//
// Hata mesajında parola GEÇMEZ.
func (s *Service) hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cost)
	if err != nil {
		return "", errors.Wrap(err, errors.KindInternal, CodeWeakPassword,
			"parola hash'lenemedi")
	}
	return string(hash), nil
}

// SetPassword kullanıcının parolasını belirler; giriş kimliği yoksa oluşturur.
//
// Parola politikası [validatePassword] ile uygulanır. Düz parola yalnızca bu
// çağrının içinde yaşar: hash'lenir, hash saklanır ve parolanın kendisi hiçbir
// yapıya, log satırına ya da hata mesajına geçmez.
//
// Başarılı çağrı kilit sayaçlarını da sıfırlar: parolasını değiştiren
// kullanıcı, eski parolayla yapılmış denemelerin bıraktığı kilitle
// karşılaşmamalıdır.
//
// # Mevcut oturumlar DÜŞER
//
// Yazma, kimliğin [sessionAnchor] değerini bu ana taşır ve daha önce üretilmiş
// oturum jetonları bir sonraki isteklerinde reddedilir
// (bkz. [Service.principalFromToken]). Bu, parola değişiminin yan etkisi değil
// AMACIDIR: sızmış bir yönetici jetonu, parola değişmemişse süresi dolana
// kadar geçerli kalırdı.
//
// İlerletilen satır YALNIZCA [models.ProviderEmailPass] kimliğininkidir; parola
// o kimliğin bilgisidir ve başka sağlayıcıların satırlarında karşılığı yoktur.
// Yine de düşen oturumlar HEPSİDİR, çünkü doğrulama çapayı sağlayıcıya göre
// seçmez: kullanıcının en yeni çapasını okur. Bu yüzden çıkışın aksine burada
// bütün satırlara dokunmak gerekmez.
//
// Oturumları kapatmak için parolayı değiştirmek GEREKMEZ: aynı çapayı
// kimlik bilgisine dokunmadan ilerleten uç [Service.Logout]'tur.
func (s *Service) SetPassword(ctx context.Context, userID, password string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(userID, models.UserIDPrefix, "kullanıcı kimliği"); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}

	user, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}

	hash, err := s.hashPassword(password)
	if err != nil {
		return err
	}

	if _, err := s.repo.SetPasswordHash(
		ctx, user.ID, models.ProviderEmailPass, user.Email, hash, s.clock(),
	); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "kullanıcı parolası güncellendi",
		slog.String("user_id", user.ID),
	)
	return nil
}

// Login e-posta ve parolayla kimlik doğrular ve bir oturum jetonu üretir.
//
// # Tek hata, tek süre
//
// Başarısız her yol AYNI hatayı ([CodeInvalidCredentials]) ve KABACA AYNI
// süreyi üretir:
//
//	kullanıcı yok · giriş kimliği yok · parola atanmamış · hesap kilitli ·
//	parola yanlış
//
// Ayrım yapılsaydı, yanıtın kendisi "bu e-posta kayıtlı" bilgisini verirdi ve
// saldırgan önce geçerli yönetici adreslerini toplar, sonra yalnızca onlara
// yüklenirdi. Süre eşitliği [Service.equalizeTiming] ile sağlanır; gerçek
// gerekçe yalnızca LOG'a yazılır ve istemciye gitmez.
//
// Süre eşitliği kabaca sağlanır: dallar arasında bir veritabanı sorgusu
// farkı kalabilir, ama o fark (sub-milisaniye) süreye hâkim olan bcrypt
// turunun (yüz milisaniyeler) yanında ölçülemez.
//
// # Dönüş
//
// Jeton ve son kullanma anı döner. Jeton bir SIRDIR; çağıran onu loglamamalı,
// yalnızca yanıt gövdesinde iletmelidir.
func (s *Service) Login(ctx context.Context, email, password string) (string, time.Time, error) {
	if err := s.ready(); err != nil {
		return "", time.Time{}, err
	}
	// Boş girdi bir hesap sorusu DEĞİL, istemci hatasıdır; ayrı hata dönmesi
	// hiçbir hesabın varlığını ele vermez.
	if email == "" || password == "" {
		return "", time.Time{}, errors.Invalid(CodeInvalidInput,
			"e-posta ve parola zorunludur")
	}

	normalized, err := normalizeEmail(email)
	if err != nil {
		// Biçimi bozuk bir e-posta hiçbir hesapla eşleşemez; yine de kimlik
		// bilgisi hatası olarak ve eşit sürede döner, çünkü bu yol bir hesap
		// SORGUSUDUR ve biçim hatası ile "yok" arasındaki fark saldırgana
		// hangi adreslerin denenmeye değer olduğunu söylerdi.
		s.equalizeTiming(password)
		return "", time.Time{}, s.failLogin(ctx, "e-posta biçimi geçersiz", "")
	}

	user, err := s.repo.GetUserByEmail(ctx, normalized)
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", time.Time{}, err
		}
		s.equalizeTiming(password)
		return "", time.Time{}, s.failLogin(ctx, "kullanıcı bulunamadı", "")
	}

	identity, err := s.repo.GetIdentity(ctx, user.ID, models.ProviderEmailPass)
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", time.Time{}, err
		}
		s.equalizeTiming(password)
		return "", time.Time{}, s.failLogin(ctx, "giriş kimliği yok", user.ID)
	}

	now := s.clock()
	if identity.IsLocked(now) {
		s.equalizeTiming(password)
		return "", time.Time{}, s.failLogin(ctx, "hesap geçici olarak kilitli", user.ID)
	}
	if identity.PasswordHash == "" {
		// Parolası atanmamış bir kimlik (örn. yalnızca OAuth) parola ile
		// giremez. Boş hash'i bcrypt'e vermek "hash çok kısa" hatası döndürür
		// ve o dal ÖLÇÜLEBİLİR biçimde hızlıdır.
		s.equalizeTiming(password)
		return "", time.Time{}, s.failLogin(ctx, "parola atanmamış", user.ID)
	}

	if cmpErr := bcrypt.CompareHashAndPassword(
		[]byte(identity.PasswordHash), []byte(password),
	); cmpErr != nil {
		s.registerFailure(ctx, identity.ID, now)
		return "", time.Time{}, s.failLogin(ctx, "parola eşleşmedi", user.ID)
	}

	if err := s.repo.RegisterLoginSuccess(ctx, identity.ID, now); err != nil {
		// Sayaç temizlenemediyse giriş yine de geçerlidir; kullanıcıyı
		// içeri almamak, sayaç yazımının geçici bir hatası yüzünden yönetimi
		// kilitlemek olurdu. Durum loglanır.
		s.log.WarnContext(ctx, "başarılı giriş kaydedilemedi",
			slog.String("user_id", user.ID), slog.Any("error", err))
	}

	token, expiresAt, err := s.issueToken(user.ID, user.Scopes, now)
	if err != nil {
		return "", time.Time{}, err
	}

	s.log.InfoContext(ctx, "yönetim girişi başarılı",
		slog.String("user_id", user.ID),
	)
	return token, expiresAt, nil
}

// failLogin başarısız girişin gerekçesini LOGLAR ve genel hatayı döner.
//
// Gerekçe istemciye gitmez; e-posta ve parola log'a da yazılmaz (plan Bölüm 8:
// hassas veri loglanmaz). Kullanıcı biliniyorsa yalnızca kimliği yazılır.
func (s *Service) failLogin(ctx context.Context, reason, userID string) error {
	attrs := []any{slog.String("reason", reason)}
	if userID != "" {
		attrs = append(attrs, slog.String("user_id", userID))
	}
	s.log.WarnContext(ctx, "yönetim girişi reddedildi", attrs...)

	return errors.Unauthorized(CodeInvalidCredentials, "e-posta ya da parola hatalı")
}

// registerFailure başarısız denemeyi sayar; sayım hatası girişi etkilemez.
//
// Sayaç yazılamazsa istek yine de reddedilir — sayaç bir koruma katmanıdır,
// doğruluğun kaynağı değil.
func (s *Service) registerFailure(ctx context.Context, identityID string, now time.Time) {
	identity, err := s.repo.RegisterLoginFailure(
		ctx, identityID, s.threshold, now.Add(s.lockFor), now,
	)
	if err != nil {
		s.log.WarnContext(ctx, "başarısız giriş sayacı güncellenemedi",
			slog.String("identity_id", identityID), slog.Any("error", err))
		return
	}
	if identity.IsLocked(now) {
		s.log.WarnContext(ctx, "hesap geçici olarak kilitlendi",
			slog.String("user_id", identity.UserID),
			slog.Int("failed_attempts", identity.FailedAttempts),
			slog.Time("locked_until", *identity.LockedUntil),
		)
	}
}
