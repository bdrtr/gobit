package service

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// maxLocationRegions bir depoya bağlanabilecek azami bölge sayısıdır. Sınır,
// tek bir isteğin sınırsız satır yazmasını engeller.
const maxLocationRegions = 100

// SetShippingLocationInput bir deponun kargo politikasıdır.
//
// Girdi TOPTANDIR: verilen alanların hepsi MUTLAK değerlerdir ve eksik verilen
// bir alan "değiştirme" anlamına GELMEZ. Bölge listesi boş verilirse deponun
// tüm bölge bağları SİLİNİR ve depo tüm bölgelere hizmet eder hâle gelir —
// "bölgeleri olduğu gibi bırak" diye bir ifade yoktur. Alternatifi, boş dilim
// ile nil dilimi ayırt eden bir yüzeydi; o ayrım JSON'dan geçerken kaybolur ve
// istemcinin gönderdiğini sandığı şeyle sunucunun yaptığı şey ayrışırdı.
type SetShippingLocationInput struct {
	// LocationID politikanın yazılacağı stok lokasyonudur.
	//
	// Deponun VAR OLDUĞU doğrulanmaz: bu modül stok modülünü bilmez ve bir
	// lokasyon kimliğinin geçerliliğini soracağı kimse yoktur. Var olmayan bir
	// depo için yazılmış politika ZARARSIZDIR — seçim yalnızca stok modülünün
	// ÜRETTİĞİ adayları eler ve sıralar, kümeye eleman ekleyemez.
	LocationID string
	// Priority tercih sırasıdır; KÜÇÜK OLAN KAZANIR. Sıfır varsayılandır ve
	// politikası hiç olmayan bir depoyla aynı sıraya denk gelir; bir depoyu
	// varsayılanların üstüne çıkarmak için NEGATİF değer verilir.
	Priority int64
	// RegionIDs deponun hizmet ettiği kargo bölgeleridir. BOŞ verilirse depo
	// TÜM bölgelere hizmet eder (bkz. [Service.RankLocations]).
	//
	// SIRASI ANLAMSIZDIR: bağlar bir küme kurar ve okuma yolu onları daima
	// kimliğe göre sıralı döner. Yinelenen kimlik hata değildir, elenir.
	RegionIDs []string
}

// SetShippingLocation bir deponun kargo politikasını yazar ya da üzerine yazar.
//
// Öncelik ile bölge bağları AYNI işlemde yazılır. Ayrı yazılsalardı araya giren
// bir seçim, depoyu yeni önceliğiyle ama eski bölgeleriyle görürdü; daha kötüsü,
// bağlar silinmişken henüz yazılmamışken görülen depo BÜTÜN bölgelere açık
// olurdu ve kapsamı daraltmak için yapılan bir düzenleme, bir an için onu
// genişletirdi.
func (s *Service) SetShippingLocation(
	ctx context.Context,
	in SetShippingLocationInput,
) (models.ShippingLocation, error) {
	locationID := strings.TrimSpace(in.LocationID)
	if err := requireText("lokasyon kimliği", locationID); err != nil {
		return models.ShippingLocation{}, err
	}

	regions, err := normalizeRegionIDs(in.RegionIDs)
	if err != nil {
		return models.ShippingLocation{}, err
	}

	var out models.ShippingLocation
	txErr := s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, upErr := s.store.UpsertShippingLocation(ctx, locationID, in.Priority); upErr != nil {
			return upErr
		}
		if repErr := s.store.ReplaceShippingLocationRegions(ctx, locationID, regions); repErr != nil {
			return repErr
		}
		saved, getErr := s.store.GetShippingLocation(ctx, locationID)
		if getErr != nil {
			return getErr
		}
		out = saved
		return nil
	})
	if txErr != nil {
		return models.ShippingLocation{}, txErr
	}

	s.log.InfoContext(ctx, "depo kargo politikası yazıldı",
		"location_id", locationID, "priority", in.Priority, "regions", len(regions))
	return out, nil
}

