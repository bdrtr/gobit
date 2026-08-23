# ADR 0005 — Link şeması migration dosyalarında değil, bildirim anında kurulur

- **Durum:** Kabul edildi
- **Tarih:** 2026-08-23
- **Faz:** 2

## Bağlam

Plan Bölüm 8, migration konvansiyonunu net koyuyor: *"Modül başına ayrı klasör;
geri-alınabilir (up/down)."* Link tabloları ise bu kalıba girmiyor.

Sebep, link'lerin **kim tarafından** bildirildiğidir. Plan Bölüm 5.1'e göre
modüller link tanımlarını `Module.Register` sırasında bildirir, ve Faz 9'daki
plugin sistemi bir eklentinin **çekirdeğe dokunmadan** kendi linkini eklemesini
gerektirir. Yani hangi link tablolarının var olacağı derleme zamanında bilinmez;
`migrations/` altında sabit bir dosya kümesi olarak yazılamaz.

## Değerlendirilen seçenekler

**A. Çekirdekte tek bir global `links` tablosu** — `(link_name, from_id, to_id)`
şeklinde. Migration'la kurulabilirdi. Ama kardinalite kısıtları (bkz. ADR
bağlamı: `OneToOne` her iki uçta benzersizlik ister) tek tabloda link adına göre
**kısmi benzersiz indeks** gerektirir; her yeni link için yine çalışma zamanında
DDL demektir. Ayrıca tek tablo tüm linklerin sıcak noktası olur.

**B. Link başına migration dosyası üretmek** — bir kod üreticisiyle. Plugin'in
çekirdeğe dokunmama şartını bozar ve derleme adımı ekler.

**C. Bildirim anında idempotent DDL** — `Define` çağrısı tabloyu ve kısıtlarını
`CREATE ... IF NOT EXISTS` ile kurar.

## Karar

**Seçenek C.** `LinkService.Define` şemayı bildirim anında kurar.

Güvenliği sağlayan dört önlem:

1. **Tek işlem + danışma kilidi.** Bildirim `pg_advisory_xact_lock` altında tek
   bir işlemde yürür; aynı anda açılan iki süreç birbirinin DDL'iyle yarışmaz.
2. **Kalıcı tanım defteri.** Tanım `link_definitions` tablosuna yazılır ve her
   açılışta karşılaştırılır. Sürümler arasında sessizce değişen bir tanım
   `errors.Conflict` ile yakalanır — migration'ın sürüm defterinin yerini tutan
   şey budur.
3. **Ad doğrulaması.** Link adı `^[a-z][a-z0-9_]{0,39}$` desenine uymalıdır ve
   defter tablosuyla ya da indeks ad uzayıyla çakışan adlar reddedilir. Tablo
   adları SQL'de parametrelenemediği için doğrulama tek savunmadır.
4. **DDL sonrası doğrulama.** `CREATE ... IF NOT EXISTS`, o adda **başka türden**
   bir ilişki varsa hata değil `NOTICE` üretip atlar. Bu yüzden DDL'den sonra
   `pg_class` üzerinden ilişkinin gerçekten tablo olduğu ve gereken her indeksin
   kurulduğu denetlenir; aksi hâlde işlem geri alınır.

## Sonuçlar

**Olumlu:** Plugin'ler çekirdeğe dokunmadan link ekleyebilir. Şema, tanımın
kendisiyle tek yerde durur; ikisinin ayrışması mümkün değildir.

**Olumsuz / bilinen sınırlar**

- **Geri alma (down) yolu yoktur.** Bir link tanımı kaldırıldığında tablosu
  veritabanında kalır. Bu bilinçlidir: tabloyu otomatik düşürmek, bir dağıtım
  hatası yüzünden geçici olarak kaybolan bir tanımın tüm bağları silmesi
  demekti. Temizlik operasyonel bir karardır ve elle yapılır.
- **`db.Version` link şemasını görmez.** Modül migration'larının sürüm defteri
  `<owner>_schema_migrations`'tır; link şeması oraya yazılmaz. Bir ortamın link
  şemasının güncelliği `link_definitions` tablosundan okunur.
- **Şema değişikliği (örn. bir sütun eklemek) elle migration ister.** `Define`
  yalnızca "yoksa oluştur" yapar; var olan bir tabloyu ALTER etmez. Link
  tablosunun şekli değişirse çekirdek bir migration yazılmalıdır.

## İlgili

- Plan Bölüm 5.2, Bölüm 8 (bu ADR o konvansiyona bilinçli bir istisnadır), Faz 2, Faz 9
- `internal/core/link/service.go` — `declare`, `verifySchema`
