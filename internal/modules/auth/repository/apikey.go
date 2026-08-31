package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// CreateAPIKey kanal bağı OLMAYAN bir API anahtarı kaydı yazar.
//
// Yazılan tek sır alanı [models.APIKey.TokenHash]'tir; düz metin bu imzada
// GEÇMEZ ve veritabanına hiç ulaşmaz.
//
// Kanallara bağlanacak bir publishable anahtar için
// [Repo.CreateAPIKeyWithChannels] kullanılmalıdır; gerekçe orada.
func (r *Repo) CreateAPIKey(ctx context.Context, k models.APIKey) (models.APIKey, error) {
	return r.CreateAPIKeyWithChannels(ctx, k, nil)
}

// CreateAPIKeyWithChannels anahtarı ve satış kanalı bağlarını TEK işlemde yazar.
//
// Atomiklik burada bir incelik değil, geri alınamazlığın gereğidir: yazım iki
// ayrı işleme bölünseydi ve bağlardan biri hata verseydi, ortada düz metni
// ÇAĞIRANA HİÇ ULAŞMAMIŞ bir anahtar satırı kalırdı. O satır kimseye
// verilmediği için kullanılamaz, düz metni bir daha üretilemediği için de
// tamamlanamaz — yalnızca elle temizlenebilecek bir çöp kayıttır. İşlem geri
// alındığında ise geriye hiçbir şey kalmaz.
//
// Bağ kurulurken kanalın CANLI olması aranır (bkz. queries/sales_channels.sql,
// LockLiveSalesChannel).
func (r *Repo) CreateAPIKeyWithChannels(
	ctx context.Context,
	k models.APIKey,
	channelIDs []string,
) (models.APIKey, error) {
	if err := r.ready(); err != nil {
		return models.APIKey{}, err
	}
	if len(channelIDs) == 0 {
		// Tek ifade zaten kendi başına atomiktir; burada işlem açmak yalnızca
		// her anahtar yazımına fazladan bir BEGIN/COMMIT turu eklerdi.
		return anahtarYaz(ctx, r.q, k)
	}

	var created models.APIKey
	if err := r.inTx(ctx, func(q *authdb.Queries) error {
		var err error
		if created, err = anahtarYaz(ctx, q, k); err != nil {
			return err
		}
		for _, channelID := range channelIDs {
			// Bağın zamanı anahtarın oluşturma anıdır: ikisi tek işlemde
			// yazıldığı için ayrı bir "şimdi" okumak yalnızca aynı olayı iki
			// farklı damgayla gösterirdi.
			if err := kanalaBagla(ctx, q, created.ID, channelID, k.CreatedAt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return models.APIKey{}, err
	}
	return created, nil
}

// anahtarYaz anahtar satırını yazar; çağıranın işlemi içinde de çalışabilir.
func anahtarYaz(ctx context.Context, q *authdb.Queries, k models.APIKey) (models.APIKey, error) {
	row, err := q.InsertAPIKey(ctx, authdb.InsertAPIKeyParams{
		ID:        k.ID,
		Type:      k.Type.String(),
		Title:     k.Title,
		TokenHash: k.TokenHash,
		Redacted:  k.Redacted,
		Scopes:    k.Scopes,
		CreatedBy: k.CreatedBy,
		CreatedAt: fromTime(k.CreatedAt),
	})
	if err != nil {
		if ConstraintName(err) == IndexTokenHash {
			// Buraya düşmek 256 bitlik üretecin çakıştığı anlamına gelir;
			// pratikte imkânsızdır ve sessiz geçilemez.
			return models.APIKey{}, errors.Wrap(err, errors.KindConflict, CodeDuplicate,
				"api anahtarı özeti zaten kayıtlı: %s", k.ID)
		}
		return models.APIKey{}, wrapDB(err, "api anahtarı oluşturulamadı")
	}
	return toAPIKey(row), nil
}

// GetAPIKey kimliğe göre anahtar döner; yoksa errors.NotFound.
func (r *Repo) GetAPIKey(ctx context.Context, id string) (models.APIKey, error) {
	if err := r.ready(); err != nil {
		return models.APIKey{}, err
	}

	row, err := r.q.GetAPIKey(ctx, id)
	if err != nil {
		return models.APIKey{}, notFoundOr(err, CodeAPIKeyNotFound, "api anahtarı bulunamadı: %s", id)
	}
	return toAPIKey(row), nil
}

// GetAPIKeyByHash verilen özete karşılık gelen anahtarı döner; yoksa
// errors.NotFound.
//
// İPTAL EDİLMİŞ anahtarlar da döner: iptalin ayrı ve açık bir dal olması,
// "iptal edilmiş anahtar reddedilir" iddiasının testle kanıtlanabilmesi
// içindir (bkz. queries/api_keys.sql).
//
// Hata mesajında özet GEÇMEZ; sızması zararsız olsa da bir sır alanının log'a
// düşmesi alışkanlık hâline gelmemelidir.
func (r *Repo) GetAPIKeyByHash(ctx context.Context, tokenHash string) (models.APIKey, error) {
	if err := r.ready(); err != nil {
		return models.APIKey{}, err
	}

	row, err := r.q.GetAPIKeyByHash(ctx, tokenHash)
	if err != nil {
		return models.APIKey{}, notFoundOr(err, CodeAPIKeyNotFound, "api anahtarı bulunamadı")
	}
	return toAPIKey(row), nil
}

// ListAPIKeys süzgeçlenmiş ve sayfalanmış anahtar listesini, filtreye uyan
// TOPLAM kayıt sayısıyla birlikte döner.
func (r *Repo) ListAPIKeys(
	ctx context.Context,
	filter models.APIKeyFilter,
	limit, offset int64,
) ([]models.APIKey, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	var keyType *string
	if filter.Type != nil {
		value := filter.Type.String()
		keyType = &value
	}

	rows, err := r.q.ListAPIKeys(ctx, authdb.ListAPIKeysParams{
		KeyType: keyType,
		Revoked: filter.Revoked,
		Lim:     toInt32(limit),
		Off:     toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "api anahtarı listesi alınamadı")
	}

	total, err := r.q.CountAPIKeys(ctx, authdb.CountAPIKeysParams{
		KeyType: keyType,
		Revoked: filter.Revoked,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "api anahtarı sayısı alınamadı")
	}
	return toAPIKeys(rows), total, nil
}

// RevokeAPIKey anahtarı iptal eder; zaten iptalliyse errors.Conflict döner.
//
// Zaten iptalli bir anahtarda sessiz no-op dönmek, iptal zamanının ikinci
// çağrıyla kaymasına ya da çağıranın "iptal ettim" sanmasına yol açardı.
func (r *Repo) RevokeAPIKey(
	ctx context.Context,
	id, revokedBy string,
	now time.Time,
) (models.APIKey, error) {
	if err := r.ready(); err != nil {
		return models.APIKey{}, err
	}

	row, err := r.q.RevokeAPIKey(ctx, authdb.RevokeAPIKeyParams{
		ID:        id,
		RevokedAt: fromTime(now),
		RevokedBy: revokedBy,
	})
	if err == nil {
		return toAPIKey(row), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.APIKey{}, wrapDB(err, "api anahtarı iptal edilemedi")
	}

	// Satır dönmedi: anahtar ya yok ya da zaten iptal edilmiş. İkisini ayırmak
	// için kaydı okuruz; ayrım olmadan çağıran "yok" ile "zaten kapalı"
	// arasında seçim yapamazdı.
	existing, getErr := r.GetAPIKey(ctx, id)
	if getErr != nil {
		return models.APIKey{}, getErr
	}
	return models.APIKey{}, errors.Conflict(CodeAlreadyRevoked,
		"api anahtarı zaten iptal edilmiş: %s", existing.ID)
}

// DeleteAPIKey anahtarı yumuşak siler ve satış kanalı bağlarını kaldırır.
//
// Bağlar AYNI işlemde silinir: yumuşak silme bir UPDATE olduğu için foreign
// key CASCADE devreye girmez ve bağ satırları silinmiş bir anahtarı kanala
// bağlı göstermeye devam ederdi.
func (r *Repo) DeleteAPIKey(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.inTx(ctx, func(q *authdb.Queries) error {
		if _, err := q.SoftDeleteAPIKey(ctx, authdb.SoftDeleteAPIKeyParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeAPIKeyNotFound, "api anahtarı bulunamadı: %s", id)
		}
		if err := q.DeleteLinksOfAPIKey(ctx, id); err != nil {
			return wrapDB(err, "api anahtarının kanal bağları silinemedi")
		}
		return nil
	})
}

// MarkAPIKeyUsed anahtarın son kullanım anını YAKLAŞIK olarak günceller.
//
// staleBefore eşiği yazmayı seyreltir: sütun her istekte yazılsaydı sıcak bir
// publishable anahtar üzerinde kimlik doğrulama tek satıra yazan bir darboğaza
// dönüşürdü (bkz. queries/api_keys.sql).
func (r *Repo) MarkAPIKeyUsed(ctx context.Context, id string, usedAt, staleBefore time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if err := r.q.MarkAPIKeyUsed(ctx, authdb.MarkAPIKeyUsedParams{
		ID:          id,
		UsedAt:      fromTime(usedAt),
		StaleBefore: fromTime(staleBefore),
	}); err != nil {
		return wrapDB(err, "api anahtarının kullanım anı güncellenemedi")
	}
	return nil
}

// LinkSalesChannel publishable anahtarı bir satış kanalına bağlar.
//
// Aynı bağın tekrarı hata DEĞİLDİR (bağ kümedir). Var olmayan bir anahtar
// foreign key ihlaliyle errors.Invalid, var olmayan ya da yumuşak silinmiş bir
// kanal errors.NotFound döner.
func (r *Repo) LinkSalesChannel(ctx context.Context, apiKeyID, channelID string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	// Denetim ve yazım TEK işlemdedir: kanalın canlılığı ayrı bir turda
	// sorulsaydı, arada yapılan bir yumuşak silme yine ölü bir bağ bırakırdı.
	return r.inTx(ctx, func(q *authdb.Queries) error {
		return kanalaBagla(ctx, q, apiKeyID, channelID, now)
	})
}

// kanalaBagla bağı, kanalın CANLI olduğunu doğrulayarak yazar.
//
// Foreign key tek başına yetmez: yumuşak silinmiş bir kanalın satırı yerinde
// durduğu için FK'yi geçer, ama okuma sorguları onu süzer. Böyle bir bağ
// kurulsaydı publishable anahtar ÖLÜ DOĞARDI — bağlı görünür, hiçbir kanala
// ulaşamaz ve mağaza kimliği kuramazdı (bkz. queries/sales_channels.sql,
// LockLiveSalesChannel).
func kanalaBagla(
	ctx context.Context,
	q *authdb.Queries,
	apiKeyID, channelID string,
	now time.Time,
) error {
	if _, err := q.LockLiveSalesChannel(ctx, channelID); err != nil {
		return notFoundOr(err, CodeSalesChannelNotFound,
			"satış kanalı bulunamadı: %s", channelID)
	}
	if err := q.LinkAPIKeySalesChannel(ctx, authdb.LinkAPIKeySalesChannelParams{
		ApiKeyID:       apiKeyID,
		SalesChannelID: channelID,
		CreatedAt:      fromTime(now),
	}); err != nil {
		return wrapDB(err, "api anahtarı %s kanalına bağlanamadı", channelID)
	}
	return nil
}

// UnlinkSalesChannel bağı kaldırır; bağ yoksa errors.NotFound.
//
// Var olmayan bir bağı kaldırmayı sessizce başarı saymak, çağırana yanlış
// kanalın adını yazdığını hiç söylemezdi.
func (r *Repo) UnlinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error {
	if err := r.ready(); err != nil {
		return err
	}

	affected, err := r.q.UnlinkAPIKeySalesChannel(ctx, authdb.UnlinkAPIKeySalesChannelParams{
		ApiKeyID:       apiKeyID,
		SalesChannelID: channelID,
	})
	if err != nil {
		return wrapDB(err, "api anahtarının kanal bağı kaldırılamadı")
	}
	if affected == 0 {
		return errors.NotFound(CodeSalesChannelNotFound,
			"%s anahtarı %s kanalına bağlı değil", apiKeyID, channelID)
	}
	return nil
}

// ChannelIDsOfKey anahtarın bağlı olduğu ETKİN kanalların kimliklerini döner.
//
// Devre dışı ve silinmiş kanallar süzülür; mağaza kimliği bu listeden kurulur
// ve devre dışı bir kanalın kataloğu görünmemelidir.
func (r *Repo) ChannelIDsOfKey(ctx context.Context, apiKeyID string) ([]string, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	ids, err := r.q.ListChannelIDsForKey(ctx, apiKeyID)
	if err != nil {
		return nil, wrapDB(err, "api anahtarının kanalları alınamadı")
	}
	return ids, nil
}

// ChannelsOfKey anahtarın bağlı olduğu kanalların TAMAMINI döner.
//
// Devre dışı kanallar da dâhildir: yönetim yüzeyi bağı olduğu gibi göstermeli,
// bir kanalın devre dışı olduğunu gizlememelidir.
func (r *Repo) ChannelsOfKey(ctx context.Context, apiKeyID string) ([]models.SalesChannel, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListChannelsForKey(ctx, apiKeyID)
	if err != nil {
		return nil, wrapDB(err, "api anahtarının kanalları alınamadı")
	}
	return toSalesChannels(rows)
}
