package arch_test

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file enforces ADR 0012: the repository's working language is English.
//
// # Why a ledger and not a clean sweep
//
// The repository was written in Turkish and the switch is INCREMENTAL: a file
// is translated when it is touched. A rule that simply forbade Turkish would
// fail on day one for every file, so it would have to be turned off — and a
// rule that is off enforces nothing.
//
// The ledger inverts that. Every file that still contains Turkish is listed by
// name. Files OUTSIDE the ledger must be English. The list can only shrink:
// removing a line requires the file to actually be translated, and adding a
// line requires touching this file in a review. A new file is born clean
// because nobody adds it to the ledger.
//
// # Why five lanes
//
// A detector that only looked for Turkish LETTERS would be satisfied by a
// single transliteration pass. This was measured, not guessed: running
// `sed y/çğıöşü/cgiosu/` across the whole tree drops the diacritic lane from
// 724 files to 0 while the repository stays exactly as Turkish as it was. The
// repository already writes Turkish this way — of 2852 test function names,
// zero carry a Turkish letter and hundreds are transliterated Turkish.
//
// So the letter scan is one lane of five, and the other four survive
// transliteration:
//
//   - [laneDiacritic] — Turkish-specific letters anywhere in the file.
//   - [laneWord] — Turkish function words that are not English words, in
//     COMMENTS and STRING LITERALS only.
//   - [laneIdentifier] — Turkish stems as whole parts of an identifier.
//   - [laneTextStem] — the same stems as whole words of a Go comment or string
//     literal (ADR 0408).
//   - [laneSuffix] — words ending the way Turkish inflects and no English word
//     ends, in Go comments and literals and in every other file (D279).
//
// A file is Turkish if ANY lane fires. The lanes report separately so the
// message says what to fix.

const (
	// laneDiacritic scans the raw bytes for letters that exist in Turkish and
	// not in English.
	laneDiacritic = "diacritic"

	// laneWord scans comments and string literals for Turkish function words.
	//
	// It deliberately does NOT scan identifiers. The reason is a real Go
	// idiom: `x, ok := ...` shortened to `xok`, and `y, ok := ...` to `yok`,
	// produces the Turkish word "yok" as a variable name. It occurs twice in
	// the Go standard library. Scanning identifiers with this word list would
	// flag correct English code.
	laneWord = "word"

	// laneIdentifier scans identifiers for Turkish stems, matching WHOLE
	// camelCase or snake_case parts.
	//
	// Whole-part matching is what makes the lane usable: substring matching
	// reads "module" as the Turkish "modul", "rollback" as "rol" and "reason"
	// as "son". Measured against the repository's one fully English package,
	// substring matching produced 14 false-positive files and whole-part
	// matching produced zero.
	laneIdentifier = "identifier"

	// laneTextStem scans Go comments and string literals for the stems
	// [laneIdentifier] reads in names, matching whole words (ADR 0408, D263).
	//
	// The word lane holds prose to a list short enough to collide with nothing,
	// so Turkish typed without its marks passed every lane wherever it used none
	// of those words: on 2026-10-06, a day after the ledger was declared empty,
	// the manual fulfillment provider returned "reference zorunludur" to API
	// callers and 25 Go files kept 183 stem words in their comments and test
	// data. The stems are content words this repository used, so they reach
	// what a function-word list cannot.
	//
	// It reads a literal as WRITTEN, an escape blanked, rather than its value:
	// an escaped literal is data the file chose to keep out of the letter lane
	// (see [diacriticDataExemptions]), and the case-folding tests spell Turkish
	// input that way on purpose. It reads Go alone. Markdown links name the
	// historical records by their Turkish file names and quote dated search
	// terms, and a lane that read them would live on exemptions; a name the
	// path ledger carries is subtracted here for the same reason, since that
	// debt is paid by a rename the path ledger already counts.
	laneTextStem = "text stem"

	// laneSuffix scans Go comments and string literals, as [laneTextStem] reads
	// them, and every line of every other file, for a word ending in one of
	// [turkishEndings], or a name carrying a Turkish case ending after an
	// apostrophe ([turkishApostropheEndings]) (D279).
	//
	// The word and stem lanes read a list of whole words, and a list of words
	// cannot follow a language that builds a word from a stem and a chain of
	// suffixes: a comment in core/http, "Status" followed by four Turkish words
	// typed without their marks, carried none of them and passed every lane
	// from 2026-09-04, when its file left the ledger, to 2026-10-11, beside eight
	// more half-translated lines in core/http and core/query and Turkish words
	// in the test data of a dozen modules. The suffix chain is the part Turkish
	// shares across its whole vocabulary, so this lane reads the grammar rather
	// than the vocabulary.
	laneSuffix = "suffix"
)

// turkishLedgerPath lists every hand-written source file that still contains
// Turkish. Paths are repo-relative, one per line.
const turkishLedgerPath = "testdata/turkish_ledger.txt"

// turkishPathLedgerPath lists every file whose own PATH still carries Turkish.
//
// It is separate from [turkishLedgerPath] because the two debts are paid at
// different moments: a file's contents can be translated in place, but
// renaming it is a move that touches imports, build tags and every reference
// to the name. Keeping them in one list would make a translated-but-unrenamed
// file impossible to express.
const turkishPathLedgerPath = "testdata/turkish_paths.txt"

// ledgerUpdateEnv rewrites the ledgers from the current tree.
//
// Shrinking a ledger by hand across hundreds of lines is where mistakes get
// made, so the maintenance path is written down instead of improvised. Setting
// it makes the test WRITE the ledgers and then FAIL: a run with the flag on can
// never be green, so CI cannot accidentally rewrite the debt it is supposed to
// be measuring.
const ledgerUpdateEnv = "GOBIT_UPDATE_TURKISH_LEDGER"

// detectorFile is the one file exempt from the content scan: this one.
//
// It carries the letter class, the word list and the stem list as data, so
// every lane fires on it by construction. Exempting it is unavoidable; leaving
// the exemption unnamed would not be. [TestDetectorExemptsOnlyItself] proves
// the hole is exactly one file wide and that it is still needed.
const detectorFile = "internal/arch/language_test.go"

// turkishLetters are the letters that exist in Turkish and not in English.
//
// The class deliberately EXCLUDES â î û å é ô. Those carry no Turkish signal
// and do appear in this repository as legitimate reference data — the ISO
// 3166 seed holds Åland, Barthélemy, Côte d'Ivoire and Réunion. Widening the
// class to all non-ASCII would turn correct reference data into a permanent
// violation.
const turkishLetters = "çğıöşüÇĞİÖŞÜ"

// safeTurkishWords are Turkish words that are not English words and not Go
// identifiers.
//
// The list is short ON PURPOSE. Each entry was measured against the 7711 Go
// files of the standard library, and only words with ZERO hits survived. The
// rejected candidates say more than the accepted ones: "ve" matched 300
// standard-library files, "bu" 14, and "var" is a Go keyword. A word list that
// produced false positives would be switched off within a week.
//
// Both spellings are listed. The diacritic spellings are already caught by
// [laneDiacritic], but a lane that depended on another lane for its coverage
// would silently lose it the day someone transliterates the file.
//
// The last five came with ADR 0408: suffixed forms the stem list cannot see,
// found in this tree on 2026-10-06 ("reference zorunludur" in an error message,
// "olmayan", "gizli" and "degistirildi" in test data). Each was measured the
// same way, as a whole word in the 7710 Go files of the go1.26.6 standard
// library, and each had zero hits.
//
// "desteklemiyor" and "filtresini" came after it (D263): the order and inventory
// query providers refused an unknown filter with "%q entity'si %q filtresini
// desteklemiyor", which the stem list did not reach. Both had zero hits in the
// 7711 Go files of the same library.
var safeTurkishWords = []string{
	"bir",
	"cunku", "çünkü",
	"degil", "değil",
	"gerek",
	"icin", "için",
	"olur",
	"veya",
	"yalnizca", "yalnızca",
	"yok",
	"degistirildi", "değiştirildi",
	"desteklemiyor",
	"filtresini",
	"gizli",
	"olmayan",
	"zorunlu",
	"zorunludur",
}

