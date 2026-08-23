// Package container gobit'in bağımlılık enjeksiyonu (DI) kabını sağlar.
//
// Kap, servisleri ADLA tutar ve [Resolve] ile tip parametresi vererek geri
// verir. Modüller birbirini import etmediği için (bkz. ADR 0001) bir modül,
// ihtiyaç duyduğu dar arayüzü kendi paketinde tanımlar ve sağlayıcının somut
// tipini buradan adla çözer.
//
// # Neden samber/do değil
//
// Plan Bölüm 3 DI için samber/do v2'yi öneriyor, Bölüm 5.1'deki sözleşme ise
// bağlayıcı: Provide(name string, ctor any) error ve Resolve[T](c, name).
// do v2'nin kayıt yüzeyi tip parametrelidir (ProvideNamed[T]); "any" alan bir
// Provide'ı onun üstüne kurmak için her servisi do'ya any olarak vermek
// gerekirdi. O anda do'nun getirdiği üç şey de elden gidiyor:
//
//  1. Tip bilgisi any'ye düzleştiği için do'nun hataları ADR 0001'in istediği
//     "kayıtlı somut tip vs beklenen tip" teşhisini veremez.
//  2. do çift kayıtta panic eder; sözleşme errors.Conflict istiyor.
//  3. do kapatmayı kendi bağımlılık grafiğine göre yapar ve yalnızca kendi
//     Shutdowner arayüzlerini tanır; sözleşme KAYIT sırasının tersini ve
//     io.Closer desteğini şart koşuyor.
//
// Geriye do'dan yalnızca mutex'li bir map kalıyordu; bu paket onu sözleşmenin
// istediği davranışla doğrudan yazar. Dışarıya yalnızca buradaki yüzey
// göründüğü için gövde ileride bir kütüphaneye taşınabilir.
//
// # Eşzamanlılık
//
// Tüm metodlar goroutine-güvenlidir. Bir adın tembel yapıcısı, aynı anda 100
// Resolve çağrılsa bile tam olarak bir kez çalışır; diğer çağıranlar sonucu
// bekler.
//
// [Container.Shutdown] kapanışla yarışan çözümleri de kapsamaya ÇALIŞIR:
// kapanış sırasında kurulmayı bitiren bir servis, kurulumu Shutdown'a verilen
// ctx bütçesi İÇİNDE biterse kapatılır. Bütçe dolarsa o servis kapatılmadan
// kalır (Shutdown bunu hataya ekler); her iki durumda da çağıranına verilmez —
// kapatılmış kap CANLI servis dağıtmaz, errors.Unavailable döner.
//
// Pratikte bu, Shutdown'a verilen ctx'in en yavaş yapıcıdan uzun olması
// gerektiği anlamına gelir.
package container

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	codeInvalidName  = "container_invalid_name"
	codeInvalidCtor  = "container_invalid_ctor"
	codeDuplicate    = "container_duplicate_service"
	codeNotFound     = "container_service_not_found"
	codeTypeMismatch = "container_type_mismatch"
	codeCycle        = "container_dependency_cycle"
	codeCtorFailed   = "container_ctor_failed"
	codeCtorPanic    = "container_ctor_panic"
	codeCtorNil      = "container_ctor_nil"
	codeShutdown     = "container_shutdown_failed"
	codeClosePanic   = "container_close_panic"
	codeCanceled     = "container_shutdown_canceled"
	codeClosed       = "container_closed"
)

// defaultWaitWarn kurulmakta olan bir servisi bekleyen çağıranın uyarı
// loglamadan önce sessizce bekleyeceği süredir. Bkz. registry.waitReady.
const defaultWaitWarn = 5 * time.Second

// Ctor tembel yapıcının imzasıdır. İlk [Resolve] çağrısında bir kez çalışır.
//
// Yapıcı, kendi bağımlılıklarını KENDİSİNE VERİLEN *Container üzerinden
// çözmelidir; kapanışla (closure) dıştaki container'ı kullanmak bağımlılık
// döngüsü tespitini devre dışı bırakır. Bu durumda karşılıklı bağımlı iki
// yapıcı birbirini süresiz bekler; bekleme uzarsa bekleme grafiğiyle birlikte
// bir uyarı loglanır (bkz. [New] log parametresi), ama hata dönmez.
//
// Bu imzayı tutturamayan bir işlev (örn. somut tip dönen bir yapıcı) [Provide]
// tarafından reddedilir; sessizce hazır değer olarak kaydedilmez.
type Ctor = func(*Container) (any, error)

