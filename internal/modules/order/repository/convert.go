package repository

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// Bu dosya pgtype <-> domain modeli dönüşümlerinin ve sürücü hatası
// sınıflandırmasının TEK yeridir.
//
// Sınırın burada olması bilinçlidir: sürücüye özgü tipler (pgtype.Timestamptz,
// jsonb için []byte, *pgconn.PgError) repository'nin dışına ÇIKMAZ. Servis ve
// API katmanı time.Time, map[string]any ve core/errors tipli hatalarını görür.

// Hata kodları. Çağıran taraf errors.CodeOf ile bunlara bakabilir; API katmanı
// da aynı kodları istemciye geçirir.
const (
	codeOrderNotFound      = "order_not_found"
	codeSummaryNotFound    = "order_summary_not_found"
	codeReturnNotFound     = "order_return_not_found"
	codeExchangeNotFound   = "order_exchange_not_found"
	codeClaimNotFound      = "order_claim_not_found"
	codeDisplayIDTaken     = "order_display_id_taken"
	codeIdempotencyReplay  = "order_idempotency_key_taken"
	codeSummaryExists      = "order_summary_exists"
	codeOrderExists        = "order_already_exists"
	codeStateChanged       = "order_state_changed"
	codeTotalsInconsistent = "order_totals_inconsistent"
	codeAmountOutOfRange   = "order_amount_out_of_range"
	codeStatusInvalid      = "order_status_invalid"
	codeInconsistentState  = "order_inconsistent_state"
	codeMetadataInvalid    = "order_metadata_invalid"
	codeTxRequired         = "order_tx_required"
	codeQueryFailed        = "order_query_failed"
	codeConcurrentUpdate   = "order_concurrent_update"
)

// Kısıt adları; sürücü hatasını anlamlı bir tipli hataya çevirmek için
// kullanılır. Adlar migration'daki adlarla BİREBİR aynıdır.
const (
	constraintOrdersPK            = "orders_pkey"
	constraintDisplayIDUniq       = "orders_display_id_uniq"
	constraintIdempotencyUniq     = "orders_idempotency_key_uniq"
	constraintSummaryOrderUniq    = "order_summaries_order_id_key"
	constraintOrderTotals         = "orders_totals_consistent"
	constraintOrderDiscount       = "orders_discount_within_subtotal"
	constraintLineTotals          = "order_line_items_totals_consistent"
	constraintLineDiscount        = "order_line_items_discount_within_subtotal"
	constraintLineQtyPositive     = "order_line_items_quantity_positive"
	constraintRefundWithinPaid    = "order_summaries_refund_within_paid"
	constraintOrdersCanceledStamp = "orders_canceled_stamp"
	constraintOrdersCompleteStamp = "orders_completed_stamp"
	// constraintStatusSuffix durum kümesini zorlayan tüm CHECK kısıtlarının
	// ortak sonekidir (orders_status_valid, order_returns_status_valid …);
	// tek tek saymak yerine sonekle tanınırlar.
	constraintStatusSuffix = "_status_valid"
	// constraintNonnegSuffix negatif para yasağı koyan tüm CHECK kısıtlarının
	// ortak sonekidir.
	constraintNonnegSuffix = "_nonneg"
	// constraintOrderFKSuffix siparişe bağlanan tüm çocuk tablolarının foreign
	// key adlarının ortak sonekidir.
	constraintOrderFKSuffix = "_order_id_fkey"
)

// PostgreSQL SQLSTATE kodları.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	sqlStateDeadlockDetected    = "40P01"
)

