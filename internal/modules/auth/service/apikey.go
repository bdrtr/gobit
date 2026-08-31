package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// CreateAPIKeyInput bir API anahtarının yazma girdisidir.
//
// DÜZ ANAHTAR BU YAPIDA YOKTUR: anahtar burada üretilmez, [Service.CreateAPIKey]
// tarafından üretilir ve yalnızca o çağrının DÖNÜŞ DEĞERİ olarak verilir.
type CreateAPIKeyInput struct {
	// Type anahtarın türüdür: [models.APIKeyPublishable] ya da
	// [models.APIKeySecret]. Zorunludur.
	Type models.APIKeyType
	// Title anahtarın insan tarafından okunan adıdır; zorunludur.
	Title string
	// Scopes anahtarın yetkileridir.
	//
	// YALNIZCA gizli anahtarlar için anlamlıdır; nil verilirse
	// [models.ScopeAdmin] uygulanır. Publishable anahtarda dolu verilmesi
	// REDDEDİLİR (bkz. [Service.CreateAPIKey]).
	Scopes []string
	// CreatedBy anahtarı üretenin kimliğidir; boş bırakılabilir.
	CreatedBy string
	// SalesChannelIDs publishable anahtarın bağlanacağı kanallardır.
	//
	// YALNIZCA publishable anahtarlar için anlamlıdır; gizli anahtarda dolu
	// verilmesi REDDEDİLİR.
	SalesChannelIDs []string
}

// CreateAPIKey yeni bir API anahtarı üretir ve DÜZ METNİ bir kez döner.
//
// # Düz metin yalnızca burada
//
// İkinci dönüş değeri anahtarın düz hâlidir ve tek kopyasıdır: veritabanına
// yalnızca SHA-256 özeti yazılır (gerekçe için bkz. [models.HashToken]).
// Çağıran bunu istemciye ilettikten sonra unutmalıdır — bir daha hiçbir
// yerden okunamaz. Kaybedilen anahtar geri getirilemez; yapılacak şey iptal
// edip yenisini üretmektir.
//
// # Tür kuralları
//
// Publishable anahtar YETKİ TAŞIMAZ ve satış kanallarına bağlanır; gizli
// anahtar yetki taşır ve kanala bağlanmaz. İkisini karıştıran bir girdi
// sessizce düzeltilmez, REDDEDİLİR: "publishable ama admin yetkili" bir
// anahtar, tarayıcıya konan bir yönetim kimliği demek olurdu.
//
// # Anahtar ve bağları birlikte doğar
//
// Kanal bağlarından biri kurulamazsa anahtar da YAZILMAZ. Yarım kalan bir
// yazım geri alınamazdı: düz metin yalnızca başarılı çağrının dönüşünde
// verilir, hatalı çağrıda verilmez — geriye kimsenin bilmediği, hiçbir zaman
// kullanılamayacak ve tamamlanamayacak bir kayıt kalırdı.
//
// # Yetki yükseltilemez
//
// Üretilen anahtar, ÇAĞIRANIN KENDİSİNDE OLMAYAN bir yetkiyi taşıyamaz
// (bkz. [requireGrantableScopes]); aksi hâlde yalnızca "orders:read" taşıyan
// bir anahtar, tek istekte kendine tam yetkili bir halef üretebilirdi.
//
// # Son kullanma tarihi yoktur
//
// API anahtarlarının süresi DOLMAZ ve bu bilinçlidir: süreli anahtar, süresi
// dolduğunda sessizce çalışmayı bırakan bir entegrasyon demektir ve bu, gece
// yarısı bozulan bir vitrin olarak geri döner. Anahtarın kapanması AÇIK bir
// eylemle olur ([Service.RevokeAPIKey]); "artık kullanılmıyor mu" sorusunun
// cevabı ise [models.APIKey.LastUsedAt] alanındadır.
func (s *Service) CreateAPIKey(
	ctx context.Context,
	in CreateAPIKeyInput,
) (models.APIKey, string, error) {
	if err := s.ready(); err != nil {
		return models.APIKey{}, "", err
	}
	if !in.Type.Valid() {
		return models.APIKey{}, "", errors.Invalid(CodeInvalidInput,
			"api anahtarı türü %q ya da %q olmalı, %q verildi",
			models.APIKeyPublishable, models.APIKeySecret, in.Type)
	}
	if err := requireText("api anahtarı başlığı", in.Title); err != nil {
		return models.APIKey{}, "", err
	}
	if err := checkLen("api anahtarı başlığı", in.Title, models.MaxNameLen); err != nil {
		return models.APIKey{}, "", err
	}

	scopes, err := s.keyScopes(in)
	if err != nil {
		return models.APIKey{}, "", err
	}
	// Denetim ÇÖZÜLMÜŞ yetkiler üzerinde yapılır, girdideki ham dilim üzerinde
	// değil: gizli anahtarta nil yetki [models.ScopeAdmin]'e açılır ve "yetki
	// vermedim" diyen bir istek tam yetkili bir anahtar üretirdi.
	if err := requireGrantableScopes(ctx, scopes); err != nil {
		return models.APIKey{}, "", err
	}
	if in.Type == models.APIKeySecret && len(in.SalesChannelIDs) > 0 {
		return models.APIKey{}, "", errors.Invalid(CodeAPIKeyTypeMismatch,
			"gizli anahtar satış kanalına bağlanamaz; kanal bağı publishable anahtarlara aittir")
	}
	for _, channelID := range in.SalesChannelIDs {
		if err := requireID(channelID, models.SalesChannelIDPrefix, "satış kanalı kimliği"); err != nil {
			return models.APIKey{}, "", err
		}
	}

	plaintext, err := models.NewToken(in.Type)
	if err != nil {
		return models.APIKey{}, "", errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"api anahtarı üretilemedi")
	}

	now := s.clock()
	created, err := s.createKeyWithChannels(ctx, models.APIKey{
		ID:        models.NewAPIKeyID(now),
		Type:      in.Type,
		Title:     in.Title,
		TokenHash: models.HashToken(plaintext),
		Redacted:  models.RedactToken(plaintext),
		Scopes:    scopes,
		CreatedBy: in.CreatedBy,
		CreatedAt: now,
	}, in.SalesChannelIDs, now)
	if err != nil {
		return models.APIKey{}, "", err
	}

	// Anahtarın kendisi ve özeti LOGLANMAZ; kimlik ve tür izlemeye yeter.
	s.log.InfoContext(ctx, "api anahtarı oluşturuldu",
		slog.String("api_key_id", created.ID),
		slog.String("type", created.Type.String()),
		slog.Int("sales_channel_count", len(in.SalesChannelIDs)),
	)
	return created, plaintext, nil
}

