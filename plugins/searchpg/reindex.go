package searchpg

import (
	"context"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// Hata kodları.
const (
	codeReindexFailed = "searchpg_reindex_failed"
	codeCatalogIDs    = "searchpg_catalog_ids_invalid"
)

// reindexSayfaBoyu yeniden indekslemenin tek turda işlediği ürün sayısıdır.
//
// Sayfalama ZORUNLUDUR: tüm katalogu tek seferde belleğe almak, katalog
// büyüdükçe büyüyen ve bir gün süreci öldüren bir istek üretirdi.
//
// Değer kataloğun toplu okuma sınırını (product service.MaxLimit) AŞMAZ; aşsaydı
// sayfanın kimlikleri tek bir "product.interop" çağrısına sığmaz ve tur ilk
// sayfada hata alırdı.
const reindexSayfaBoyu = 100

// yenidenIndeksSonucu bir yeniden indeksleme turunun özetidir.
type yenidenIndeksSonucu struct {
	// Indexed bu turda yazılan (eklenen ya da güncellenen) belge sayısıdır.
	Indexed int `json:"indexed"`
	// Removed süpürmede silinen bayat kayıt sayısıdır.
	Removed int64 `json:"removed"`
	// Pages okunan katalog sayfası sayısıdır; operasyon için maliyet göstergesi.
	Pages int `json:"pages"`
}

// yenidenIndeksle tüm yayındaki katalogu baştan indeksler.
//
// Boş bir indeks hiçbir işe yaramaz ve olaylar yalnızca AÇILIŞTAN SONRAKİ
// değişiklikleri taşır: eklenti var olan bir kuruluma takıldığında, veri yolu
// bir süre erişilemez kaldığında ya da bir olay kaçtığında indeksi gerçekle
// buluşturan tek yol budur (bkz. events.go, hata politikası).
//
// # Kimlikler nereden geliyor
//
// Ürün kimlikleri çekirdeğin Query katmanından SAYFA SAYFA okunur; katalog
// tablosuna doğrudan SQL atılmaz. İki sebep: modülün tablosu onun iç meselesidir
// ve bir eklentinin ona bağlanması, şema değiştiğinde eklentiyi sessizce
// bozardı; ayrıca Query, modülleri import etmeden okumanın çekirdekteki tanımlı
// yoludur (ADR 0004).
//
// Süzgeç "status = published"tır: vitrinde görünmeyen bir ürünü indekslemenin
// anlamı yoktur ve okunmayan her sayfa, ödenmeyen bir katalog turudur.
//
// # Neden ÖNCE yaz, SONRA süpür
//
// Tur, veritabanı saatinden alınan bir eşikle başlar; her yazma damgayı
// tazeler; tur BİTTİĞİNDE eşikten eski kalan satırlar silinir. Böylece artık
// yayında olmayan ya da silinmiş ürünler indeksten düşer — hiçbir olay
// almadan.
//
// Sıra terse çevrilip önce silinseydi (TRUNCATE + doldur), tur boyunca arama
// BOŞ dönerdi. Süpürme yalnızca TAM tamamlanan turdan sonra çalışır: yarıda
// kalan bir turun ardından süpürmek, okunamamış sayfalardaki geçerli kayıtları
// silmek olurdu.
//
// # Kabul edilen sınırlar
//
//   - Sayfalama OFFSET tabanlıdır. Tur sırasında bir ürün silinirse sonraki
//     sayfa bir kayıt kaydırır ve o kayıt bu turda okunmaz; süpürme onu
//     indeksten düşürebilir. Bir sonraki tur ya da ürünün ilk yazması onarır.
//   - Tur, çağıranın bağlamına bağlıdır: istemci koparsa iş yarıda kalır ve
//     süpürme YAPILMAZ, yani indeks bozulmaz — yalnızca güncellenmemiş kalır.
func (m *modul) yenidenIndeksle(ctx context.Context) (yenidenIndeksSonucu, error) {
	if err := m.hazir(); err != nil {
		return yenidenIndeksSonucu{}, err
	}
	if m.graph == nil {
		return yenidenIndeksSonucu{}, coreerrors.Unavailable(codeNotRegistered,
			"%s modülü query katmanını çözmedi; yeniden indeksleme yapılamaz", ModuleName)
	}

	esik, err := m.indeks.Now(ctx)
	if err != nil {
		return yenidenIndeksSonucu{}, err
	}

	var sonuc yenidenIndeksSonucu
	for offset := 0; ; offset += reindexSayfaBoyu {
		ids, err := m.urunKimlikleri(ctx, offset)
		if err != nil {
			return yenidenIndeksSonucu{}, err
		}
		if len(ids) == 0 {
			break
		}
		sonuc.Pages++

		belgeler, err := m.belgeler(ctx, ids)
		if err != nil {
			return yenidenIndeksSonucu{}, err
		}
		if err := m.indeks.Upsert(ctx, belgeler); err != nil {
			return yenidenIndeksSonucu{}, err
		}
		sonuc.Indexed += len(belgeler)

		// Eksik sayfa son sayfadır; bir tur daha atıp boş sayfa okumaya gerek
		// yok.
		if len(ids) < reindexSayfaBoyu {
			break
		}
	}

	silinen, err := m.indeks.Sweep(ctx, esik)
	if err != nil {
		return yenidenIndeksSonucu{}, err
	}
	sonuc.Removed = silinen

	m.log.InfoContext(ctx, "arama indeksi yeniden kuruldu",
		"indekslenen", sonuc.Indexed,
		"silinen", sonuc.Removed,
		"sayfa", sonuc.Pages)

	return sonuc, nil
}

// urunKimlikleri yayındaki ürünlerin kimliklerini tek sayfa okur.
//
// Query'den YALNIZCA "id" alanı istenir: kayıtların geri kalanı burada
// kullanılmaz, çünkü indekslenecek metin vitrin gösteriminden gelir
// (bkz. [modul.belgeler]). Tüm alanları istemek, katalog sayfası başına
// gereksiz bir taşıma maliyeti olurdu.
func (m *modul) urunKimlikleri(ctx context.Context, offset int) ([]string, error) {
	kayitlar, err := m.graph.Graph(ctx, query.GraphSpec{
		Entity:  catalogEntity,
		Fields:  []string{query.IDField},
		Filters: map[string]any{catalogStatusFilter: catalogStatusPublished},
		Limit:   reindexSayfaBoyu,
		Offset:  offset,
	})
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindOf(err), codeReindexFailed,
			"katalog kimlikleri okunamadı (offset %d)", offset)
	}

	ids := make([]string, 0, len(kayitlar))
	for _, kayit := range kayitlar {
		ham, ok := kayit[query.IDField]
		if !ok {
			return nil, coreerrors.Internal(codeCatalogIDs,
				"katalog kaydında %q alanı yok (offset %d)", query.IDField, offset)
		}
		id, ok := ham.(string)
		if !ok || id == "" {
			return nil, coreerrors.Internal(codeCatalogIDs,
				"katalog kaydındaki %q alanı boş ya da dize değil (gelen tip: %T)",
				query.IDField, ham)
		}
		ids = append(ids, id)
	}

	return ids, nil
}
