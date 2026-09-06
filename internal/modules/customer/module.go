// Package customer müşteri modülüdür (plan Bölüm 6, Faz 5).
//
// Sorumluluğu tek cümleyle: kimin alışveriş yaptığını bilmek — misafir de olsa,
// kayıtlı da olsa. Modül Customer, CustomerGroup ve CustomerAddress verisinin
// TEK yazma yetkilisidir (Prensip 2.3).
//
// # Misafir mi, hesap mı
//
// Modülün merkezî kararı e-posta benzersizliğinin yalnızca KAYITLI hesaplarda
// uygulanmasıdır; misafir kayıtları aynı e-postayı paylaşabilir. Kural
// veritabanındaki kısmi benzersiz indekstedir ve gerekçesi
// internal/modules/customer/models, Customer godoc'unda yazılıdır.
//
// # Neyi bilmez
//
// customer hiçbir modülü import etmez ve sepetlerin, siparişlerin varlığından
// haberdar değildir. cart ↔ customer ve order ↔ customer bağlarını, ilişkinin
// sahibi olan o modüller Module Links ile kurar; customer o linkleri hiç görmez
// (Prensip 2.2: cross-module FK yoktur). Ülke kodları da doğrulanır ama
// LİSTELENMEZ: ülke listesinin sahibi region modülüdür.
//
// # Dışarıya açtığı yüzeyler
//
//   - "customer.service" — modüller arası çağrılar için servis (bkz.
//     internal/modules/customer/service, interop.go).
//   - "customer.query" — Query katmanına açılan okuma sağlayıcısı (ADR 0004).
//     Kayıtlar GRUP KİMLİKLERİYLE döner ki fiyat hesabının kural bağlamı tek
//     çağrıda kurulabilsin.
//   - /admin/v1/customers, /admin/v1/customer-groups … — yönetim API'si.
//   - /store/v1/customers … — vitrin API'si. KORUMASIZDIR ve öyle kalır:
//     müşteri kimliğini doğrulamak gömen uygulamanın işidir (ADR 0008); bkz.
//     internal/modules/customer/api paket belgesi.
//
// # Link'i bildiren tarafa not
//
// Query, bir genişletmenin hedef sağlayıcısını link tanımının UCUNDAKİ MODÜL
// ADINDAN bulur (hedef ad + ".query" aranır). customer ucu ENTITY ADIYLA
// yazılmalıdır:
//
//	link.LinkDefinition{
//	    Name:        "b2b_employee_customer",
//	    From:        link.LinkSide{Module: "b2b", Field: "employee_id"},
//	    To:          link.LinkSide{Module: "customer", Field: "customer_id"},
//	    Cardinality: link.OneToOne,
//	}
//
// Örnek GERÇEKTİR: b2b modülü bu bağı bildirir ve çalışanın müşteri kaydını
// ONUN ÜZERİNDEN okur. Okunmayan bir bağ bildirmek yerine sütun kullanmak
// gerekir; bunun neden böyle olduğu için bkz. internal/arch
// TestTheLinkDefinitionsAreTraversed.
//
// Burada entity adı ile modül adı aynıdır ("customer"), ama bu bir rastlantıdır
// ve sağlayıcı adı [ProviderName] sabitinden okunmalıdır.
package customer

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/customer/api"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// Container'daki adlar.
const (
	// ModuleName modülün benzersiz adıdır; migration versiyon tablosunun öneki
	// de budur.
	ModuleName = "customer"
	// ServiceName servisin container'daki adıdır. Tüketici modüller onu bu adla
	// ve KENDİ tanımladıkları dar arayüzle çözer (ADR 0001).
	ServiceName = ModuleName + ".service"
	// ProviderName query sağlayıcısının container'daki adıdır (ADR 0004).
	ProviderName = service.Entity + query.ProviderSuffix
	// dbServiceName çekirdek veritabanı havuzunun container'daki adıdır.
	dbServiceName = "core.db"
)

// codeSetupFailed modül kurulumunun başarısız olduğunu bildirir.
const codeSetupFailed = "customer_module_setup_failed"