// classify sürücü hatasını tipli hataya çevirir.
//
// Benzersizlik, foreign key ve CHECK ihlalleri istemcinin (ya da workflow'un)
// düzeltebileceği durumlardır; sınıflandırılmazsa hepsi 500 olarak görünür ve
// gerçek sebep yalnızca logda kalırdı. Kilitlenme (deadlock) de aynı sebeple
// ayrı ele alınır: işlemin kendisinde bir yanlışlık yoktur, YENİDEN DENENEBİLİR.
func classify(err error, code, format string, a ...any) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}

	switch pgErr.Code {
	case sqlStateUniqueViolation:
		return classifyUnique(err, pgErr.ConstraintName, code, format, a...)
	case sqlStateForeignKeyViolation:
		// Satır, özet ya da iade kaydı OLMAYAN bir siparişe bağlanamaz.
		if strings.HasSuffix(pgErr.ConstraintName, constraintOrderFKSuffix) {
			return errors.Wrap(err, errors.KindNotFound, codeOrderNotFound, "sipariş bulunamadı")
		}
	case sqlStateCheckViolation:
		return classifyCheck(err, pgErr.ConstraintName, code, format, a...)
	case sqlStateDeadlockDetected:
		// Kilit sırası tekleştirildiği için normal akışlarda oluşmaz; burası
		// son savunmadır. İşlem geri alınmıştır, aynı istek olduğu gibi
		// yeniden denenebilir — bu yüzden Internal (500) değil Conflict.
		return errors.Wrap(err, errors.KindConflict, codeConcurrentUpdate,
			"eşzamanlı bir işlemle çakışıldı; istek yeniden denenebilir")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// classifyUnique benzersizlik ihlallerini tipli hataya çevirir.
func classifyUnique(err error, constraint, code, format string, a ...any) error {
	switch constraint {
	case constraintDisplayIDUniq:
		// Sequence çakışmaz; buraya düşmek sequence'ın elle geri sarıldığı
		// (setval) ya da bir kaydın kopyalandığı anlamına gelir. Sipariş
		// yazılmadan yakalanması bu kısıtın var oluş sebebidir.
		return errors.Wrap(err, errors.KindConflict, codeDisplayIDTaken,
			"sipariş numarası zaten kullanılıyor; display_id sequence'ı elle değiştirilmiş olabilir")
	case constraintIdempotencyUniq:
		return errors.Wrap(err, errors.KindConflict, codeIdempotencyReplay,
			"bu idempotency anahtarıyla bir sipariş zaten var")
	case constraintSummaryOrderUniq:
		return errors.Wrap(err, errors.KindConflict, codeSummaryExists,
			"siparişin özeti zaten var")
	case constraintOrdersPK:
		return errors.Wrap(err, errors.KindConflict, codeOrderExists,
			"bu kimlikle bir sipariş zaten var")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// classifyCheck CHECK kısıtı ihlallerini tipli hataya çevirir.
//
// Toplam kimliği ihlali ayrı tutulur: servis aynı kontrolü daha okunabilir bir
// hatayla ÖNCE yapar, buraya düşmesi kontrolün atlandığı (ya da doğrudan SQL
// müdahalesi olduğu) anlamına gelir ve mesaj bunu söylemelidir.
func classifyCheck(err error, constraint, code, format string, a ...any) error {
	switch {
	case constraint == constraintOrderTotals:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"sipariş toplamları tutarsız: total = subtotal - discount_total + tax_total + shipping_total olmalı")
	case constraint == constraintLineTotals:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"satır toplamları tutarsız: total = subtotal - discount_total + tax_total olmalı")
	case constraint == constraintOrderDiscount, constraint == constraintLineDiscount:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"indirim ara toplamı aşamaz (kısıt: %s)", constraint)
	case constraint == constraintLineQtyPositive:
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange,
			"satır adedi pozitif olmalı")
	case constraint == constraintRefundWithinPaid:
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange,
			"iade edilen tutar tahsil edilen tutarı aşamaz")
	case constraint == constraintOrdersCanceledStamp, constraint == constraintOrdersCompleteStamp:
		// Durum ile damganın ayrışması yalnızca doğrudan SQL müdahalesiyle
		// mümkündür; servisin hiçbir yolu bu kısıtı ihlal edemez.
		return errors.Wrap(err, errors.KindInternal, codeInconsistentState,
			"sipariş durumu ile zaman damgası tutarsız (kısıt: %s)", constraint)
	case strings.HasSuffix(constraint, constraintStatusSuffix):
		return errors.Wrap(err, errors.KindInvalid, codeStatusInvalid,
			"tanımsız durum değeri (kısıt: %s)", constraint)
	case strings.HasSuffix(constraint, constraintNonnegSuffix):
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange,
			"tutar negatif olamaz (kısıt: %s)", constraint)
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
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

// toTime timestamptz değerini UTC time.Time'a çevirir.
//
// Geçersiz (NULL) değer sıfır zaman döner: NOT NULL sütunlarda bu durum
// oluşmaz, dolayısıyla sıfır zaman görülmesi veri bozukluğunun işaretidir.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toTimePtr nullable timestamptz değerini *time.Time'a çevirir.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
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
		return nil, errors.Wrap(err, errors.KindInternal, codeMetadataInvalid,
			"JSON alanı çözümlenemedi")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromJSONMap haritayı jsonb sütununa yazılacak bayta çevirir.
//
// nil harita boş nesneye ('{}') çevrilir: sütun NOT NULL'dur ve "veri yok" ile
// "veri boş" ayrımı bu modülde bir şey ifade etmez.
func fromJSONMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, codeMetadataInvalid,
			"JSON alanı kodlanamadı")
	}
	return raw, nil
}

// toOrder veritabanı satırını domain modeline çevirir.
func toOrder(row orderdb.Order) (models.Order, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Order{}, err
	}
	return models.Order{
		ID:             row.ID,
		DisplayID:      row.DisplayID,
		Status:         models.OrderStatus(row.Status),
		RegionID:       row.RegionID,
		CustomerID:     stringValue(row.CustomerID),
		Email:          stringValue(row.Email),
		CurrencyCode:   row.CurrencyCode,
		CartID:         stringValue(row.CartID),
		IdempotencyKey: stringValue(row.IdempotencyKey),
		Subtotal:       row.Subtotal,
		DiscountTotal:  row.DiscountTotal,
		TaxTotal:       row.TaxTotal,
		ShippingTotal:  row.ShippingTotal,
		Total:          row.Total,
		Metadata:       meta,
		PlacedAt:       toTime(row.PlacedAt),
		CompletedAt:    toTimePtr(row.CompletedAt),
		CanceledAt:     toTimePtr(row.CanceledAt),
		CancelReason:   stringValue(row.CancelReason),
		CreatedAt:      toTime(row.CreatedAt),
		UpdatedAt:      toTime(row.UpdatedAt),
		DeletedAt:      toTimePtr(row.DeletedAt),
	}, nil
}

