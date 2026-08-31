package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// Bu dosya auth'un DIŞARIYA açtığı iki yüzeyi taşır.
//
// # 1. Kimlik doğrulama yüzeyi
//
// [Interop], çekirdeğin corehttp.Authenticator arayüzünü YAPISAL olarak
// karşılar ve container'a "auth.interop" adıyla kaydedilir. Çekirdek onu ADLA
// çözer; auth modülünü import ETMEZ (Prensip 2.4, ADR 0001).
//
// corehttp bir MODÜL DEĞİL ÇEKİRDEKTİR, bu yüzden buradan import edilebilir.
// corehttp.Principal tipi bu pakette YENİDEN TANIMLANMAZ: aynı adı taşıyan
// ikinci bir tip, yapısal uyumu kırar ve çekirdek arayüzü karşılanamazdı.
//
// # 2. Modüller arası ilkel yüzey
//
// Diğer modüller (örn. product'ın satış kanalına göre katalog süzmesi) auth'u
// import EDEMEZ; bu yüzden onlara açılan metotlar YALNIZCA ilkel ve stdlib
// tipleri kullanır ve tüketici kendi paketinde aynı imzayı tekrar tanımlar:
//
//	// product modülünde, auth import EDİLMEDEN:
//	type SalesChannelReader interface {
//	    ActiveSalesChannelIDs(ctx context.Context) ([]string, error)
//	}
//	channels, err := container.Resolve[SalesChannelReader](c, "auth.service")
//
// Yüzey bilinçli olarak DARDIR: buraya eklenen her metot auth'un bir daha
// değiştiremeyeceği bir sözleşmedir. Bir kanalın tüm alanları gerekiyorsa
// doğru yol yeni bir ilkel metot değil, Query katmanıdır (bkz. provider.go).

// Principal.Kind değerleri; çekirdeğin beklediği sözlük budur.
const (
	// PrincipalKindUser kimliğin bir yönetim kullanıcısı olduğunu bildirir.
	PrincipalKindUser = "user"
	// PrincipalKindAPIKey kimliğin bir API anahtarı olduğunu bildirir.
	PrincipalKindAPIKey = "api_key"
)

// schemeBearer yönetim yüzeyinin kabul ettiği tek Authorization şemasıdır.
const schemeBearer = "bearer"

// jwtSegmentCount bir JWT'nin nokta ile ayrılmış bölüm sayısıdır.
const jwtSegmentCount = 3

// Interop auth'un çekirdeğe açtığı kimlik doğrulama yüzeyidir.
//
// Çekirdekteki corehttp.Authenticator arayüzünü yapısal olarak karşılar;
// arayüz TÜKETİCİ tarafında (çekirdekte) tanımlıdır, bu tip yalnızca imzayı
// taşır (ADR 0001).
type Interop struct {
	svc *Service
}

var _ corehttp.Authenticator = (*Interop)(nil)

// NewInterop verilen servis üzerinde çalışan kimlik doğrulayıcıyı üretir.
func NewInterop(svc *Service) *Interop {
	return &Interop{svc: svc}
}

