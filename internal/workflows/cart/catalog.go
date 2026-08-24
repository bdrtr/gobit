package cart

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// attrRegionID pricing'in kural bağlamında bölgeyi taşıyan öznitelik adıdır.
//
// Ad, pricing modülünün kural kayıtlarında geçen adla BİREBİR aynı olmak
// zorundadır: kuralın baktığı alan bağlamda yoksa kural eşleşmez ve bölgeye
// özgü fiyat sessizce elenip taban fiyat seçilirdi (bkz. pricing matchRule).
const attrRegionID = "region_id"

// priceSetsFor verilen varyantların fiyat kümelerini TEK link sorgusuyla
// çözer.
//
// Sorgu toplu yapılır: satır başına ayrı çağrı, on satırlık bir sepette on
// gidiş dönüş demektir ve N+1 plan Bölüm 5.3'ün açıkça kapattığı yoldur.
//
// # Fiyat kümesi olmayan varyant REDDEDİLİR
//
// Karar errors.Invalid'dir. Fiyat kümesi olmayan bir varyantın hiçbir para
// biriminde fiyatı yoktur; sepete girmesine izin vermek, birim fiyatı SIFIR
// olan bir satır açmak demektir ve sıfır tutarlı satır sepeti sessizce
// ucuzlatır — cart modülünün toplam sözleşmesinin (kapsama zorunluluğu) tam
// olarak kapatmaya çalıştığı sessiz para kaybı budur. Hata NotFound değildir
// çünkü varyant VARDIR; eksik olan, satılabilir olmasıdır ve çağıran isteği
// düzeltebilir (başka bir varyant seçer).
//
// # Birden çok küme
//
// "product_variant_price_set" tanımı OneToOne'dır ve veritabanı indeksi ikinci
// bağı imkânsız kılar. Yine de birden çok küme görülürse hangisinin
// fiyatlanacağı belirsizdir; sessizce ilkini seçmek fiyatı sıralama tesadüfüne
// bağlardı. Bu yüzden durum errors.Internal ile bildirilir: veri, kısıtın
// arkasından bozulmuştur.
func (w *Workflows) priceSetsFor(ctx context.Context, variantIDs []string) (map[string]string, error) {
	if len(variantIDs) == 0 {
		return map[string]string{}, nil
	}

	linked, err := w.links.ListMany(ctx, LinkVariantPriceSet, variantIDs)
	if err != nil {
		// Altyapı arızası İŞ durumu gibi raporlanmaz: CodeVariantNotPriced
		// "bu ürünün fiyatı yok" demektir ve istemci ona göre dallanır. Geçici
		// bir veritabanı kesintisinin vitrine kalıcı bir "fiyatsız ürün"
		// mesajı olarak ulaşması, gerçek fiyatsız varyanttan ayırt edilemez
		// olurdu. Alttaki hatanın sınıfı KORUNUR, kodu yeniden yazılmaz.
		return nil, errors.Wrap(err, errors.KindOf(err), CodeLinkReadFailed,
			"%q bağı okunamadı (%d varyant)", LinkVariantPriceSet, len(variantIDs))
	}

	out := make(map[string]string, len(variantIDs))
	for _, variantID := range variantIDs {
		sets := linked[variantID]
		switch len(sets) {
		case 0:
			return nil, errors.Invalid(CodeVariantNotPriced,
				"%s varyantının fiyatı yok; fiyatı olmayan ürün sepete giremez", variantID)
		case 1:
			out[variantID] = sets[0]
		default:
			return nil, errors.Internal(CodeVariantPriceSetAmbiguous,
				"%s varyantı %d fiyat kümesine bağlı görünüyor; %q tanımı tekil olmalı",
				variantID, len(sets), LinkVariantPriceSet)
		}
	}
	return out, nil
}

// variantTitle varyantın katalogdaki başlığını Query katmanından okur.
//
// # Neden başlık katalogdan okunuyor
//
// Sepet satırının başlığı varyanttan KOPYALANIR (bkz. cart modülünde LineItem):
// katalog sonradan değişse bile sepette görülen ad değişmez. Kopyalayabilecek
// tek taraf bu akıştır — cart modülü product'ı tanımaz.
//
// Başlığı çağırandan almak daha ucuz olurdu ama iki şeyi kaybettirirdi:
// vitrinin gönderdiği serbest metin sepete olduğu gibi girerdi ve varyantın
// GERÇEKTEN var olduğu hiçbir yerde doğrulanmazdı. İkincisi teorik değildir:
// product modülü silinen bir varyantın fiyat/stok bağlarını EN İYİ ÇABA ile
// temizler ve temizleyemediğinde yalnızca uyarı loglar, yani yetim bir fiyat
// bağı üzerinden silinmiş bir varyant sepete girebilirdi.
//
// Okuma Query üzerinden yapılır çünkü product servisinin okuma imzaları kendi
// model tipleriyle konuşur ve modüller arası çağrıya kapalıdır; Query tam bu
// boşluk için vardır (ADR 0004).
func (w *Workflows) variantTitle(ctx context.Context, variantID string) (string, error) {
	records, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{query.IDField, FieldTitle},
		Filters: map[string]any{query.IDField: variantID},
		Limit:   1,
	})
	if err != nil {
		// Aynı gerekçe: okuma arızası "varyant katalogda yok" DEĞİLDİR
		// (bkz. priceSetsFor).
		return "", errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"%s varyantı katalogdan okunamadı", variantID)
	}
	if len(records) == 0 {
		return "", errors.NotFound(CodeVariantUnknown,
			"%s varyantı katalogda yok", variantID)
	}

	title, ok := records[0][FieldTitle].(string)
	if !ok || title == "" {
		return "", errors.Internal(CodeVariantUnknown,
			"%s varyantının başlığı okunamadı (%q alanı: %v)",
			variantID, FieldTitle, records[0][FieldTitle])
	}
	return title, nil
}
