package repository

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository/fulfillmentdb"
)

// Bu dosya pgtype <-> domain modeli dönüşümlerinin ve sürücü hatası
// sınıflandırmasının TEK yeridir.
//
// Sınırın burada olması bilinçlidir: sürücüye özgü tipler (pgtype.Timestamptz,
// jsonb için []byte, *pgconn.PgError) repository'nin dışına ÇIKMAZ. Servis ve
// API katmanı time.Time, json.RawMessage ve core/errors tipli hatalarını görür.

// Hata kodları. Çağıran taraf errors.CodeOf ile bunlara bakabilir; API katmanı
// da aynı kodları istemciye geçirir.
const (
	codeProfileNotFound        = "fulfillment_shipping_profile_not_found"
	codeOptionNotFound         = "fulfillment_shipping_option_not_found"
	codeRuleNotFound           = "fulfillment_shipping_option_rule_not_found"
	codeFulfillmentNotFound    = "fulfillment_not_found"
	codeManualShipmentNotFound = "fulfillment_manual_shipment_not_found"
	codeLocationNotFound       = "fulfillment_shipping_location_not_found"
	codeProfileNameExists      = "fulfillment_shipping_profile_name_exists"
	codeFulfillmentExists      = "fulfillment_idempotency_key_exists"
	codeItemExists             = "fulfillment_item_already_added"
	codeAmountOutOfRange       = "fulfillment_amount_out_of_range"
	codeQuantityOutOfRange     = "fulfillment_quantity_out_of_range"
	codeStatusInvalid          = "fulfillment_status_invalid"
	codeCurrencyInvalid        = "fulfillment_currency_invalid"
	codeDataInvalid            = "fulfillment_json_invalid"
	codeRuleInvalid            = "fulfillment_rule_invalid"
	codeStampMissing           = "fulfillment_status_stamp_missing"
	codeTxRequired             = "fulfillment_tx_required"
	codeTxBeginFailed          = "fulfillment_tx_begin_failed"
	codeTxCommitFailed         = "fulfillment_tx_commit_failed"
	codeQueryFailed            = "fulfillment_query_failed"
	codeConcurrentUpdate       = "fulfillment_concurrent_update"
)

// Kısıt ve indeks adları; sürücü hatasını anlamlı bir tipli hataya çevirmek
// için kullanılır. Adlar migration'daki adlarla BİREBİR aynıdır.
const (
	constraintProfileNameUniq     = "shipping_profiles_name_uniq"
	constraintFulfillmentKeyUniq  = "fulfillments_idempotency_uniq"
	constraintFulfillmentItemUniq = "fulfillment_items_line_uniq"
	// constraintStatusSuffix durum değerini denetleyen CHECK kısıtlarının
	// ortak sonekidir.
	constraintStatusSuffix = "_status_valid"
	// constraintStampSuffix durum ile zaman damgasının birbirini tutmasını
	// isteyen CHECK kısıtlarının ortak sonekidir.
	constraintStampSuffix = "_stamp"
)

// kisitMesajlari alan düzeyinde doğrulama yapan CHECK kısıtlarının insan
// tarafından okunabilir karşılıklarıdır.
//
// İhlalleri istemcinin düzeltebileceği GEÇERSİZ GİRDİ durumlarıdır; servis
// bunları zaten reddeder, buradaki eşleme doğrudan SQL ile yapılan bir
// müdahalenin de anlaşılır bir hata dönmesini sağlar.
var kisitMesajlari = map[string]string{
	"shipping_options_calculated_zero":             "hesaplanan kargo seçeneğinin tutarı sıfır olmalı; ücret sağlayıcıdan gelir",
	"shipping_options_price_type_valid":            "kargo seçeneğinin fiyat türü 'flat' ya da 'calculated' olmalı",
	"shipping_options_name_check":                  "kargo seçeneğinin adı boş olamaz",
	"shipping_options_provider_check":              "kargo seçeneğinin sağlayıcısı boş olamaz",
	"shipping_profiles_name_check":                 "kargo profilinin adı boş olamaz",
	"shipping_profiles_type_valid":                 "kargo profilinin türü 'default', 'gift_card' ya da 'custom' olmalı",
	"shipping_option_rules_attribute_check":        "kural alanı boş olamaz",
	"shipping_option_rules_operator_check":         "tanınmayan kural işleci",
	"shipping_option_rules_values_check":           "kural en az bir değer içermeli",
	"fulfillments_reference_check":                 "gönderinin referansı boş olamaz",
	"fulfillments_provider_check":                  "gönderinin sağlayıcısı boş olamaz",
	"fulfillments_key_check":                       "idempotency anahtarı boş olamaz",
	"fulfillment_items_line_check":                 "gönderi kaleminin sipariş satırı boş olamaz",
	"fulfillment_manual_shipments_key_check":       "idempotency anahtarı boş olamaz",
	"fulfillment_manual_shipments_reference_check": "gönderinin referansı boş olamaz",
}