// turkishStems are Turkish word stems that appear as identifier parts.
//
// Turkish is agglutinative, so a stem list can never be complete: one stem
// carries dozens of suffixed forms and matching whole parts sees only the
// bare stem. The list is therefore a FLOOR, not a fence — it catches the
// vocabulary this repository actually uses. [TestDetectorIsNotBlind] pins that
// floor so the list cannot quietly empty out.
//
// English words are excluded even when Turkish shares them: "test", "panel",
// "kanal" as a spelling of "channel", "rol" inside "rollback". Whole-part
// matching handles most of these; the rest are simply absent from the list.
//
// The last block came with D279: Turkish words a sweep of the tree found in
// test data and comments, which [laneSuffix] cannot read because they carry no
// ending, each absent from the English word forms and the standard-library
// words [turkishEndings] was measured against. Words the tree keeps as the
// subject of a folding test ("kirmizi", "tisort", "mavi") are not on it.
var turkishStems = []string{
	"ac", "adet", "adres", "agac", "akis", "akislar", "alan", "altinda", "ara",
	"arac", "araci",
	"arasi", "arizada", "atif", "atiflar", "atla", "ayristir", "bagimli", "baglanti",
	"basla", "baslik", "bayat", "bekle", "belge", "belgeler", "bilesim",
	"birim", "bitir", "bolge", "bul", "butun", "buyuk", "cevap", "cikan",
	"cikis", "cizge", "cok", "coklu", "davranis", "dene", "denetim", "denetle", "depo", "devam",
	"deger", "disi", "disinda", "dize", "dizin", "donen", "donus", "dorduncu", "dosya", "dosyalar",
	"dugum", "dur", "eksik", "ekle", "erisimi", "esik", "fazla", "firlat", "fiyat",
	"gec", "gecerli", "gecersiz", "getir", "giren", "giris", "gonder",
	"govde", "guncelle", "guven", "guvenlik", "halka", "harita", "hata", "hatalar",
	"hatayolu",
	"havuz", "hazir", "hazirla", "ici", "iletisim", "indirim", "iptali", "iskelet", "istek",
	"istemci", "izin", "kanal", "kanit", "kapat", "kapsam", "kargo", "katalog",
	"kayit",
	"kayitlar", "kimligi", "kiracililik", "klasor", "kok", "korumali", "koruma", "kos", "kucuk", "kullanici",
	"kume", "kural", "kurallar", "kurulum", "liste", "listele", "metin",
	"mimari", "modul", "moduller", "muaf", "muafiyet", "musteri", "odeme", "oku",
	"olustur", "onceki", "ornek", "ornekler", "oturum", "ozet", "paneli", "para",
	"politika", "politikasi", "sabit",
	"sabitler", "sablon", "sablonlar", "satir", "sayac", "sayi", "secim",
	"secme", "semasi", "sepet", "sertlestirme", "sey", "sil", "sinir", "siniri", "siparis",
	"sonraki", "sorgu", "stok", "sunucu", "sunu", "surum", "sutun", "tablo",
	"tarayici",
	"tarih", "tek", "toplam", "toplu", "tuketici", "tutar", "urun", "uretim",
	"ustunde",
	"varyant", "veri", "veritabani", "vergi", "vitrin", "yakala", "yanit", "yapilandirma",
	"yardimci", "yaz", "yazmasi", "yeni", "yer", "yeter", "yetki", "yigin", "yol",
	"yollar", "yonetim", "zaman",
	// D279: the words a sweep of the tree found on 2026-10-11 that no ending
	// in turkishEndings reads.
	"aktif", "bagsiz", "bambaska", "baska", "bedava", "belki", "bitti", "butce",
	"deposu", "detay", "dijital", "elektronik", "eski", "filtre", "fincan",
	"garanti", "garantiler", "gecmis", "giyim", "gonderi", "guncelleme", "havale",
	"hesap", "kampanya", "kapali", "karakter", "kategori", "kilit", "kimlik",
	"kimlikli", "kimliksiz", "kopru", "magaza", "misafir", "muhasebe", "okuma",
	"onalti", "onbes", "onek", "panik", "parola", "pasif", "patlar", "patlat",
	"pencere", "rastgele", "renk", "sahte", "sanayi", "seviye", "tanimsiz",
	"taslak", "telafi", "terlik", "varyantlar", "yabanci", "yarim", "yuzde",
}

// turkishEndings are Turkish inflectional endings in their folded spelling,
// read by [laneSuffix] at the end of a word.
//
// Each is a suffix chain in the vowel-harmony forms it takes: the copula, the
// negative and the ability, the progressive, the aorist and the passive, the
// verbal nouns and participles, the reported past, the gerunds, a plural or a
// possessive followed by a case, the relative "-ki", the privative, "-lik", the
// agent noun, the past and the conditional. An ending is on the list only if no
// English word ends with it, and that is measured rather than judged: on
// 2026-10-11 none of the 328,631 word forms hunspell's en_US and en_GB
// dictionaries expand to, and none of the 74,235 words in the comments and
// literals of the go1.26.9 standard library, ended in one, counting only words
// at least [suffixStemFloor] letters longer than the ending. The 38 literals
// and comments of that library that carry a Turkish letter were left out as
// case-mapping data; the one ending found in them is a Turkish verb in the
// strings package's example. The forms that failed say what the measurement
// is for: "-yor" ends "mayor", "-nir" "souvenir", "-mis" "dermis", "-dir"
// "basedir", "-mez" "Gomez", and "-lmaz" the surname Yilmaz, which this
// repository keeps as data.
//
// It is not the suffix list ADR 0408 rejected. That one matched a stem followed
// by a suffix, so "para" read "parade"; this one needs no stem, and keeps an
// ending only where English has no word ending that way. Over the 71,288 lines
// of Turkish this repository wrote with its marks, transliterated, the word and
// stem lanes found 68.6% and this list alone 76.7%; together they find 92.3%.
var turkishEndings = []string{
	// The copula, -DIr.
	"idir", "odur", "dudur", "tudur", "ludur", "sudur", "ktir", "rdur", "ldur", "mdur",
	// The negative aorist, -mAz.
	"amaz", "emez", "lmez", "nmaz", "nmez", "rmaz",
	// The progressive, -Iyor.
	"iyor", "uyor", "yordu",
	// The aorist and the passive, with the ability -AbIlIr.
	"ilir", "ulur", "inir", "unur", "anir", "rir",
	// The verbal nouns and the necessity.
	"mek", "makta", "mekte", "maktan", "mekten", "meye", "mesi", "masini", "mesini",
	"masina", "mesine", "mayi", "meyi", "meli", "mamali", "meden",
	// The participles.
	"digi", "dugu", "tigi", "tugu", "digini", "dugunu", "tigini", "tugunu",
	"acak", "ecek", "acagi", "ecegi", "mayan", "eyen", "dilen",
	// The reported past, -mIş.
	"nmis", "tmis", "kmis", "smis", "zmis", "ilmis", "ulmus", "mistir", "mustur",
	"misti", "mamis", "memis",
	// The gerunds.
	"yarak", "yerek", "larak", "lerek", "rarak", "rerek", "narak", "nerek", "inca",
	"unca", "yken",
	// A plural followed by a case or the copula.
	"larin", "larda", "lerde", "lardan", "lerden", "larla", "lerle", "larini",
	"lerini", "larinda", "lerinde", "larinin", "lerinin", "lardir", "lerdir",
	// A possessive followed by a case.
	"sunu", "suna", "sune", "sinda", "sinde", "sunda", "sunde", "sindan", "suyla",
	"suyle", "unun", "inden", "undan",
	// The relative -ki and the instrumental.
	"daki", "deki", "teki", "iyla", "iyle",
	// The privative and -lIk.
	"suz", "sizlik", "suzluk", "sizdir", "rsiz", "tsiz", "isiz", "nsiz", "lsiz",
	"ligi", "ligini", "likli", "luklu",
	// The agent noun, -CI.
	"yici", "cisi", "cinin",
	// The past and the conditional.
	"irdi", "urdu", "ildi", "uldu", "ydi", "ydu", "irse", "ursa",
}

// suffixStemFloor is how many letters a word must carry in front of an ending
// before [laneSuffix] reads the ending.
//
// Two, because one reads the city of Bursa as the conditional "-ursa", and the
// customer module's fixtures ship to Bursa.
const suffixStemFloor = 2