// atomicAPIKeyWriter anahtarı ve kanal bağlarını TEK veritabanı işleminde
// yazabilen depodur.
//
// Yetenek [Repository] arayüzüne ZORUNLU olarak konmadı: işlem yönetimi ancak
// gerçek bir veritabanı deposunun verebileceği bir garantidir ve arayüza
// konsaydı servisi sahte bir depoyla sınamak, o depoya da işlem taklidi
// yazmayı gerektirirdi. Depo yeteneği sunuyorsa kullanılır, sunmuyorsa aynı
// sonuç TELAFİ ile sağlanır (bkz. [Service.createKeyWithChannels]).
type atomicAPIKeyWriter interface {
	CreateAPIKeyWithChannels(
		ctx context.Context,
		k models.APIKey,
		channelIDs []string,
	) (models.APIKey, error)
}

// createKeyWithChannels anahtarı ve kanal bağlarını "ya hepsi ya hiçbiri"
// kuralıyla yazar.
//
// Kuralın gerekçesi [Service.CreateAPIKey] godoc'undadır: bağ kurulamadığında
// anahtar satırı yerinde kalsaydı geri alınamaz bir çöp kayıt olurdu.
func (s *Service) createKeyWithChannels(
	ctx context.Context,
	key models.APIKey,
	channelIDs []string,
	now time.Time,
) (models.APIKey, error) {
	if writer, ok := s.repo.(atomicAPIKeyWriter); ok {
		return writer.CreateAPIKeyWithChannels(ctx, key, channelIDs)
	}

	created, err := s.repo.CreateAPIKey(ctx, key)
	if err != nil {
		return models.APIKey{}, err
	}
	for _, channelID := range channelIDs {
		linkErr := s.repo.LinkSalesChannel(ctx, created.ID, channelID, now)
		if linkErr == nil {
			continue
		}
		// İşlem açamayan depoda geri alma TELAFİ ile yapılır: yazılan anahtar
		// silinir. Silme de başarısız olursa hata YUTULMAZ, loglanır; çağırana
		// dönen hata bağın kurulamaması olmalıdır — temizliğin kendisi
		// çağıranın çözebileceği bir sorun değildir.
		if delErr := s.repo.DeleteAPIKey(ctx, created.ID, now); delErr != nil {
			s.log.ErrorContext(ctx, "kanal bağı kurulamayan api anahtarı geri alınamadı",
				slog.String("api_key_id", created.ID), slog.Any("error", delErr))
		}
		return models.APIKey{}, linkErr
	}
	return created, nil
}

