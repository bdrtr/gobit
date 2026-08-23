package repository

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/repository/cartdb"
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
	codeCartNotFound        = "cart_not_found"
	codeLineItemNotFound    = "cart_line_item_not_found"
	codeShippingNotFound    = "cart_shipping_method_not_found"
	codeCartCompleted       = "cart_completed"
	codeLineItemExists      = "cart_line_item_exists"
	codeAddressExists       = "cart_address_exists"
	codeShippingOptionTaken = "cart_shipping_option_already_added"
	codeTotalsInconsistent  = "cart_totals_inconsistent"
	codeAmountOutOfRange    = "cart_amount_out_of_range"
	codeMetadataInvalid     = "cart_metadata_invalid"
	codeTxRequired          = "cart_tx_required"
	codeQueryFailed         = "cart_query_failed"
	codeConcurrentUpdate    = "cart_concurrent_update"
)

// Kısıt adları; sürücü hatasını anlamlı bir tipli hataya çevirmek için
// kullanılır. Adlar migration'daki adlarla BİREBİR aynıdır.
const (
	constraintLineVariantUniq = "cart_line_items_cart_variant_uniq"
	constraintAddressTypeUniq = "cart_addresses_cart_type_uniq"
	constraintShippingOptUniq = "cart_shipping_methods_cart_option_uniq"
	constraintCartTotals      = "carts_totals_consistent"
	constraintLineTotals      = "cart_line_items_totals_consistent"
	constraintTotalsRevRange  = "carts_totals_revision_range"
	constraintLineQtyPositive = "cart_line_items_quantity_positive"
	constraintCartLineItemsFK = "cart_line_items_cart_id_fkey"
	constraintCartAddressesFK = "cart_addresses_cart_id_fkey"
	constraintCartShippingFK  = "cart_shipping_methods_cart_id_fkey"
	// constraintNonnegSuffix negatif para yasağı koyan tüm CHECK kısıtlarının
	// ortak sonekidir; tek tek saymak yerine sonekle tanınırlar.
	constraintNonnegSuffix = "_nonneg"
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
		switch pgErr.ConstraintName {
		case constraintLineVariantUniq:
			return errors.Wrap(err, errors.KindConflict, codeLineItemExists,
				"bu varyant sepette zaten var; adedi artırılmalı")
		case constraintAddressTypeUniq:
			return errors.Wrap(err, errors.KindConflict, codeAddressExists,
				"sepette bu türden bir kayıt zaten var")
		case constraintShippingOptUniq:
			return errors.Wrap(err, errors.KindConflict, codeShippingOptionTaken,
				"bu kargo seçeneği sepete zaten eklenmiş")
		}
	case sqlStateForeignKeyViolation:
		// Satır, adresi ya da kargo yöntemi OLMAYAN bir sepete bağlanamaz.
		switch pgErr.ConstraintName {
		case constraintCartLineItemsFK, constraintCartAddressesFK, constraintCartShippingFK:
			return errors.Wrap(err, errors.KindNotFound, codeCartNotFound, "sepet bulunamadı")
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

// classifyCheck CHECK kısıtı ihlallerini tipli hataya çevirir.
//
// Toplam kimliği ihlali ayrı tutulur: servis aynı kontrolü daha okunabilir bir
// hatayla ÖNCE yapar, buraya düşmesi kontrolün atlandığı (ya da doğrudan SQL
// müdahalesi olduğu) anlamına gelir ve mesaj bunu söylemelidir.
func classifyCheck(err error, constraint, code, format string, a ...any) error {
	switch {
	case constraint == constraintCartTotals:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"sepet toplamları tutarsız: total = subtotal - discount_total + tax_total + shipping_total olmalı")
	case constraint == constraintLineTotals:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"satır toplamları tutarsız: total = subtotal - discount_total + tax_total olmalı")
	case constraint == constraintTotalsRevRange:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"toplamlar henüz olmayan bir sepet şekli için damgalanamaz")
	case constraint == constraintLineQtyPositive:
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange,
			"satır adedi pozitif olmalı")
	case strings.HasSuffix(constraint, constraintNonnegSuffix):
		// carts_*_nonneg, cart_line_items_*_nonneg ve
		// cart_shipping_methods_amount_nonneg aynı sınıfa düşer: negatif para.
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

// toCart veritabanı satırını domain modeline çevirir.
func toCart(row cartdb.Cart) (models.Cart, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Cart{}, err
	}
	return models.Cart{
		ID:             row.ID,
		RegionID:       row.RegionID,
		CustomerID:     stringValue(row.CustomerID),
		Email:          stringValue(row.Email),
		CurrencyCode:   row.CurrencyCode,
		Subtotal:       row.Subtotal,
		DiscountTotal:  row.DiscountTotal,
		TaxTotal:       row.TaxTotal,
		ShippingTotal:  row.ShippingTotal,
		Total:          row.Total,
		Revision:       row.Revision,
		TotalsRevision: row.TotalsRevision,
		Metadata:       meta,
		CompletedAt:    toTimePtr(row.CompletedAt),
		CreatedAt:      toTime(row.CreatedAt),
		UpdatedAt:      toTime(row.UpdatedAt),
		DeletedAt:      toTimePtr(row.DeletedAt),
	}, nil
}

// toCarts satır dilimini domain modeli dilimine çevirir.
func toCarts(rows []cartdb.Cart) ([]models.Cart, error) {
	out := make([]models.Cart, 0, len(rows))
	// Döngü indeksle gezilir: satır yapıları büyüktür ve değerle kopyalamak
	// her tur birkaç yüz baytı boşuna taşır.
	for i := range rows {
		cart, err := toCart(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, cart)
	}
	return out, nil
}

// toLineItem veritabanı satırını domain modeline çevirir.
func toLineItem(row cartdb.CartLineItem) (models.LineItem, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.LineItem{}, err
	}
	return models.LineItem{
		ID:            row.ID,
		CartID:        row.CartID,
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
func toLineItems(rows []cartdb.CartLineItem) ([]models.LineItem, error) {
	out := make([]models.LineItem, 0, len(rows))
	for i := range rows {
		item, err := toLineItem(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// toCartAddress veritabanı satırını domain modeline çevirir.
func toCartAddress(row cartdb.CartAddress) (models.CartAddress, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.CartAddress{}, err
	}
	return models.CartAddress{
		ID:              row.ID,
		CartID:          row.CartID,
		Type:            models.AddressType(row.AddressType),
		SourceAddressID: stringValue(row.SourceAddressID),
		FirstName:       stringValue(row.FirstName),
		LastName:        stringValue(row.LastName),
		Company:         stringValue(row.Company),
		Address1:        stringValue(row.Address1),
		Address2:        stringValue(row.Address2),
		City:            stringValue(row.City),
		Province:        stringValue(row.Province),
		PostalCode:      stringValue(row.PostalCode),
		CountryCode:     stringValue(row.CountryCode),
		Phone:           stringValue(row.Phone),
		Metadata:        meta,
		CreatedAt:       toTime(row.CreatedAt),
		UpdatedAt:       toTime(row.UpdatedAt),
	}, nil
}

// toShippingMethod veritabanı satırını domain modeline çevirir.
func toShippingMethod(row cartdb.CartShippingMethod) (models.ShippingMethod, error) {
	data, err := toJSONMap(row.Data)
	if err != nil {
		return models.ShippingMethod{}, err
	}
	return models.ShippingMethod{
		ID:               row.ID,
		CartID:           row.CartID,
		Name:             row.Name,
		ShippingOptionID: stringValue(row.ShippingOptionID),
		Amount:           row.Amount,
		Data:             data,
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
	}, nil
}