// turkishApostropheEndings are the case and possessive endings Turkish writes
// after an apostrophe on a name, a number or a code span ("Abort'un", "2026'da"),
// in folded spelling.
//
// English writes "'s", "'t", "'d", "'m", "'re", "'ve" and "'ll" there, none of
// which is on the list, and a quotation opens after a space or a bracket rather
// than after a letter. The name must carry two characters, since French elides
// a one-letter word the same way ("d'un").
var turkishApostropheEndings = []string{
	"a", "e", "i", "u", "ya", "ye", "yi", "yu", "da", "de", "ta", "te", "dan", "den",
	"tan", "ten", "in", "un", "nin", "nun", "yla", "yle", "la", "le", "lar", "ler",
	"lari", "leri", "larin", "lerin", "daki", "deki", "taki", "teki", "nda", "nde",
	"ndan", "nden", "na", "ne", "ni", "nu", "si", "su", "sini", "sunu", "sine", "suna",
	"sinde", "sunda", "sinden", "sundan", "yken",
}

// apostropheEnding matches a name of at least two characters, an apostrophe and
// the letters after it.
var apostropheEnding = regexp.MustCompile("[\\p{L}\\p{N}`\\])\"]{2,}['\\x{2019}](\\p{L}+)")

// scanWitnesses are files the content scan must read, named because each is a
// kind of file it once did not (D227).
//
// The scan used to read seven extensions under a list of roots, and both lists
// were taken from the files that had been checked rather than from the files
// the repository has. On 2026-10-04 the ledger had been empty for a day while
// a Turkish letter stood on 590 lines of `.env.example`, 210 of the Makefile,
// 38 of `.gitignore` and 3 of `deploy/Dockerfile`: three have no extension on
// the list and deploy/ was not a root. The list's own comment called
// `.env.example` "833 lines of Turkish" and the file was still never read.
//
// The population is now every text file git lists, so no list can fall behind.
// What a list cannot prove is that the derivation did not quietly narrow, and
// a witness can: drop files without an extension, or a dot-directory, or a tree
// outside the Go ones, and the witness for it goes missing.
var scanWitnesses = []string{
	".env.example", ".gitignore", ".github/workflows/ci.yml", "Makefile",
	"deploy/Dockerfile", "deploy/docker-compose.yml", "go.mod",
}

// skippedDirs never hold hand-written source.
//
// `.claude` is the agent tooling's own directory and it earns its place the
// hard way: it holds git WORKTREES, which are full copies of this repository.
// A gate walking one sees every file twice — its own population doubled, or, as
// happened three times on 2026-09-08, a collector resolving nothing at all and
// reporting a tree it never read. The copies are transient and belong to no
// commit, so no gate should ever have been reading them.
var skippedDirs = []string{
	".git", ".claude", "node_modules", "vendor", "bin", ".idea", ".vscode",
}

// generatedMarker is the header every code generator in this repository
// writes. Generated files are out of scope: their language is decided by the
// .sql and .graphqls files they are generated FROM, and those ARE scanned.
const generatedMarker = "Code generated"

// diacriticDataExemptions lists text that carries a Turkish letter without
// being Turkish prose, and since ADR 0408 text that carries a Turkish word the
// word or text stem lane reads, and since D279 the suffix lane, for the same
// reason. The name stayed: ADR 0009, which takes no edit, names it.
//
// The key is a repo-relative path, the value the exact substrings to ignore.
// Line numbers are deliberately not used: they rot on the first edit, which is
// the same reason [TestTheDocsCarryNoLineNumberReference] forbids pointing at a
// line number from a document.
//
// # Who is in it
//
// Text that an English sentence has to carry because the letters or the words
// are its subject: the decision records and comments about case folding (ADR
// 0012 quotes the letter class it defines; ADR 0015, the search plugin and the
// compose file name the pair of words a C-locale cluster cannot fold),
// quotations of text that was Turkish when it was quoted (ADR 0009, the
// changelog), and reference names that reach the database as written (the
// region seed's ISO names). Each entry carries its own argument at the map
// literal below, where a reviewer weighing another will be standing.
//
// An escape is better than an exemption wherever the letters are data in Go
// source rather than prose: an exemption is a hole somebody has to keep honest,
// while an escaped literal is simply ASCII and no gate has to know about it.
// core/db/casefold.go, the product service's slug folding and the cart
// module's erasure and disclosure fixtures spell their letters as \u escapes
// for that reason.
var diacriticDataExemptions = map[string][]string{
	// The entries below came with ADR 0408 and carry no Turkish letter: each is
	// a Turkish word the word or text stem lane reads, kept because it is data
	// or a name quoted as written.
	//
	// The rig reproduces the catalog measured on 2026-09-03 character for
	// character, and its package says why: a rebuilt rig must diff clean against
	// the surviving one, and the selective search figure was measured against a
	// q that matched exactly one of these titles. Translating them would keep
	// every structural claim and quietly invalidate that one.
	"internal/rig/catalog.go": {
		`"Ana Depo"`, `'buyuk-' || n, 'Buyuk Urun ' || n`, `'tek'`,
		`'urun-' || n, 'Urun ' || n`,
	},
	"internal/rig/rig.go": {"buyuk-<n>", "urun-<n>"},
	// PayTR's API paths are the provider's contract, not this repository's
	// prose; a translated path would reach no endpoint. The ground's fake PayTR
	// answers at the same two paths, which is what makes it PayTR's stand-in
	// (ADR 0438).
	"plugins/paymentpaytr/provider.go":    {`"/odeme/api/get-token"`, `"/odeme/iade"`},
	"internal/e2e/paytr_checkout_test.go": {`"/odeme/api/get-token"`, `"/odeme/iade"`},
	// The incident this gate exists for quotes the Makefile's selector as it
	// was written, and the matcher's own probe replays it.
	"internal/arch/build_files_test.go": {"TestTemelYukAltindaDogruKalir"},
	// The smoke audit's incident quotes the log message whose translation broke
	// a smoke test, as it was written (D279).
	"internal/arch/messages_test.go": {`"izleme kuruldu"`},
	// The reference audit names a file and two tests as the records that take
	// no edit wrote them (ADR 0012, ADR 0047, and this file's own example), and
	// one comment gives three Turkish words as the example of what the link rule
	// mistakes for a link.
	"internal/arch/doc_references_test.go": {
		`"hatayolu_test.go"`, `"TestYerineKonanFiyatSatirdanSilinir"`, `"TestKayitBayatlamiyor"`,
		`"zorunlu", "sonuc" or "tanim"`,
		// D279: two more test names the same records quote, and the suffix a
		// path reference has to be trimmed of, given as its example.
		`"TestSilinenKabinFiyatlariDamgalanir"`, `"TestKrediDefteriGuncellenmez"`,
		`("openapi.go'da")`,
	},
	// A slug test's expected handle is the ASCII fold of its Turkish input,
	// which is spelled in escapes above it.
	"internal/modules/product/service/internal_test.go": {`"cok-guzel"`, `"bosluklu"`},

	// The changelog was translated on 2026-10-03, and what keeps a Turkish
	// letter is quotation. Two entries report test bindings that searched for a
	// message's Turkish text, and the dead text IS the report, as in ADR 0012's
	// example. The case-folding entry is about non-ASCII letters, so its words
	// have to carry one, for ADR 0015's reason above.
	"CHANGELOG.md": {
		"`\"b\" adımı`", "`ELLE MÜDAHALE`",
		"`\"çanta\"`", "`\"Çanta\"`", "q=çanta", "q=Çanta", "`'Ç' ILIKE 'ç'`",
		// D279: three entries about the language work quote, as they were
		// written, a message the gate once missed, a word a test searched for
		// and the message whose translation broke a smoke test.
		`olamaz: %d"`, `"kimlikli"`, "`\"izleme kuruldu\"`",
	},
	// The region module's seed holds ISO 3166-1 short names as data, and two
	// of them carry a letter this lane reads: Curaçao, and Türkiye, the name
	// ISO and the United Nations register for TR. Spelling either in ASCII
	// would seed a wrong country name into every installation; the comments
	// around them are English.
	"internal/modules/region/migrations/000002_region_seed.up.sql": {
		"('CW', 'Curaçao'),", "('TR', 'Türkiye'),",
	},
	// ADR 0012, ADR 0047 and two measurements quote names of tests and of an
	// operation that have since been renamed, as they were written (D279).
	"docs/adr/0012-repository-language-and-solid.md": {"`çğıöşüÇĞİÖŞÜ`", "`TestKayitBayatlamiyor`"},
	"docs/adr/0047-a-replaced-price-is-deleted.md": {
		"`TestSilinenKabinFiyatlariDamgalanir`", "`TestYerineKonanFiyatSatirdanSilinir`",
	},
	"docs/measurements/0164-a-name-that-was-already-taken.md":           {"`TestKrediDefteriGuncellenmez`"},
	"docs/measurements/0400-a-shopping-agent-is-a-storefront-client.md": {"`islemKimligi`"},
	// ADR 0015 records the same defect and has to quote the same two words to
	// show it: the whole finding is that one of them does not match the other.
	"docs/adr/0015-postgresql-cluster-contract.md": {
		"`çanta`", "`Çanta`", "q=çanta", "q=Çanta",
	},
	// The compose file's database comment tells the same story beside the
	// setting it is about, and was read for the first time with D227.
	"deploy/docker-compose.yml": {`"çanta"`, `"Çanta"`},
	// The search plugin's package documentation, translated on 2026-09-07. Its
	// two Turkish words are the SUBJECT of the paragraph they sit in, not prose:
	// the point is that a C-locale cluster does not fold non-ASCII case, so the
	// example words have to CARRY a letter outside ASCII. An all-ASCII pair
	// would fold on every cluster and the paragraph would document a problem
	// that does not exist — the same reasoning as ADR 0015's entry above.
	// core/db/casefold.go, where the behavior is actually probed, writes its
	// pair as escapes, and there the pair really does differ only in a letter
	// outside ASCII.
	//
	// The stemming example a few lines earlier ("kalem"/"kalemler") is Turkish
	// too and is NOT here: it carries no diacritic, so this lane never sees it.
	// That is worth writing down rather than leaving as luck — the detector's
	// blind spot for diacritic-free Turkish is known, and the words survive here
	// for the same reason as these two: they are the data the sentence is about.
	"plugins/searchpg/plugin.go": {
		// One line each; the subtraction is per line.
		`a search for "Gömlek" does`, `a product that says "gömlek". That the cluster`,
	},
	// ADR 0009 was translated on 2026-09-07 and every Turkish word left in it is
	// a QUOTATION of text in a file that is still Turkish — headings, sentences
	// and a task item, from the plan and the README. Translating a quotation
	// would make it stop being one: the whole point of the sentences around
	// these is that a reader can go and find the quoted text, and a paraphrase
	// sends them looking for text that is not there.
	"docs/adr/0009-cok-kiracililik-kurulum-siniri.md": {
		// Each entry has to fit on ONE line: the subtraction is per line, and a
		// quoted heading that markdown wrapped across two of them matches
		// neither half. That is how the first attempt at this list failed.
		"ilk sürümde yok", "Sonraki Sürüm", "şimdilik kapsam dışı",
		"İlk commit", "Çoklu-tenant bu listede değildir",
		"örnek mi, birden çok mu?", "Aynı ölçütün henüz uygulanmadığı yer",
	},
	// ~~The architecture document's one remaining Turkish is a LINK ANCHOR derived
	// from a README heading that is still Turkish. Changing it would not
	// translate anything; it would break the link.~~
	// **Removed 2026-09-07:** the README's API security section moved to
	// docs/security.md, which is English, so the link now points at an English
	// anchor and the document carries no Turkish at all. This is the shape an
	// exemption is supposed to end in — the debt was paid by moving the text,
	// not by widening the hole.
}

