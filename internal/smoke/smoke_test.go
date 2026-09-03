//go:build smoke

// Package smoke uygulamanın GERÇEK açılışını, gerçek migration'larını, gerçek
// yapılandırma yüklemesini ve gerçek sinyal işlemesini sınar.
//
// # Neden bu paket var
//
// Depoda birim ve entegrasyon testleri (~%76 kapsam) ile lint tertemiz
// geçerken, uygulama ELLE çalıştırıldığında testlerin GÖRMEDİĞİ dört arıza
// çıktı ve dördü de aynı sınıftandı:
//
//  1. Üç örnek boş bir veritabanına aynı anda açıldığında ikisi
//     "admin_bootstrap_failed" ile öldü (replicas:3 ile crash-loop).
//  2. OTEL_EXPORTER_OTLP_ENDPOINT'in belirtime uyan biçimi (http://host:4317)
//     sessizce yutuluyordu: "telemetry is set up" loglanıyor, tek span gitmiyordu.
//  3. Metrik aralığı değişkeninin adı OpenTelemetry belirtimiyle çakışıyordu;
//     belirtime uyan değer (60000) uygulamayı AÇILIŞTA düşürüyordu.
//  4. make migrate-up dokuz faz önceki bir özelliği bekletiyordu.
//
// Ortak noktaları, hiçbirinin bir PAKETİN içinde yaşamamasıdır: dördü de
// cmd/server'ın kablolamasında, açılış sırasında ya da süreç davranışındadır.
//
// internal/e2e bunları göremez ve görmesi de beklenmemelidir: httptest ile
// router'ı sürer, yani main.go'nun kablolamasını, açılıştaki migration'ları,
// config yüklemesini ve sinyal işlemeyi ATLAR. Bu paket tam olarak o boşluğu
// kapatır.
//
// # Ne sınanır, ne sınanmaz
//
// Ölçüt "iş mantığı mı, altyapı mı" DEĞİLDİR; ölçüt şudur: iddia, ancak GERÇEK
// SÜRECİN kararlarıyla doğrulanabiliyor mu? Bir uç mount edilmiş mi, bir modül
// kayıtlı mı, bir akış kurulu mu, bir migration açılışta koşmuş mu — bunların
// hepsini main() belirler ve hiçbir modül testi göremez. Böyle bir iddianın
// gövdesi kaçınılmaz olarak iş mantığından geçer (sepet açılır, fiyat okunur,
// sipariş doğar), ama sınanan şey hesabın kendisi değil YOLUN AÇIK OLMASIDIR.
//
// Hesabın doğruluğu buraya girmez: aynı toplamı sınayan bir iddia
// internal/e2e'de ve modül testlerinde çok daha ucuza koşar ve orada koşar.
// Bu paketteki her senaryo, konteyner + açılış + gerçek süreç maliyetini
// ödediği için ancak o maliyetin karşılığını veren soruyu sorar.
//
// # Hangi senaryo hangi arızayı bekliyor
//
//   - acilis_test.go: taze bir kurulumun elle adım olmadan kullanılabilir hâle
//     geldiğini kanıtlar. Dördüncü arızanın ("migrate-up ayrı bir komut
//     bekletiyordu") bugünkü cevabı da budur: migration'lar AÇILIŞTA uygulanır
//     ve bunu doğrulayan şey artık Makefile'daki bir cümle değil, boş bir
//     veritabanına açılıp giriş yapılabilen bir süreçtir.
//   - yaris_test.go: birinci arıza, tohum yarışı.
//   - izleme_test.go: ikinci ve üçüncü arıza, OTLP adres biçimi ve metrik
//     aralığı değişkeninin ad çakışması.
//   - yapilandirma_test.go ve kapanis_test.go: aynı sınıfın kapatılmamış iki
//     kanadı — kusurlu yapılandırmayla AÇILMAK ve sinyalle KAPANAMAMAK.
//   - b2b_test.go ve graphql_test.go: gerçek süreçte HİÇ koşmamış iki yüzey;
//     ikisi de yalnızca bileşim kökündeki bir kayıt satırı sayesinde vardır.
//   - anahtar_test.go: belgeyi izleyen geliştiricinin düştüğü KURULUM TUZAĞI —
//     kanalsız üretilen publishable anahtar 201 alır ama mağaza yüzeyinde her
//     zaman 401 alır ve teşhis kodu yanıtta değil sunucunun logundadır.
//   - vitrin_test.go: sepetten siparişe giden yolun gerçekten AÇIK olduğu.
//     internal/arch'taki statik değişmez akışların bileşim kökünde KURULDUĞUNU
//     görür ama kurulumun KOŞTUĞUNU göremez; o yarının kanıtı burada durur.
//
// # Kurulum
//
// Testler TEK bir Postgres ve TEK bir Redis konteyneri paylaşır; senaryolar
// ayrışmayı AYRI VERİTABANIYLA sağlar (bkz. [senaryoVeritabani]). Sunucu
// ikilisi de TEK sefer derlenir (bkz. [ikiliyiDerle]) ve her senaryo onu
// çalıştırır.
//
// # Neden go run değil, derlenmiş ikili
//
// "go run" araya bir üst süreç koyar ve SIGTERM'i alt sürece iletmeyebilir;
// sınamak istediğimiz şeylerden biri tam olarak sinyal davranışıdır
// (bkz. kapanis_test.go). Derlenmiş ikiliyi doğrudan çalıştırmak, testin
// gönderdiği sinyalin üretimde orkestratörün gönderdiği sinyalle aynı yere
// düşmesini garanti eder.
package smoke

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/bdrtr/gobit/internal/core/db"
)

