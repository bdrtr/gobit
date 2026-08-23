# ADR 0004 — Query katmanı modüllerden veriyi nasıl çeker

- **Durum:** Kabul edildi
- **Tarih:** 2026-08-23
- **Faz:** 2

## Bağlam

Plan Bölüm 5.3, Query katmanının akışını tarif ediyor: *kök modülden kayıtları
çek → link'lerle ilgili ID'leri bul → ilgili modüllerin servislerinden batch ile
getir → birleştir.*

Ama "modülün servisinden getir" kısmı bir çelişkiyle karşılaşıyor:

- `core/query` çekirdektedir, **Prensip 2.4** gereği modülleri tanıyamaz.
- **ADR 0001** gereği modüller arası derleme zamanı bağımlılığı yoktur.
- Yine de Query, çalışma zamanında `product`, `pricing`, `inventory` gibi
  **önceden bilinmeyen** modüllerden veri çekebilmelidir.

Yani Query'nin hangi modüle sorduğunu derleme zamanında bilmesi imkânsızdır.

## Değerlendirilen seçenekler

**A. Query modülleri import etsin** — Prensip 2.4'ün doğrudan ihlali; ayrıca
her yeni modül çekirdeği değiştirmeyi gerektirirdi.

**B. Query doğrudan SQL yazsın** — tablolara çekirdekten erişmek Prensip 2.1'i
(veri sahipliği) ihlal eder ve cross-module JOIN kapısını açar.

**C. Modüller kendilerini bir sağlayıcı olarak kaydeder** — her modül
`Register` sırasında container'a `"<modül>.query"` adıyla dar bir arayüz koyar.
Query bunu **isimle** çözer. Derleme zamanı bağımlılığı yok, çekirdek modül
tanımıyor, yeni modül çekirdeğe dokunmadan sorgulanabilir hâle geliyor.

## Karar

**Seçenek C.** `core/query` şu dar arayüzü tanımlar; modüller onu karşılayan
somut bir tip kaydeder:

```go
// Record bir kaydın alan adı -> değer eşlemesidir.
type Record map[string]any

// Provider bir modülün Query katmanına açtığı okuma yüzeyidir.
// Modül bunu Register sırasında "<modül adı>.query" adıyla container'a koyar.
type Provider interface {
    // Entity sağlayıcının sunduğu entity adıdır (örn. "product").
    Entity() string

    // List kök kayıtları döner. Query bunu YALNIZCA kök entity için çağırır.
    List(ctx context.Context, opts ListOptions) ([]Record, error)

    // FetchByIDs verilen ID'lere karşılık gelen kayıtları döner.
    // Bulunamayan ID için kayıt DÖNMEZ; bu bir hata değildir.
    // Query bunu link'lerden çıkan ID kümesiyle BATCH olarak çağırır (N+1 yok).
    FetchByIDs(ctx context.Context, ids []string, fields []string) ([]Record, error)
}
```

Sağlayıcı, ADR 0001'in tüketici tarafı interface örüntüsünün özel bir hâlidir:
arayüzü **tüketen** taraf (`core/query`) tanımlar, sağlayan modül yalnızca
imzayı karşılar ve hiçbir şey import etmez.

## Sonuçlar

**Olumlu**

- Yeni bir modül sorgulanabilir hâle gelmek için çekirdeğe dokunmaz; tek yaptığı
  `Register` içinde bir satır kayıt eklemektir.
- `FetchByIDs` batch olduğu için genişletme (expand) başına tek çağrı yapılır;
  N+1 yapısal olarak engellenir.
- Query test edilirken gerçek modül gerekmez; sahte sağlayıcı birkaç satırdır.

**Olumsuz / bedeli**

- Alan seçimi (`fields`) ve filtreleme sağlayıcıya bırakılır; Query bunları
  doğrulayamaz. Sağlayıcı desteklemediği bir alan görürse `errors.Invalid`
  dönmelidir.
- `Record` gevşek tiplidir (`map[string]any`). Bu, çekirdeğin modül modellerini
  tanımamasının kaçınılmaz bedelidir; tip güvenliği API sınırında (store/admin
  handler'larında) yeniden kazanılır.
- Sağlayıcı kaydı unutulursa hata çalışma zamanında ortaya çıkar. Query bu
  durumda `errors.NotFound` ile **hangi adın aranıp bulunamadığını** yazmalıdır.

## İlgili

- Plan Bölüm 2.1, 2.4, Bölüm 5.3, Faz 2
- [ADR 0001](0001-modul-arasi-iletisim.md) — tüketici tarafı interface örüntüsü
- [ADR 0002](0002-di-container-el-yazmasi.md) — isimle çözümün teşhis edilebilir olması