// tutarKisitlari kargo tutarının aralığını denetleyen CHECK kısıtlarıdır.
// Ayrı tutulurlar çünkü kodları [codeAmountOutOfRange]'dir: istemci hangi
// alanın sınırı aştığını kodun kendisinden ayırt edebilmelidir.
var tutarKisitlari = map[string]string{
	"shipping_options_amount_nonneg": "kargo tutarı negatif olamaz",
	"shipping_options_amount_max":    "kargo tutarı üst sınırı aşıyor",
}

// jsonNullLiteral jsonb sütununda "değer yok" anlamına gelen JSON gövdesidir.
// Sürücü NULL bir sütunu boş dilim, JSON null'ı ise bu dize olarak verir.
const jsonNullLiteral = "null"

// jsonEmptyObject boş bir JSON nesnesidir; NOT NULL sütunların varsayılanıdır.
const jsonEmptyObject = "{}"

// PostgreSQL SQLSTATE kodları.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	sqlStateDeadlockDetected    = "40P01"
)

// profileNotFound eksik kargo profili için ortak hatayı üretir.
func profileNotFound(id string) error {
	return errors.NotFound(codeProfileNotFound, "kargo profili bulunamadı: %s", id)
}

// optionNotFound eksik kargo seçeneği için ortak hatayı üretir.
func optionNotFound(id string) error {
	return errors.NotFound(codeOptionNotFound, "kargo seçeneği bulunamadı: %s", id)
}

// ruleNotFound eksik kural için ortak hatayı üretir.
func ruleNotFound(id string) error {
	return errors.NotFound(codeRuleNotFound, "kargo seçeneği kuralı bulunamadı: %s", id)
}

// fulfillmentNotFound eksik gönderi için ortak hatayı üretir.
func fulfillmentNotFound(id string) error {
	return errors.NotFound(codeFulfillmentNotFound, "gönderi bulunamadı: %s", id)
}

// manualShipmentNotFound eksik sağlayıcı gönderisi için ortak hatayı üretir.
func manualShipmentNotFound(id string) error {
	return errors.NotFound(codeManualShipmentNotFound,
		"manuel sağlayıcı gönderisi bulunamadı: %s", id)
}

// locationNotFound politikası olmayan depo için ortak hatayı üretir.
//
// "Politikası yok" ile "depo yok" AYNI ŞEY DEĞİLDİR ve mesaj bunu söyler:
// bu modül bir deponun var olup olmadığını bilmez (o stok modülünün bilgisidir),
// yalnızca kendi kaydının bulunmadığını bildirir.
func locationNotFound(id string) error {
	return errors.NotFound(codeLocationNotFound,
		"depo kargo politikası bulunamadı: %s", id)
}