// postgresImage ve redisImage testlerin paylaştığı imajlardır.
//
// Sürümler entegrasyon testleriyle AYNIDIR (bkz. internal/e2e ve
// internal/core/http/redisguard): açılışta çalışan migration'ların davranışı
// iki koşum arasında ayrışmamalıdır.
const (
	postgresImage = "postgres:16-alpine"
	redisImage    = "redis:7-alpine"
)

// yonetimVeritabani konteynerin ilk veritabanıdır ve senaryolarınkini YARATMAK
// dışında kullanılmaz; senaryolar asla buraya bağlanmaz.
const yonetimVeritabani = "gobit_smoke"

// derlemeSuresi ikilinin derlenmesi için tanınan azami süredir.
//
// Cömerttir çünkü soğuk bir CI önbelleğinde tüm bağımlılık ağacı derlenir;
// buradaki bir zaman aşımı, gerçek bir arıza değil yalnızca yavaş bir runner
// anlamına gelirdi.
const derlemeSuresi = 5 * time.Minute

// Konteyner ve derleme çıktısı; TestMain doldurur, senaryolar okur.
var (
	// yonetimDSN yönetim veritabanının bağlantı adresidir.
	yonetimDSN string
	// yonetimHavuzu senaryo veritabanlarını yaratan havuzdur.
	yonetimHavuzu *db.Pool
	// redisURL kaldırılan Redis'in bağlantı adresidir.
	redisURL string
	// ikiliYolu derlenmiş sunucu ikilisinin tam yoludur.
	ikiliYolu string
)

// veritabaniSayaci senaryo veritabanı adlarını benzersiz kılar.
//
// Zaman damgası yerine sayaç kullanılır: aynı milisaniyede yaratılan iki
// veritabanı adının çakışması, tam da eşzamanlılık senaryosunda ortaya çıkacak
// ve arızayı testin kendisine yükleyecek bir kırılganlıktı.
var veritabaniSayaci atomic.Int64

// TestMain konteynerleri kaldırır, ikiliyi derler ve senaryoları çalıştırır.
func TestMain(m *testing.M) {
	os.Exit(zeminIleCalistir(m))
}

// zeminIleCalistir zemini kurup çıkış kodunu döner.
//
// os.Exit defer'ları atladığı için ayrı bir fonksiyondadır: konteynerler,
// havuz ve derleme dizini ancak burada güvenle kapatılabilir.
func zeminIleCalistir(m *testing.M) int {
	ctx := context.Background()

	pgCtr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(yonetimVeritabani),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(pgCtr); termErr != nil {
			fmt.Fprintf(os.Stderr, "postgres konteyneri durdurulamadı: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres konteyneri başlatılamadı: %v\n", err)
		return 1
	}

	yonetimDSN, err = pgCtr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres bağlantı adresi alınamadı: %v\n", err)
		return 1
	}

	redisCtr, err := tcredis.Run(ctx, redisImage)
	defer func() {
		if termErr := testcontainers.TerminateContainer(redisCtr); termErr != nil {
			fmt.Fprintf(os.Stderr, "redis konteyneri durdurulamadı: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "redis konteyneri başlatılamadı: %v\n", err)
		return 1
	}

	redisURL, err = redisCtr.ConnectionString(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "redis bağlantı adresi alınamadı: %v\n", err)
		return 1
	}

	yonetimHavuzu, err = db.New(ctx, db.DefaultConfig(yonetimDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yönetim havuzu açılamadı: %v\n", err)
		return 1
	}
	defer yonetimHavuzu.Close()

	dizin, err := os.MkdirTemp("", "gobit-smoke-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "derleme dizini yaratılamadı: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dizin) }()

	ikiliYolu = filepath.Join(dizin, "gobit")
	if err := ikiliyiDerle(ctx, ikiliYolu); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	return m.Run()
}

