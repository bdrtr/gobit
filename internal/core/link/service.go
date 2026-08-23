package link

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
)

// Log alanlarında ve hata ayrıntılarında kullanılan anahtarlar.
const (
	keyLink   = "link"
	keyFromID = "from_id"
	keyToID   = "to_id"
)

// uniqueViolation PostgreSQL'in benzersizlik ihlali SQLSTATE kodudur.
// (github.com/jackc/pgerrcode bağımlılığı eklemek yerine sabit yazıldı.)
const uniqueViolation = "23505"

// defineLockKey Define'ın aldığı danışma (advisory) kilidinin anahtarıdır;
// "link_def" dizgesinin ASCII karşılığıdır. Sabitin okunabilir bir kaynaktan
// türetilmesi, başka bir alt sistemin aynı anahtarı kazara seçmesini zorlaştırır.
const defineLockKey int64 = 0x6C696E6B5F646566

// lockSQL bildirimleri sıraya dizen danışma kilidini alır. Kilit İŞLEM
// sonunda kendiliğinden bırakılır; ayrı bir unlock çağrısı (ve onu unutma
// riski) yoktur.
const lockSQL = `SELECT pg_advisory_xact_lock($1)`

// relkindSQL geçerli şemadaki bir ilişkinin türünü (pg_class.relkind) okur.
// Tablolar ve indeksler aynı ad uzayını paylaştığı için tür kontrolü şarttır.
const relkindSQL = `SELECT relkind::text FROM pg_class
WHERE relname = $1 AND relnamespace = current_schema()::regnamespace`

// createDefinitionsTableSQL kalıcı kayıt defterini oluşturur.
//
// Bu tablo bir "link tablosu" değildir; link tanımlarının kendisini tutar ve
// yine hiçbir modül tablosuna FK vermez.
const createDefinitionsTableSQL = `CREATE TABLE IF NOT EXISTS ` + definitionsTable + ` (
	name        TEXT PRIMARY KEY,
	from_module TEXT NOT NULL,
	from_field  TEXT NOT NULL,
	to_module   TEXT NOT NULL,
	to_field    TEXT NOT NULL,
	cardinality TEXT NOT NULL,
	created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// upsertDefinitionSQL tanımı deftere yazar ve DEFTERDEKİ hâlini geri döner.
//
// "DO UPDATE SET name = <tablo>.name" kasıtlı bir yok-işlemdir: DO NOTHING
// seçilseydi çakışma hâlinde RETURNING hiçbir satır döndürmez, kayıtlı tanımı
// okumak için ikinci bir gidiş-dönüş gerekirdi. Bu biçimde ekleme ve
// karşılaştırma tek ifadede, tek kilit altında olur.
const upsertDefinitionSQL = `INSERT INTO ` + definitionsTable + `
	(name, from_module, from_field, to_module, to_field, cardinality)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (name) DO UPDATE SET name = ` + definitionsTable + `.name