// turkishHit is one lane firing on one line.
type turkishHit struct {
	lane   string
	line   int
	detail string
}

func (h turkishHit) String() string {
	return fmt.Sprintf("%s lane, line %d: %s", h.lane, h.line, h.detail)
}

// turkishFolder maps the Turkish letters onto their ASCII counterparts.
var turkishFolder = strings.NewReplacer(
	"ç", "c", "Ç", "C",
	"ğ", "g", "Ğ", "G",
	"ı", "i", "I", "I",
	"İ", "I",
	"ö", "o", "Ö", "O",
	"ş", "s", "Ş", "S",
	"ü", "u", "Ü", "U",
)

// foldTurkish folds the Turkish letters out of a string and lowercases it.
//
// Folding runs BEFORE lowercasing on purpose. Go's strings.ToLower turns "İ"
// into "i" followed by a combining dot, which then matches no word in any
// list — the letter would fold into something invisible instead of into "i".
func foldTurkish(s string) string {
	return strings.ToLower(turkishFolder.Replace(s))
}

// splitIdentifier breaks an identifier into its camelCase and snake_case
// parts.
//
// Written by hand rather than with a regular expression: Go's regexp engine
// has no lookahead, and the acronym boundary in "JSONBody" cannot be expressed
// without one.
func splitIdentifier(s string) []string {
	var parts []string
	var cur []rune
	runes := []rune(s)

	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, string(cur))
			cur = nil
		}
	}

	for i, r := range runes {
		if r == '_' || r == '-' || r == '.' || r == '/' {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 {
			prevUpper := unicode.IsUpper(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			// A boundary is either lower-to-upper ("adminUI") or the end of an
			// acronym ("JSONBody" splits before "Body", not before "SON").
			if !prevUpper || nextLower {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()

	return parts
}

// letterWords returns the runs of letters in a string.
func letterWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) })
}

// wordHit reports the first safe Turkish word in the text, or "".
func wordHit(text string) string {
	for _, w := range letterWords(text) {
		folded := foldTurkish(w)
		if slices.Contains(safeTurkishWords, folded) {
			return w
		}
	}
	return ""
}

// stemHit reports the first Turkish stem appearing as a whole identifier part,
// or "".
func stemHit(ident string) string {
	for _, part := range splitIdentifier(ident) {
		if slices.Contains(turkishStems, foldTurkish(part)) {
			return part
		}
	}
	return ""
}

// textStemHit reports the first Turkish stem appearing as a whole word of a
// comment or a literal as written, or "".
//
// The names in pathNames are taken out first, and an escape is blanked rather
// than decoded (see [laneTextStem]). A word is split like an identifier too,
// so a camelCase key inside a string is read part by part. A part of two
// letters is passed over: in prose it is an abbreviation, and the one stem that
// short, "ac", read every "ACKed" and "ACKing" of the event bus's comments.
func textStemHit(text string, pathNames []string) string {
	for _, name := range pathNames {
		text = strings.ReplaceAll(text, name, " ")
	}
	text = goEscape.ReplaceAllString(text, " ")
	for _, w := range letterWords(text) {
		for _, part := range splitIdentifier(w) {
			if len(part) > 2 && slices.Contains(turkishStems, foldTurkish(part)) {
				return part
			}
		}
	}
	return ""
}

// suffixHit reports the first word of the text that ends in one of
// [turkishEndings], or the first Turkish ending after an apostrophe, or "".
//
// The names in pathNames are taken out first, as [textStemHit] takes them out,
// and a word is read part by part, so a Turkish word inside a test name or a
// camelCase key is read on its own. An escape needs no blanking here: it ends
// the run of letters before it, so it can neither hide an ending nor make one.
func suffixHit(text string, pathNames []string) string {
	for _, name := range pathNames {
		text = strings.ReplaceAll(text, name, " ")
	}
	for _, m := range apostropheEnding.FindAllStringSubmatch(text, -1) {
		if slices.Contains(turkishApostropheEndings, foldTurkish(m[1])) {
			return m[0]
		}
	}
	for _, w := range letterWords(text) {
		for _, part := range splitIdentifier(w) {
			folded := foldTurkish(part)
			for _, ending := range turkishEndings {
				if len(folded) >= len(ending)+suffixStemFloor && strings.HasSuffix(folded, ending) {
					return part
				}
			}
		}
	}
	return ""
}

// goEscape matches one escape sequence of a Go string or rune literal.
var goEscape = regexp.MustCompile(`\\(u[0-9a-fA-F]{4}|U[0-9a-fA-F]{8}|x[0-9a-fA-F]{2}|[0-7]{3}|.)`)

// withoutExemptions removes a file's exempt substrings from a piece of its
// text: an exemption says the text is data, which is a fact about the text
// rather than about one lane.
func withoutExemptions(text string, exempt []string) string {
	for _, e := range exempt {
		text = strings.ReplaceAll(text, e, "")
	}
	return text
}