// GetShippingLocation deponun politikasını bölgeleriyle döner.
//
// Politikası olmayan depo için NotFound döner ve bu, "böyle bir depo yok"
// DEMEK DEĞİLDİR: bu modül depoların varlığını bilmez, yalnızca kendi kaydının
// bulunmadığını bildirir. Politikasız bir depo seçimde geçerlidir ve
// varsayılan davranışı görür.
func (s *Service) GetShippingLocation(
	ctx context.Context,
	locationID string,
) (models.ShippingLocation, error) {
	trimmed := strings.TrimSpace(locationID)
	if err := requireText("lokasyon kimliği", trimmed); err != nil {
		return models.ShippingLocation{}, err
	}
	return s.store.GetShippingLocation(ctx, trimmed)
}

// ListShippingLocations yazılmış politikaları öncelik sırasıyla döner; ikinci
// değer TÜM satırların sayısıdır.
//
// Liste yalnızca POLİTİKASI OLAN depoları içerir. Kurulumdaki depoların tam
// listesi stok modülündedir ve buradan görünmez; iki listeyi birleştirmek
// yönetim yüzeyinin işidir.
func (s *Service) ListShippingLocations(
	ctx context.Context,
	page Page,
) ([]models.ShippingLocation, int64, error) {
	normalized, err := page.normalize()
	if err != nil {
		return nil, 0, err
	}
	return s.store.ListShippingLocations(ctx, models.LocationFilter{
		Limit:  normalized.Limit,
		Offset: normalized.Offset,
	})
}

// DeleteShippingLocation deponun politikasını siler; yoksa NotFound.
//
// Silmek depoyu KAPATMAZ, VARSAYILANA DÖNDÜRÜR: kaydı olmayan depo sıfır
// öncelikte ve tüm bölgelere hizmet ediyor sayılır. Bir depoyu adaylıktan
// çıkarmak kargo modülünün yetkisinde değildir — aday listesini stok olgusu
// üretir.
func (s *Service) DeleteShippingLocation(ctx context.Context, locationID string) error {
	trimmed := strings.TrimSpace(locationID)
	if err := requireText("lokasyon kimliği", trimmed); err != nil {
		return err
	}
	if err := s.store.DeleteShippingLocation(ctx, trimmed); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "depo kargo politikası silindi", "location_id", trimmed)
	return nil
}

