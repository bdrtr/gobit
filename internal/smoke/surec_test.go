//go:build smoke

package smoke

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// acilisSuresi sürecin /health'e yanıt vermesi için tanınan azami süredir.
//
// Cömerttir: taze bir veritabanında açılış, çekirdeğin ve on üç modülün
// migration'larını uygular. Yavaş bir CI runner'ında geçen süre bir arıza
// değildir; asıl arıza, sürecin HİÇ açılmamasıdır ve onu bu süre yakalar.
const acilisSuresi = 90 * time.Second

// yoklamaAraligi /health yoklamaları arasındaki beklemedir.
//
// SABİT UYKU YOKTUR ve olmamalıdır: "3 saniye bekle, açılmıştır" varsayan bir
// test, yavaş bir runner'da yanlış yere düşer ve hızlı bir makinede boşuna
// bekler. Yoklama, hazır olduğu ANDA devam eder.
const yoklamaAraligi = 100 * time.Millisecond

// istekSuresi tek bir HTTP isteğine tanınan süredir.
const istekSuresi = 5 * time.Second

// gunlukSuresi bir log satırının tampona düşmesi için tanınan azami süredir.
//
// Sıfır olamaz ve gerekçesi bir yarıştır: uygulama teşhis satırını yanıtı
// yazmadan ÖNCE loglar, ama boruyu tampona kopyalayan taraf exec.Cmd'nin AYRI
// bir goroutine'idir. Yanıt elde olduğu anda tamponu tek seferde okumak,
// arızası olmayan bir senaryoyu makinenin hızına bağlardı.
const gunlukSuresi = 5 * time.Second

// oldurmeSuresi kapanış sinyalinden sonra süreci zorla öldürmeden önce
// beklenen süredir.
//
// Uygulamanın kendi SHUTDOWN_TIMEOUT varsayılanından (15s) uzundur: kapanış
// senaryosu sürenin KENDİSİNİ ölçer ve temizlik adımının onu erken kesmesi,
// ölçülen şeyi bozardı.
const oldurmeSuresi = 30 * time.Second

// ayarlar sunucu sürecine verilecek ortam değişkenleridir.
type ayarlar map[string]string

// gunlukTamponu sürecin çıktısını eşzamanlı yazmaya güvenli biçimde biriktirir.
//
// Kilit ŞART: exec.Cmd, io.Writer verildiğinde boruyu KENDİ goroutine'inde
// kopyalar; test ise süreç hâlâ koşarken tamponu okur (örneğin zaman aşımında
// logu basmak için). Kilitsiz bir bytes.Buffer'da bu, -race altında güvenilir
// biçimde patlayan gerçek bir yarıştır.
type gunlukTamponu struct {
	mu sync.Mutex
	b  bytes.Buffer
}

// Write io.Writer sözleşmesini kilit altında uygular.
func (g *gunlukTamponu) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.b.Write(p)
}

// String o ana kadar biriken çıktıyı döner.
func (g *gunlukTamponu) String() string {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.b.String()
}

// surec çalışan bir sunucu ikilisini ve çıktısını temsil eder.
type surec struct {
	t   *testing.T
	cmd *exec.Cmd
	// adres sürecin dinlediği taban adrestir (http://127.0.0.1:port).
	adres string
	// stdout uygulamanın yapısal loglarıdır (logger varsayılanı os.Stdout).
	stdout *gunlukTamponu
	// stderr açılışı durduran "fatal:" satırının ve OTel SDK'sının yazdığı
	// yerdir; yanlış yapılandırma iddiaları buraya bakar.
	stderr *gunlukTamponu
	// bitti, Wait döndüğünde kapanır.
	bitti chan struct{}
	// mu aşağıdaki iki alanı korur; Wait'i çağıran goroutine yazar.
	mu          sync.Mutex
	cikisKodu   int
	cikisHatasi error
}