// keyScopes girdideki yetkileri türe göre doğrular ve varsayılanı uygular.
func (s *Service) keyScopes(in CreateAPIKeyInput) ([]string, error) {
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return nil, err
	}

	if in.Type == models.APIKeyPublishable {
		if len(scopes) > 0 {
			return nil, errors.Invalid(CodeAPIKeyTypeMismatch,
				"publishable anahtar yetki taşıyamaz; yetki gizli anahtarlara aittir")
		}
		// nil ile boş dilim arasındaki fark burada kalkar: publishable
		// anahtarın yetkisi HER ZAMAN boştur.
		return []string{}, nil
	}
	if scopes == nil {
		return []string{models.ScopeAdmin}, nil
	}
	return scopes, nil
}

// CodeScopeEscalation çağıranın kendisinde olmayan bir yetkiyi vermeye
// çalıştığını bildirir.
//
// Sabit, kardeşleri service.go'daki kod bloğunda değil, zorlandığı yerin
// yanında durur: kodu okuyan kişi kuralı ve adını bir arada görür.
const CodeScopeEscalation = "auth_scope_escalation"

// requireGrantableScopes verilecek yetkilerin ÇAĞIRANDA da bulunduğunu
// doğrular.
//
// Yetki yükseltmenin en ucuz yolu, yetki dağıtan bir ucun dağıtılan yetkiyi
// sorgusuz kabul etmesidir: "orders:read" taşıyan bir gizli anahtar, tek
// istekte kendine {"scopes":["admin"]} bir anahtar üretebilirdi. Route
// üzerindeki corehttp.RequireScope bu uçları bugün zaten admin'e kapatıyor;
// buradaki denetim onun YERİNE değil, ONUNLA BİRLİKTE durur — uçların yetki
// haritası bir gün gevşetilirse kapı burada kapalı kalır.
//
// # Kimliksiz çağrı serbesttir
//
// Context'te kimlik yoksa denetim uygulanmaz ve bu bir boşluk değildir: kimlik
// taşımayan tek meşru çağıran sürecin kendisidir — ilk yöneticiyi yaratan tohum
// adımı, kimseden yetki devralmadığı için kimseyi de yükseltemez. HTTP'den
// gelen her istek corehttp.RequireAdmin'den geçtiği için context'inde kimlik
// vardır; geçmeseydi yönetim yüzeyi zaten tümüyle açık olurdu ve burada
// kapatılacak bir şey kalmazdı.
func requireGrantableScopes(ctx context.Context, scopes []string) error {
	if len(scopes) == 0 {
		return nil
	}

	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok {
		return nil
	}

	for _, scope := range scopes {
		if !principal.HasScope(scope) {
			// Hata KindForbidden'dır, KindUnauthorized değil: çağıran
			// tanınmıştır, isteği anlaşılmıştır ve reddedilmiştir. 401
			// dönseydi istemci kimliğini yenilemeye çalışır ve aynı duvara
			// tekrar tekrar çarpardı.
			return errors.Forbidden(CodeScopeEscalation,
				"kendinizde olmayan %q yetkisi verilemez", scope)
		}
	}
	return nil
}

