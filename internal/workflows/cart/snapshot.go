package cart

import (
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Snapshot bir hesap turunun DAYANDIĞI sepet şeklidir.
//
// Tip, [Carts.CartSnapshotJSON] gövdesinin şemasıdır: sepet modülü bu alanları
// üretir, bu paket okur. Şema BİLİNÇLİ OLARAK dardır — hesaba giren ne varsa
// odur ve fazlası yoktur. Tanınmayan alanlar sessizce atlanır ki sepet
// modülü şemayı büyüttüğünde bu paketin güncellenmesi gerekmesin.
//
// Görüntü TEK OKUMADA alınır ve tutarlıdır: satırlar, kargo yöntemleri ve
// [Snapshot.Revision] aynı ana aittir. Alan başına ayrı çağrılar olsaydı
// araya giren bir değişiklik, satırları bir şekilden, revision'ı başka bir
// şekilden okumaya yol açar ve hesap sessizce yanlış damgalanırdı.
type Snapshot struct {
	// ID sepetin kimliğidir.
	ID string `json:"id"`
	// RegionID sepetin bölgesidir; vergi oranı ve fiyat bağlamı buradan gelir.
	RegionID string `json:"region_id"`
	// CustomerID sepetin sahibidir; boşsa sepet misafire aittir.
	CustomerID string `json:"customer_id"`
	// CurrencyCode sepetin para birimidir (ISO 4217).
	CurrencyCode string `json:"currency_code"`
	// Revision sepetin şekil sayacıdır; hesabın damgası budur.
	Revision int64 `json:"revision"`
	// Completed sepetin tamamlanmış olup olmadığını bildirir.
	Completed bool `json:"completed"`
	// Items sepetin satırlarıdır.
	Items []SnapshotItem `json:"items"`
	// ShippingMethods sepete seçilmiş kargo yöntemleridir.
	ShippingMethods []SnapshotShippingMethod `json:"shipping_methods"`
}

// SnapshotItem bir sepet satırının hesaba giren alanlarıdır.
//
// Satırın SAKLI tutarları burada YOKTUR ve olmamalıdır: her hesap turu fiyatı
// pricing'den yeniden alır. Saklı tutarı okuyup güvenmek, katalogda değişen
// bir fiyatın sepette sonsuza kadar donması demek olurdu.
type SnapshotItem struct {
	// ID satırın kimliğidir.
	ID string `json:"id"`
	// VariantID satırın gösterdiği ürün varyantıdır.
	VariantID string `json:"variant_id"`
	// Quantity satırdaki adettir.
	Quantity int64 `json:"quantity"`
}

// SnapshotShippingMethod bir kargo yönteminin hesaba giren alanlarıdır.
type SnapshotShippingMethod struct {
	// ID yöntemin kimliğidir.
	ID string `json:"id"`
	// Amount kargo tutarıdır (minor unit).
	Amount int64 `json:"amount"`
}

// VariantIDs satırların varyant kimliklerini TEKRARSIZ ve satır sırasında
// döner.
//
// Sıra korunur ki toplu link sorgusunun girdisi (dolayısıyla üretilen hata
// mesajları) yeniden üretilebilir olsun; tekrarsızlık ise aynı varyanttan iki
// satır bulunan bir sepette link sorgusunu gereksiz büyütmemek içindir.
func (s Snapshot) VariantIDs() []string {
	seen := make(map[string]struct{}, len(s.Items))
	out := make([]string, 0, len(s.Items))
	for i := range s.Items {
		id := s.Items[i].VariantID
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// decodeSnapshot sepet modülünden gelen gövdeyi çözer ve DOĞRULAR.
//
// Doğrulama, gövdenin sepet modülünden gelmesine rağmen yapılır: bu sınır
// derleyicinin denetleyemediği tek sınırdır (ADR 0006'nın kabul edilen bedeli)
// ve bozuk bir alan sessizce hesabın içine girerse hata, sepetin tutarında
// günler sonra görünürdü. Bozuk gövde errors.Internal'dır — çağıranın
// düzeltebileceği bir şey yoktur, sağlayıcı sözleşmeyi çiğnemiştir.
func decodeSnapshot(cartID string, payload json.RawMessage) (Snapshot, error) {
	var snap Snapshot
	if len(payload) == 0 {
		return Snapshot{}, errors.Internal(CodeSnapshotInvalid,
			"sepet anlık görüntüsü boş geldi: %s", cartID)
	}
	if err := json.Unmarshal(payload, &snap); err != nil {
		return Snapshot{}, errors.Wrap(err, errors.KindInternal, CodeSnapshotInvalid,
			"sepet anlık görüntüsü çözülemedi: %s", cartID)
	}
	if err := snap.validate(cartID); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// validate anlık görüntünün hesaba girebilecek durumda olduğunu doğrular.
func (s Snapshot) validate(cartID string) error {
	if s.ID != cartID {
		return errors.Internal(CodeSnapshotInvalid,
			"anlık görüntü başka bir sepete ait: istenen %s, gelen %q", cartID, s.ID)
	}
	if s.RegionID == "" {
		return errors.Internal(CodeSnapshotInvalid, "sepetin bölgesi boş: %s", cartID)
	}
	if s.CurrencyCode == "" {
		return errors.Internal(CodeSnapshotInvalid, "sepetin para birimi boş: %s", cartID)
	}
	if s.Revision < 0 {
		return errors.Internal(CodeSnapshotInvalid,
			"sepetin şekil sayacı negatif: %s (%d)", cartID, s.Revision)
	}

	for i := range s.Items {
		if err := s.Items[i].validate(cartID); err != nil {
			return err
		}
	}
	for i := range s.ShippingMethods {
		method := s.ShippingMethods[i]
		if method.Amount < 0 || method.Amount > MaxAmount {
			return errors.Internal(CodeSnapshotInvalid,
				"kargo tutarı [0, %d] aralığında olmalı: %s (%q -> %d)",
				MaxAmount, cartID, method.ID, method.Amount)
		}
	}
	return nil
}

// validate tek bir satırın hesaba girebilecek durumda olduğunu doğrular.
func (i SnapshotItem) validate(cartID string) error {
	if i.ID == "" {
		return errors.Internal(CodeSnapshotInvalid, "sepette kimliksiz satır var: %s", cartID)
	}
	if i.VariantID == "" {
		return errors.Internal(CodeSnapshotInvalid,
			"satırın varyantı boş: %s (%q)", cartID, i.ID)
	}
	if i.Quantity < MinQuantity || i.Quantity > MaxQuantity {
		return errors.Internal(CodeSnapshotInvalid,
			"satır adedi [%d, %d] aralığında olmalı: %s (%q -> %d)",
			MinQuantity, MaxQuantity, cartID, i.ID, i.Quantity)
	}
	return nil
}
