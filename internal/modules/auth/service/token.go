package service

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// signingMethod oturum jetonlarının TEK kabul edilen imzalama yöntemidir.
//
// HS256 seçilmiştir çünkü jetonu üreten ve doğrulayan taraf aynı süreçtir;
// asimetrik bir imza (RS256) yalnızca doğrulayanın imzalayamaması gerektiğinde
// değer taşır ve burada öyle bir taraf yoktur.
var signingMethod = jwt.SigningMethodHS256

// tokenClaims oturum jetonunun taşıdığı iddialardır.
//
// Kayıtlı iddialar (sub, exp, iat, iss) gömülüdür; scopes bu modülün eklediği
// tek özel iddiadır. Jetona e-posta ya da ad KONMAZ: jetonun gövdesi
// imzalıdır ama ŞİFRELİ DEĞİLDİR ve base64 çözen herkes okuyabilir. Yetki
// kararı için gereken en az bilgi taşınır.
type tokenClaims struct {
	// Scopes çağıranın yetkileridir.
	Scopes []string `json:"scopes"`
	jwt.RegisteredClaims
}

// issueToken kullanıcı için imzalı bir oturum jetonu üretir.
//
// Doldurulan iddialar: sub (kullanıcı kimliği), scopes, iat, exp, iss.
// "nbf" bilinçli olarak yazılmaz — jeton üretildiği anda geçerlidir ve
// gelecekte başlayan bir oturumun bu akışta karşılığı yoktur.
//
// İmza sırrı yoksa errors.Unavailable döner: imzasız ya da sabit sırlı bir
// jeton üretmektense hiç üretmemek doğrudur.
func (s *Service) issueToken(userID string, scopes []string, now time.Time) (string, time.Time, error) {
	if len(s.secret) == 0 {
		return "", time.Time{}, errors.Unavailable(CodeSecretMissing,
			"JWT imza sırrı yapılandırılmamış; oturum jetonu üretilemez")
	}

	expiresAt := now.Add(s.tokenTTL)
	claims := tokenClaims{
		Scopes: scopes,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	signed, err := jwt.NewWithClaims(signingMethod, claims).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, errors.Wrap(err, errors.KindInternal, CodeTokenInvalid,
			"oturum jetonu imzalanamadı")
	}
	return signed, expiresAt, nil
}

// parsedToken doğrulanmış bir jetonun taşıdığı bilgidir.
type parsedToken struct {
	// Subject jetonun sahibi kullanıcının kimliğidir.
	Subject string
	// Scopes jetonun taşıdığı yetkilerdir.
	Scopes []string
	// IssuedAt jetonun üretildiği andır ("iat", UTC).
	//
	// Alan yalnızca bilgi değildir, oturum İPTALİNİN dayanağıdır: parola
	// değiştiğinde bu andan öncesinde üretilmiş jetonlar reddedilir
	// (bkz. [parsedToken.issuedBefore] ve [Service.principalFromToken]).
	// Çözünürlüğü SANİYEDİR.
	IssuedAt time.Time
}

// issuedBefore jetonun verilen andan KESİN OLARAK önce üretildiğini bildirir.
//
// # Neden "kesin olarak"
//
// "iat" iddiası saniye çözünürlüklüdür (jwt.TimePrecision): 10:00:00.900'de
// imzalanan bir jeton 10:00:00 taşır. Yani jetonun gerçek üretim anı tek bir
// nokta değil, [iat, iat+1sn) ARALIĞIDIR ve bu aralık karşılaştırılan anla aynı
// saniyeye düşerse hangisinin önce olduğu jetondan OKUNAMAZ.
//
// Belirsiz saniye KULLANILABİLİRLİK lehine çözülür: yalnızca aralığın tamamı
// moment'ten önceyse "önce" denir. Ters tercih — güvenlik lehine, belirsiz
// saniyeyi de reddetmek — parolasını değiştirip hemen giriş yapan kullanıcının
// TAZE jetonunu düşürürdü: giriş 200 döner, o jetonla atılan ilk istek 401 alır
// ve kullanıcı bir saniye bekleyip tekrar denemek zorunda kalırdı. Kurulum
// betikleri (kullanıcı yarat → parola ata → giriş yap) tam olarak bu aralıkta
// çalışır, yani sapma nadir değil TİPİKTİR.
//
// Kabul edilen bedel, moment ile AYNI saniyede üretilmiş bir jetonun hayatta
// kalmasıdır. İptalin hedeflediği senaryoda — dakikalar ya da saatler önce
// sızmış bir jeton — böyle bir çakışma yoktur; saldırganın bundan yararlanması
// için jetonu, kurbanın parolayı değiştirdiği saniyenin İÇİNDE elde etmiş
// olması gerekirdi.
func (p parsedToken) issuedBefore(moment time.Time) bool {
	return !p.IssuedAt.Add(jwt.TimePrecision).After(moment)
}