// AuthenticateAdmin yönetim yüzeyinin kimliğini çözer.
//
// # Kabul edilen kimlikler ve SIRA
//
// Şema yalnızca "Bearer" olabilir. Kimlik bilgisi iki biçimden biridir ve
// sırayla denenir:
//
//  1. OTURUM JETONU (JWT) — normal, insan yolu: yönetici giriş yapar, jeton
//     alır, jetonla gezer. Önce denenir çünkü yaygın olan budur. Jetonun
//     kendisi bir ARAMA gerektirmez; imzası doğrulandıktan sonra iki indeksli
//     okuma yapılır (sahibi hâlâ var mı, oturumu jetondan sonra düşürüldü mü —
//     bkz. [Service.principalFromToken]).
//  2. GİZLİ API ANAHTARI — makineden makineye yol: betikler ve entegrasyonlar.
//
// Sıra bir DENEME-YANILMA DEĞİLDİR. İki biçim sözdizimsel olarak ayrıktır:
// JWT tam iki nokta içerir, API anahtarı "sk_" ile başlar ve nokta içermez.
// Bu yüzden dallanma kesindir; ikisini de sırayla çalıştırıp ilk başarılıyı
// almak, her yanlış kimlikte gereksiz bir veritabanı araması yapmak ve
// publishable bir anahtarın yönetim yüzeyinde yoklanmasına izin vermek olurdu.
//
// Publishable anahtar BURADA KABUL EDİLMEZ: "pk_" öneki ne JWT'ye benzer ne de
// "sk_" ile başlar, dolayısıyla daha ilk adımda elenir. Elenmese bile
// [Service.authenticateKey] içindeki tür denetimi ikinci kapı olarak durur.
//
// Her başarısızlık errors.Unauthorized döner; gerekçe çekirdeğin middleware'i
// tarafından loglanır ve istemciye SIZDIRILMAZ.
func (i *Interop) AuthenticateAdmin(
	ctx context.Context,
	scheme, credential string,
) (corehttp.Principal, error) {
	if i == nil || i.svc == nil {
		return corehttp.Principal{}, errors.Unavailable(CodeUnconfigured, "auth servisi kurulmamış")
	}
	if !strings.EqualFold(scheme, schemeBearer) {
		return corehttp.Principal{}, errors.Unauthorized(CodeInvalidCredentials,
			"%q şeması desteklenmiyor; \"Bearer\" bekleniyor", scheme)
	}

	credential = strings.TrimSpace(credential)
	switch {
	case looksLikeJWT(credential):
		return i.svc.principalFromToken(ctx, credential)
	case strings.HasPrefix(credential, models.SecretKeyPrefix):
		return i.svc.principalFromSecretKey(ctx, credential)
	default:
		return corehttp.Principal{}, errors.Unauthorized(CodeInvalidCredentials,
			"kimlik bilgisi ne oturum jetonu ne de gizli api anahtarı biçiminde")
	}
}

// AuthenticateStore mağaza yüzeyinin kimliğini çözer.
//
// Yalnızca PUBLISHABLE anahtar kabul edilir. Gizli bir anahtar burada
// REDDEDİLİR ve bu iki bağımsız kapıyla sağlanır: "sk_" öneki beklenen "pk_"
// önekiyle eşleşmez ve kayıttaki tür alanı da tutmaz.
//
// Publishable anahtar bir SIR DEĞİLDİR; tek işi isteği bir satış kanalına
// bağlamaktır. Bu yüzden dönen kimliğe HİÇBİR YETKİ konmaz: mağaza yüzeyinden
// gelen bir istek, anahtar kaydında yetki yazsa bile yönetim ucuna geçemez.
//
// İptal edilmiş anahtar ve etkin kanalı kalmamış anahtar reddedilir
// (bkz. [Service.authenticatePublishable]).
func (i *Interop) AuthenticateStore(ctx context.Context, key string) (corehttp.Principal, error) {
	if i == nil || i.svc == nil {
		return corehttp.Principal{}, errors.Unavailable(CodeUnconfigured, "auth servisi kurulmamış")
	}

	apiKey, channelIDs, err := i.svc.authenticatePublishable(ctx, strings.TrimSpace(key))
	if err != nil {
		return corehttp.Principal{}, err
	}

	return corehttp.Principal{
		ID:   apiKey.ID,
		Kind: PrincipalKindAPIKey,
		// Yetki listesi BİLEREK boştur; kayıttaki değer okunmaz bile. Bu,
		// publishable anahtarın yetki taşımadığı kuralının ÜÇÜNCÜ kapısıdır
		// (ilk ikisi: oluşturmada reddetme, tür denetimi).
		Scopes:          nil,
		SalesChannelIDs: channelIDs,
	}, nil
}