// scanSource runs the five lanes over one file.
//
// The second result reports a generated file, which the caller drops: a
// generated file's language is not editable, so flagging it would create debt
// nobody can pay from the file itself.
//
// The exemption map is a PARAMETER rather than being read from the package
// variable, and that is not a style choice. The fixture in
// [TestDiacriticDataExemptionsAreHonest] has to scan with an exemption in place
// and with one absent; reaching that by mutating the package variable made two
// parallel tests write and read the same map, and `go test -race` reported the
// data race. A parameter removes the shared state instead of guarding it.
// pathNames is a parameter for the same reason: the base names of the files
// the path ledger lists, which [laneTextStem] reads as names
// ([pathLedgerNames]).
func scanSource(rel string, src []byte, exemptions map[string][]string, pathNames []string) (hits []turkishHit, generated bool) {
	text := string(src)
	if idx := strings.Index(text, generatedMarker); idx >= 0 && idx < 2000 {
		return nil, true
	}

	exempt := exemptions[rel]

	for i, line := range strings.Split(text, "\n") {
		stripped := line
		for _, e := range exempt {
			stripped = strings.ReplaceAll(stripped, e, "")
		}
		if idx := strings.IndexAny(stripped, turkishLetters); idx >= 0 {
			hits = append(hits, turkishHit{
				lane:   laneDiacritic,
				line:   i + 1,
				detail: strings.TrimSpace(line),
			})
			break
		}
	}

	if filepath.Ext(rel) != ".go" {
		// Outside Go there is no parser to separate comment from code, so the
		// word lane reads the raw text. Markdown, SQL and templates are prose
		// and queries; neither carries the `yok` idiom that made the
		// restriction necessary in Go.
		//
		// The exemptions are subtracted HERE TOO, and they did not use to be.
		// Corrected 2026-09-07, when ADR 0009 was translated: every Turkish word
		// left in it is a QUOTATION of text in a file that is still Turkish,
		// and one of those quotations ends in the word `yok`. The
		// diacritic lane accepted the quotation and the word lane rejected the
		// same characters, so the file could not be made to pass without
		// paraphrasing a quotation — which would have made it stop being one.
		//
		// The general form is the part worth keeping: an exemption says THIS
		// TEXT IS DATA, NOT PROSE, and that is a fact about the text rather than
		// about a lane. A subtraction that applies to one lane and not the other
		// was a bug in the mechanism, not a property of the rule.
		//
		// The suffix lane reads these lines too (D279), with the path ledger's
		// names out like the exemptions: a link to a historical record spells
		// its Turkish file name, and that debt is the path ledger's.
		return append(hits, rawLineHits(text, exempt, pathNames)...), false
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
	if err != nil {
		// A file that does not parse cannot be scanned precisely; fall back to
		// the raw text rather than reporting it clean.
		return append(hits, rawLineHits(text, exempt, pathNames)...), false
	}

	wordFound, identFound, textStemFound, suffixFound := false, false, false, false
	record := func(lane, detail string, pos token.Pos) {
		hits = append(hits, turkishHit{lane: lane, line: fset.Position(pos).Line, detail: detail})
	}

	for _, group := range file.Comments {
		for _, c := range group.List {
			text := withoutExemptions(c.Text, exempt)
			if w := wordHit(text); w != "" && !wordFound {
				record(laneWord, w, c.Pos())
				wordFound = true
			}
			if s := textStemHit(text, pathNames); s != "" && !textStemFound {
				record(laneTextStem, s, c.Pos())
				textStemFound = true
			}
			if s := suffixHit(text, pathNames); s != "" && !suffixFound {
				record(laneSuffix, s, c.Pos())
				suffixFound = true
			}
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if wordFound && identFound && textStemFound && suffixFound {
			return false
		}
		switch node := n.(type) {
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return true
			}
			written := withoutExemptions(node.Value, exempt)
			if s := textStemHit(written, pathNames); s != "" && !textStemFound {
				record(laneTextStem, s, node.Pos())
				textStemFound = true
			}
			if s := suffixHit(written, pathNames); s != "" && !suffixFound {
				record(laneSuffix, s, node.Pos())
				suffixFound = true
			}
			if wordFound {
				return true
			}
			value := written
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
			if w := wordHit(value); w != "" {
				record(laneWord, w, node.Pos())
				wordFound = true
			}
		case *ast.Ident:
			if identFound {
				return true
			}
			if s := stemHit(node.Name); s != "" {
				record(laneIdentifier, node.Name, node.Pos())
				identFound = true
			}
		}
		return true
	})

	return hits, false
}

// rawLineHits runs the word and suffix lanes over a file's lines, its exempt
// text taken out of each, and reports the first hit of each lane.
func rawLineHits(text string, exempt, pathNames []string) []turkishHit {
	var hits []turkishHit
	wordFound, suffixFound := false, false
	for i, line := range strings.Split(text, "\n") {
		stripped := withoutExemptions(line, exempt)
		if w := wordHit(stripped); w != "" && !wordFound {
			hits = append(hits, turkishHit{lane: laneWord, line: i + 1, detail: w})
			wordFound = true
		}
		if s := suffixHit(stripped, pathNames); s != "" && !suffixFound {
			hits = append(hits, turkishHit{lane: laneSuffix, line: i + 1, detail: s})
			suffixFound = true
		}
		if wordFound && suffixFound {
			break
		}
	}
	return hits
}

// repositoryFiles returns every path that is part of the repository, as a set
// of repo-relative slash paths: the files git TRACKS, plus the files it does
// not track and .gitignore does not exclude.
//
// # Why the scan asks git rather than the filesystem
//
// ADR 0012's ratchet is about the language of the REPOSITORY, and a working
// directory is not the repository: a build artifact, anything .gitignore keeps
// out — none of it ships, and none of it is debt anybody can pay by translating
// it.
//
// This was not a theoretical distinction. A planning document at the repository
// root, Turkish and gitignored, was carried in the ledger for exactly this
// reason: the scan found it, so the ledger had to list it, so the ledger named a
// file CI could not see. Every push was then red on
// [TestLedgerIsNotStale] — "go-commerce-framework-plan.md is no longer a scanned
// file" — while every developer's machine stayed green, because the file was
// sitting right there. It is the same shape as the compiled binary that occupied
// a directory name in plugins/webhookout's census: a gitignored artifact making
// a local green FAKE, which is the failure mode this repository has now met
// twice.
//
// # Why UNTRACKED is not the same as ignored, which cost a red CI run
//
// The first version of this asked only for tracked files, and that read
// "untracked" as "scratch". It is not. A file a developer has just WRITTEN is
// untracked for the minutes between typing it and committing it — and those are
// exactly the minutes in which the ratchet is supposed to speak, because after
// them the file is in the history and the debt is already taken on.
//
// Measured on 2026-09-08: internal/modules/customer/identity_integration_test.go
// was written, ran green through every local lane including this test, was
// committed, was pushed, and turned CI red on the first job that reached this
// package. Nothing about the file changed in between. The gate simply could not
// see it until it was too late to be useful, and so it reported the ONE state a
// gate must never report — clean, about a tree that was not.
//
// The fix is to separate the two questions the original conflated. "Is this file
// ignored?" is what decides whether it is debt, and git answers it directly with
// --exclude-standard. "Is this file committed yet?" decides nothing here.
//
// The ledger stays TRACKED-only on purpose, and the asymmetry is the point: a
// file may be SCANNED before it is committed, but it may not be LEDGERED before
// it is committed, because a ledger entry for a file CI cannot see is the exact
// stale-ledger failure the section above describes. So the only way to get a new
// Turkish file past this gate is to translate it — which is what a ratchet is
// for.
//
// A failure to run git is fatal rather than a fallback to "scan everything". A
// gate that quietly widens its own scope when a tool is missing is a gate whose
// result nobody can read.
func repositoryFiles(t *testing.T) map[string]bool {
	t.Helper()

	files := map[string]bool{}
	for _, args := range [][]string{
		{"ls-files", "-z"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
	} {
		out, err := exec.Command("git", append([]string{"-C", repoRoot}, args...)...).Output()
		require.NoError(t, err,
			"git %s could not be run, so the scan cannot tell a file of the repository "+
				"from an ignored artifact; refusing to guess", strings.Join(args, " "))

		for _, path := range strings.Split(string(out), "\x00") {
			if path != "" {
				files[path] = true
			}
		}
	}

	require.NotEmpty(t, files, "git ls-files reported no files at all; the scan has gone BLIND")

	return files
}

// scannedFiles returns the repo-relative paths the content scan covers, sorted:
// every file of the repository ([repositoryFiles]) that is text, outside
// [skippedDirs].
//
// Text is decided as git decides it, by a NUL byte in the first 8000, so an
// image is the only kind of file left out and no list of kinds is kept (see
// [scanWitnesses] for what a list cost). A file git still lists but the
// working tree has deleted is not there to read, and not debt.
func scannedFiles(t *testing.T) []string {
	t.Helper()

	var found []string
	for rel := range repositoryFiles(t) {
		if slices.ContainsFunc(strings.Split(rel, "/"), func(segment string) bool {
			return slices.Contains(skippedDirs, segment)
		}) {
			continue
		}

		src, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		require.NoError(t, err, "%s could not be read", rel)
		if !isText(src) {
			continue
		}
		found = append(found, rel)
	}

	slices.Sort(found)
	return found
}

// isText reports whether src is text as git judges it: no NUL byte in its
// first 8000.
func isText(src []byte) bool {
	return !bytes.Contains(src[:min(len(src), 8000)], []byte{0})
}

// loadLedger reads a ledger file into a path set.
func loadLedger(t *testing.T, path string) map[string]bool {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err, "%s must exist; it is the record of the language debt", path)

	entries := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		require.False(t, entries[line], "%s lists %q twice", path, line)
		entries[line] = true
	}
	return entries
}