// Bildirimde adı geçen tabloların adları ([Module.PersonalData] için).
//
// tableCustomer, [ModuleName] ile aynı harfleri taşır ama ondan TÜRETİLMEZ:
// biri veritabanındaki bir tablonun adı, öteki modülün container'daki adıdır ve
// birinin değişmesi ötekini değiştirmez. Aynı gerekçe [ProviderName]'in
// dayandığı entity adı için de yazılıdır (bkz. paket belgesi).
const (
	tableCustomer = "customer"
	tableAddress  = "customer_address"
	tableGroup    = "customer_group"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// golang-migrate kaynağı KÖKTEN okur ve embed.FS dosyaları klasör adıyla
// birlikte taşırdı.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Module customer modülünün [module.Module] uygulamasıdır.
type Module struct {
	svc     *service.Service
	handler *api.Handler
	log     *slog.Logger
}

var _ module.Module = (*Module)(nil)

// Belgeyi anlatabildiği de derleme zamanında sabitlenir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve kompozisyon kökü onu TİP
// İDDİASIYLA arar; metot adı ya da imzası kayarsa hiçbir şey derlemede
// kırılmaz, yalnızca müşterinin uçları belgeden sessizce düşerdi. Bu satır o
// sessizliği kapatır.
var _ openapi.Describer = (*Module)(nil)

// Unutulma (erasure) yetenekleri de derleme zamanında sabitlenir.
//
// Gerekçe [openapi.Describer] pininin aynısıdır ve burada bedeli daha ağırdır:
// iki arayüz de TİP İDDİASIYLA aranır (ADR 0029), yani metot adı ya da imzası
// kaydığında hiçbir şey kırılmaz — modül taramadan sessizce düşer. Belge
// örneğinde bunun bedeli eksik bir yol, burada ise bir kişiye "veriniz
// silindi" denirken e-postasının, adının ve adresinin yerinde kalmasıdır.
//
// İki arayüz AYRI AYRI sabitlenir çünkü ayrı yeteneklerdir: bildirebilen ama
// silemeyen bir tutucu vardır (bkz. personaldata.Declarer belgesi) ve tek bir pin
// ikisini birbirine bağlardı.
var (
	_ personaldata.Eraser   = (*Module)(nil)
	_ personaldata.Declarer = (*Module)(nil)
)

// New kurulmamış bir customer modülü üretir; servis [Module.Register] içinde
// kurulur. log nil ise loglar atılır.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log}
}

// Name modülün adını döner.
func (m *Module) Name() string { return ModuleName }

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register servisi ve query sağlayıcısını container'a kaydeder.
//
// customer hiçbir MODÜLÜN servisine ihtiyaç duymaz; yalnızca çekirdek havuzunu
// çözer. Havuz Bootstrap'tan ÖNCE kaydedildiği için burada doğrudan çözmek
// güvenlidir — modül sırasına bağımlılık yaratan tek şey başka bir MODÜLÜN
// servisini çözmek olurdu ve bu yapılmaz.
//
// Link tanımı BİLDİRİLMEZ: customer, kendisine işaret eden bağların ucudur,
// sahibi değil. Bugünkü tek sahip b2b modülüdür ("b2b_employee_customer").
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü %q servisini çözemedi", ModuleName, dbServiceName)
	}

	repo := repository.New(pool.Pool())
	m.svc = service.New(repo, service.Options{Logger: m.log})
	m.handler = api.New(m.svc)

	if err := c.Provide(ServiceName, m.svc); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(m.svc)); err != nil {
		return err
	}

	m.log.InfoContext(ctx, "customer modülü kaydedildi",
		slog.String("servis", ServiceName),
		slog.String("saglayici", ProviderName),
	)
	return nil
}

// Routes modülün admin ve store route'larını router'a bağlar.
//
// Register'dan SONRA çağrılır (bkz. module.Registry.Bootstrap); handler bu
// yüzden kurulmuş olur. Yine de nil kontrolü vardır: Register hata verip
// Bootstrap yarıda kesilirse Routes hiç çağrılmaz, ama modül elle kullanılırsa
// panik yerine sessiz bir no-op daha güvenlidir.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		return
	}
	m.handler.Routes(r)
}