// classify sürücü hatasını tipli hataya çevirir.
//
// Benzersizlik foreign key ve CHECK ihlalleri istemcinin düzeltebileceği
// durumlardır; sınıflandırılmazsa hepsi 500 olarak görünür ve gerçek sebep
// yalnızca logda kalırdı. Kilitlenme (deadlock) de aynı sebeple ayrı ele
// alınır: işlemin kendisinde bir yanlışlık yoktur, YENİDEN DENENEBİLİR.
func classify(err error, code, format string, a ...any) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}

	switch pgErr.Code {
	case sqlStateUniqueViolation:
		switch pgErr.ConstraintName {
		case constraintProfileNameUniq:
			return errors.Wrap(err, errors.KindConflict, codeProfileNameExists,
				"bu adda bir kargo profili zaten var")
		case constraintFulfillmentKeyUniq:
			return errors.Wrap(err, errors.KindConflict, codeFulfillmentExists,
				"bu idempotency anahtarıyla oluşturulmuş bir gönderi zaten var")
		case constraintFulfillmentItemUniq:
			return errors.Wrap(err, errors.KindConflict, codeItemExists,
				"aynı sipariş satırı gönderide iki kez yer alamaz")
		}
	case sqlStateForeignKeyViolation:
		return foreignKeyError(err, pgErr.ConstraintName)
	case sqlStateCheckViolation:
		return checkError(err, pgErr.ConstraintName, code, format, a...)
	case sqlStateDeadlockDetected:
		// Gönderi akışları tek satır kilidi aldığı için normal işleyişte
		// oluşmaz; burası son savunmadır. İşlem geri alınmıştır, aynı istek
		// olduğu gibi yeniden denenebilir — bu yüzden Internal (500) değil
		// Conflict.
		return errors.Wrap(err, errors.KindConflict, codeConcurrentUpdate,
			"eşzamanlı bir işlemle çakışıldı; istek yeniden denenebilir")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// foreignKeyError foreign key ihlalini anlamlı bir tipli hataya çevirir.
//
// Hangi bağın kırıldığını kısıt adı söyler: seçenek profile, gönderi seçeneğe,
// kalem gönderiye bağlıdır. Gönderisi olan bir seçeneğin SİLİNMESİ ise
// RESTRICT'e takılır ve bu bir çakışmadır — istemci önce gönderiyi
// kapatmalıdır.
func foreignKeyError(err error, constraint string) error {
	switch {
	case strings.Contains(constraint, "shipping_profile_id"):
		return errors.Wrap(err, errors.KindNotFound, codeProfileNotFound,
			"kargo profili bulunamadı")
	case strings.Contains(constraint, "shipping_option_id"):
		// Gönderi tablosundan seçeneğe verilen RESTRICT kısıtı iki yönde de
		// aynı adı taşır: eksik seçeneğe yazma da, kullanılan seçeneği silme
		// de buraya düşer. Silme yolu servis tarafında yumuşak silmeye
		// çevrildiği için burada kalan durum eksik seçenektir.
		return errors.Wrap(err, errors.KindNotFound, codeOptionNotFound,
			"kargo seçeneği bulunamadı")
	case strings.Contains(constraint, "fulfillment_id"):
		return errors.Wrap(err, errors.KindNotFound, codeFulfillmentNotFound,
			"gönderi bulunamadı")
	default:
		return errors.Wrap(err, errors.KindNotFound, codeOptionNotFound,
			"bağlı kayıt bulunamadı")
	}
}

// checkError CHECK kısıtı ihlalini anlamlı bir tipli hataya çevirir.
func checkError(err error, constraint, code, format string, a ...any) error {
	if message, ok := tutarKisitlari[constraint]; ok {
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange, "%s", message)
	}
	if message, ok := kisitMesajlari[constraint]; ok {
		return errors.Wrap(err, errors.KindInvalid, codeRuleInvalid, "%s", message)
	}
	switch {
	case constraint == "fulfillment_items_quantity_check":
		return errors.Wrap(err, errors.KindInvalid, codeQuantityOutOfRange,
			"gönderi kalemi adedi pozitif olmalı")
	case strings.HasSuffix(constraint, constraintStampSuffix):
		// Durum yazıldı ama ona eşlik eden zaman damgası boş bırakıldı.
		// Servis bunu üretmez; buraya düşen bir hata modülün kendi
		// tutarsızlığıdır ve teşhis edilmelidir.
		return errors.Wrap(err, errors.KindInternal, codeStampMissing,
			"gönderi durumu, zaman damgası olmadan yazılamaz")
	case strings.HasSuffix(constraint, constraintStatusSuffix):
		return errors.Wrap(err, errors.KindInvalid, codeStatusInvalid,
			"tanımsız durum değeri")
	case strings.HasSuffix(constraint, "_currency_format"):
		return errors.Wrap(err, errors.KindInvalid, codeCurrencyInvalid,
			"para birimi üç harfli ISO 4217 kodu olmalı")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// --- çeviri ------------------------------------------------------------------

// toTime pgtype damgasını UTC time.Time'a çevirir.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toTimePtr nullable damgayı *time.Time'a çevirir.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// fromTimePtr *time.Time'ı pgtype damgasına çevirir; nil SQL NULL olur.
func fromTimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// toJSONRaw jsonb sütununu ham JSON'a çevirir. Boş sütun nil döner.
func toJSONRaw(raw []byte) json.RawMessage {
	if len(raw) == 0 || string(raw) == jsonNullLiteral || string(raw) == jsonEmptyObject {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

// fromJSONRaw ham JSON'ı jsonb sütununa yazılacak bayta çevirir.
//
// Sütun NOT NULL'dur ve "veri yok" ile "veri boş" ayrımı bu modülde bir şey
// ifade etmez; sağlayıcı verisi olmayan bir gönderi boş nesne taşır.
func fromJSONRaw(raw json.RawMessage) []byte {
	if len(raw) == 0 || string(raw) == jsonNullLiteral {
		return []byte(jsonEmptyObject)
	}
	return raw
}

// toJSONMap jsonb sütununu haritaya çevirir.
//
// Boş ya da JSON null değer nil harita döner; böylece API yanıtında
// "metadata": null yerine alan hiç görünmez (omitempty).
func toJSONMap(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == jsonNullLiteral {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeDataInvalid,
			"JSON alanı çözümlenemedi")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromJSONMap haritayı jsonb sütununa yazılacak bayta çevirir.
func fromJSONMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte(jsonEmptyObject), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, codeDataInvalid,
			"JSON alanı kodlanamadı")
	}
	return raw, nil
}

// toProfile veritabanı satırını domain modeline çevirir.
func toProfile(row fulfillmentdb.ShippingProfile) (models.ShippingProfile, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.ShippingProfile{}, err
	}
	return models.ShippingProfile{
		ID:        row.ID,
		Name:      row.Name,
		Type:      models.ProfileType(row.Type),
		Metadata:  meta,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}, nil
}

// toOption veritabanı satırını domain modeline çevirir.
//
// Rules DOLDURULMAZ: kurallar ayrı bir sorgudan gelir ve çağıran onları toplu
// olarak (N+1 yapmadan) iliştirir.
func toOption(row fulfillmentdb.ShippingOption) (models.ShippingOption, error) {
	data, err := toJSONMap(row.Data)
	if err != nil {
		return models.ShippingOption{}, err
	}
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.ShippingOption{}, err
	}
	return models.ShippingOption{
		ID:                row.ID,
		Name:              row.Name,
		ProviderID:        row.ProviderID,
		ShippingProfileID: row.ShippingProfileID,
		PriceType:         models.PriceType(row.PriceType),
		Amount:            row.Amount,
		CurrencyCode:      row.CurrencyCode,
		RegionID:          row.RegionID,
		IsReturn:          row.IsReturn,
		AdminOnly:         row.AdminOnly,
		Data:              data,
		Metadata:          meta,
		CreatedAt:         toTime(row.CreatedAt),
		UpdatedAt:         toTime(row.UpdatedAt),
		DeletedAt:         toTimePtr(row.DeletedAt),
	}, nil
}