// Shutdowner bağlam farkında kapanışı olan servislerin arayüzüdür.
// [Container.Shutdown] bir servis hem bunu hem io.Closer'ı karşılıyorsa
// bunu tercih eder (bağlamı iletebilmek için).
type Shutdowner interface {
	Shutdown(ctx context.Context) error
}

// entry tek bir kayıttır. Alanları yalnızca registry.mu altında okunur/yazılır;
// istisna, yapıcının kilit DIŞINDA çalıştırılmasıdır (bkz. resolve).
type entry struct {
	name string
	// ctor tembel yapıcıdır; hazır değer kaydında nil olur.
	ctor Ctor
	// value ve err yapıcı bittikten sonra doldurulur.
	value any
	err   error
	// built true ise value/err nihaidir (hazır değerlerde kayıtta true olur).
	built bool
	// building true ise bir goroutine yapıcıyı çalıştırıyor; bekleyenler
	// ready kanalının kapanmasını bekler.
	building bool
	ready    chan struct{}
}

// registry tüm kaplar (kök ve yapıcıya verilen türevleri) tarafından paylaşılan
// durumdur. Container değeri hafiftir; asıl durum burada tutulur.
type registry struct {
	mu      sync.Mutex
	log     *slog.Logger
	entries map[string]*entry
	// order kayıt sırasıdır; Shutdown bunun tersini kullanır.
	order []string
	// blocked bekleme grafiğidir: blocked[a] kümesinde b varsa "a'nın yapıcısı
	// b'yi bekliyor" demektir. Bir yapıcı goroutine ile aynı anda birden çok
	// Resolve çağırabildiği için düğüm başına birden çok kenar tutulur.
	// Döngü tespiti bu grafik üzerinde yapılır.
	blocked map[string]map[string]struct{}
	closed  bool
	// building o an KİLİT DIŞINDA çalışan yapıcı sayısıdır; Shutdown, hiçbir
	// servisin fotoğrafın dışında kalmaması için bunların bitmesini bekler.
	building int
	// drained, Shutdown uçuştaki yapıcıları beklerken oluşturulur; sayaç
	// sıfıra inince kapatılır.
	drained chan struct{}
	// waitWarn, kurulumu bekleyen çağıranın uyarı loglamadan önce beklediği
	// süredir; testler kısaltabilsin diye alan olarak tutulur.
	waitWarn time.Duration
}

// Container adla kayıtlı servisleri tutan DI kabıdır.
//
// Sıfır değeri kullanılamaz; [New] ile oluşturulmalıdır.
type Container struct {
	reg *registry
	// current, bu container'ın hangi servisin yapıcısına verildiğini tutar.
	// Kök container'da boştur; döngü tespiti bu alanla çalışır.
	current string
}

// New boş bir container üretir. log nil ise loglar atılır.
func New(log *slog.Logger) *Container {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Container{reg: &registry{
		log:      log,
		entries:  make(map[string]*entry),
		blocked:  make(map[string]map[string]struct{}),
		waitWarn: defaultWaitWarn,
	}}
}

// Provide bir servisi adla kaydeder.
//
// ctor iki biçimden biri olabilir:
//
//  1. Doğrudan bir değer (hazır kurulmuş servis).
//  2. [Ctor] imzalı tembel yapıcı; ilk Resolve'da bir kez çalışır.
//
// Tembel biçim modül sırasından bağımsızlık sağlar: A modülü, B modülü henüz
// kaydolmadan B'nin servisine bağımlı bir yapıcı kaydedebilir.
//
// Aynı ad ikinci kez kaydedilirse errors.Conflict döner. Şunlar errors.Invalid
// döner: boş ad; nil kayıt (arayüz-nil ve (*T)(nil) gibi tipli nil dahil); ilk
// parametresi *Container olup [Ctor] imzasını tutturamayan bir işlev — böyle
// bir işlev hazır değer sayılmaz, en yaygın yazım hatası olduğu için kayıt
// anında reddedilir.
func (c *Container) Provide(name string, ctor any) error {
	if name == "" {
		return errors.Invalid(codeInvalidName, "servis adı boş olamaz")
	}
	// Tipli nil de elenir: (*Pool)(nil) gibi bir değer arayüz-nil DEĞİLDİR,
	// kaba girerse ilk kullanımda ya da kapanışta paniklerdi.
	if isNil(ctor) {
		return errors.Invalid(codeInvalidCtor,
			"%q için nil kayıt yapılamaz (verilen tip: %s)", name, typeName(reflect.TypeOf(ctor)))
	}

	r := c.reg
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return errors.Unavailable(codeClosed, "container kapatıldı; %q kaydedilemez", name)
	}
	if _, dup := r.entries[name]; dup {
		return errors.Conflict(codeDuplicate, "%q adıyla bir servis zaten kayıtlı", name)
	}

	e := &entry{name: name}
	switch fn := ctor.(type) {
	case Ctor:
		e.ctor = fn
	default:
		if misusedCtor(ctor) {
			return errors.Invalid(codeInvalidCtor,
				"%q için verilen %s bir yapıcı gibi görünüyor ama imzası tutmuyor; beklenen imza: func(*container.Container) (any, error)",
				name, typeName(reflect.TypeOf(ctor)))
		}
		e.value, e.built = ctor, true
	}

	r.entries[name] = e
	r.order = append(r.order, name)
	r.log.Debug("servis kaydedildi", "servis", name, "tembel", e.ctor != nil)
	return nil
}

