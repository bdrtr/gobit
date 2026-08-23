# ADR 0001 — Modüller arası iletişim: tüketici tarafı interface

- **Durum:** Kabul edildi
- **Tarih:** 2026-08-23
- **Faz:** 0 (uygulaması Faz 4'te başlar)

## Bağlam

Uygulama planının Bölüm 2'sindeki iki değişmez kural, harfiyen okunduğunda çelişiyor:

> **2.1 Modül izolasyonu:** … Erişim yalnızca diğer modülün **public service
> interface'i** üzerinden olur.

> **2.4 Bağımlılık yönü:** … Modüller birbirine **derleme zamanında bağımlı olmaz**
> (yalnızca interface paketleri paylaşılır).

Go'da interface'ler paketlerde yaşar. `cart` modülü, `product` modülünün
interface'ini kullanmak için `internal/modules/product/service` paketini import
ederse bu **derleme zamanı bağımlılığıdır** — 2.4 ihlal edilir. Yani 2.1 ve 2.4
aynı anda, "interface'i sahibi yayımlar" okumasıyla sağlanamaz.

Bu belirsizlik Faz 4'te (`product ↔ pricing ↔ inventory`) ilk kez, Faz 6'da
(`complete_cart` saga'sı beş modüle dokunur) ise yıkıcı biçimde ortaya çıkar.
Faz 4'te alınacak yanlış karar, sonraki tüm modüllerde çözülmesi pahalı bir
bağımlılık ağı bırakır.

## Değerlendirilen seçenekler

**A. Sahibi interface'i yayımlar** — `cart`, `product/service` paketini import eder.
2.1'i sağlar, 2.4'ü ihlal eder. Modüller derleme zamanında birbirine kenetlenir;
bir modülü ayrı servise çıkarmak import grafiğini kırar.

**B. Ortak sözleşme paketi** — `internal/contracts/product` gibi nötr bir paket.
Her iki modül de buraya bağımlı olur, birbirine olmaz. Çalışır; ancak her modül
için ikinci bir paket, sözleşme ile implementasyonun ayrı yerde sürüklenmesi ve
"sözleşme paketi kimin?" sorusu getirir. Ayrıca sözleşme paketi zamanla tüm
modüllerin bildiği bir god-package'a dönüşmeye eğilimlidir.

**C. Tüketici tarafı interface** — İhtiyacı olan modül, ihtiyaç duyduğu **dar**
interface'i **kendi paketinde** tanımlar. Sağlayıcının somut tipi bu interface'i
yapısal olarak (structural typing) karşılar; hiçbir import gerekmez.

## Karar

**Seçenek C.** Modüller arası her bağımlılık, tüketicinin kendi paketinde
tanımladığı dar bir interface üzerinden kurulur. Somut implementasyon çalışma
zamanında container'dan isimle çözülür.

Bu seçim 2.4'ü harfiyen sağlar (sıfır derleme zamanı bağımlılığı), 2.1'in amacını
korur (erişim yalnızca servis sözleşmesi üzerinden), ve Go'nun kendi öğüdüyle
örtüşür: *"interface'i tüketen taraf tanımlar; üreten taraf somut tip döner."*

### Örüntü

```go
// internal/modules/cart/service/service.go
package service

// ProductReader, cart'ın product modülünden ihtiyaç duyduğu TEK yetenektir.
// Bilinçli olarak dardır: product'ın tüm servisini değil, yalnızca burada
// kullanılanı tanımlar. product paketi import EDİLMEZ.
type ProductReader interface {
    GetVariant(ctx context.Context, variantID string) (Variant, error)
}

// Variant, cart'ın ihtiyaç duyduğu alanlardır; product'ın modeli değildir.
type Variant struct {
    ID    string
    Title string
}

type Service struct {
    products ProductReader // container'dan "product.service" adıyla çözülür
}
```

Sağlayıcı tarafı hiçbir şey yapmaz: `product`'ın somut servisi `GetVariant`
metodunu taşıdığı sürece bu interface'i karşılar.

### Zorlama

`.golangci.yml` içindeki `depguard` kuralları modüller arası **her** import'u
yasaklar (12 modül × 11 yasak paket). Bu ADR'nin ihlali derlemeden önce CI'da
yakalanır — kural yorum değil, kapıdır.

Yeni modül eklenirken `depguard.rules` listesine hem yeni modülün kendi kuralı
hem de mevcut kuralların deny listesine yeni modül eklenmelidir.

## Sonuçlar

**Olumlu**

- Modüller arasında sıfır derleme zamanı bağımlılığı; herhangi bir modül ayrı
  servise çıkarılabilir, import grafiği kırılmaz.
- Tüketici yalnızca gerçekten kullandığı yüzeye bağlanır; sağlayıcının
  interface'i büyüdükçe tüketiciler etkilenmez.
- Test etmek kolaydır: dar interface'in sahte (fake) implementasyonu birkaç satırdır.

**Olumsuz / bedeli**

- Aynı kavram (örn. bir varyantın kimliği ve başlığı) birden çok modülde küçük
  DTO'lar hâlinde tekrar tanımlanır. Bu, izolasyonun **kabul edilen bedelidir**;
  ortak bir model paketi kurma dürtüsüne direnilmelidir.
- Sağlayıcı bir metot imzasını değiştirdiğinde derleyici tüketiciyi uyarmaz;
  uyumsuzluk container'dan çözüm anında ortaya çıkar. Bunu telafi etmek için:
  container'a kayıt sırasında derleme zamanı doğrulaması yapılır ve her tüketici
  için entegrasyon testi zorunludur.

### Container kayıt doğrulaması

Sağlayıcı modül, tüketicilerin beklediği yüzeyi karşıladığını kendi paketinde
`var _` bildirimi ile değil (bu import gerektirirdi), **entegrasyon testinde**
doğrular. Faz 1'de `container.Resolve[T]` tip parametresiyle çözüm yaptığı için
uyumsuzluk açık ve tipli bir hata olarak döner:

```go
products, err := container.Resolve[cartservice.ProductReader](c, "product.service")
// err: "product.service, cartservice.ProductReader arayüzünü karşılamıyor"
```

## İlgili

- Plan Bölüm 2.1, 2.4 — bu ADR ile netleştirildi
- Plan Bölüm 5.1 — `Container.Provide(name, ctor)` / `Resolve[T](c, name)`
- Plan Faz 4 — ilk gerçek uygulama (`product ↔ pricing ↔ inventory`)