RETURNING from_module, from_field, to_module, to_field, cardinality`

// storedDefinition kalıcı defterdeki satırın ham hâlidir.
//
// Kardinalite diskte METİN olarak durur (bkz. [Cardinality.String]); bu yüzden
// satır tipli LinkDefinition'a çevrilmez, karşılaştırma metin üzerinden yapılır.
// Böylece diskte tanınmayan bir kardinalite bulunsa bile karşılaştırma anlamlı
// kalır ve çakışma mesajı diskteki değeri olduğu gibi gösterir.
type storedDefinition struct {
	fromModule  string
	fromField   string
	toModule    string
	toField     string
	cardinality string
}

// matches defterdeki satırın verilen tanımla aynı olup olmadığını bildirir.
func (s storedDefinition) matches(def LinkDefinition) bool {
	return s.fromModule == def.From.Module &&
		s.fromField == def.From.Field &&
		s.toModule == def.To.Module &&
		s.toField == def.To.Field &&
		s.cardinality == def.Cardinality.String()
}

// String defterdeki satırı LinkDefinition.String ile aynı biçimde yazar.
func (s storedDefinition) String() string {
	return "(" + s.fromModule + "." + s.fromField +
		" -> " + s.toModule + "." + s.toField + ", " + s.cardinality + ")"
}

// service LinkService'in PostgreSQL uygulamasıdır.
type service struct {
	pool *db.Pool
	log  *slog.Logger
	defs *definitions
}

// New verilen bağlantı havuzu üzerinde çalışan bir LinkService üretir.
//
// log nil ise loglama yapılmaz. pool nil (veya kapatılmış) ise servis kurulur
// ama veritabanına dokunan her çağrı errors.Unavailable döner; kurulum sırası
// bu yüzden bir panikle değil, tipli bir hatayla bildirilir.
func New(pool *db.Pool, log *slog.Logger) LinkService {
	return newService(pool, log)
}

// newService somut servisi üretir; New'in ve paket içi testlerin ortak yoludur.
func newService(pool *db.Pool, log *slog.Logger) *service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &service{pool: pool, log: log, defs: newDefinitions()}
}

// Define bir link tanımını bildirir ve tablosunu (yoksa) oluşturur.
// Sözleşme ve idempotency kuralları için bkz. [LinkService].
func (s *service) Define(ctx context.Context, def LinkDefinition) error {
	if err := def.Validate(); err != nil {
		return err
	}

	// Hızlı yol: bu süreçte aynı tanım zaten bildirildiyse veritabanına hiç
	// gidilmez. Açılışta onlarca modül aynı tanımları yeniden bildirir.
	if existing, ok := s.defs.lookup(def.Name); ok {
		if existing.def != def {
			return conflictWithExisting(existing.def, def)
		}
		return nil
	}

	lt, err := newLinkTable(def)
	if err != nil {
		return err
	}
	if err := s.declare(ctx, lt); err != nil {
		return err
	}

	s.defs.put(lt)
	s.log.InfoContext(ctx, "link tanımlandı",
		slog.String(keyLink, def.Name),
		slog.String("tablo", lt.table),
		slog.String("kardinalite", def.Cardinality.String()),
	)
	return nil
}

// declare tanımı kalıcı deftere yazar ve link tablosunu oluşturur.
//
// Tümü TEK işlemde ve danışma kilidi altında yürür. İki gerekçe:
//
//  1. Aynı anda açılan iki süreç aynı DDL'i çalıştırırsa "CREATE TABLE IF NOT
//     EXISTS" bile yarışabilir (PostgreSQL katalog düzeyinde benzersizlik
//     ihlali verir). Kilit tüm link bildirimlerini tek sıraya dizer.
//  2. Defter satırı ile tablonun birlikte oluşması sağlanır; PostgreSQL'de DDL
//     işlemseldir, dolayısıyla yarıda kalan bir bildirim geri alınır.
func (s *service) declare(ctx context.Context, lt *linkTable) error {
	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return wrapDB(err, codeDefineFailed, "%q linki için işlem başlatılamadı", lt.def.Name)
	}
	// Commit'ten sonraki Rollback ErrTxClosed döner; anlamsız olduğu için yutulur.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, lockSQL, defineLockKey); err != nil {
		return wrapDB(err, codeDefineFailed, "%q linki için kilit alınamadı", lt.def.Name)
	}
	if _, err := tx.Exec(ctx, createDefinitionsTableSQL); err != nil {
		return wrapDB(err, codeDefineFailed, "%s tablosu oluşturulamadı", definitionsTable)
	}

	var stored storedDefinition
	err = tx.QueryRow(ctx, upsertDefinitionSQL,
		lt.def.Name, lt.def.From.Module, lt.def.From.Field,
		lt.def.To.Module, lt.def.To.Field, lt.def.Cardinality.String(),
	).Scan(&stored.fromModule, &stored.fromField, &stored.toModule, &stored.toField, &stored.cardinality)
	if err != nil {
		return wrapDB(err, codeDefineFailed, "%q linkinin tanımı deftere yazılamadı", lt.def.Name)
	}
	if !stored.matches(lt.def) {
		return conflictWithStored(stored, lt.def)
	}

	for _, stmt := range lt.ddl() {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return wrapDB(err, codeDefineFailed, "%q linkinin tablosu oluşturulamadı", lt.def.Name)
		}
	}

	if err := verifySchema(ctx, tx, lt); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return wrapDB(err, codeDefineFailed, "%q linkinin tanımı kalıcılaştırılamadı", lt.def.Name)
	}
	return nil
}

// Create fromID ile toID arasında bağ kurar.
// Idempotency ve kardinalite kuralları için bkz. [LinkService].
func (s *service) Create(ctx context.Context, name, fromID, toID string) error {
	lt, err := s.linkFor(name)
	if err != nil {
		return err
	}
	if err := validateIDPair(fromID, toID); err != nil {
		return err
	}
	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	tag, err := pool.Exec(ctx, lt.insert, fromID, toID)
	if err != nil {
		return lt.writeError(err, fromID, toID)
	}
	if tag.RowsAffected() == 0 {
		// Çift zaten bağlı: idempotent no-op. Sessiz kalmak yerine loglanır ki
		// beklenmedik tekrarlar (örn. döngüye giren bir saga) görünür olsun.
		s.log.DebugContext(ctx, "link zaten mevcut",
			slog.String(keyLink, name), slog.String(keyFromID, fromID), slog.String(keyToID, toID))
	}
	return nil
}

// Delete fromID ile toID arasındaki bağı kaldırır.
// Bağ yoksa çağrı no-op'tur; gerekçe için bkz. [LinkService].
func (s *service) Delete(ctx context.Context, name, fromID, toID string) error {
	lt, err := s.linkFor(name)
	if err != nil {
		return err
	}
	if err := validateIDPair(fromID, toID); err != nil {
		return err
	}
	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	tag, err := pool.Exec(ctx, lt.remove, fromID, toID)
	if err != nil {
		return wrapDB(err, codeQueryFailed, "%q linki silinemedi", name)
	}
	if tag.RowsAffected() == 0 {
		s.log.DebugContext(ctx, "silinecek link yok",
			slog.String(keyLink, name), slog.String(keyFromID, fromID), slog.String(keyToID, toID))
	}
	return nil
}

// List fromID'ye bağlı toID'leri artan sırada döner.
func (s *service) List(ctx context.Context, name, fromID string) ([]string, error) {
	lt, err := s.linkFor(name)
	if err != nil {
		return nil, err
	}
	if err := validateID(fromID, "fromID"); err != nil {
		return nil, err
	}
	pool, err := s.rawPool()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, lt.list, fromID)
	if err != nil {
		return nil, wrapDB(err, codeQueryFailed, "%q linkinin kayıtları okunamadı", name)
	}
	// CollectRows satırları kapatır ve rows.Err()'i de sonuca katar; hiç satır
	// yoksa BOŞ dilim döner (nil değil), böylece JSON'da "null" değil "[]" olur.
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, wrapDB(err, codeQueryFailed, "%q linkinin kayıtları okunamadı", name)
	}
	return ids, nil
}

// ListMany birden çok fromID'nin bağlarını tek sorguda döner.
// Batch gerekçesi için bkz. [LinkService] ve ADR 0004.
func (s *service) ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error) {
	lt, err := s.linkFor(name)
	if err != nil {
		return nil, err
	}
	for _, id := range fromIDs {
		if err := validateID(id, "fromID"); err != nil {
			return nil, err
		}
	}
	// Boş küme için sorgu açmaya gerek yok; sonuç zaten boştur.
	if len(fromIDs) == 0 {
		return map[string][]string{}, nil
	}
	pool, err := s.rawPool()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, lt.listMany, fromIDs)
	if err != nil {
		return nil, wrapDB(err, codeQueryFailed, "%q linkinin kayıtları okunamadı", name)
	}

	var fromID, toID string
	result := make(map[string][]string, len(fromIDs))
	// ForEachRow satırları kapatır ve rows.Err()'i döner.
	if _, err := pgx.ForEachRow(rows, []any{&fromID, &toID}, func() error {
		result[fromID] = append(result[fromID], toID)
		return nil
	}); err != nil {
		return nil, wrapDB(err, codeQueryFailed, "%q linkinin kayıtları okunamadı", name)
	}
	return result, nil
}

// ListManyByTo ters yönü toplu çözer: verilen toID'lerin her biri için bağlı
// fromID'leri döner. Bulunmayan toID sonuçta yer almaz.
//
// [service.ListMany] ile aynı desendedir; tek fark sorgunun yönüdür. Sonuç
// haritasının anahtarı toID, değeri o toID'ye bağlı fromID'lerdir.
func (s *service) ListManyByTo(ctx context.Context, name string, toIDs []string) (map[string][]string, error) {
	lt, err := s.linkFor(name)
	if err != nil {
		return nil, err
	}
	for _, id := range toIDs {
		if err := validateID(id, "toID"); err != nil {
			return nil, err
		}
	}
	// Boş küme için sorgu açmaya gerek yok; sonuç zaten boştur.
	if len(toIDs) == 0 {
		return map[string][]string{}, nil
	}
	pool, err := s.rawPool()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, lt.listManyByTo, toIDs)
	if err != nil {
		return nil, wrapDB(err, codeQueryFailed, "%q linkinin ters yönlü kayıtları okunamadı", name)
	}

	var toID, fromID string
	result := make(map[string][]string, len(toIDs))
	// ForEachRow satırları kapatır ve rows.Err()'i döner.
	if _, err := pgx.ForEachRow(rows, []any{&toID, &fromID}, func() error {
		result[toID] = append(result[toID], fromID)
		return nil
	}); err != nil {
		return nil, wrapDB(err, codeQueryFailed, "%q linkinin ters yönlü kayıtları okunamadı", name)
	}
	return result, nil
}

// Definition adı verilen linkin tanımını döner.
func (s *service) Definition(ctx context.Context, name string) (LinkDefinition, error) {
	// Bellekten okunsa bile iptal edilmiş bir bağlam sonuç üretmemelidir;
	// çağıranın bütçesi dolduysa akış burada da durur.
	if err := ctx.Err(); err != nil {
		return LinkDefinition{}, errors.Wrap(err, errors.KindUnavailable, codeCanceled,
			"%q linkinin tanımı okunmadan iptal edildi", name)
	}
	lt, err := s.linkFor(name)
	if err != nil {
		return LinkDefinition{}, err
	}
	return lt.def, nil
}

// linkFor adı verilen linkin çalışma zamanı bilgisini döner; tanımsızsa
// errors.NotFound üretir.
//
// Bu kapı iki işi birden görür: teşhis (hangi ad arandı, neler tanımlı) ve
// güvenlik — SQL'e giren tablo adı YALNIZCA doğrulanmış bir tanımdan gelebilir.
func (s *service) linkFor(name string) (*linkTable, error) {
	if lt, ok := s.defs.lookup(name); ok {
		return lt, nil
	}
	return nil, errors.NotFound(codeNotDefined,
		"%q adıyla tanımlı link yok; tanımlı linkler: %s", name, joinNames(s.defs.names())).
		WithDetails(map[string]any{keyLink: name})
}

// rawPool ham pgx havuzunu döner; havuz kurulmamışsa tipli hata üretir.
func (s *service) rawPool() (*pgxpool.Pool, error) {
	// db.Pool.Pool() nil alıcıya karşı güvenlidir; nil havuz nil döner.
	pool := s.pool.Pool()
	if pool == nil {
		return nil, errors.Unavailable(codeUnavailable,
			"link servisi için veritabanı havuzu kurulmamış")
	}
	return pool, nil
}

// writeError yazma yolundaki ham sürücü hatasını tipli hataya çevirir.
//
// Kardinalite ihlali burada okunur: PostgreSQL benzersizlik ihlalinde İHLAL
// EDİLEN indeksin adını bildirir, biz de hangi ucun dolu olduğunu söyleyen bir
// mesaj üretebiliriz. (from_id, to_id) birincil anahtarı ihlali buraya gelmez;
// onu INSERT'teki ON CONFLICT yutar ve no-op'a çevirir.
func (lt *linkTable) writeError(err error, fromID, toID string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		details := map[string]any{
			keyLink:       lt.def.Name,
			"kardinalite": lt.def.Cardinality.String(),
			keyFromID:     fromID,
			keyToID:       toID,
		}
		switch pgErr.ConstraintName {
		case lt.fromIndex:
			return errors.Conflict(codeCardinalityViolation,
				"%s linki %s kardinalitesindedir: %q kaydı zaten başka bir hedefe bağlı",
				lt.def.Name, lt.def.Cardinality, fromID).WithDetails(details)
		case lt.toIndex:
			return errors.Conflict(codeCardinalityViolation,
				"%s linki %s kardinalitesindedir: %q hedefi zaten başka bir kayda bağlı",
				lt.def.Name, lt.def.Cardinality, toID).WithDetails(details)
		default:
			return errors.Wrap(err, errors.KindConflict, codeCardinalityViolation,
				"%s linki oluşturulamadı: %s kısıtı ihlal edildi",
				lt.def.Name, pgErr.ConstraintName).WithDetails(details)
		}
	}
	return wrapDB(err, codeQueryFailed, "%q linki oluşturulamadı", lt.def.Name)
}

// conflictWithExisting süreç içi defterdeki tanımla çakışmayı bildirir.
func conflictWithExisting(existing, incoming LinkDefinition) error {
	return errors.Conflict(codeDefinitionConflict,
		"%q linki bu süreçte zaten farklı tanımlandı: kayıtlı %s, gelen %s",
		incoming.Name, existing, incoming).
		WithDetails(map[string]any{keyLink: incoming.Name, "kayitli": existing.String()})
}

// conflictWithStored kalıcı defterdeki tanımla çakışmayı bildirir.
//
// Bu yol, tanımın önceki bir SÜRÜMDE farklı bildirildiğini yakalar. Sessizce
// kabul edilseydi, örneğin kardinaliteyi daraltan bir değişiklik var olan
// fazla bağları görmeden yeni kısıtı uygulamaya çalışır ve açılış anlaşılmaz
// bir indeks hatasıyla düşerdi.
func conflictWithStored(stored storedDefinition, incoming LinkDefinition) error {
	return errors.Conflict(codeDefinitionConflict,
		"%q linki %s tablosunda farklı tanımlı: kayıtlı %s, gelen %s",
		incoming.Name, definitionsTable, stored, incoming).
		WithDetails(map[string]any{keyLink: incoming.Name, "kayitli": stored.String()})
}

// validateIDPair bir bağın iki ucunu birlikte doğrular.
func validateIDPair(fromID, toID string) error {
	if err := validateID(fromID, "fromID"); err != nil {
		return err
	}
	return validateID(toID, "toID")
}

// wrapDB ham sürücü hatasını tipli hataya çevirir.
//
// İptal edilmiş bir bağlam KindUnavailable ile ayrı raporlanır: çağıran
// "veritabanı bozuk" ile "benim bütçem doldu" arasında ayrım yapabilmelidir
// (internal/core/db ile aynı ayrım).
func wrapDB(err error, code, format string, a ...any) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.Wrap(err, errors.KindUnavailable, codeCanceled,
			format+" (bağlam iptal edildi)", a...)
	default:
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}
}

// joinNames ad listesini mesajda okunabilir biçimde yazar.
func joinNames(names []string) string {
	if len(names) == 0 {
		return "(tanım yok)"
	}
	return strings.Join(names, ", ")
}

// verifySchema DDL'in gerçekten istenen şemayı ürettiğini doğrular.
//
// "CREATE ... IF NOT EXISTS" ifadeleri, o adda BAŞKA TÜRDEN bir ilişki varsa
// hata değil NOTICE üretip atlar. PostgreSQL'de tablolar ve indeksler aynı ad
// uzayını paylaştığı için bu, kardinalite kısıtının sessizce hiç kurulmaması
// (ya da link tablosunun hiç oluşmaması) demektir. Ad doğrulaması bilinen
// çakışma desenlerini önler; bu kontrol ise kalan her sınıfı kapatır ve
// bozulmayı veri kirlenmeden ÖNCE yakalar.
//
// İşlem içinde çalışır: başarısız olursa defer'daki Rollback tanımı da geri alır.
func verifySchema(ctx context.Context, tx pgx.Tx, lt *linkTable) error {
	var relkind string
	err := tx.QueryRow(ctx, relkindSQL, lt.table).Scan(&relkind)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.Internal(codeDefineFailed,
			"%q linkinin tablosu (%s) oluşturulamadı; aynı adda bir ilişki DDL'i atlatmış olabilir",
			lt.def.Name, lt.table)
	}
	if err != nil {
		return wrapDB(err, codeDefineFailed, "%q linkinin tablosu doğrulanamadı", lt.def.Name)
	}
	if relkind != relkindTable {
		return errors.Internal(codeDefineFailed,
			"%s bir tablo değil (relkind=%q); %q link adı mevcut bir ilişkiyle çakışıyor",
			lt.table, relkind, lt.def.Name)
	}

	for _, index := range lt.requiredIndexes() {
		var kind string
		err := tx.QueryRow(ctx, relkindSQL, index).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind != relkindIndex) {
			return errors.Internal(codeDefineFailed,
				"%q linkinin %s kardinalitesini zorlayan %s indeksi kurulamadı; kısıt olmadan devam edilmez",
				lt.def.Name, lt.def.Cardinality, index)
		}
		if err != nil {
			return wrapDB(err, codeDefineFailed, "%q linkinin indeksi doğrulanamadı", lt.def.Name)
		}
	}
	return nil
}