// toRule veritabanı satırını domain modeline çevirir.
func toRule(row fulfillmentdb.ShippingOptionRule) models.ShippingOptionRule {
	values := make([]string, len(row.RuleValues))
	copy(values, row.RuleValues)
	return models.ShippingOptionRule{
		ID:               row.ID,
		ShippingOptionID: row.ShippingOptionID,
		Attribute:        row.Attribute,
		Operator:         models.RuleOperator(row.Operator),
		Values:           values,
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
		DeletedAt:        toTimePtr(row.DeletedAt),
	}
}

// toFulfillment veritabanı satırını domain modeline çevirir.
//
// Items DOLDURULMAZ: kalemler ayrı bir sorgudan gelir ve çağıran onları toplu
// olarak iliştirir.
func toFulfillment(row fulfillmentdb.Fulfillment) (models.Fulfillment, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Fulfillment{}, err
	}
	return models.Fulfillment{
		ID:               row.ID,
		Reference:        row.Reference,
		ShippingOptionID: row.ShippingOptionID,
		ProviderID:       row.ProviderID,
		ExternalID:       row.ExternalID,
		Status:           models.FulfillmentStatus(row.Status),
		TrackingNumber:   row.TrackingNumber,
		TrackingURL:      row.TrackingUrl,
		IdempotencyKey:   row.IdempotencyKey,
		ShippedAt:        toTimePtr(row.ShippedAt),
		DeliveredAt:      toTimePtr(row.DeliveredAt),
		CanceledAt:       toTimePtr(row.CanceledAt),
		Data:             toJSONRaw(row.Data),
		Metadata:         meta,
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
		DeletedAt:        toTimePtr(row.DeletedAt),
	}, nil
}

// toItem veritabanı satırını domain modeline çevirir.
func toItem(row fulfillmentdb.FulfillmentItem) models.FulfillmentItem {
	return models.FulfillmentItem{
		ID:            row.ID,
		FulfillmentID: row.FulfillmentID,
		LineItemID:    row.LineItemID,
		Quantity:      row.Quantity,
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
	}
}

// toManualShipment veritabanı satırını sağlayıcının defter modeline çevirir.
func toManualShipment(row fulfillmentdb.FulfillmentManualShipment) models.ManualShipment {
	return models.ManualShipment{
		ID:             row.ID,
		IdempotencyKey: row.IdempotencyKey,
		Reference:      row.Reference,
		OptionID:       row.OptionID,
		Status:         models.FulfillmentStatus(row.Status),
		TrackingNumber: row.TrackingNumber,
		TrackingURL:    row.TrackingUrl,
		Data:           toJSONRaw(row.Data),
		CreatedAt:      toTime(row.CreatedAt),
		UpdatedAt:      toTime(row.UpdatedAt),
	}
}