// bosPort işletim sisteminden boş bir TCP portu ister.
//
// # Neden sabit port değil
//
// Senaryolar aynı makinede, kimi zaman aynı anda (bkz. yaris_test.go) sunucu
// açar. Sabit bir port, ikinci süreci "address already in use" ile düşürür ve
// arızayı testin kendisine yükler.
//
// # Neden bu yol
//
// Uygulama APP_PORT=0'ı kabul etmez (config.Validate 1-65535 ister), yani
// portu işletim sistemine SEÇTİREMEYİZ; sormak zorundayız. Dinleyiciyi kapatıp
// numarayı kullanmak teorik bir yarış bırakır — kapanışla sunucunun bağlanması
// arasında başkası aynı portu alabilir. Pratikte Linux çekirdeği efemer port
// aralığını sırayla dolaşır, yani az önce bırakılan numara hemen tekrar
// dağıtılmaz; ayrıca yarışın kaybedilmesi SESSİZ değildir: süreç açılışta
// ölür, [surec.hazirBekle] zaman aşımına düşer ve sürecin logunu basar.
func bosPort(t *testing.T) int {
	t.Helper()

	dinleyici, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "boş port alınamadı")

	adres, tcpMi := dinleyici.Addr().(*net.TCPAddr)
	require.True(t, tcpMi, "tcp dinleyicisinden tcp adresi beklenir, %T geldi", dinleyici.Addr())
	require.NoError(t, dinleyici.Close(), "geçici dinleyici kapatılamadı")

	return adres.Port
}

// temelAyarlar bir senaryonun çalışan en küçük yapılandırmasıdır.
//
// Senaryolar bunu alır ve YALNIZCA sınadıkları anahtarı değiştirir; ortak
// tabanın burada durması, "bu senaryo neden farklı davrandı" sorusunun cevabını
// tek bir satırlık farka indirger.
func temelAyarlar(dsn string, port int) ayarlar {
	return ayarlar{
		"APP_ENV":  "development",
		"APP_PORT": strconv.Itoa(port),

		"DATABASE_URL": dsn,
		// JWT sırrı AÇIKÇA verilir: verilmediğinde cmd/server her açılışa özel
		// rastgele bir sır üretir ve bu, çok örnekli senaryoda örnekten örneğe
		// geçersiz jetonlar demektir.
		"JWT_SECRET": smokeJWTSirri,

		// Metin biçimi yalnızca TEŞHİS içindir: bir senaryo düştüğünde sürecin
		// logu teste basılır ve JSON satırları okunmayı zorlaştırırdı.
		"LOG_FORMAT": "text",
		"LOG_LEVEL":  "info",
	}
}

// smokeJWTSirri senaryoların paylaştığı imza sırrıdır.
//
// 32 karakterden UZUNDUR ki paylaşılan ortam (staging) senaryolarında
// config.Validate'in uzunluk kapısına takılmasın.
const smokeJWTSirri = "smoke-testi-imza-sirri-32-bayttan-uzun"

// Tohum senaryolarının paylaştığı ilk yönetici kimliği.
//
// Parola 16 karakterden uzundur: yerel geliştirmede yalnızca auth modülünün
// 12'lik tabanı geçerlidir ama paylaşılan ortam senaryoları
// config.MinBootstrapPasswordLen kapısından da geçmelidir.
const (
	tohumEposta = "admin@gobit.test"
	tohumParola = "smoke-tohum-parolasi-42"
)

// sunucuBaslat sunucu ikilisini başlatır ve hazır olmasını BEKLEMEZ.
//
// Süreç t.Cleanup ile MUTLAKA durdurulur: kaçan bir süreç, testcontainers'ın
// veritabanını kapatamamasına ve CI koşucusunun asılı kalmasına yol açar.
func sunucuBaslat(t *testing.T, ayar ayarlar) *surec {
	t.Helper()

	port, err := strconv.Atoi(ayar["APP_PORT"])
	require.NoError(t, err, "APP_PORT sayı olmalı: %q", ayar["APP_PORT"])

	s := &surec{
		t:      t,
		adres:  fmt.Sprintf("http://127.0.0.1:%d", port),
		stdout: &gunlukTamponu{},
		stderr: &gunlukTamponu{},
		bitti:  make(chan struct{}),
	}

	s.cmd = exec.Command(ikiliYolu)
	s.cmd.Env = ortam(ayar)
	s.cmd.Stdout = s.stdout
	s.cmd.Stderr = s.stderr

	require.NoError(t, s.cmd.Start(), "sunucu süreci başlatılamadı")

	go func() {
		err := s.cmd.Wait()

		s.mu.Lock()
		s.cikisHatasi = err
		s.cikisKodu = s.cmd.ProcessState.ExitCode()
		s.mu.Unlock()

		close(s.bitti)
	}()

	t.Cleanup(s.durdur)

	return s
}