// Has verilen adın kayıtlı olup olmadığını bildirir. Kaydı kurmaz.
func (c *Container) Has(name string) bool {
	c.reg.mu.Lock()
	defer c.reg.mu.Unlock()
	_, ok := c.reg.entries[name]
	return ok
}

// Names kayıtlı tüm servis adlarını sıralı olarak döner.
func (c *Container) Names() []string {
	c.reg.mu.Lock()
	defer c.reg.mu.Unlock()
	return slices.Sorted(maps.Keys(c.reg.entries))
}

// resolve adı tipsiz olarak çözer; gerekiyorsa tembel yapıcıyı çalıştırır.
//
// Yapıcı KİLİT TUTULMADAN çalıştırılır: yapıcının içinden yapılan Resolve
// çağrıları da kilide ihtiyaç duyar. Kayıt, çalışma boyunca building
// bayrağıyla korunur; aynı anda gelen diğer çağıranlar ready kanalını bekler.
//
// Kilit her bırakılıp yeniden alındığında r.closed yeniden okunur: bu arada
// Shutdown çağrılmış olabilir ve kapatılmış bir kap CANLI servis dağıtmamalıdır.
func (c *Container) resolve(name string) (any, error) {
	r := c.reg
	r.mu.Lock()

	if r.closed {
		r.mu.Unlock()
		return nil, errClosed(name)
	}

	e, ok := r.entries[name]
	if !ok {
		known := slices.Sorted(maps.Keys(r.entries))
		r.mu.Unlock()
		return nil, errors.NotFound(codeNotFound,
			"%q adıyla kayıtlı servis yok; kayıtlı adlar: %s", name, joinNames(known)).
			WithDetails(map[string]any{"servis": name})
	}

	// Hızlı yol: kurulmuş (ya da hazır kaydedilmiş) servis.
	if e.built {
		value, err := e.value, e.err
		r.mu.Unlock()
		return value, err
	}

	// Bu çözüm bir yapıcının içinden geliyorsa bekleme grafiğine kenar ekle.
	// Kenar bir döngü kapatıyorsa deadlock'a girmeden hata dön.
	owner := c.current
	if owner != "" {
		if path := r.addEdge(owner, name); path != nil {
			r.mu.Unlock()
			return nil, errors.Conflict(codeCycle, "bağımlılık döngüsü: %s", strings.Join(path, " -> ")).
				WithDetails(map[string]any{"dongu": path})
		}
	}

	// Kaydı ya biz kurarız ya da kuran goroutine'i bekleriz.
	for !e.built && e.building {
		ready, warnAfter := e.ready, r.waitWarn
		r.mu.Unlock()
		r.waitReady(ready, name, warnAfter)
		r.mu.Lock()
	}
	if e.built {
		value, err, closed := e.value, e.err, r.closed
		r.removeEdge(owner, name)
		r.mu.Unlock()
		if closed {
			return nil, errClosed(name)
		}
		return value, err
	}
	e.building = true
	e.ready = make(chan struct{})
	ctor := e.ctor
	r.building++
	r.mu.Unlock()

	value, err := runCtor(ctor, &Container{reg: r, current: name}, name)

	r.mu.Lock()
	e.value, e.err, e.built, e.building = value, err, true, false
	close(e.ready)
	r.removeEdge(owner, name)
	closed := r.closed
	r.buildFinished()
	r.mu.Unlock()

	if err != nil {
		r.log.Error("servis kurulamadı", "servis", name, "hata", err)
		return nil, err
	}
	r.log.Debug("servis kuruldu", "servis", name)
	if closed {
		// Kap, yapıcı kilit dışında çalışırken kapatıldı. Kayıt kurulmuş
		// bırakılır: Shutdown, building sayacı sıfırlanana kadar beklediği
		// için bu servis onun fotoğrafına girer ve kapatılır. Çağırana ise
		// canlı servis değil, kapalı kap hatası döner.
		return nil, errClosed(name)
	}
	return value, nil
}