// pathLedgerNames returns the base names of the files [turkishPathLedgerPath]
// lists: names a comment or a literal mentions as names, whose Turkish is paid
// by a rename rather than a translation (ADR 0408).
func pathLedgerNames(t *testing.T) []string {
	t.Helper()

	var names []string
	for _, rel := range slices.Sorted(maps.Keys(loadLedger(t, turkishPathLedgerPath))) {
		names = append(names, filepath.Base(rel))
	}
	return names
}

// writeLedger rewrites a ledger file; see [ledgerUpdateEnv].
func writeLedger(t *testing.T, path, header string, entries []string) {
	t.Helper()

	var b strings.Builder
	b.WriteString(header)
	for _, e := range entries {
		b.WriteString(e)
		b.WriteString("\n")
	}
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
}

const turkishLedgerHeader = `# Files that still contain Turkish.
#
# Every path listed here is hand-written source that has not been translated
# yet. Files NOT listed here must be English — see internal/arch/language_test.go
# and docs/adr/0012-repository-language-and-solid.md.
#
# This list may only SHRINK. Removing a line requires the file to be
# translated; the test refuses a line whose file is already clean.
#
`

const turkishPathLedgerHeader = `# Files whose PATH still contains Turkish.
#
# Renaming a file is a separate move from translating it, so this debt is
# tracked separately from testdata/turkish_ledger.txt. New files must be named
# in English; this list may only shrink.
#
`

// TestNoTurkishOutsideLedger proves that any file not named in the ledger is
// English.
//
// This is the rule ADR 0012 exists for. It is worth stating what the test
// canNOT see, because the gap is where the rule will actually be broken:
// English words arranged as Turkish sentences ("the record is not found
// returns") carry no signal at all, and a stem list can never cover an
// agglutinative language's tail. The test is a floor under the rule, not the
// rule itself.
func TestNoTurkishOutsideLedger(t *testing.T) {
	t.Parallel()

	files := scannedFiles(t)
	require.NotEmpty(t, files, "the scan found no files at all")

	ledger := loadLedger(t, turkishLedgerPath)
	names := pathLedgerNames(t)
	update := os.Getenv(ledgerUpdateEnv) != ""

	var dirty []string
	var violations []string

	for _, rel := range files {
		if rel == detectorFile {
			continue
		}
		src, err := os.ReadFile(filepath.Join(repoRoot, rel))
		require.NoError(t, err)

		hits, generated := scanSource(rel, src, diacriticDataExemptions, names)
		if generated {
			continue
		}
		if len(hits) == 0 {
			continue
		}
		dirty = append(dirty, rel)
		if ledger[rel] {
			continue
		}
		violations = append(violations, fmt.Sprintf("  %s\n      %s", rel, hits[0]))
	}

	if update {
		writeLedger(t, turkishLedgerPath, turkishLedgerHeader, dirty)
		t.Fatalf("%s was set: %s rewritten with %d entries. Re-run without the flag.",
			ledgerUpdateEnv, turkishLedgerPath, len(dirty))
	}

	if len(violations) > 0 {
		t.Errorf("%d file(s) outside %s contain Turkish:\n%s\n\n"+
			"The repository's working language is English (ADR 0012). Translate the "+
			"file, or — if it is a file the migration has not reached yet — add it to "+
			"the ledger in the same change, so the debt stays counted.",
			len(violations), turkishLedgerPath, strings.Join(violations, "\n"))
	}
}

// TestLedgerIsNotStale catches the ledger rotting.
//
// A ledger line rots in two directions and both are silent. The file may be
// GONE — deleted or renamed — and the line then waits to quietly excuse a
// future file that happens to take the same path. Or the file may already be
// CLEAN, and the line then hides the fact that the debt was paid, so the
// remaining count overstates the work left and nobody notices the finish line.
//
// The shape is the one [checkStaleExemptions] already uses for the wiring
// exemptions: an exemption is a debt, and a debt that has been paid must have
// its record removed.
func TestLedgerIsNotStale(t *testing.T) {
	t.Parallel()

	ledger := loadLedger(t, turkishLedgerPath)
	names := pathLedgerNames(t)
	scanned := map[string]bool{}
	for _, rel := range scannedFiles(t) {
		scanned[rel] = true
	}

	for _, rel := range slices.Sorted(maps.Keys(ledger)) {
		if !scanned[rel] {
			t.Errorf("ledger entry STALE: %q is no longer a scanned file.\n"+
				"The file was deleted or renamed; the ledger line must go with it, or it "+
				"will one day excuse a new file written at the same path.", rel)
			continue
		}

		src, err := os.ReadFile(filepath.Join(repoRoot, rel))
		require.NoError(t, err)

		hits, generated := scanSource(rel, src, diacriticDataExemptions, names)
		if generated {
			t.Errorf("ledger entry STALE: %q is generated code and is never scanned.\n"+
				"Its language comes from the file it is generated from; list that one "+
				"instead.", rel)
			continue
		}
		if len(hits) == 0 {
			t.Errorf("ledger entry STALE: %q no longer contains Turkish.\n"+
				"The debt was paid — delete the line. Leaving it makes the remaining "+
				"count wrong and lets the file quietly turn Turkish again.", rel)
		}
	}
}

// TestDetectorIsNotBlind pins the floor under every lane.
//
// A detector loses its teeth silently. Empty the word list, narrow the
// population by a kind of file or a tree, or tighten a pattern by one
// character, and the suite stays green while the rule stops being enforced —
// the ledger would then read as "almost done" precisely when the scan had
// stopped working.
//
// So each lane keeps its own counter, every tree git lists must contribute a
// file, and the kinds of file the scan once skipped are named and must be read:
// a tree or a kind that contributes nothing was not scanned, and every file of
// it would be silently excused.
func TestDetectorIsNotBlind(t *testing.T) {
	t.Parallel()

	files := scannedFiles(t)
	names := pathLedgerNames(t)

	perLane := map[string]int{}
	perRoot := map[string]int{}
	generated := 0

	for _, rel := range files {
		if rel == detectorFile {
			continue
		}
		root := "."
		if idx := strings.Index(rel, "/"); idx >= 0 {
			root = rel[:idx]
		}
		perRoot[root]++

		src, err := os.ReadFile(filepath.Join(repoRoot, rel))
		require.NoError(t, err)

		hits, gen := scanSource(rel, src, diacriticDataExemptions, names)
		if gen {
			generated++
			continue
		}
		for _, h := range hits {
			perLane[h.lane]++
		}
	}

	// A lane with nothing left on the ledger to read has nothing to find, and
	// its zero is then the migration being complete rather than the lane being
	// blind: the identifier and text stem lanes read Go source alone, the other
	// three every scanned file. A finished lane's teeth are proven by the planted
	// control ([TestDetectorFindsPlantedTurkish]), and a hit would be a file
	// outside the ledger, which [TestNoTurkishOutsideLedger] reports. The ledger has
	// been empty since 2026-10-03, and the migration was taken to be complete on
	// 2026-10-04, when the scan first read every file the ledger had been silent
	// about (D227); its last lines, which no lane could read, were found and
	// translated on 2026-10-11 (D279).
	ledger := loadLedger(t, turkishLedgerPath)
	goLeft := false
	for rel := range ledger {
		if strings.HasSuffix(rel, ".go") {
			goLeft = true

			break
		}
	}
	for _, lane := range []string{laneDiacritic, laneWord, laneIdentifier, laneTextStem, laneSuffix} {
		finished := len(ledger) == 0 || ((lane == laneIdentifier || lane == laneTextStem) && !goLeft)
		if finished {
			assert.Zero(t, perLane[lane],
				"the %s lane has nothing left on %s to read, so nothing it reads may "+
					"carry Turkish", lane, turkishLedgerPath)

			continue
		}
		assert.Positive(t, perLane[lane],
			"the %s lane found nothing in the whole repository while %s still lists "+
				"files it reads; the lane is broken.", lane, turkishLedgerPath)
	}

	// The trees are counted against git's list rather than against anything
	// the scan was told, since a check that iterates the list it checks loses
	// its assertion with the entry: when the trees were a list, deleting
	// "plugins" from it left the suite green.
	listed := map[string]bool{}
	for rel := range repositoryFiles(t) {
		root := "."
		if idx := strings.Index(rel, "/"); idx >= 0 {
			root = rel[:idx]
		}
		if !slices.Contains(skippedDirs, root) {
			listed[root] = true
		}
	}
	for _, root := range slices.Sorted(maps.Keys(listed)) {
		assert.Positive(t, perRoot[root],
			"git lists files under %q and the scan read none of them, so everything "+
				"under it is being excused.", root)
	}

	for _, witness := range scanWitnesses {
		assert.Contains(t, files, witness,
			"%s is not read by the content scan; a kind of file the repository has "+
				"has fallen out of the population (D227).", witness)
	}

	assert.Positive(t, generated,
		"no generated file was recognized. The generator header changed, so generated "+
			"code is now being scanned as if it were hand-written.")
}