// Describe modülün uçlarını OpenAPI belgesine işler.
//
// Anlatımın kendisi [api.Describe]'dedir: gövde şemaları o paketin dışa kapalı
// DTO'larından türetilir ve tipleri yalnızca belge uğruna dışa açmak modülün
// yüzeyini genişletirdi.
//
// [Module.Routes]'un tersine handler kontrolü YOKTUR ve gerekmez: şema
// tiplerden gelir, servisten değil. Kontrol koymak, kurulmamış bir modülün
// belgesini de sessizce boşaltırdı.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Erase kişinin müşteri kayıtlarını ANONİMLEŞTİRİR; hiçbirini silmez.
//
// Kararın kendisi ve gerekçesi servistedir (bkz. service.Service.Erase): satır
// gidemez, çünkü sepet ve sipariş müşteri kimliğini foreign key'siz düz bir
// TEXT sütununda taşır (Prensip 2.2) ve satırı silmek onları sessizce sahipsiz
// bırakırdı. Modül burada yalnızca DELEGE eder — iş servisin, çünkü işlem
// (transaction) ve depo yalnızca oradan görünür.
//
// Register çağrılmamışsa servis nil'dir ve bu bir paniğe DÖNÜŞMEZ: servisin
// ready denetimi nil alıcıyı da kapsar ve tipli bir Unavailable hatası döner.
// Sessiz bir "0 satır anonimleştirildi" yanıtı ise kabul edilemezdi — kurulmamış
// bir modül, hiç veri tutmayan bir modül gibi görünürdü.
func (m *Module) Erase(ctx context.Context, s personaldata.Subject) (personaldata.Result, error) {
	return m.svc.Erase(ctx, s)
}