// RankLocations adayları TERCİH SIRASINA dizer: gönderi ilkinden çıkar.
//
// # Karar burada durur çünkü bir KARGO kararıdır
//
// Hangi depodan gönderileceği bir kargo kararıdır; kuralları kargo bölgesine ve
// işletmecinin tercih sırasına bakar. "Hangi lokasyonlarda yeterli stok var" ise
// bir STOK OLGUSUDUR ve stok modülünün yüzeyinden gelir. İş bölümü bilinçlidir:
// iki yarıyı tek modülde toplamak, stok sorgusunu kargo politikasına ya da
// kargo politikasını stok şemasına bağımlı kılardı.
//
// Sepet akışı bu yüzden sırayı KENDİ kurmaz: adayları stoktan alır, sırayı
// buraya sorar.
//
// # Neden tek lokasyon değil SIRA döner
//
// Çağıran ilk depoda ayırmayı deneyip başarısız olabilir: adaylar kilitsiz
// okunur ve seçilen depo aradaki pencerede tükenebilir. Tek lokasyon dönseydi
// çağıranın geri düşecek yeri kalmaz ya da her tükenişte bu yüzey YENİDEN
// çağrılır, aynı kayıtlar aynı sıra için tekrar tekrar okunurdu. Sıra bir kez
// hesaplanır; ikinci, üçüncü denemenin bedeli sıfırdır.
//
// # Politika: ELE, SIRALA, EŞİTLİĞİ BOZ
//
// Sırayla:
//
//  1. ELEME — bir depoya en az bir bölge bağlanmışsa ve destinationRegionID
//     onların arasında DEĞİLSE aday düşer. Hiç bölge bağlanmamış depo TÜM
//     bölgelere hizmet eder ve elenmez.
//  2. SIRALAMA — kalanlar [models.LocationPolicy.Priority] küçükten büyüğe
//     dizilir. Politika kaydı olmayan depo sıfır önceliktedir, yani önceliği
//     açıkça sıfır yazılmış bir depoyla AYNI sıradadır.
//  3. EŞİTLİK BOZMA — eşit öncelikte kimliği küçük olan öne geçer.
//
// Politika kaydı hiç yoksa üç adımın sonucu tek başına üçüncü adımdır: sıranın
// başı, bu politikanın eklenmesinden ÖNCEKİ seçimin ta kendisidir.
//
// # Dönen dilim girdinin ALT KÜMESİDİR
//
// Elemanlar candidateLocationIDs'in elemanlarıyla BİREBİR aynı dizelerdir;
// normalleştirilmiş bir kopya ya da politika satırından okunmuş bir eş DEĞİL.
// Eşleştirme baştaki ve sondaki boşluklar atılarak yapılır ama dönen değer yine
// çağıranın verdiği dizedir: çağıran sonucu kendi aday defterinde arar ve
// bulamazsa akışı bir iç hata olarak düşürür.
//
// Aynı aday dilimde iki kez geçmez.
//
// # Neyi GARANTİ ETMEZ
//
// Üç şey bu yüzeyin dışındadır ve okuyucu onları buradan beklememelidir:
//
//   - Stok DAĞILIMI karara girmez. "En çok stoğu olan depoyu öne al" ifade
//     edilemez. Veri stok modülünde VARDIR — aday listesini üretirken lokasyon
//     başına satılabilir adedi zaten hesaplar — ama modüller arası İLKEL
//     yüzeyde yoktur ve oraya eklemek, stok modülünün "mağazaya lokasyon
//     kırılımı sızmaz" sınırıyla temas eder. İkinci ve daha ağır sebep
//     determinizmdir: politika işletmecinin AYARIDIR ve değişmesi beklenen bir
//     sonuçtur, oysa stok hızlı değişen bir olgudur ve aynı savunma orada
//     çalışmaz.
//   - MALİYET karara girmez. Depo ile taşıyıcı arasında bir tarife modeli
//     yoktur; yazılsaydı dayandığı veri uydurma olurdu.
//   - Sipariş DÜZEYİNDE bir karar verilmez. Sıra SATIR başına sorulur ve bu
//     yüzey sepetin tamamını görmez; "tüm satırları tek depodan çıkar" ya da
//     "gönderi sayısını azalt" burada ifade edilemez.
//
// "Yakınlık" bu sistemde coğrafi mesafe DEĞİL, kargo bölgesi kapsamıdır:
// depoların koordinatı yoktur ve uydurulmadı.
//
// # Sıra deterministiktir, ama neye göre
//
// Aynı adaylar VE aynı politika kayıtlarıyla ikinci çağrı aynı sırayı döner;
// sonuç adayların GELİŞ SIRASINDAN bağımsızdır. İşletmeci politikayı iki çağrı
// arasında değiştirirse sıra da değişir — bu bir ayarın beklenen sonucudur ve
// determinizm iddiası onu KAPSAMAZ.
//
// Aday dilimi sıralanmaz, KOPYALANIR: yerinde sıralamak çağıranın dilimini
// bozmak olurdu ve bir karar yüzeyi kendisine verilen veriyi değiştiremez.
//
// # Boş sonuç Conflict'tir
//
// İki ayrı boşluk vardır ve ikisi de errors.Conflict döner:
//
//   - Aday listesi BOŞ gelirse [CodeNoShippingLocation]. Eksik olan dünyanın
//     durumudur, isteğin biçimi değil; errors.Invalid yanlış olurdu.
//   - Tüm adaylar ELENİRSE [CodeNoServiceableLocation]. Kod ayrıdır çünkü
//     işletmecinin yapacağı iş de ayrıdır: birincisinde stok yoktur,
//     ikincisinde depoların bölge kapsamı yanlış kurulmuştur.
//
// Sınıfın Conflict olması iki şeyi birden belirler ve ikisi de ölçülebilir:
// hatanın HTTP karşılığı (çağıran hatayı sararken sınıfı korur, 409) ve
// yeniden denenebilirliği (motorun varsayılan yüklemi KindConflict'i DENEMEZ,
// KindInternal'ı dener). Elenmiş bir aday kümesi tekrar denemekle değişmez;
// Internal seçilseydi, telafi yeniden deneme açıldığı gün işletmecinin elle
// düzeltmesi gereken bir yapılandırma hatası geçici arıza sanılıp tekrarlanırdı.
//
// Kod ÇAĞIRANA ulaşır: sepet akışı adım hatasını sararken alt hatanın kodunu
// DEVRALIR, yani vitrin istemcisinin gövdede gördüğü kod buradakidir. Bu bir
// varsayım değil, sepet akışının yazılı sözleşmesidir ve ölçülmüş bir sebebi
// vardır — kod ezilseydi dolu raflarla "stok ayrılamadı" raporlanırdı.
//
// destinationRegionID boşsa errors.Invalid döner. Bu bir SAVUNMADIR: bugünkü
// tek çağıran sepet akışıdır ve orada bölgesi boş bir plan zaten kurulamaz.
// Yüzeyin kendini savunmasının sebebi, kapsam değerlendirilemezken elemeyi
// atlamanın o bölgeye hizmet ETMEYEN bir depoyu sessizce öne almak olmasıdır.
// Aday listesindeki BOŞ bir kimlik de aynı sebeple reddedilir.
func (s *Service) RankLocations(
	ctx context.Context,
	destinationRegionID string,
	candidateLocationIDs []string,
) ([]string, error) {
	if len(candidateLocationIDs) == 0 {
		return nil, errors.Conflict(CodeNoShippingLocation,
			"gönderi yapılabilecek lokasyon yok")
	}

	regionID := strings.TrimSpace(destinationRegionID)
	if err := requireText("hedef bölge kimliği", regionID); err != nil {
		return nil, err
	}

	candidates := make([]locationCandidate, 0, len(candidateLocationIDs))
	keys := make([]string, 0, len(candidateLocationIDs))
	for i, candidate := range candidateLocationIDs {
		key := strings.TrimSpace(candidate)
		if key == "" {
			return nil, errors.Invalid(CodeInvalidInput,
				"aday lokasyon kimliği boş olamaz (%d. aday)", i+1)
		}
		if err := checkTextLen("aday lokasyon kimliği", key); err != nil {
			return nil, err
		}
		candidates = append(candidates, locationCandidate{original: candidate, key: key})
		keys = append(keys, key)
	}

	rows, err := s.store.LocationPolicies(ctx, keys)
	if err != nil {
		return nil, err
	}

	policies := make(map[string]models.LocationPolicy, len(rows))
	for _, row := range rows {
		policies[row.LocationID] = row
	}

	ranked := rankLocations(regionID, candidates, policies)
	if len(ranked) == 0 {
		return nil, errors.Conflict(CodeNoServiceableLocation,
			"%s bölgesine hizmet eden depo yok; elenen adaylar: %s",
			regionID, elenenlerOzeti(candidates, policies))
	}
	return ranked, nil
}