// principalFromToken oturum jetonundan kimlik kurar.
//
// # Yetkiler jetondan DEĞİL veritabanından okunur
//
// Jeton "scopes" iddiasını taşır ama yetki kararı ona bakılarak VERİLMEZ:
// kullanıcının kaydındaki güncel yetkiler okunur. Aksi hâlde bir yöneticinin
// yetkisi geri alındıktan sonra elindeki jeton, süresi dolana kadar (varsayılan
// 12 saat) eski yetkiyle çalışmaya devam ederdi. Jetondaki liste yalnızca
// istemcinin arayüzü çizmesine yarayan bir kopyadır.
//
// # Kullanıcının hâlâ var olduğu sorulur
//
// İmzası geçerli bir jeton, sahibi SİLİNMİŞSE kabul edilmez. Sorgu birincil
// anahtar üzerinden tek okumadır; bedeli, silinen bir yöneticinin 12 saat
// boyunca içeride kalabilmesinin bedelinin yanında hiçtir.
//
// # Çıkış ve parola değişimi jetonu DÜŞÜRÜR
//
// Jeton, sahibinin oturum çapası jetonun üretiminden SONRA ilerlediyse
// reddedilir. Çapayı iki iş ilerletir: çıkış ([Service.Logout]) ve parola
// değişimi ([Service.SetPassword]). Bu denetim olmadan sızmış bir yönetici
// jetonu, ikisi de yapılsa bile [DefaultJWTTTL] boyunca (varsayılan 12 saat)
// tam yetkili kimlik üretmeye devam ederdi — yani ne "çıkış yaptım" ne de
// "parolamı değiştirdim" hiçbir şeyi geri alırdı.
//
// Karşılaştırma jetonun "iat" iddiası ile kimliğin [sessionAnchor] değeri
// arasındadır; saniye çözünürlüğünün getirdiği sınır durumu ve orada yapılan
// tercih [parsedToken.issuedBefore] godoc'unda açıktır.
//
// Giriş kimliği HİÇ YOKSA jeton reddedilir. Bu yol normalde imkânsızdır —
// jeton yalnızca [Service.Login] tarafından, yani kimliği olan bir kullanıcı
// için üretilir — ama kimlik silinmişse jetonun ne zaman geçersizleştiğini
// söyleyecek bir değer de kalmaz; böyle bir durumda kabul etmek, denetimi
// kimliği silerek atlatmaya açık kapı bırakmak olurdu.
//
// # Maliyet
//
// Kimlik okuması EK BİR TUR DEĞİLDİR: bu yol zaten istek başına bir
// veritabanı okuması yapıyordu (yetkiler jetondan değil kayıttan okunuyor,
// yukarıya bakınız). İkinci okuma da indeksli tek satırdır
// (auth_identity_user_idx) ve isteğin bütçesinde ölçülebilir bir yer tutmaz;
// karşılığında iptal ANINDA etkili olur.
func (s *Service) principalFromToken(ctx context.Context, raw string) (corehttp.Principal, error) {
	if err := s.ready(); err != nil {
		return corehttp.Principal{}, err
	}

	parsed, err := s.parseToken(raw)
	if err != nil {
		return corehttp.Principal{}, err
	}

	user, err := s.repo.GetUser(ctx, parsed.Subject)
	if err != nil {
		if errors.IsNotFound(err) {
			return corehttp.Principal{}, errors.Unauthorized(CodeTokenInvalid,
				"jetondaki kullanıcı artık yok: %s", parsed.Subject)
		}
		return corehttp.Principal{}, err
	}

	identity, err := s.repo.GetIdentity(ctx, user.ID, models.ProviderEmailPass)
	if err != nil {
		if errors.IsNotFound(err) {
			return corehttp.Principal{}, errors.Unauthorized(CodeTokenInvalid,
				"jetonun dayandığı giriş kimliği artık yok: %s", user.ID)
		}
		return corehttp.Principal{}, err
	}
	if parsed.issuedBefore(sessionAnchor(identity)) {
		return corehttp.Principal{}, errors.Unauthorized(CodeTokenInvalid,
			"jeton, çıkış ya da parola değişiminden önce üretilmiş: %s", user.ID)
	}

	return corehttp.Principal{
		ID:     user.ID,
		Kind:   PrincipalKindUser,
		Scopes: user.Scopes,
	}, nil
}