// PersonalData modülün kişisel veri tuttuğu HER sütunu bildirir.
//
// # Neden modülde, neden statik
//
// Bildirim KODUN bir özelliğidir, verinin değil: boş veritabanında da dolusunda
// da aynı cümledir ve bir denetim onu bağlantı açmadan okur (ADR 0029). Bu
// yüzden servise değil modüle bağlıdır ve Register çağrılmamışken de doğru
// yanıt verir.
//
// # Neden eksiksiz olmak zorunda
//
// Liste, gömen uygulamanın "bu kişi hakkında nerede ne var" sorusuna
// verebileceği tek yanıttır. Eksik bir satır, listeyi kısaltmaz — YALAN hâline
// getirir: bildirilmeyen sütun hiçbir denetimde aranmaz. Liste bu yüzden
// migrations/000001_customer_init.up.sql'in AÇTIĞI DÖRT TABLONUN sütunlarından
// birebir türetilir ve eksiksizliği testle sabitlenir (bkz.
// TestPersonalDataCoversEveryPersonalColumn); test dört tablonun her sütununu
// okur ve bildirilmeyen her sütun için yazılı bir gerekçe ister.
//
// # Öteki iki tablodan neden yalnızca bir sütun
//
// customer_group bir SEGMENTTİR ("toptancılar"); adı gruba aittir, kişiye
// değil. metadata'sı ise başka bir şeydir: customer.metadata ile aynı cinsten,
// gömen uygulamanın SERBESTÇE yazdığı bir jsonb'dir ve gobit içine bakmaz.
// Bakmadığı için de "kişisel veri değildir" diyemez — o kararı ADR 0029 gömen
// uygulamaya bırakır — ve bu yüzden [personaldata.Open] olarak BİLDİRİLİR, hiçbir
// öznede yeniden yazılmasa bile; Erase onu her cevapta Kept'te sayar (bkz.
// service.Service.Erase).
//
// customer_group_customer üyelik OLGUSUDUR ve yalnızca iki kimlik taşır;
// müşteri satırı anonimleştikten sonra taşıdığı kimlik hiç kimseyi göstermez.
// Kimlik sütunlarının kendisi hiçbir tabloda bildirilmez: kişiyi tanımlayan şey
// kimliğin yanındaki sütunlardır ve anonimleştirmenin dayandığı gerçek tam
// olarak budur. Kimlik bildirilseydi, kişiyi göstermeyi bırakmış bir anahtarı
// "kişisel veri" diye listelemiş olurduk.
//
// [personaldata.Named] ile [personaldata.Open] ayrımı sorumluluk ayrımıdır: adlı sütunu
// oraya gobit yazdı ve ne olduğunu bilir, açık sütuna ne yazıldığına ise
// yalnızca gömen uygulama karar verebilir.
func (m *Module) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{
		Holder: ModuleName,
		Holdings: []personaldata.Holding{
			{
				Table: tableCustomer, Column: "email", Kind: personaldata.Named,
				Why: "the address the person gave; it is also how a guest checkout is recognized",
			},
			{
				Table: tableCustomer, Column: "first_name", Kind: personaldata.Named,
				Why: "the person's first name as they typed it",
			},
			{
				Table: tableCustomer, Column: "last_name", Kind: personaldata.Named,
				Why: "the person's last name as they typed it",
			},
			{
				Table: tableCustomer, Column: "phone", Kind: personaldata.Named,
				Why: "the person's phone number, used to reach them about an order",
			},
			{
				Table: tableCustomer, Column: "metadata", Kind: personaldata.Open,
				Why: "free-form context the shop writes about the customer; gobit puts nothing in it and never rewrites it, so whether it holds personal data is the controller's judgement",
			},
			{
				Table: tableGroup, Column: "metadata", Kind: personaldata.Open,
				Why: "free-form context the shop writes about a customer segment; the group is not a person, but the blob is the shop's to fill and gobit never looks inside it, so whether it names anybody is the controller's judgement",
			},
			{
				Table: tableAddress, Column: "first_name", Kind: personaldata.Named,
				Why: "the first name on a saved address, which may be the customer's or a recipient's",
			},
			{
				Table: tableAddress, Column: "last_name", Kind: personaldata.Named,
				Why: "the last name on a saved address, which may be the customer's or a recipient's",
			},
			{
				Table: tableAddress, Column: "company", Kind: personaldata.Named,
				Why: "the company the address is delivered to; for a sole trader it names the person",
			},
			{
				Table: tableAddress, Column: "address_1", Kind: personaldata.Named,
				Why: "the street line of a saved address — where the person lives or takes deliveries",
			},
			{
				Table: tableAddress, Column: "address_2", Kind: personaldata.Named,
				Why: "the second address line: flat, floor or door, which narrows the street line to a household",
			},
			{
				Table: tableAddress, Column: "city", Kind: personaldata.Named,
				Why: "the city of a saved address",
			},
			{
				Table: tableAddress, Column: "postal_code", Kind: personaldata.Named,
				Why: "the postal code of a saved address; in some countries it reaches a single building",
			},
			{
				Table: tableAddress, Column: "phone", Kind: personaldata.Named,
				Why: "the contact phone left on a saved address for the courier",
			},
			{
				Table: tableAddress, Column: "country_code", Kind: personaldata.Named,
				Why: "the country of a saved address; it is declared but deliberately NOT erased, because it names the jurisdiction whose tax and retention rules apply, a two-letter code points at tens of millions of people, and the column's CHECK constraint refuses an empty value",
			},
		},
	}
}

// Service kurulmuş servisi döner; Register çağrılmadıysa nil.
//
// Modülü doğrudan kullanan testler ve gömen uygulamalar içindir; normal akışta
// servis container'dan [ServiceName] adıyla çözülür.
func (m *Module) Service() *service.Service { return m.svc }

// mustSub gömülü dosya sisteminin alt ağacını açar.
//
// Yol derleme zamanında sabittir; buraya düşmek migrations klasörünün
// gömülmediği anlamına gelir ve sessiz geçilemez — migration'sız açılan bir
// modül, tabloları olmadan çalışmaya başlardı.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("customer: migration kaynağı açılamadı: " + err.Error())
	}
	return sub
}