// runCtor yapıcıyı çalıştırır; panikleri ve nil sonucu tipli hataya çevirir.
//
// Yapıcının hatası ÖNBELLEĞE ALINIR (çağıran resolve tarafından): kayıt bir kez
// kurulur, bir daha denenmez. Gerekçe: (a) sözleşme "yapıcı tam bir kez çalışır"
// diyor, tekrar deneme yan etkileri (bağlantı açma, handler kaydı) ikinci kez
// tetikleyebilir; (b) DI hataları (eksik bağımlılık, tip uyumsuzluğu) belirlidir,
// tekrar denemek aynı hatayı üretip asıl arıza noktasını gizler; (c) geçici
// kaynak hataları (DB/Redis erişilemiyor) yapıcıda değil, servisin kendi
// içinde yeniden denenmelidir — yapıcı yalnızca bağlantıyı kurar, kullanmaz.
func runCtor(ctor Ctor, c *Container, name string) (value any, err error) {
	defer func() {
		if p := recover(); p != nil {
			value = nil
			err = errors.Internal(codeCtorPanic, "%q yapıcısı panikledi: %v", name, p)
		}
	}()

	value, err = ctor(c)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), codeCtorFailed, "%q servisi kurulamadı", name)
	}
	// Tipli nil de elenir; (*Pool)(nil) arayüz-nil değildir ama servis olarak
	// kullanılamaz.
	if isNil(value) {
		return nil, errors.Internal(codeCtorNil,
			"%q yapıcısı nil servis döndürdü (tip: %s)", name, typeName(reflect.TypeOf(value)))
	}
	return value, nil
}

// isNil değerin nil olup olmadığını, tipli nil'leri de sayarak bildirir.
// value == nil karşılaştırması yalnızca ARAYÜZ-nil'i yakalar: (*Pool)(nil) gibi
// bir değer o karşılaştırmayı geçer ama ilk metot çağrısında panikler.
func isNil(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Interface,
		reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}

// misusedCtor değerin, [Ctor] imzasını tutturamamış bir yapıcı olup olmadığını
// bildirir: ilk parametresi *Container olan bir işlev hazır değer olarak
// kaydedilmek istenmiş olamaz.
func misusedCtor(value any) bool {
	t := reflect.TypeOf(value)
	if t.Kind() != reflect.Func {
		return false
	}
	// Sıfır parametreli işlevler BİLİNÇLİ olarak değer sayılır. func() *Svc
	// bir yapıcı yazım hatası OLABİLİR, ama işlevin kendisinin servis olduğu
	// meşru kalıptan (func() time.Time saat servisi, kimlik üreteci vb.)
	// ayırt edilemez. Yanlış pozitif, geçerli bir kaydı reddederdi; bu yüzden
	// yalnızca ilk parametresi *Container olan — yani yapıcı olmaya
	// ÇALIŞTIĞI belli olan — imzalar reddedilir.
	if t.NumIn() == 0 {
		return false
	}
	return t.In(0) == reflect.TypeFor[*Container]()
}

// errClosed kapatılmış kapta çözüm denendiğini bildiren hatayı üretir.
func errClosed(name string) error {
	return errors.Unavailable(codeClosed, "container kapatıldı; %q çözülemez", name)
}

// waitReady yapıcının bitmesini bekler; bekleme warnAfter'ı aşarsa bekleme
// grafiğiyle birlikte bir uyarı loglar ve beklemeye devam eder.
//
// Uyarı, tespit edilemeyen döngüler içindir: kök container'ı closure ile
// yakalayan bir yapıcı bekleme grafiğine kenar eklemez, dolayısıyla kapattığı
// döngü görülemez ve bu bekleme sonsuza kadar sürer. Uyarı, o sessiz
// kilitlenmeyi hiç değilse loglara taşır.
func (r *registry) waitReady(ready <-chan struct{}, name string, warnAfter time.Duration) {
	timer := time.NewTimer(warnAfter)
	defer timer.Stop()

	select {
	case <-ready:
		return
	case <-timer.C:
	}

	r.mu.Lock()
	graph := r.waitEdges()
	r.mu.Unlock()

	r.log.Warn("servis kurulumu uzun sürüyor; bağımlılık döngüsü olabilir",
		"servis", name, "bekleme", warnAfter, "bekleme_grafigi", graph)
	<-ready
}