// elenenlerOzeti hata mesajı için adayların bağlı olduğu bölgeleri yazar.
//
// Özet, elenmenin en sinsi sebebini görünür kılar: ölü bir bölge kimliği.
// İşletmeci bir bölgeyi silip aynı adla yeniden açarsa kimlik değişir, politika
// satırları eskisini taşımaya devam eder ve mağazadaki HER sipariş elenir.
// Yalnızca "hizmet eden depo yok" diyen bir mesajla operatör, kimliklerin
// ayrıştığını göremezdi.
//
// Özetin NEREDE görüldüğü ayrı bir sorudur ve abartılmamalıdır: vitrin
// istemcisinin gövdesine yalnızca KOD ulaşır. Bu metin sunucu logunda ve sepet
// akışının yürütme kaydında durur, yani okuyucusu operatördür.
//
// # Çağrıldığında her adayın politikası VARDIR ve bağları DOLUDUR
//
// Fonksiyon yalnızca sıra boş kaldığında çağrılır; sıranın boş kalması her
// adayın elendiği anlamına gelir ve eleme kuralı yalnızca kaydı OLAN ve bağı
// DOLU olan adayı düşürür (kaydı olmayan da bağı boş olan da tüm bölgelere
// hizmet eder). Bu yüzden burada "politikasız" ya da "bağı yok" durumu için dal
// YOKTUR: yazılsalardı hiçbir zaman koşmayan, dolayısıyla hiçbir zaman
// sınanamayan iki satır olurlardı.
func elenenlerOzeti(candidates []locationCandidate, policies map[string]models.LocationPolicy) string {
	parcalar := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		policy := policies[candidate.key]
		parcalar = append(parcalar,
			candidate.key+" → ["+strings.Join(policy.RegionIDs, " ")+"]")
	}
	return strings.Join(parcalar, ", ")
}

