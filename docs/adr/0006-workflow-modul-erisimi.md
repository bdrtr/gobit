# ADR 0006 — Workflow'lar modüllere nasıl erişir

- **Durum:** Kabul edildi
- **Tarih:** 2026-08-23
- **Faz:** 5

## Bağlam

Faz 5'e kadar modüller arası tek erişim biçimi **okumaydı** ve Query katmanı
üzerinden çözülmüştü (ADR 0004). Sepet akışı bunu değiştiriyor: `calculate_totals`
bir varyantın fiyatını hesaplatmak için `pricing` modülünün **servisini
çağırmak** zorunda; `complete_cart` (Faz 6) ise `inventory.Reserve` ve
`payment.Authorize` çağıracak. Yani ilk kez modüller arası **yazma/çağrı**
gerekiyor.

Plan Bölüm 2.5 nereye yazılacağını söylüyor: *"Tek modüle ait kural modül
servisinde; birden çok modüle dokunan akış workflow'da yazılır."* Ama
workflow'un o modüllere **nasıl** ulaşacağı açık değil.

`internal/workflows` çekirdek değildir (Prensip 2.4 onu bağlamaz) ve modül de
değildir (depguard kuralları `internal/modules/*` içindir). Yani hiçbir mevcut
kural onu kısıtlamıyor — modülleri doğrudan import edebilirdi.

## Değerlendirilen seçenekler

**A. Workflow'lar modülleri doğrudan import etsin.** En kısa yol. Ama
`internal/workflows` tüm modülleri tanıyan tek bir düğüme dönüşür: bir modülü
ayrı servise çıkarmak ya da değiştirmek workflow'ları derleme zamanında kırar,
ve workflow testleri gerçek modül kurulumu (dolayısıyla veritabanı) ister.

**B. Çekirdeğe ortak bir sözleşme paketi.** ADR 0001'de modüller için
reddedilen çözümün aynısı; aynı gerekçelerle burada da reddedilir (god-package
eğilimi, sözleşme ile uygulamanın ayrışması).

**C. Tüketici tarafı interface + container'dan adla çözüm.** ADR 0001'in
örüntüsünün workflow'lara uygulanması: workflow, ihtiyaç duyduğu DAR yüzeyi
kendi paketinde tanımlar, somut servisi container'dan adla çözer.

## Karar

**Seçenek C.** `internal/workflows` de modülleri import ETMEZ.

```go
// internal/workflows/cart/totals.go
package cart

// PriceCalculator sepet toplamının pricing modülünden ihtiyaç duyduğu TEK
// yetenektir. pricing paketi import EDİLMEZ; somut servis container'dan
// "pricing.service" adıyla çözülür ve bu arayüzü yapısal olarak karşılar.
type PriceCalculator interface {
    CalculatePrice(ctx context.Context, priceSetID string, params CalcParams) (Money, error)
}
```

Sonuç: `internal/workflows` altında `internal/modules/...` import'u YASAKTIR ve
bu kural `internal/arch` testleriyle otomatik denetlenir — modüller arası
izolasyonda olduğu gibi.

## Sonuçlar

**Olumlu**

- Workflow'lar gerçek modül olmadan test edilebilir: dar arayüzün sahtesi
  birkaç satırdır, veritabanı gerekmez. Saga telafisini sınamak için bir adımın
  patlatılması gerekir ve bu ancak sahte ile pratiktir.
- Bir modülü ayrı servise çıkarmak workflow'ları derleme zamanında kırmaz;
  yalnızca container'daki kayıt değişir.
- Workflow'un bir modülden gerçekte NE istediği, tanımladığı arayüzün
  yüzeyinden okunur. `pricing.service`'in tamamına değil, `CalculatePrice`'a
  bağımlı olduğu görünür.

**Olumsuz / bedeli**

- Uyumsuzluk derleme zamanında değil, container'dan çözüm anında yakalanır.
  Telafisi ADR 0002'deki teşhis edilebilir tip uyumsuzluğu hatasıdır: mesaj hem
  kayıtlı somut tipi hem beklenen arayüzü yazar. Ayrıca her workflow için
  gerçek modüllerle koşan bir entegrasyon testi ZORUNLUDUR.
- Aynı kavram (para, adet, kimlik) workflow ve modül paketlerinde ayrı ayrı
  tanımlanır. İzolasyonun kabul edilen bedelidir (bkz. ADR 0001).

## İlgili

- Plan Bölüm 2.4, 2.5, Bölüm 4 (`/internal/workflows`), Faz 5, Faz 6
- [ADR 0001](0001-modul-arasi-iletisim.md) — örüntünün kaynağı
- [ADR 0004](0004-query-veri-erisimi.md) — okuma yolunun karşılığı