// parseToken jetonu doğrular ve iddialarını döner.
//
// # Reddedilen saldırılar
//
// Doğrulama şu dört şeyi AÇIKÇA yapar ve dördü de kütüphanenin varsayılanına
// bırakılmaz:
//
//  1. "alg: none" ve ALGORİTMA KARIŞIKLIĞI. jwt.WithValidMethods yalnızca
//     HS256'yı kabul eder; ayrıca keyfunc içinde imzalama yönteminin
//     *jwt.SigningMethodHMAC olduğu bir kez daha denetlenir. İki kapı da
//     gereklidir: yöntem listesi bir gün gevşetilirse ikinci kapı, saldırganın
//     "alg: RS256" yazıp HMAC sırrını genel anahtar gibi kullanmasını yine de
//     engeller.
//  2. SÜRE. jwt.WithExpirationRequired, "exp" iddiası OLMAYAN bir jetonu
//     reddeder. Bu şart olmadan süresiz bir jeton üretmek, imzayı ele
//     geçirmeden yalnızca bir alanı atlamakla mümkün olurdu.
//  3. ÜRETİCİ. jwt.WithIssuer, başka bir sistemin aynı sırla ürettiği bir
//     jetonun buraya kabul edilmesini engeller.
//  4. ÜRETİM ANININ EKSİKLİĞİ. "iat" iddiası ZORUNLU sayılır. Kütüphanenin
//     jwt.WithIssuedAt seçeneği iddiayı yalnızca VARSA doğrular (gelecekte
//     olmadığını); yokluğunu sessizce geçer ve değer sıfıra düşerdi. Oturum
//     iptali bu değere dayanır (bkz. [Service.principalFromToken]) ve sıfır
//     bir "iat" bugün TESADÜFEN doğru sonucu verir: sıfır zaman her parola
//     değişiminden öncedir, jeton reddedilir. Denetim yine de açıkça yazılır
//     ki ret bir tesadüfe değil bir KURALA dayansın — karşılaştırma bir gün
//     değişirse (örneğin "iat yoksa denetimi atla" gibi bir kolaylık
//     eklenirse) eksik iddia sessizce kabule dönüşmemelidir.
//
// İmzanın kendisi kütüphane tarafından sabit zamanlı karşılaştırılır.
//
// Doğrulama bu fonksiyonda BİTMEZ: jetonun sahibi kullanıcının hâlâ var olup
// olmadığı ve jetonun parola değişiminden ÖNCE üretilip üretilmediği çağıran
// tarafta sorulur (bkz. interop.go).
func (s *Service) parseToken(raw string) (parsedToken, error) {
	if len(s.secret) == 0 {
		return parsedToken{}, errors.Unavailable(CodeSecretMissing,
			"JWT imza sırrı yapılandırılmamış; oturum jetonu doğrulanamaz")
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{signingMethod.Alg()}),
		jwt.WithIssuer(s.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(0),
		jwt.WithTimeFunc(s.clock),
	)

	var claims tokenClaims
	token, err := parser.ParseWithClaims(raw, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("beklenmeyen imzalama yöntemi: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		// Hatanın ayrıntısı (süresi geçmiş mi, imza mı tutmadı) istemciye
		// GİTMEZ; çağıran bunu loglar. Fark, saldırgana hangi denemesinin
		// hangi aşamada takıldığını söylerdi.
		return parsedToken{}, errors.Wrap(err, errors.KindUnauthorized, CodeTokenInvalid,
			"oturum jetonu geçersiz")
	}
	if !token.Valid {
		return parsedToken{}, errors.Unauthorized(CodeTokenInvalid, "oturum jetonu geçersiz")
	}
	if claims.Subject == "" {
		return parsedToken{}, errors.Unauthorized(CodeTokenInvalid,
			"oturum jetonunda kullanıcı kimliği yok")
	}
	if claims.IssuedAt == nil {
		return parsedToken{}, errors.Unauthorized(CodeTokenInvalid,
			"oturum jetonunda üretim anı yok")
	}

	return parsedToken{
		Subject:  claims.Subject,
		Scopes:   claims.Scopes,
		IssuedAt: claims.IssuedAt.UTC(),
	}, nil
}