// toOrders satır dilimini domain modeli dilimine çevirir.
func toOrders(rows []orderdb.Order) ([]models.Order, error) {
	out := make([]models.Order, 0, len(rows))
	// Döngü indeksle gezilir: satır yapıları büyüktür ve değerle kopyalamak
	// her tur birkaç yüz baytı boşuna taşır.
	for i := range rows {
		order, err := toOrder(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, order)
	}
	return out, nil
}

// toLineItem veritabanı satırını domain modeline çevirir.
func toLineItem(row orderdb.OrderLineItem) (models.OrderLineItem, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.OrderLineItem{}, err
	}
	return models.OrderLineItem{
		ID:            row.ID,
		OrderID:       row.OrderID,
		VariantID:     row.VariantID,
		Title:         row.Title,
		Quantity:      row.Quantity,
		UnitPrice:     row.UnitPrice,
		Subtotal:      row.Subtotal,
		DiscountTotal: row.DiscountTotal,
		TaxTotal:      row.TaxTotal,
		Total:         row.Total,
		Metadata:      meta,
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
	}, nil
}

// toLineItems satır dilimini domain modeli dilimine çevirir.
func toLineItems(rows []orderdb.OrderLineItem) ([]models.OrderLineItem, error) {
	out := make([]models.OrderLineItem, 0, len(rows))
	for i := range rows {
		item, err := toLineItem(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// toSummary veritabanı satırını domain modeline çevirir.
func toSummary(row orderdb.OrderSummary) models.OrderSummary {
	return models.OrderSummary{
		ID:            row.ID,
		OrderID:       row.OrderID,
		PaidTotal:     row.PaidTotal,
		RefundedTotal: row.RefundedTotal,
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
	}
}

// toReturn veritabanı satırını domain modeline çevirir.
func toReturn(row orderdb.OrderReturn) (models.Return, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Return{}, err
	}
	return models.Return{
		ID:           row.ID,
		OrderID:      row.OrderID,
		Status:       models.ReturnStatus(row.Status),
		RefundAmount: row.RefundAmount,
		Reason:       stringValue(row.Reason),
		Note:         stringValue(row.Note),
		Metadata:     meta,
		ReceivedAt:   toTimePtr(row.ReceivedAt),
		CanceledAt:   toTimePtr(row.CanceledAt),
		CreatedAt:    toTime(row.CreatedAt),
		UpdatedAt:    toTime(row.UpdatedAt),
	}, nil
}

// toReturns satır dilimini domain modeli dilimine çevirir.
func toReturns(rows []orderdb.OrderReturn) ([]models.Return, error) {
	out := make([]models.Return, 0, len(rows))
	for i := range rows {
		item, err := toReturn(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// toExchange veritabanı satırını domain modeline çevirir.
func toExchange(row orderdb.OrderExchange) (models.Exchange, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Exchange{}, err
	}
	return models.Exchange{
		ID:            row.ID,
		OrderID:       row.OrderID,
		Status:        models.ExchangeStatus(row.Status),
		DifferenceDue: row.DifferenceDue,
		Note:          stringValue(row.Note),
		Metadata:      meta,
		CompletedAt:   toTimePtr(row.CompletedAt),
		CanceledAt:    toTimePtr(row.CanceledAt),
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
	}, nil
}

// toExchanges satır dilimini domain modeli dilimine çevirir.
func toExchanges(rows []orderdb.OrderExchange) ([]models.Exchange, error) {
	out := make([]models.Exchange, 0, len(rows))
	for i := range rows {
		item, err := toExchange(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// toClaim veritabanı satırını domain modeline çevirir.
func toClaim(row orderdb.OrderClaim) (models.Claim, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Claim{}, err
	}
	return models.Claim{
		ID:           row.ID,
		OrderID:      row.OrderID,
		Type:         models.ClaimType(row.ClaimType),
		Status:       models.ClaimStatus(row.Status),
		RefundAmount: row.RefundAmount,
		Reason:       stringValue(row.Reason),
		Note:         stringValue(row.Note),
		Metadata:     meta,
		CompletedAt:  toTimePtr(row.CompletedAt),
		CanceledAt:   toTimePtr(row.CanceledAt),
		CreatedAt:    toTime(row.CreatedAt),
		UpdatedAt:    toTime(row.UpdatedAt),
	}, nil
}

// toClaims satır dilimini domain modeli dilimine çevirir.
func toClaims(rows []orderdb.OrderClaim) ([]models.Claim, error) {
	out := make([]models.Claim, 0, len(rows))
	for i := range rows {
		item, err := toClaim(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