// ikiliyiDerle sunucuyu BİR KEZ derler.
//
// Derlemenin testlerin İÇİNDE değil burada olması bilinçlidir: her senaryoda
// derlemek, en yavaş adımı senaryo sayısıyla çarpardı. Ayrıca tek derleme, tüm
// senaryoların AYNI ikiliyi sürdüğünü garanti eder — senaryolar arasında
// kaynak değişmediği için ayrı derlemelerin sağlayacağı bir şey de yok.
func ikiliyiDerle(ctx context.Context, hedef string) error {
	kok, err := filepath.Abs("../..")
	if err != nil {
		return fmt.Errorf("depo kökü bulunamadı: %w", err)
	}

	derlemeCtx, iptal := context.WithTimeout(ctx, derlemeSuresi)
	defer iptal()

	cmd := exec.CommandContext(derlemeCtx, "go", "build", "-o", hedef, "./cmd/server")
	cmd.Dir = kok
	// Ortam DEVRALINIR: derleme GOCACHE, GOMODCACHE ve GOPATH'e ihtiyaç duyar.
	// Sunucu SÜRECİNİN ortamı ise tersine sıfırdan kurulur (bkz. surec_test.go).
	cmd.Env = os.Environ()

	if cikti, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sunucu ikilisi derlenemedi: %w\n%s", err, cikti)
	}

	return nil
}

// senaryoVeritabani senaryoya ÖZEL, boş bir veritabanı yaratır ve DSN'ini döner.
//
// # Neden senaryo başına veritabanı, senaryo başına konteyner değil
//
// Senaryoların çoğu BOŞ bir veritabanı ister: soğuk açılış tohumun çalıştığını,
// eşzamanlı açılış ise üç örneğin yarıştığını ancak hiç kullanıcı yokken
// kanıtlayabilir. Paylaşılan tek bir veritabanında ilk senaryonun yarattığı
// yönetici, sonrakinin tohum adımını sessizce atlatır ve test yeşil kalırken
// hiçbir şey kanıtlamaz.
//
// Ayrımı KONTEYNER başına yapmak da aynı işi görürdü ama her senaryoya bir
// Postgres kaldırmak, testin süresine imaj çekme + başlatma maliyetini senaryo
// sayısı kadar eklerdi. CREATE DATABASE aynı ayrımı milisaniyeler içinde verir;
// migration'lar zaten her veritabanında sıfırdan çalışır, yani senaryonun
// gördüğü şema da gerçekten taze olur.
func senaryoVeritabani(t *testing.T) string {
	t.Helper()

	ad := fmt.Sprintf("smoke_%s_%d", veritabaniAdiTemizle(t.Name()), veritabaniSayaci.Add(1))

	// pgx.Identifier tırnaklamayı sürücünün kurallarıyla yapar; ad zaten
	// süzülmüş olsa da elle tırnaklamak, süzgecin bir gün gevşemesi hâlinde
	// sessizce bir enjeksiyon yüzeyi bırakırdı.
	_, err := yonetimHavuzu.Pool().Exec(t.Context(),
		"CREATE DATABASE "+pgx.Identifier{ad}.Sanitize())
	require.NoError(t, err, "senaryo veritabanı yaratılamadı: %s", ad)

	adres, err := url.Parse(yonetimDSN)
	require.NoError(t, err, "yönetim DSN'i ayrıştırılamadı")
	adres.Path = "/" + ad

	return adres.String()
}

// veritabaniAdiTemizle test adını Postgres tanımlayıcısına çevirir.
//
// Alt testler t.Name()'e '/' koyar, Türkçe adlar ise ASCII dışı karakter
// taşıyabilir; ikisi de tırnaksız bir tanımlayıcıda geçersizdir. Ad yalnızca
// TEŞHİS içindir (hangi veritabanı hangi senaryonun), benzersizliği sayaç
// sağlar; bu yüzden kayıpsız bir dönüşüm gerekmez.
func veritabaniAdiTemizle(ad string) string {
	const enFazla = 40

	var b strings.Builder
	for _, r := range strings.ToLower(ad) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= enFazla {
			break
		}
	}

	return b.String()
}
