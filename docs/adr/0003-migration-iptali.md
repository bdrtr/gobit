# ADR 0003 — Migration iptali: bağlantı sahipliği bizde

- **Durum:** Kabul edildi
- **Tarih:** 2026-08-23
- **Faz:** 1

## Bağlam

`Module.Migrations() fs.FS` sözleşmesi gereği migration'lar `golang-migrate` ile
uygulanıyor. Ancak kütüphanenin PostgreSQL sürücüsü **kurulumdan sonra hiçbir
yerde çağıranın context'ini kullanmıyor**:

- `Run`, `Version`, `SetVersion`, `Drop` — hepsi `context.Background()`
- `Lock` — `SELECT pg_advisory_lock($1)` üzerinde, kaynak kodun kendi yorumuyla
  *"will wait indefinitely until the lock can be acquired"*

Bu, iki somut arızaya yol açıyor:

1. **Süresiz asılma.** Paketleri düşüren bir güvenlik duvarının arkasındaki
   veritabanına yapılan `Migrate` çağrısı, çağıranın 30 saniyelik bütçesine
   rağmen işletim sistemi TCP zaman aşımına kadar (dakikalar) bloke olur. İki
   replika aynı anda açılıyorsa ikincisi advisory lock üzerinde süresiz bekler.
2. **Sessiz yarım şema.** İptal yalnızca *beklemeyi* durdurup işi arka planda
   bırakırsa, çağıran "yarıda kesildi" hatası alır ama terk edilen goroutine
   kalan migration'ları uygulamaya devam eder. Şema, hata dönmüş bir çağrının
   ardından sessizce tamamlanır.

## Değerlendirilen seçenekler

**A. Yalnızca `GracefulStop`** — golang-migrate migration'lar *arasında* durur.
Uçuştaki tek bir uzun migration'ı ve `Lock()` beklemesini durduramaz.

**B. DSN'e zaman aşımı parametreleri** (`connect_timeout`, `x-statement-timeout`)
— tek bir *ifadeyi* sınırlar. Arka arkaya çalışan kısa ifadelerden oluşan bir
migration dizisini durdurmaz; ölçüldü, durdurmadı.

**C. Goroutine'i terk edip ctx sınırında dönmek** — çağıran zamanında döner ama
2 numaralı arıza aynen kalır. Ölçüldü: iptal raporlandıktan sonra kalan
migration'lar uygulandı ve sürüm 3'e çıktı.

**D. Bağlantının sahibi olmak** — `*sql.Conn`'u biz açarız, sürücüye
`postgres.WithConnection(ctx, conn, cfg)` ile veririz. İptalde bağlantıyı
kapatırız: uçuştaki ifade kopar, sonraki her ifade başarısız olur.

## Karar

**D**, A ile birlikte ve katmanlı olarak.

`internal/core/db`'deki `session` tipi bağlantının sahibidir. İptalde sırayla:

1. `GracefulStop` — sonraki migration'ın *başlaması* engellenir,
2. `conn.Close()` — *uçuştaki* ifade koparılır,
3. işin gerçekten sonlanması beklenir (`cancelGracePeriod`); goroutine terk
   edilmez, sonlanmazsa bu durum hata mesajında açıkça belirtilir,
4. çağıran döndükten sonra `defer session.close()` kalan tüm kaynakları kapatır.

Yan kazanç: versiyon tablosu artık DSN'e `x-migrations-table` yazarak değil,
`postgres.Config.MigrationsTable` ile veriliyor — DSN'e parametre enjekte etmek
gerekmiyor. DSN şeması `sql.Open`'ın tembelliği yüzünden gizlenmesin diye
bağlanmadan önce ayrıca doğrulanıyor.

## Sonuçlar

**Olumlu:** `ctx` gerçekten sınır koyar; iptal edilen bir migration akışı
dönüşten sonra ilerlemez; erişilemez sunucuda çağrı bütçe içinde döner.

**Olumsuz:** `database/sql` + `pgx/stdlib` katmanı, `pgxpool`'un yanında ikinci
bir bağlantı yolu demek. Migration tek bağlantı üzerinde yürüdüğü için
(`SetMaxOpenConns(1)` — advisory lock'un aynı bağlantıda alınıp bırakılması
zorunlu) maliyeti ihmal edilebilir.

**Test notu:** Düzeltme katmanlı olduğu için tek tek mutasyonlar (yalnızca
`GracefulStop`'u ya da yalnızca `conn.Close()`'u kaldırmak) regresyon testini
düşürmez — diğer katman yakalar. `TestCancellationActuallyStopsRemainingMigrations`
uçtan uca özelliği sınar ve tam mutasyonda (goroutine tümüyle terk edilmiş)
sürüm 3'e çıkarak düşer; bu doğrulandı.

## İlgili

- Plan Bölüm 8 (Migration konvansiyonları), Faz 1
- `internal/core/db/migrate.go` — `session.run`