// ortam sürecin ortam değişkeni listesini SIFIRDAN kurar.
//
// Testi çalıştıranın ortamı DEVRALINMAZ ve bu, senaryoların doğruluğu için
// zorunludur: cmd/server eklenti ayarlarını os.Environ()'dan okur, yani
// geliştiricinin kabuğunda duran bir STRIPE_API_KEY, "anahtar yok" senaryosunu
// sessizce geçirirdi. Aynısı DATABASE_URL ve PLUGINS için de geçerlidir.
//
// PATH ve HOME devralınır: ikisi de uygulamanın davranışını değiştirmez ama
// süreç başlatmanın ve DNS/TLS kök deposunun beklediği asgari ortamdır.
func ortam(ayar ayarlar) []string {
	disari := make([]string, 0, len(ayar)+2)

	for _, ad := range []string{"PATH", "HOME"} {
		if deger, varsa := os.LookupEnv(ad); varsa {
			disari = append(disari, ad+"="+deger)
		}
	}

	for ad, deger := range ayar {
		disari = append(disari, ad+"="+deger)
	}

	return disari
}

// hazirBekle süreç /health'e 200 dönene kadar YOKLAR.
//
// Zaman aşımında sürecin LOGU teste basılır: onsuz CI'da düşen bir senaryonun
// nedeni ("port dolu" mu, "migration kilidi" mi, "config hatası" mı)
// anlaşılamaz ve tek bilgi "zaman aşımı" olurdu.
func (s *surec) hazirBekle(sure time.Duration) {
	s.t.Helper()

	istemci := &http.Client{Timeout: istekSuresi}
	son := time.Now().Add(sure)

	for {
		if s.oldu() {
			kod, cikisHatasi := s.cikisDurumu()
			s.t.Fatalf("süreç açılışta öldü (çıkış kodu %d, %v)\n%s", kod, cikisHatasi, s.gunluk())
		}

		yanit, err := istemci.Get(s.adres + "/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, yanit.Body)
			_ = yanit.Body.Close()

			if yanit.StatusCode == http.StatusOK {
				return
			}
		}

		if time.Now().After(son) {
			s.t.Fatalf("süreç %s içinde /health'e yanıt vermedi (son hata: %v)\n%s",
				sure, err, s.gunluk())
		}

		time.Sleep(yoklamaAraligi)
	}
}

// iste sürece bir HTTP isteği yapar ve durum kodu ile gövdeyi döner.
func (s *surec) iste(metot, yol, jeton string) (kod int, govde string) {
	s.t.Helper()

	istek, err := http.NewRequestWithContext(s.t.Context(), metot, s.adres+yol, http.NoBody)
	require.NoError(s.t, err, "istek kurulamadı: %s %s", metot, yol)

	if jeton != "" {
		istek.Header.Set("Authorization", "Bearer "+jeton)
	}

	return s.gonder(istek)
}

// gonder hazır bir isteği gönderir ve durum kodu ile gövdeyi döner.
func (s *surec) gonder(istek *http.Request) (kod int, govde string) {
	s.t.Helper()

	istemci := &http.Client{Timeout: istekSuresi}

	yanit, err := istemci.Do(istek)
	require.NoError(s.t, err, "istek başarısız: %s %s\n%s",
		istek.Method, istek.URL.Path, s.gunluk())
	defer func() { _ = yanit.Body.Close() }()

	ham, err := io.ReadAll(yanit.Body)
	require.NoError(s.t, err, "yanıt gövdesi okunamadı")

	return yanit.StatusCode, string(ham)
}

// sigterm sürece SIGTERM gönderir.
//
// Orkestratörlerin (Kubernetes, systemd, docker stop) gönderdiği sinyal budur;
// düzgün kapanış iddiası ancak aynı sinyalle sınanırsa bir şey kanıtlar.
func (s *surec) sigterm() {
	s.t.Helper()

	require.NoError(s.t, s.cmd.Process.Signal(syscall.SIGTERM), "SIGTERM gönderilemedi")
}