// buildFinished uçuştaki yapıcı sayacını azaltır; sayaç sıfıra inince Shutdown'ı
// bekleten kanalı kapatır. Çağıran r.mu'yu tutmalıdır.
func (r *registry) buildFinished() {
	r.building--
	if r.building == 0 && r.drained != nil {
		close(r.drained)
		r.drained = nil
	}
}

// drainSignal uçuşta yapıcı varsa hepsi bitince kapanacak kanalı döner; yoksa
// nil. Çağıran r.mu'yu tutmalıdır.
func (r *registry) drainSignal() <-chan struct{} {
	if r.building == 0 {
		return nil
	}
	if r.drained == nil {
		r.drained = make(chan struct{})
	}
	return r.drained
}

// addEdge from'un yapıcısının to'yu beklediğini kaydeder. Kenar bir döngü
// kapatıyorsa kenar eklenmez ve döngü yolu (baştaki düğüm sonda tekrar ederek)
// döner. Çağıran r.mu'yu tutmalıdır.
//
// Bir yapıcı kendi içinden goroutine ile AYNI ANDA birden çok Resolve
// çağırabilir; bu yüzden düğüm başına birden çok kenar tutulur ve hiçbiri
// diğerini ezmez.
func (r *registry) addEdge(from, to string) []string {
	edges, ok := r.blocked[from]
	if !ok {
		edges = make(map[string]struct{}, 1)
		r.blocked[from] = edges
	}
	edges[to] = struct{}{}

	if path := r.cyclePath(to); path != nil {
		r.removeEdge(from, to)
		return path
	}
	return nil
}

// removeEdge from'un to'yu beklediği kenarı siler; from'un başka beklemesi
// varsa onlara dokunmaz. Çağıran r.mu'yu tutmalıdır.
func (r *registry) removeEdge(from, to string) {
	if from == "" {
		return
	}
	edges := r.blocked[from]
	delete(edges, to)
	if len(edges) == 0 {
		delete(r.blocked, from)
	}
}

// cyclePath start'tan başlayarak bekleme kenarlarını derinlik öncelikli izler;
// yol üstündeki bir düğüme geri dönülüyorsa döngüyü (baştaki düğüm sonda tekrar
// ederek) döner, dönülmüyorsa nil. Çağıran r.mu'yu tutmalıdır.
func (r *registry) cyclePath(start string) []string {
	var (
		path    []string
		onPath  = make(map[string]bool, len(r.blocked)+1)
		visited = make(map[string]bool, len(r.blocked)+1)
		walk    func(node string) []string
	)

	walk = func(node string) []string {
		if onPath[node] {
			return append(slices.Clone(path[slices.Index(path, node):]), node)
		}
		if visited[node] {
			return nil
		}
		visited[node] = true
		onPath[node] = true
		path = append(path, node)

		// Sıralı gezinti, aynı grafik için hep aynı yolu üretir.
		for _, next := range slices.Sorted(maps.Keys(r.blocked[node])) {
			if found := walk(next); found != nil {
				return found
			}
		}

		path = path[:len(path)-1]
		onPath[node] = false
		return nil
	}

	return walk(start)
}

// waitEdges bekleme grafiğini "a -> b" biçiminde sıralı olarak yazar.
// Çağıran r.mu'yu tutmalıdır.
func (r *registry) waitEdges() []string {
	edges := make([]string, 0, len(r.blocked))
	for _, from := range slices.Sorted(maps.Keys(r.blocked)) {
		for _, to := range slices.Sorted(maps.Keys(r.blocked[from])) {
			edges = append(edges, from+" -> "+to)
		}
	}
	return edges
}

// Resolve adla kayıtlı servisi T tipinde çözer. Servis tembelse ilk çağrıda
// kurulur.
//
// Ad kayıtlı değilse errors.NotFound döner. Kayıtlı değer T'yi karşılamıyorsa
// errors.Invalid döner; hata mesajı hem kayıtlı somut tipi hem beklenen T'yi,
// T bir arayüzse eksik/uyumsuz metodları da yazar (ADR 0001).
func Resolve[T any](c *Container, name string) (T, error) {
	var zero T

	value, err := c.resolve(name)
	if err != nil {
		return zero, err
	}

	typed, ok := value.(T)
	if !ok {
		return zero, typeMismatch(name, value, reflect.TypeFor[T]())
	}
	return typed, nil
}

