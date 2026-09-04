package repository

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
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
	codeCollectionNotFound    = "payment_collection_not_found"
	codeSessionNotFound       = "payment_session_not_found"
	codePaymentNotFound       = "payment_not_found"
	codeManualSessionNotFound = "payment_manual_session_not_found"
	codeSessionExists         = "payment_session_idempotency_key_exists"
	codePaymentExists         = "payment_session_already_captured"
	codeAmountOutOfRange      = "payment_amount_out_of_range"
	codeInconsistentAmounts   = "payment_amounts_inconsistent"
	codeStatusInvalid         = "payment_status_invalid"
	codeCurrencyInvalid       = "payment_currency_invalid"
	codeDataInvalid           = "payment_json_invalid"
	codeTxRequired            = "payment_tx_required"
	codeTxBeginFailed         = "payment_tx_begin_failed"
	codeTxCommitFailed        = "payment_tx_commit_failed"
	codeQueryFailed           = "payment_query_failed"
	codeConcurrentUpdate      = "payment_concurrent_update"
)

// Kısıt ve indeks adları; sürücü hatasını anlamlı bir tipli hataya çevirmek
// için kullanılır. Adlar migration'daki adlarla BİREBİR aynıdır.
const (
	constraintSessionIdempotencyUniq = "payment_sessions_provider_idempotency_uniq"
	constraintManualIdempotencyUniq  = "payment_manual_sessions_idempotency_uniq"
	constraintPaymentSessionUniq     = "payments_session_uniq"
	// constraintCurrencySuffix para birimi biçimini denetleyen tüm CHECK
	// kısıtlarının ortak sonekidir; tek tek saymak yerine sonekle tanınırlar.
	constraintCurrencySuffix = "_currency_format"
	// constraintStatusSuffix durum değerini denetleyen CHECK kısıtlarının
	// ortak sonekidir.
	constraintStatusSuffix = "_status_valid"
	// constraintPositiveSuffix pozitif tutar şartı koyan CHECK kısıtlarının
	// ortak sonekidir.
	constraintPositiveSuffix = "_amount_positive"
)

// Tutar kısıtlarının ortak açıklamaları.
const (
	// msgAuthorizedNonneg bloke tutarın negatife düşemeyeceğini bildirir.
	msgAuthorizedNonneg = "yetkilendirilen tutar negatif olamaz"
	// msgCapturedNonneg tahsil edilen tutarın negatife düşemeyeceğini bildirir.
	msgCapturedNonneg = "tahsil edilen tutar negatif olamaz"
	// msgRefundedNonneg iade edilen tutarın negatife düşemeyeceğini bildirir.
	msgRefundedNonneg = "iade edilen tutar negatif olamaz"
	// msgRefundLeCapture iadenin tahsilatı aşamayacağını bildirir.
	msgRefundLeCapture = "iade edilen tutar tahsil edilen tutarı aşamaz"
)

// tutarKisitlari tutarlar arası tutarlılığı denetleyen CHECK kısıtlarıdır.
// İhlalleri istemcinin düzeltebileceği ÇAKIŞMA durumlarıdır: olmayan parayı
// iade etmek ya da bloke edilenden fazlasını çekmek gibi.
var tutarKisitlari = map[string]string{
	"payment_collections_refund_le_capture":     msgRefundLeCapture,
	"payment_collections_authorized_le_amount":  "yetkilendirilen tutar koleksiyon tutarını aşamaz",
	"payment_collections_captured_le_amount":    "tahsil edilen tutar koleksiyon tutarını aşamaz",
	"payment_sessions_authorized_le_amount":     "yetkilendirilen tutar oturum tutarını aşamaz",
	"payments_refund_le_amount":                 "iade edilen tutar tahsilat tutarını aşamaz",
	"payment_manual_sessions_captured_le_auth":  "tahsil edilen tutar yetkilendirilen tutarı aşamaz",
	"payment_manual_sessions_refund_le_capture": msgRefundLeCapture,
	"payment_collections_authorized_nonneg":     msgAuthorizedNonneg,
	"payment_collections_captured_nonneg":       msgCapturedNonneg,
	"payment_collections_refunded_nonneg":       msgRefundedNonneg,
	"payment_sessions_authorized_nonneg":        msgAuthorizedNonneg,
	"payments_refunded_nonneg":                  msgRefundedNonneg,
	"payment_manual_sessions_authorized_nonneg": msgAuthorizedNonneg,
	"payment_manual_sessions_captured_nonneg":   msgCapturedNonneg,
	"payment_manual_sessions_refunded_nonneg":   msgRefundedNonneg,
}