// TestDetectorFindsPlantedTurkish is the positive control.
//
// [TestDetectorIsNotBlind] proves the population is whole and, while the ledger
// held files, that the lanes still fired on them; neither proves a lane can
// catch anything NEW, and with the ledger empty a lane has nothing to fire on
// at all. This test plants a known sample in
// each lane and requires each to be caught, including the transliterated
// spelling that the letter lane cannot see.
func TestDetectorFindsPlantedTurkish(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		file string
		src  string
		lane string
	}{
		{
			name: "Turkish letter in a comment",
			src:  "package p\n\n// bu satır Türkçedir.\nfunc F() {}\n",
			lane: laneDiacritic,
		},
		{
			name: "transliterated Turkish in a comment",
			src:  "package p\n\n// bu kural icin gerekli degil.\nfunc F() {}\n",
			lane: laneWord,
		},
		{
			name: "transliterated Turkish in a string",
			src:  "package p\n\nfunc F() string { return \"kayit bulunamadi, bir daha deneyin\" }\n",
			lane: laneWord,
		},
		{
			name: "Turkish identifier",
			src:  "package p\n\nfunc yeniSablon() {}\n",
			lane: laneIdentifier,
		},
		{
			name: "suffixed Turkish in an error message",
			src:  "package p\n\nfunc F() string { return \"reference zorunludur\" }\n",
			lane: laneWord,
		},
		{
			name: "transliterated stem in a comment",
			src:  "package p\n\n// The siparis is read first.\nfunc F() {}\n",
			lane: laneTextStem,
		},
		{
			name: "transliterated stem in a string",
			src:  "package p\n\nconst code = \"KARGO20\"\n",
			lane: laneTextStem,
		},
		{
			name: "transliterated stem after an escape",
			src:  "package p\n\nconst body = \"line one\\nfiyat\"\n",
			lane: laneTextStem,
		},
		{
			name: "transliterated stem in a camelCase key",
			src:  "package p\n\nconst key = `{\"musteriName\":\"Ada\"}`\n",
			lane: laneTextStem,
		},
		{
			name: "a word the D279 sweep added to the stem floor",
			src:  "package p\n\nconst code = \"BEDAVA\"\n",
			lane: laneTextStem,
		},
		// D279: the comment that passed every other lane, as it stood.
		{
			name: "Turkish endings in a field comment",
			src:  "package p\n\ntype R struct {\n\t// Status kaydedilen HTTP durum kodudur.\n\tStatus int\n}\n",
			lane: laneSuffix,
		},
		{
			name: "a Turkish ending in a string literal",
			src:  "package p\n\nconst msg = \"httptest Hijack desteklemez\"\n",
			lane: laneSuffix,
		},
		{
			name: "a Turkish ending in an upper-case code",
			src:  "package p\n\nconst code = \"TUKENMIS\"\n",
			lane: laneSuffix,
		},
		{
			name: "a Turkish ending inside a camelCase key",
			src:  "package p\n\nconst key = `{\"kaydedilenStatus\":200}`\n",
			lane: laneSuffix,
		},
		{
			name: "a Turkish case ending after an apostrophe",
			src:  "package p\n\n// See Abort'un contract.\nfunc F() {}\n",
			lane: laneSuffix,
		},
		{
			name: "a Turkish ending in Markdown",
			file: "planted.md",
			src:  "Folding it into one bucket is better than patlatmaktan.\n",
			lane: laneSuffix,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			file := cmp.Or(tc.file, "planted.go")
			hits, generated := scanSource(file, []byte(tc.src), diacriticDataExemptions, nil)
			require.False(t, generated)

			lanes := make([]string, 0, len(hits))
			for _, h := range hits {
				lanes = append(lanes, h.lane)
			}
			assert.Contains(t, lanes, tc.lane,
				"the %s lane missed a planted sample; got %v", tc.lane, lanes)
		})
	}
}

// TestDetectorPassesEnglishSource is the negative control.
//
// A detector that flags correct English is worse than none: the first false
// positive is answered by widening an exemption, and the rule dies by
// exemption rather than by decision. The samples are the ones that actually
// broke earlier versions of this scan — "module" read as "modul", "rollback"
// as "rol", "reason" as "son", and the `x, ok` idiom that spells "yok" — and
// the ones [laneTextStem] must pass: English words that begin like a stem
// ("parade", "depot", "paragraph"), an escaped case-folding input, and a file
// the path ledger lists, named in a comment. [laneSuffix] must pass the words
// that end the way the forms left off [turkishEndings] do, the city whose name
// [suffixStemFloor] is set by, and the apostrophes English and French write.
func TestDetectorPassesEnglishSource(t *testing.T) {
	t.Parallel()

	const src = `package p

// The module resolves a reason from the JSON body and rolls back on failure.
// A parade passes the depot, and the paragraph names the alarm. A message not
// ACKed is delivered again.
func Rollback(modules []string) error {
	yok := len(modules) == 0
	if yok {
		return errors.New("the parade reached the depot: module rollback")
	}
	return nil
}

// The case-folding input is spelled as escapes, so no lane reads it as words.
const folded = "\u00c7OK-\u0130Y\u0130"

// The architecture document is docs/mimari.md, named by its file name.

// The mayor's surveyor keeps a souvenir in the basedir and the datadir, and the
// dermis is not the epidermis. Ms Gomez and Mr Yilmaz ship to Bursa, don't they?
// It's O'Brien's flag, set d'un coup.
const shipped = "a parcel for Bursa"
`

	hits, generated := scanSource("english.go", []byte(src), diacriticDataExemptions, pathLedgerNames(t))
	require.False(t, generated)
	assert.Empty(t, hits, "correct English source must not be flagged: %v", hits)
}

// TestDetectorExemptsOnlyItself proves the one hole in the content scan is
// exactly one file wide and still needed.
func TestDetectorExemptsOnlyItself(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile(filepath.Join(repoRoot, detectorFile))
	require.NoError(t, err, "%s must exist", detectorFile)

	hits, generated := scanSource(detectorFile, src, diacriticDataExemptions, nil)
	require.False(t, generated)
	assert.NotEmpty(t, hits,
		"%s no longer trips its own lanes, so the exemption has no reason to exist "+
			"and must be deleted.", detectorFile)

	ledger := loadLedger(t, turkishLedgerPath)
	assert.False(t, ledger[detectorFile],
		"%s is exempt in code; listing it in the ledger too would count the same hole "+
			"twice and make the remaining debt look larger than it is.", detectorFile)
}