// locationCandidate bir adayın çağırandan gelen HÂLİNİ ve eşleştirmede
// kullanılan anahtarını birlikte taşır.
//
// İkisinin ayrı durması zorunludur: eşleştirme boşluklar atılmış anahtarla
// yapılır, dönen değer ise çağıranın verdiği dizedir. Tek alan olsaydı
// " sloc_a " yazan bir çağıran, aday defterinde bulamayacağı "sloc_a" cevabını
// alırdı.
type locationCandidate struct {
	original string
	key      string
}

// rankLocations politikayı adaylara uygular ve tercih sırasını döner.
//
// Fonksiyon SAFTIR ve veritabanına dokunmaz: kararın kendisi böylece gerçek bir
// Postgres olmadan, tek tek kurulmuş politika kümeleriyle sınanabilir. Aynı
// ayrım kargo seçeneği uygunluğunda da yapılır — ucuz eleme SQL'de, kuralın
// kendisi burada.
//
// policies YALNIZCA kaydı olan adayları taşır; haritada bulunmayan aday
// varsayılandır (sıfır öncelik, tüm bölgelere hizmet). Ayrım burada yapılır
// çünkü "kayıt yok" ile "kaydı var ama önceliği sıfır" AYNI sonucu vermelidir
// ve sorgunun bunu bilmesi gerekmez.
//
// Sıralama KARARLIDIR ve iki anahtarlıdır (öncelik, sonra anahtar); anahtar
// adaylar arasında tek olduğu için sonuç, girdinin sırasından bağımsızdır.
func rankLocations(
	regionID string,
	candidates []locationCandidate,
	policies map[string]models.LocationPolicy,
) []string {
	type sirali struct {
		original string
		key      string
		priority int64
	}

	kalan := make([]sirali, 0, len(candidates))
	gorulen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, dup := gorulen[candidate.key]; dup {
			continue
		}
		gorulen[candidate.key] = struct{}{}

		policy, configured := policies[candidate.key]
		if configured && !policy.ServesRegion(regionID) {
			continue
		}

		priority := int64(0)
		if configured {
			priority = policy.Priority
		}
		kalan = append(kalan, sirali{
			original: candidate.original,
			key:      candidate.key,
			priority: priority,
		})
	}

	slices.SortFunc(kalan, func(a, b sirali) int {
		if a.priority != b.priority {
			return cmp.Compare(a.priority, b.priority)
		}
		return strings.Compare(a.key, b.key)
	})

	out := make([]string, 0, len(kalan))
	for _, aday := range kalan {
		out = append(out, aday.original)
	}
	return out
}

// normalizeRegionIDs bölge kimliklerini doğrular ve YİNELENENLERİ eler.
//
// Yinelenen bir kimlik hata değildir: "aynı bölgeyi iki kez bağla" ifadesi tek
// kez bağlamakla aynı sonucu verir ve çağıranı bir çakışma hatasıyla
// karşılamak, düzeltilecek bir şey olmadığı hâlde isteği düşürürdü.
//
// Girdinin SIRASI ANLAMSIZDIR ve korunmaz: bölge bağları bir küme kurar, bir
// liste değil. Okuma yolu onları daima KİMLİĞE göre sıralı döner, yani aynı
// kümeyi farklı sırayla yazan iki istek aynı kaydı üretir. Bu fonksiyonun
// eleme sırası yalnızca kendi içinde kararlıdır.
func normalizeRegionIDs(regionIDs []string) ([]string, error) {
	if len(regionIDs) > maxLocationRegions {
		return nil, errors.Invalid(CodeInvalidInput,
			"bir depoya en fazla %d bölge bağlanabilir: %d", maxLocationRegions, len(regionIDs))
	}

	seen := make(map[string]struct{}, len(regionIDs))
	out := make([]string, 0, len(regionIDs))
	for i, regionID := range regionIDs {
		trimmed := strings.TrimSpace(regionID)
		if trimmed == "" {
			return nil, errors.Invalid(CodeInvalidInput,
				"bölge kimliği boş olamaz (%d. bölge)", i+1)
		}
		if err := checkTextLen("bölge kimliği", trimmed); err != nil {
			return nil, err
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out, nil
}