// PostgreSQL SQLSTATE kodları.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	sqlStateDeadlockDetected    = "40P01"
)

// collectionNotFound eksik koleksiyon için ortak hatayı üretir.
func collectionNotFound(id string) error {
	return errors.NotFound(codeCollectionNotFound, "ödeme koleksiyonu bulunamadı: %s", id)
}

// sessionNotFound eksik oturum için ortak hatayı üretir.
func sessionNotFound(id string) error {
	return errors.NotFound(codeSessionNotFound, "ödeme oturumu bulunamadı: %s", id)
}

// paymentNotFound eksik tahsilat için ortak hatayı üretir.
func paymentNotFound(id string) error {
	return errors.NotFound(codePaymentNotFound, "tahsilat bulunamadı: %s", id)
}

// manualSessionNotFound eksik sağlayıcı oturumu için ortak hatayı üretir.
func manualSessionNotFound(id string) error {
	return errors.NotFound(codeManualSessionNotFound,
		"manuel sağlayıcı oturumu bulunamadı: %s", id)
}

// classify sürücü hatasını tipli hataya çevirir.
//
// Benzersizlik, foreign key ve CHECK ihlalleri istemcinin düzeltebileceği
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
		case constraintSessionIdempotencyUniq, constraintManualIdempotencyUniq:
			return errors.Wrap(err, errors.KindConflict, codeSessionExists,
				"bu idempotency anahtarıyla açılmış bir oturum zaten var")
		case constraintPaymentSessionUniq:
			return errors.Wrap(err, errors.KindConflict, codePaymentExists,
				"bu oturumdan zaten bir tahsilat çıkmış")
		}
	case sqlStateForeignKeyViolation:
		return foreignKeyError(err, pgErr.ConstraintName)
	case sqlStateCheckViolation:
		return checkError(err, pgErr.ConstraintName, code, format, a...)
	case sqlStateDeadlockDetected:
		// Kilit sırası tekleştirildiği için normal akışlarda oluşmaz; burası
		// son savunmadır. İşlem geri alınmıştır, aynı istek olduğu gibi
		// yeniden denenebilir — bu yüzden Internal (500) değil Conflict.
		return errors.Wrap(err, errors.KindConflict, codeConcurrentUpdate,
			"eşzamanlı bir işlemle çakışıldı; istek yeniden denenebilir")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// foreignKeyError foreign key ihlalini eksik üst kayıt hatasına çevirir.
//
// Hangi üst kaydın eksik olduğunu kısıt adı söyler: oturum ve tahsilat
// satırları koleksiyona, tahsilat ayrıca oturuma, iade ise tahsilata bağlıdır.
func foreignKeyError(err error, constraint string) error {
	switch {
	case strings.Contains(constraint, "payment_session_id"):
		return errors.Wrap(err, errors.KindNotFound, codeSessionNotFound,
			"ödeme oturumu bulunamadı")
	case strings.Contains(constraint, "payment_collection_id"):
		return errors.Wrap(err, errors.KindNotFound, codeCollectionNotFound,
			"ödeme koleksiyonu bulunamadı")
	case strings.Contains(constraint, "payment_id"):
		return errors.Wrap(err, errors.KindNotFound, codePaymentNotFound,
			"tahsilat bulunamadı")
	default:
		return errors.Wrap(err, errors.KindNotFound, codeCollectionNotFound,
			"bağlı kayıt bulunamadı")
	}
}