// TestDiacriticDataExemptionsAreHonest governs the escape hatch for text that
// carries a Turkish letter without being Turkish.
//
// Three ways for an entry to rot are checked: the file is gone, the file is
// already in the ledger (so the exemption is doing nothing and hides a second
// hole), and the substring no longer occurs (so the data it described has
// changed — which for the ISO 3166 seed also means the reference data itself
// was damaged).
//
// The fixture runs the same rules the real map runs. An empty map would
// otherwise leave the mechanism unproven until the day it is first used, which
// is the worst possible day to discover it does not work.
func TestDiacriticDataExemptionsAreHonest(t *testing.T) {
	t.Parallel()

	ledger := loadLedger(t, turkishLedgerPath)

	for _, rel := range slices.Sorted(maps.Keys(diacriticDataExemptions)) {
		src, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if !assert.NoError(t, err, "exemption STALE: %q no longer exists", rel) {
			continue
		}
		assert.False(t, ledger[rel],
			"exemption STALE: %q is already in the ledger, so the exemption excuses "+
				"nothing and quietly widens the hole when the ledger line is removed.", rel)

		for _, sub := range diacriticDataExemptions[rel] {
			assert.Contains(t, string(src), sub,
				"exemption STALE: %q no longer contains %q. Either the exemption line "+
					"is obsolete, or the data it was protecting has been altered.", rel, sub)
		}
	}

	// The mechanism itself, proven on a fixture rather than on a future entry.
	// The fixture brings its OWN map: the package variable is never written to,
	// so this test cannot race the ones scanning the repository in parallel.
	const fixture = "-- ISO 3166 reference data.\n('CW', 'Curaçao'),\n"

	hits, _ := scanSource("fixture.sql", []byte(fixture), nil, nil)
	require.NotEmpty(t, hits, "without an exemption the fixture must be flagged")

	hits, _ = scanSource("fixture.sql", []byte(fixture),
		map[string][]string{"fixture.sql": {"Curaçao"}}, nil)
	assert.Empty(t, hits, "with the exemption in place the fixture must pass: %v", hits)

	// Outside Go the suffix lane takes the exemption too (D279): a record
	// quoting a test's Turkish name quotes it as written.
	const quoting = "The test was `TestKayitBayatlamiyor` until it was renamed.\n"
	hits, _ = scanSource("quoting.md", []byte(quoting), nil, nil)
	require.NotEmpty(t, hits, "without an exemption the quotation must be flagged")
	hits, _ = scanSource("quoting.md", []byte(quoting),
		map[string][]string{"quoting.md": {"`TestKayitBayatlamiyor`"}}, nil)
	assert.Empty(t, hits, "with the exemption in place the quotation must pass: %v", hits)

	// In Go the comment and literal lanes take the exemption too (ADR 0408):
	// an external API's path is a name its provider chose, and so is a word
	// quoted as the subject of a sentence.
	const goFixture = "package p\n\n// Its \"yok\" is the subject here.\n" +
		"const path = \"/odeme/api/get-token\"\n" +
		"// The incident quoted \"izleme kuruldu\" as it was written.\n"
	hits, _ = scanSource("fixture.go", []byte(goFixture), nil, nil)
	lanes := make([]string, 0, len(hits))
	for _, h := range hits {
		lanes = append(lanes, h.lane)
	}
	require.ElementsMatch(t, []string{laneWord, laneTextStem, laneSuffix}, lanes,
		"without an exemption the Go fixture must be flagged in all three lanes")

	hits, _ = scanSource("fixture.go", []byte(goFixture),
		map[string][]string{"fixture.go": {`"yok"`, "/odeme/api/get-token", `"izleme kuruldu"`}}, nil)
	assert.Empty(t, hits, "with the exemption in place the Go fixture must pass: %v", hits)
}

// TestThePathLedgerNamesAreNamesToTheTextStemLane proves a comment may name a
// file the path ledger lists without the stems in the name counting as prose,
// and that only the NAME is excused: the same stem as a word still counts.
func TestThePathLedgerNamesAreNamesToTheTextStemLane(t *testing.T) {
	t.Parallel()

	names := pathLedgerNames(t)
	require.Contains(t, names, "mimari.md", "the fixture leans on a file the path ledger lists")

	named := "package p\n\n// docs/mimari.md prices the sagas.\nfunc F() {}\n"
	hits, _ := scanSource("named.go", []byte(named), nil, names)
	assert.Empty(t, hits, "a ledgered file's name is not prose: %v", hits)

	hits, _ = scanSource("named.go", []byte(named), nil, nil)
	assert.NotEmpty(t, hits, "without the ledger's names the fixture must be flagged")

	prose := "package p\n\n// The mimari of mimari.md is read first.\nfunc F() {}\n"
	hits, _ = scanSource("prose.go", []byte(prose), nil, names)
	require.Len(t, hits, 1, "the stem outside the name still counts")
	assert.Equal(t, laneTextStem, hits[0].lane)
}

// TestThePathLedgerNamesAreNamesToTheSuffixLane proves the same of
// [laneSuffix], in the Markdown it reads: a link to a historical record spells
// the record's Turkish file name, and the name is the path ledger's debt.
func TestThePathLedgerNamesAreNamesToTheSuffixLane(t *testing.T) {
	t.Parallel()

	names := pathLedgerNames(t)
	require.Contains(t, names, "0008-musteri-kimligi-guven-siniri.md",
		"the fixture leans on a record the path ledger lists")

	linked := "See [ADR 0008](0008-musteri-kimligi-guven-siniri.md).\n"
	hits, _ := scanSource("linked.md", []byte(linked), nil, names)
	assert.Empty(t, hits, "a ledgered record's file name is not prose: %v", hits)

	hits, _ = scanSource("linked.md", []byte(linked), nil, nil)
	require.Len(t, hits, 1, "without the ledger's names the link must be flagged")
	assert.Equal(t, laneSuffix, hits[0].lane)
}

// TestRepoPathsAreEnglishOutsideLedger covers the one layer the content scan
// structurally cannot see: the names of the files and directories themselves.
//
// A file can be fully translated inside and still be called hatayolu_test.go.
// Nothing in the content scan reads a path, so without this test a tree could
// reach "zero Turkish" while every second filename stayed Turkish.
func TestRepoPathsAreEnglishOutsideLedger(t *testing.T) {
	t.Parallel()

	ledger := loadLedger(t, turkishPathLedgerPath)
	update := os.Getenv(ledgerUpdateEnv) != ""

	var dirty []string
	var violations []string

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && slices.Contains(skippedDirs, d.Name()) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		hit := ""
		for _, segment := range strings.Split(rel, "/") {
			if s := stemHit(strings.TrimSuffix(segment, filepath.Ext(segment))); s != "" {
				hit = s
				break
			}
		}
		if hit == "" {
			return nil
		}
		dirty = append(dirty, rel)
		if !ledger[rel] {
			violations = append(violations, fmt.Sprintf("  %s (%q)", rel, hit))
		}
		return nil
	})
	require.NoError(t, err)

	slices.Sort(dirty)

	if update {
		writeLedger(t, turkishPathLedgerPath, turkishPathLedgerHeader, dirty)
		t.Fatalf("%s was set: %s rewritten with %d entries. Re-run without the flag.",
			ledgerUpdateEnv, turkishPathLedgerPath, len(dirty))
	}

	if len(violations) > 0 {
		t.Errorf("%d path(s) outside %s carry Turkish names:\n%s\n\n"+
			"New files are named in English (ADR 0012). Renaming an existing file is a "+
			"separate move from translating it; until it happens the path belongs in "+
			"the ledger.", len(violations), turkishPathLedgerPath, strings.Join(violations, "\n"))
	}
}

// TestPathLedgerIsNotStale is [TestLedgerIsNotStale] for the path ledger.
func TestPathLedgerIsNotStale(t *testing.T) {
	t.Parallel()

	ledger := loadLedger(t, turkishPathLedgerPath)

	for _, rel := range slices.Sorted(maps.Keys(ledger)) {
		info, err := os.Stat(filepath.Join(repoRoot, rel))
		if err != nil || info.IsDir() {
			t.Errorf("path ledger entry STALE: %q no longer exists.\n"+
				"The file was renamed or deleted — which is exactly the debt this line "+
				"recorded, so the line must go with it.", rel)
			continue
		}

		clean := true
		for _, segment := range strings.Split(rel, "/") {
			if stemHit(strings.TrimSuffix(segment, filepath.Ext(segment))) != "" {
				clean = false
				break
			}
		}
		assert.False(t, clean,
			"path ledger entry STALE: %q no longer carries a Turkish name.\n"+
				"Delete the line; leaving it lets the path turn Turkish again unnoticed.", rel)
	}
}