// cikisiBekle sürecin bitmesini bekler ve çıkış kodunu döner.
//
// İkinci dönüş değeri, sürecin verilen sürede BİTİP bitmediğidir; çağıran
// "geç bitti" ile "sıfır olmayan kodla bitti" durumlarını ayırt edebilmelidir.
func (s *surec) cikisiBekle(sure time.Duration) (cikisKodu int, bitti bool) {
	s.t.Helper()

	select {
	case <-s.bitti:
		kod, _ := s.cikisDurumu()

		return kod, true
	case <-time.After(sure):
		return 0, false
	}
}

// cikisDurumu sürecin çıkış kodunu ve Wait'in hatasını kilit altında okur.
//
// Hata da dönülür çünkü çıkış kodu tek başına eksik kalır: sinyalle ölen bir
// süreçte kod -1'dir ve neyin öldürdüğünü ("signal: killed") yalnızca hata
// söyler. Teşhis mesajının ikisini birden taşıması, CI'da düşen bir senaryoyu
// tek bakışta ayırt edilebilir kılar.
func (s *surec) cikisDurumu() (kod int, hata error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.cikisKodu, s.cikisHatasi
}

// oldu sürecin bitmiş olup olmadığını bildirir.
func (s *surec) oldu() bool {
	select {
	case <-s.bitti:
		return true
	default:
		return false
	}
}

// gunluk sürecin iki akışını teşhis için tek metinde birleştirir.
//
// Akışlar AYRI etiketlenir: yanlış yapılandırma iddiaları yalnızca stderr'e
// bakar ve birleştirilmiş bir metin, "fatal satırı gerçekten stderr'e mi
// düştü" sorusunu cevaplayamaz hâle getirirdi.
func (s *surec) gunluk() string {
	return fmt.Sprintf("--- stdout ---\n%s--- stderr ---\n%s--------------\n",
		s.stdout.String(), s.stderr.String())
}

// durdur süreci kapatır; t.Cleanup buradan çağırır.
//
// Önce SIGTERM denenir, ancak süre dolarsa SIGKILL'e düşülür: bir senaryonun
// asılı kalan süreci, sonraki senaryoların portlarını ve veritabanı
// bağlantılarını tutar ve en sonunda CI koşucusunu asar.
func (s *surec) durdur() {
	if s.oldu() {
		return
	}

	_ = s.cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-s.bitti:
	case <-time.After(oldurmeSuresi):
		_ = s.cmd.Process.Kill()
		<-s.bitti
	}
}

// acilistaDurmali süreci başlatır ve AÇILIŞTA durmasını bekler.
//
// Yanlış yapılandırma senaryolarının tamamı bu yolu kullanır; dönen değerler
// çıkış kodu ve stderr'dir. Süreç ayakta kalırsa test DÜŞER: "yanlış
// yapılandırmayla açıldı" tam olarak yakalanmak istenen arızadır.
func acilistaDurmali(t *testing.T, ayar ayarlar, sure time.Duration) (cikisKodu int, stderr string) {
	t.Helper()

	s := sunucuBaslat(t, ayar)

	kod, bitti := s.cikisiBekle(sure)
	if !bitti {
		t.Fatalf("süreç %s içinde durmadı; yanlış yapılandırmayla AÇILDI\n%s", sure, s.gunluk())
	}

	return kod, s.stderr.String()
}

// gunlukIceriyorMu iki akışın herhangi birinde metnin geçtiğini bildirir.
func (s *surec) gunlukIceriyorMu(metin string) bool {
	return strings.Contains(s.stdout.String(), metin) ||
		strings.Contains(s.stderr.String(), metin)
}

// gunlukBekle metin iki akıştan birinde görünene kadar YOKLAR.
//
// Yoklamanın gerekçesi gunlukSuresi godoc'unda; sabit uyku bulunmamasının
// gerekçesi ise [surec.hazirBekle] ile aynıdır. Zaman aşımında sürecin LOGU
// basılır: onsuz tek bilgi "satır görünmedi" olurdu ve satırın hiç
// yazılmaması ile geç yazılması ayırt edilemezdi.
func (s *surec) gunlukBekle(metin string, sure time.Duration) {
	s.t.Helper()

	son := time.Now().Add(sure)
	for {
		if s.gunlukIceriyorMu(metin) {
			return
		}

		if time.Now().After(son) {
			s.t.Fatalf("%q sürecin logunda %s içinde görünmedi\n%s", metin, sure, s.gunluk())
		}

		time.Sleep(yoklamaAraligi)
	}
}