// checkError CHECK kısıtı ihlalini anlamlı bir tipli hataya çevirir.
func checkError(err error, constraint, code, format string, a ...any) error {
	if message, ok := tutarKisitlari[constraint]; ok {
		return errors.Wrap(err, errors.KindConflict, codeInconsistentAmounts, "%s", message)
	}
	switch {
	case strings.HasSuffix(constraint, constraintPositiveSuffix):
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange,
			"tutar pozitif olmalı")
	case strings.HasSuffix(constraint, constraintCurrencySuffix):
		return errors.Wrap(err, errors.KindInvalid, codeCurrencyInvalid,
			"the currency has to be a three-letter ISO 4217 code")
	case strings.HasSuffix(constraint, constraintStatusSuffix):
		return errors.Wrap(err, errors.KindInvalid, codeStatusInvalid,
			"tanımsız durum değeri")
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

// fromTime time.Time'ı pgtype damgasına çevirir.
func fromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// nullString boş dizeyi SQL NULL'a çevirir.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// stringValue SQL NULL'ı boş dizeye çevirir.
func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// jsonOrEmpty boş bir JSON gövdesini '{}' ile doldurur.
//
// Sütun NOT NULL'dur ve "veri yok" ile "veri boş" ayrımı bu modülde bir şey
// ifade etmez; sağlayıcı verisi olmayan bir oturum boş nesne taşır.
func jsonOrEmpty(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

// toJSONRaw jsonb sütununu ham JSON'a çevirir. Boş sütun nil döner.
func toJSONRaw(raw []byte) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

// toJSONMap jsonb sütununu haritaya çevirir.
//
// Boş ya da JSON null değer nil harita döner; böylece API yanıtında
// "metadata": null yerine alan hiç görünmez (omitempty).
func toJSONMap(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
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
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, codeDataInvalid,
			"JSON alanı kodlanamadı")
	}
	return raw, nil
}

// toCollection veritabanı satırını domain modeline çevirir.
func toCollection(row paymentdb.PaymentCollection) (models.PaymentCollection, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.PaymentCollection{}, err
	}
	return models.PaymentCollection{
		ID:               row.ID,
		Reference:        row.Reference,
		Amount:           row.Amount,
		CurrencyCode:     row.CurrencyCode,
		Status:           models.CollectionStatus(row.Status),
		AuthorizedAmount: row.AuthorizedAmount,
		CapturedAmount:   row.CapturedAmount,
		RefundedAmount:   row.RefundedAmount,
		Metadata:         meta,
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
		DeletedAt:        toTimePtr(row.DeletedAt),
	}, nil
}

// toSession veritabanı satırını domain modeline çevirir.
func toSession(row paymentdb.PaymentSession) models.PaymentSession {
	return models.PaymentSession{
		ID:                  row.ID,
		PaymentCollectionID: row.PaymentCollectionID,
		ProviderID:          row.ProviderID,
		ExternalID:          row.ExternalID,
		Status:              models.SessionStatus(row.Status),
		Amount:              row.Amount,
		AuthorizedAmount:    row.AuthorizedAmount,
		CurrencyCode:        row.CurrencyCode,
		Data:                toJSONRaw(row.Data),
		IdempotencyKey:      row.IdempotencyKey,
		DeclineReason:       stringValue(row.DeclineReason),
		CreatedAt:           toTime(row.CreatedAt),
		UpdatedAt:           toTime(row.UpdatedAt),
		DeletedAt:           toTimePtr(row.DeletedAt),
	}
}

// toPayment veritabanı satırını domain modeline çevirir.
func toPayment(row paymentdb.Payment) models.Payment {
	return models.Payment{
		ID:                  row.ID,
		PaymentSessionID:    row.PaymentSessionID,
		PaymentCollectionID: row.PaymentCollectionID,
		Amount:              row.Amount,
		CurrencyCode:        row.CurrencyCode,
		RefundedAmount:      row.RefundedAmount,
		CapturedAt:          toTime(row.CapturedAt),
		CreatedAt:           toTime(row.CreatedAt),
		UpdatedAt:           toTime(row.UpdatedAt),
		DeletedAt:           toTimePtr(row.DeletedAt),
	}
}

// toRefund veritabanı satırını domain modeline çevirir.
func toRefund(row paymentdb.Refund) models.Refund {
	return models.Refund{
		ID:        row.ID,
		PaymentID: row.PaymentID,
		Amount:    row.Amount,
		Reason:    stringValue(row.Reason),
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}
}

// toManualSession veritabanı satırını sağlayıcının defter modeline çevirir.
func toManualSession(row paymentdb.PaymentManualSession) models.ManualSession {
	return models.ManualSession{
		ID:               row.ID,
		IdempotencyKey:   row.IdempotencyKey,
		Reference:        row.Reference,
		Amount:           row.Amount,
		CurrencyCode:     row.CurrencyCode,
		Status:           models.SessionStatus(row.Status),
		AuthorizedAmount: row.AuthorizedAmount,
		CapturedAmount:   row.CapturedAmount,
		RefundedAmount:   row.RefundedAmount,
		Data:             toJSONRaw(row.Data),
		DeclineReason:    stringValue(row.DeclineReason),
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
	}
}