// GetAPIKey kimliğe göre anahtar döner; yoksa errors.NotFound.
//
// Dönen kayıt düz metni İÇERMEZ; yalnızca maskelenmiş hâli
// ([models.APIKey.Redacted]) vardır.
func (s *Service) GetAPIKey(ctx context.Context, id string) (models.APIKey, error) {
	if err := s.ready(); err != nil {
		return models.APIKey{}, err
	}
	if err := requireID(id, models.APIKeyIDPrefix, "api anahtarı kimliği"); err != nil {
		return models.APIKey{}, err
	}
	return s.repo.GetAPIKey(ctx, id)
}

// ListAPIKeysInput anahtar listelemesinin girdisidir.
type ListAPIKeysInput struct {
	// Type verilirse yalnızca bu türdeki anahtarlar döner.
	Type *models.APIKeyType
	// Revoked verilirse iptal edilmiş/edilmemiş ayrımına göre süzer.
	Revoked *bool
	// Limit sayfa boyudur; 0 ise [DefaultLimit] uygulanır.
	Limit int64
	// Offset atlanacak kayıt sayısıdır.
	Offset int64
}

// ListAPIKeys süzgeçlenmiş ve sayfalanmış anahtar listesini döner.
func (s *Service) ListAPIKeys(ctx context.Context, in ListAPIKeysInput) (Page[models.APIKey], error) {
	if err := s.ready(); err != nil {
		return Page[models.APIKey]{}, err
	}

	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.APIKey]{}, err
	}
	if in.Type != nil && !in.Type.Valid() {
		return Page[models.APIKey]{}, errors.Invalid(CodeInvalidInput,
			"api anahtarı türü %q ya da %q olmalı, %q verildi",
			models.APIKeyPublishable, models.APIKeySecret, *in.Type)
	}

	items, total, err := s.repo.ListAPIKeys(ctx, models.APIKeyFilter{
		Type:    in.Type,
		Revoked: in.Revoked,
	}, limit, offset)
	if err != nil {
		return Page[models.APIKey]{}, err
	}
	return Page[models.APIKey]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// RevokeAPIKey anahtarı iptal eder; iptalli anahtar bir daha kabul edilmez.
//
// İptal SİLME DEĞİLDİR: kayıt listede kalır ve ne zaman, kim tarafından
// kapatıldığı görünür. Bir sızıntının ardından "hangi anahtar açıktı" sorusuna
// ancak böyle cevap verilebilir.
func (s *Service) RevokeAPIKey(ctx context.Context, id, revokedBy string) (models.APIKey, error) {
	if err := s.ready(); err != nil {
		return models.APIKey{}, err
	}
	if err := requireID(id, models.APIKeyIDPrefix, "api anahtarı kimliği"); err != nil {
		return models.APIKey{}, err
	}

	revoked, err := s.repo.RevokeAPIKey(ctx, id, revokedBy, s.clock())
	if err != nil {
		return models.APIKey{}, err
	}

	s.log.InfoContext(ctx, "api anahtarı iptal edildi",
		slog.String("api_key_id", revoked.ID),
		slog.String("revoked_by", revokedBy),
	)
	return revoked, nil
}

// DeleteAPIKey anahtarı yumuşak siler ve kanal bağlarını kaldırır.
//
// İptalden farkı, kaydın listelerden de çıkmasıdır. Sızıntı sonrası tercih
// edilmesi gereken işlem [Service.RevokeAPIKey]'dir; silme, yanlışlıkla
// oluşturulmuş bir kaydı temizlemek içindir.
func (s *Service) DeleteAPIKey(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.APIKeyIDPrefix, "api anahtarı kimliği"); err != nil {
		return err
	}
	if err := s.repo.DeleteAPIKey(ctx, id, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "api anahtarı silindi", slog.String("api_key_id", id))
	return nil
}

