# ADR 0002 — DI container: kütüphane yerine el yazması

- **Durum:** Kabul edildi
- **Tarih:** 2026-08-23
- **Faz:** 1

## Bağlam

Plan Bölüm 3, DI için `samber/do` v2'yi öneriyor. Bölüm 5.1'deki sözleşme ise
bağlayıcı:

```go
func (c *Container) Provide(name string, ctor any) error
func Resolve[T any](c *Container, name string) (T, error)
```

Yani **isimli kayıt** + **`any` alan yapıcı** + **generic çözüm**.

`samber/do` v2'nin kayıt yüzeyi ise tip parametrelidir (`do.ProvideNamed[T]`).
`any` alan bir `Provide`'ı onun üstüne kurmanın tek yolu her servisi do'ya `any`
olarak vermektir — ve o anda do'nun getirdiği üç şeyin üçü de elden gider.

## Değerlendirme

| do'nun sunduğu | `any`'ye düzleşince ne oluyor |
|---|---|
| Tipli hata mesajları | Tip bilgisi kaybolduğu için ADR 0001'in istediği "kayıtlı somut tip vs beklenen tip" teşhisi üretilemez |
| Çift kayıt koruması | do **panic** eder; sözleşme `errors.Conflict` istiyor |
| Kapatma | do kendi bağımlılık grafiğine göre ve yalnızca kendi `Shutdowner` arayüzünü tanıyarak kapatır; sözleşme **kayıt sırasının tersini** ve `io.Closer` desteğini şart koşuyor |

Geriye do'dan yalnızca mutex'li bir map kalıyordu.

## Karar

`core/container` sözleşmenin istediği davranışı **doğrudan yazar**;
`samber/do` bağımlılığı eklenmez.

Dışarıya yalnızca Bölüm 5.1'deki yüzey göründüğü için karar geri alınabilir:
gövde ileride bir kütüphaneye taşınabilir, çağıranlar etkilenmez.

Paketin sağladığı, sözleşmenin ötesindeki davranışlar:

- **Tembel singleton** — yapıcı ilk `Resolve`'da ve eşzamanlı 100 çağrıda bile
  tam olarak bir kez çalışır.
- **Bağımlılık döngüsü tespiti** — `A -> B -> A` deadlock yerine bekleme
  grafiğini içeren net bir hata döner.
- **Teşhis edilebilir tip uyumsuzluğu** — hata mesajı hem kayıtlı somut tipi
  hem beklenen arayüzü ve eksik/uyumsuz metodu yazar. ADR 0001'in tüketici
  tarafı interface örüntüsünde uyumsuzluk derleyici tarafından değil çalışma
  zamanında yakalandığı için mesaj kalitesi kritiktir.
- **Ters sırada kapatma** — `io.Closer` ve `Shutdowner` uygulayan servisler
  kayıt sırasının tersine kapatılır; panikler yakalanır, hatalar birleştirilir.

## Sonuçlar

**Olumlu:** Sözleşme birebir karşılanır, bağımlılık sayısı artmaz, hata
mesajları alan ihtiyacına göre biçimlendirilebilir.

**Olumsuz:** Eşzamanlılık ve kapatma sırası artık bizim sorumluluğumuzdadır.
Karşılığında paket yoğun biçimde test edilmiştir (eşzamanlı yapıcı, döngü,
kapanışla yarışan çözüm, panik yayan servis).

**Bilinen sınır:** `Shutdown`, uçuşta olan bir yapıcıyı yalnızca kendisine
verilen ctx bütçesi kadar bekler. Bütçe dolarsa o servis kapatılmadan kalır ve
bu durum `Shutdown`'ın döndürdüğü birleşik hataya yazılır. Pratik sonuç:
`Shutdown`'a verilen süre en yavaş yapıcıdan uzun olmalıdır.

## İlgili

- Plan Bölüm 3 (bu ADR ile güncellendi), Bölüm 5.1
- [ADR 0001](0001-modul-arasi-iletisim.md) — teşhis edilebilir tip uyumsuzluğu ihtiyacının kaynağı