// principalFromSecretKey gizli API anahtarından kimlik kurar.
func (s *Service) principalFromSecretKey(ctx context.Context, credential string) (corehttp.Principal, error) {
	key, err := s.authenticateKey(ctx, credential, models.APIKeySecret)
	if err != nil {
		return corehttp.Principal{}, err
	}

	return corehttp.Principal{
		ID:     key.ID,
		Kind:   PrincipalKindAPIKey,
		Scopes: key.Scopes,
	}, nil
}

// looksLikeJWT kimlik bilgisinin JWT biçiminde olup olmadığını bildirir.
//
// Denetim BİÇİMSELDİR, doğrulama değil: yalnızca dallanmaya karar verir.
// İmza bölümünün BOŞ olmasına izin verilir ve bu bilinçlidir — "alg: none"
// saldırısının jetonu tam olarak bu şekildedir ("başlık.gövde.") ve
// doğrulayıcıya ULAŞMASI gerekir ki orada AÇIKÇA reddedilsin. Burada elenseydi
// istek yine 401 alırdı ama reddin gerekçesi "biçim tanınmadı" olur ve
// algoritma denetiminin çalıştığı hiçbir testle kanıtlanamazdı.
func looksLikeJWT(credential string) bool {
	if strings.HasPrefix(credential, models.SecretKeyPrefix) ||
		strings.HasPrefix(credential, models.PublishableKeyPrefix) {
		return false
	}
	parts := strings.Split(credential, ".")
	if len(parts) != jwtSegmentCount {
		return false
	}
	return parts[0] != "" && parts[1] != ""
}

// ActiveSalesChannelIDs etkin satış kanallarının kimliklerini döner.
//
// Modüller arası ilkel yüzeydir: tüketici (örn. katalog süzmesi yapan bir
// modül) bu imzayı kendi paketinde tekrar tanımlar ve somut servisi
// container'dan "auth.service" adıyla çözer (ADR 0001).
//
// Devre dışı ve silinmiş kanallar DÖNMEZ. Hiç kanal yoksa boş (nil olmayan)
// dilim döner.
func (s *Service) ActiveSalesChannelIDs(ctx context.Context) ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	disabled := false
	ids := make([]string, 0, DefaultLimit)
	for offset := int64(0); ; offset += MaxLimit {
		channels, total, err := s.repo.ListSalesChannels(ctx,
			models.SalesChannelFilter{IsDisabled: &disabled}, MaxLimit, offset)
		if err != nil {
			return nil, err
		}
		for i := range channels {
			ids = append(ids, channels[i].ID)
		}
		// Sayfa boş dönerse ya da toplam sayıya ulaşıldıysa durulur; ikinci
		// koşul olmadan son sayfadan sonra bir tur daha atılırdı.
		if len(channels) == 0 || int64(len(ids)) >= total {
			break
		}
	}
	return ids, nil
}

// SalesChannelName kanalın adını döner; kanal yoksa errors.NotFound.
//
// Modüller arası ilkel yüzeydir. Kanalın tüm alanları gerekiyorsa doğru yol
// Query katmanıdır ("sales_channel" sağlayıcısı, bkz. provider.go).
func (s *Service) SalesChannelName(ctx context.Context, channelID string) (string, error) {
	channel, err := s.GetSalesChannel(ctx, channelID)
	if err != nil {
		return "", err
	}
	return channel.Name, nil
}