// LinkSalesChannel publishable anahtarı bir satış kanalına bağlar.
//
// Gizli anahtar bağlanamaz: kanal bağı, mağaza isteğinin hangi kataloğu
// göreceğini belirler ve gizli anahtarın mağaza yüzeyinde hiç işi yoktur.
func (s *Service) LinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error {
	key, err := s.linkable(ctx, apiKeyID, channelID)
	if err != nil {
		return err
	}
	if err := s.repo.LinkSalesChannel(ctx, key.ID, channelID, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "publishable anahtar satış kanalına bağlandı",
		slog.String("api_key_id", key.ID),
		slog.String("sales_channel_id", channelID),
	)
	return nil
}

// UnlinkSalesChannel bağı kaldırır; bağ yoksa errors.NotFound.
func (s *Service) UnlinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error {
	key, err := s.linkable(ctx, apiKeyID, channelID)
	if err != nil {
		return err
	}
	if err := s.repo.UnlinkSalesChannel(ctx, key.ID, channelID); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "publishable anahtarın satış kanalı bağı kaldırıldı",
		slog.String("api_key_id", key.ID),
		slog.String("sales_channel_id", channelID),
	)
	return nil
}

// linkable bağ işlemlerinin ortak ön denetimidir.
func (s *Service) linkable(ctx context.Context, apiKeyID, channelID string) (models.APIKey, error) {
	if err := s.ready(); err != nil {
		return models.APIKey{}, err
	}
	if err := requireID(apiKeyID, models.APIKeyIDPrefix, "api anahtarı kimliği"); err != nil {
		return models.APIKey{}, err
	}
	if err := requireID(channelID, models.SalesChannelIDPrefix, "satış kanalı kimliği"); err != nil {
		return models.APIKey{}, err
	}

	key, err := s.repo.GetAPIKey(ctx, apiKeyID)
	if err != nil {
		return models.APIKey{}, err
	}
	if key.Type != models.APIKeyPublishable {
		return models.APIKey{}, errors.Invalid(CodeAPIKeyTypeMismatch,
			"yalnızca publishable anahtarlar satış kanalına bağlanabilir, %q verildi", key.Type)
	}

	// Kanalın varlığı burada doğrulanır: foreign key ihlali de aynı sonucu
	// verirdi ama istemciye "kısıt ihlali" yerine "kanal bulunamadı" demek
	// hatayı kullanılabilir kılar.
	if _, err := s.repo.GetSalesChannel(ctx, channelID); err != nil {
		return models.APIKey{}, err
	}
	return key, nil
}