// MustResolve servisi çözer ve hata durumunda panikler. Yalnızca kurulum
// (bootstrap) yolunda, eksikliği programlama hatası sayılan servisler için
// kullanılır.
func MustResolve[T any](c *Container, name string) T {
	value, err := Resolve[T](c, name)
	if err != nil {
		panic(err)
	}
	return value
}

// Shutdown kurulmuş servisleri KAYIT SIRASININ TERSİNE kapatır ve hataları
// errors.Join ile birleştirir.
//
// Yalnızca [Shutdowner] veya io.Closer karşılayan, başarıyla kurulmuş servisler
// kapatılır; hiç çözülmemiş tembel kayıtlar kapatmak için KURULMAZ. Çağrı
// idempotenttir: ikinci çağrı nil döner. Kapatmadan sonra Provide ve Resolve
// errors.Unavailable döner.
//
// Kapatma HER SERVİSİ dener; tek tek arızalar kapatmayı yarıda kesmez:
//
//   - Uçuşta (kilit dışında çalışan) yapıcılar beklenir, böylece kapanış
//     sırasında kurulmayı bitiren bir servis kapatılmadan kalmaz. Bekleme
//     ctx bütçesiyle sınırlıdır; SÜRESİZ bir ctx verilirse asılı bir yapıcı
//     Shutdown'ı da bekletir.
//   - Bir servisin Close/Shutdown çağrısı paniklerse panik hataya çevrilir ve
//     kalan servisler kapatılmaya devam edilir.
//   - ctx iptal edilmişse kapatma yine de yapılır (io.Closer'lar bütçeye
//     ihtiyaç duymaz, ctx-farkındalı servisler iptali kendileri görür); iptal
//     yalnızca birleşik hataya ek bir kayıt olarak yazılır.
func (c *Container) Shutdown(ctx context.Context) error {
	r := c.reg

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	drained := r.drainSignal()
	r.mu.Unlock()

	// closed=true'dan sonra yeni yapıcı başlamaz; sayaç yalnızca azalır.
	if drained != nil {
		select {
		case <-drained:
		case <-ctx.Done():
		}
	}

	r.mu.Lock()
	targets := make([]*entry, 0, len(r.order))
	for i := len(r.order) - 1; i >= 0; i-- {
		// e.err != nil olan kayıtlarda runCtor değeri zaten atmıştır, yani
		// !isNil(e.value) tek başına yeterlidir; ayrıca e.err kontrolü ölü koddu.
		if e := r.entries[r.order[i]]; e != nil && e.built && !isNil(e.value) {
			targets = append(targets, e)
		}
	}
	r.mu.Unlock()

	var errs []error
	for _, e := range targets {
		if err := closeService(ctx, e.value); err != nil {
			r.log.Error("servis kapatılamadı", "servis", e.name, "hata", err)
			errs = append(errs, errors.Wrap(err, errors.KindOf(err), codeShutdown,
				"%q servisi kapatılamadı", e.name))
			continue
		}
		r.log.Debug("servis kapatıldı", "servis", e.name)
	}

	if err := ctx.Err(); err != nil {
		errs = append(errs, errors.Wrap(err, errors.KindUnavailable, codeCanceled,
			"kapatma bağlamı iptal edildi; %d servis yine de kapatılmaya çalışıldı", len(targets)))
	}
	return errors.Join(errs...)
}

// closeService servisi uygun arayüzle kapatır; hiçbirini karşılamıyorsa nil.
//
// Servisin panikleyen kapanışı hataya çevrilir: kapatma kayıt sırasının tersine
// yürüdüğü için ortada panikleyen tek bir servis, kendisinden önce kaydedilmiş
// tüm servislerin kapatılmasını engellerdi.
func closeService(ctx context.Context, value any) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = errors.Internal(codeClosePanic, "kapanış panikledi: %v", p)
		}
	}()

	switch s := value.(type) {
	case Shutdowner:
		return s.Shutdown(ctx)
	case io.Closer:
		return s.Close()
	default:
		return nil
	}
}

// joinNames ad listesini mesajda okunabilir biçimde yazar.
func joinNames(names []string) string {
	if len(names) == 0 {
		return "(kayıt yok)"
	}
	return strings.Join(names, ", ")
}