// SalesChannelsOfAPIKey anahtarın bağlı olduğu kanalların TAMAMINI döner.
//
// Devre dışı kanallar da dâhildir: yönetim yüzeyi bağı olduğu gibi
// göstermelidir. Mağaza kimliğinde kullanılan liste ise devre dışı kanalları
// SÜZER (bkz. [Service.authenticatePublishable]).
func (s *Service) SalesChannelsOfAPIKey(ctx context.Context, apiKeyID string) ([]models.SalesChannel, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(apiKeyID, models.APIKeyIDPrefix, "api anahtarı kimliği"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetAPIKey(ctx, apiKeyID); err != nil {
		return nil, err
	}
	return s.repo.ChannelsOfKey(ctx, apiKeyID)
}

// authenticateKey bir düz anahtarı doğrular ve kaydını döner.
//
// # İki bağımsız tür kapısı
//
// Anahtarın türü İKİ KEZ denetlenir ve bu tekrar bilinçlidir:
//
//  1. ÖNEK — düz metnin "sk_" ya da "pk_" ile başlaması. Veritabanına hiç
//     gitmeden yanlış yüzeye sunulan anahtarı eler.
//  2. KAYIT — okunan satırdaki [models.APIKey.Type] alanı. Önek bir gün
//     değişse ya da bir kayıt elle düzenlense bile gerçek tür budur.
//
// Tek kapı yeterli olsaydı, ikisinden birindeki bir hata publishable bir
// anahtarın yönetim yüzeyine geçmesi demek olurdu.
//
// Özet karşılaştırması indeksli aramaya EK OLARAK sabit zamanda yapılır
// (bkz. [models.TokenHashesEqual]).
//
// Tüm başarısızlıklar errors.Unauthorized döner; ayrıntı çekirdeğin
// middleware'i tarafından loglanır, istemciye gitmez.
func (s *Service) authenticateKey(
	ctx context.Context,
	plaintext string,
	want models.APIKeyType,
) (models.APIKey, error) {
	if err := s.ready(); err != nil {
		return models.APIKey{}, err
	}

	kind, err := models.TypeForToken(plaintext)
	if err != nil || kind != want {
		return models.APIKey{}, errors.Unauthorized(CodeAPIKeyTypeMismatch,
			"bu yüzey yalnızca %q türü api anahtarı kabul eder", want)
	}

	hash := models.HashToken(plaintext)
	key, err := s.repo.GetAPIKeyByHash(ctx, hash)
	if err != nil {
		if errors.IsNotFound(err) {
			return models.APIKey{}, errors.Unauthorized(CodeInvalidCredentials,
				"api anahtarı tanınmadı")
		}
		return models.APIKey{}, err
	}
	if !models.TokenHashesEqual(key.TokenHash, hash) {
		return models.APIKey{}, errors.Unauthorized(CodeInvalidCredentials,
			"api anahtarı tanınmadı")
	}
	if key.Type != want {
		return models.APIKey{}, errors.Unauthorized(CodeAPIKeyTypeMismatch,
			"bu yüzey yalnızca %q türü api anahtarı kabul eder", want)
	}
	if key.IsRevoked() {
		return models.APIKey{}, errors.Unauthorized(CodeAPIKeyRevoked,
			"api anahtarı iptal edilmiş: %s", key.ID)
	}

	s.touchKey(ctx, key)
	return key, nil
}

// authenticatePublishable publishable anahtarı doğrular ve bağlı ETKİN satış
// kanallarının kimliklerini döner.
//
// # Kanalsız anahtar REDDEDİLİR
//
// Etkin hiçbir kanala bağlı olmayan bir publishable anahtar kabul edilmez.
// Gerekçe: boş bir kanal listesi aşağı akışta İKİ ANLAMA gelebilir — "hiçbir
// kanal" ya da "kanal süzgeci yok". İkinci okuma katalog süzmesini tümüyle
// kaldırırdı ve belirsizlik güvensiz yönde çözülürdü. Kapıda kapalı düşmek,
// aşağıda sessizce açılmaktan iyidir.
//
// Devre dışı ve silinmiş kanallar listeden süzülür; hepsi süzülürse anahtar
// da reddedilir.
func (s *Service) authenticatePublishable(ctx context.Context, plaintext string) (models.APIKey, []string, error) {
	key, err := s.authenticateKey(ctx, plaintext, models.APIKeyPublishable)
	if err != nil {
		return models.APIKey{}, nil, err
	}

	channelIDs, err := s.repo.ChannelIDsOfKey(ctx, key.ID)
	if err != nil {
		return models.APIKey{}, nil, err
	}
	if len(channelIDs) == 0 {
		return models.APIKey{}, nil, errors.Unauthorized(CodeNoSalesChannel,
			"publishable anahtar etkin bir satış kanalına bağlı değil: %s", key.ID)
	}
	return key, channelIDs, nil
}

// touchKey anahtarın son kullanım anını YAKLAŞIK olarak günceller.
//
// Yazma [Options.UsageThrottle] ile seyreltilir ve karar BURADA verilir:
// okunan satır zaten elde olduğu için, güncel bir kayıt için veritabanına
// ikinci bir tur atılmaz.
//
// Hata girişi ETKİLEMEZ: son kullanım anı bir istatistiktir, kimlik kararının
// parçası değildir.
func (s *Service) touchKey(ctx context.Context, key models.APIKey) {
	now := s.clock()
	staleBefore := now.Add(-s.throttle)
	if key.LastUsedAt != nil && !key.LastUsedAt.Before(staleBefore) {
		return
	}

	if err := s.repo.MarkAPIKeyUsed(ctx, key.ID, now, staleBefore); err != nil {
		s.log.WarnContext(ctx, "api anahtarının kullanım anı güncellenemedi",
			slog.String("api_key_id", key.ID), slog.Any("error", err))
	}
}
