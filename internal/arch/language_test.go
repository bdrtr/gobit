package arch_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
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
// # Why three lanes
//
// A detector that only looked for Turkish LETTERS would be satisfied by a
// single transliteration pass. This was measured, not guessed: running
// `sed y/çğıöşü/cgiosu/` across the whole tree drops the diacritic lane from
// 724 files to 0 while the repository stays exactly as Turkish as it was. The
// repository already writes Turkish this way — of 2852 test function names,
// zero carry a Turkish letter and hundreds are transliterated Turkish.
//
// So the letter scan is one lane of three, and the other two survive
// transliteration:
//
//   - [laneDiacritic] — Turkish-specific letters anywhere in the file.
//   - [laneWord] — Turkish function words that are not English words, in
//     COMMENTS and STRING LITERALS only.
//   - [laneIdentifier] — Turkish stems as whole parts of an identifier.
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
	// the Go standard library and once in this repository
	// (customer/service/provider_test.go). Scanning identifiers with this word
	// list would flag correct English code.
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
var turkishStems = []string{
	"ac", "adet", "adres", "agac", "akis", "akislar", "alan", "altinda", "ara",
	"arac", "araci",
	"arasi", "atif", "atiflar", "atla", "ayristir", "bagimli", "baglanti",
	"basla", "baslik", "bayat", "bekle", "belge", "belgeler", "bilesim",
	"birim", "bitir", "bolge", "bul", "butun", "buyuk", "cevap", "cikan",
	"cikis", "cizge", "coklu", "dene", "denetim", "denetle", "depo", "devam",
	"deger", "disi", "dize", "dizin", "donen", "donus", "dosya", "dosyalar",
	"dugum", "dur", "eksik", "ekle", "esik", "fazla", "firlat", "fiyat",
	"gec", "gecerli", "gecersiz", "getir", "giren", "giris", "gonder",
	"govde", "guncelle", "guvenlik", "halka", "harita", "hata", "hatalar",
	"hatayolu",
	"havuz", "hazir", "hazirla", "ici", "indirim", "iskelet", "istek",
	"istemci", "izin", "kanal", "kanit", "kapat", "kapsam", "kargo", "katalog",
	"kayit",
	"kayitlar", "klasor", "kok", "korumali", "koruma", "kos", "kucuk", "kullanici",
	"kume", "kural", "kurallar", "kurulum", "liste", "listele", "metin",
	"modul", "moduller", "muaf", "muafiyet", "musteri", "odeme", "oku",
	"olustur", "onceki", "ornek", "ornekler", "oturum", "ozet", "para",
	"politika", "sabit",
	"sabitler", "sablon", "sablonlar", "satir", "sayac", "sayi", "secim",
	"secme", "sepet", "sertlestirme", "sey", "sil", "sinir", "siparis",
	"sonraki", "sorgu", "stok", "sunucu", "sunu", "surum", "sutun", "tablo",
	"tarayici",
	"tarih", "tek", "toplam", "toplu", "tuketici", "tutar", "urun", "uretim",
	"ustunde",
	"varyant", "veritabani", "vergi", "yakala", "yanit", "yapilandirma",
	"yardimci", "yaz", "yeni", "yer", "yeter", "yetki", "yigin", "yol",
	"yollar", "yonetim", "zaman",
}

// scannedExtensions are the file types the content scan reads.
var scannedExtensions = []string{".go", ".sql", ".gohtml", ".md", ".graphqls"}

// scannedRoots are the trees the content scan walks, plus the repository root
// itself for its top-level documents.
var scannedRoots = []string{"cmd", "internal", "plugins", "docs"}

// skippedDirs never hold hand-written source.
var skippedDirs = []string{".git", "node_modules", "vendor", "bin", ".idea", ".vscode"}

// generatedMarker is the header every code generator in this repository
// writes. Generated files are out of scope: their language is decided by the
// .sql and .graphqls files they are generated FROM, and those ARE scanned.
const generatedMarker = "Code generated"

// diacriticDataExemptions lists text that carries a Turkish letter without
// being Turkish prose.
//
// The key is a repo-relative path, the value the exact substrings to ignore.
// Line numbers are deliberately not used: they rot on the first edit, which is
// the same reason [TestTheDocsCarryNoLineNumberReference] forbids pointing at a
// line number from a document.
//
// # Who is in it, and who is not yet
//
// Two entries. The first is this rule's own decision record: ADR 0012 quotes
// the letter class as data, so the file that DEFINES the rule would otherwise
// be its first violation.
//
// The second and third are the database case-folding probe and the decision
// record that explains it. Its whole subject is that a
// C-locale cluster cannot fold non-ASCII case, and it cannot say so without
// naming a pair of letters that differ only in case outside ASCII. Replacing
// them with ASCII would make the file pass this rule and make the probe test
// nothing — the ASCII pair folds on every cluster, which is exactly the false
// all-clear the probe exists to prevent.
//
// Three more files hold legitimate Turkish letters and are absent on purpose —
// each still contains Turkish PROSE as well, so the ledger already covers them
// and a second exemption would count the same hole twice. They join this map
// when they are translated, with the substrings each will need:
//
//   - internal/modules/region/migrations/000002_region_seed.up.sql —
//     'Curaçao', 'Türkiye' (ISO 3166 reference names, not translatable)
//   - internal/modules/product/service/validate.go — the turkishASCII map,
//     which must spell the letters it folds
//   - the product service's slug tests — Unicode fixtures that are Turkish on
//     purpose
var diacriticDataExemptions = map[string][]string{
	"docs/adr/0012-repository-language-and-solid.md": {"`çğıöşüÇĞİÖŞÜ`"},
	"internal/core/db/casefold.go": {
		"'Ç' ILIKE 'ç'", "'ÇANTA'", "'çanta'", `"çanta"`, `"Çanta"`,
	},
	// ADR 0015 records the same defect and has to quote the same two words to
	// show it: the whole finding is that one of them does not match the other.
	"docs/adr/0015-postgresql-cluster-contract.md": {
		"`çanta`", "`Çanta`", "q=çanta", "q=Çanta",
	},
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

// scanSource runs the three lanes over one file.
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
func scanSource(rel string, src []byte, exemptions map[string][]string) (hits []turkishHit, generated bool) {
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
		for i, line := range strings.Split(text, "\n") {
			if w := wordHit(line); w != "" {
				hits = append(hits, turkishHit{lane: laneWord, line: i + 1, detail: w})
				break
			}
		}
		return hits, false
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
	if err != nil {
		// A file that does not parse cannot be scanned precisely; fall back to
		// the raw text rather than reporting it clean.
		for i, line := range strings.Split(text, "\n") {
			if w := wordHit(line); w != "" {
				hits = append(hits, turkishHit{lane: laneWord, line: i + 1, detail: w})
				break
			}
		}
		return hits, false
	}

	wordFound, identFound := false, false
	record := func(lane, detail string, pos token.Pos) {
		hits = append(hits, turkishHit{lane: lane, line: fset.Position(pos).Line, detail: detail})
	}

	for _, group := range file.Comments {
		if wordFound {
			break
		}
		for _, c := range group.List {
			if w := wordHit(c.Text); w != "" {
				record(laneWord, w, c.Pos())
				wordFound = true
				break
			}
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if wordFound && identFound {
			return false
		}
		switch node := n.(type) {
		case *ast.BasicLit:
			if wordFound || node.Kind != token.STRING {
				return true
			}
			value := node.Value
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

// scannedFiles walks the repository and returns the repo-relative paths the
// content scan covers, sorted.
func scannedFiles(t *testing.T) []string {
	t.Helper()

	var found []string
	roots := append(slices.Clone(scannedRoots), ".")

	for _, root := range roots {
		abs := filepath.Join(repoRoot, root)
		depth := root == "."

		err := filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if slices.Contains(skippedDirs, d.Name()) {
					return filepath.SkipDir
				}
				// The repository root is walked only for its own files; its
				// subtrees are covered by their own entries in scannedRoots.
				if depth && path != abs {
					return filepath.SkipDir
				}
				return nil
			}
			if !slices.Contains(scannedExtensions, filepath.Ext(path)) {
				return nil
			}
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				return relErr
			}
			found = append(found, filepath.ToSlash(rel))
			return nil
		})
		require.NoError(t, err, "%s could not be walked", root)
	}

	slices.Sort(found)
	return slices.Compact(found)
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
	update := os.Getenv(ledgerUpdateEnv) != ""

	var dirty []string
	var violations []string

	for _, rel := range files {
		if rel == detectorFile {
			continue
		}
		src, err := os.ReadFile(filepath.Join(repoRoot, rel))
		require.NoError(t, err)

		hits, generated := scanSource(rel, src, diacriticDataExemptions)
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

		hits, generated := scanSource(rel, src, diacriticDataExemptions)
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
// A detector loses its teeth silently. Empty the word list, narrow the walk to
// a tree that no longer exists, or tighten a pattern by one character, and the
// suite stays green while the rule stops being enforced — the ledger would
// then read as "almost done" precisely when the scan had stopped working.
//
// So each lane keeps its own counter and each must stay positive, and the
// walked roots are counted separately: a root that contributes zero files was
// not scanned, and every file under it would be silently excused.
func TestDetectorIsNotBlind(t *testing.T) {
	t.Parallel()

	files := scannedFiles(t)

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

		hits, gen := scanSource(rel, src, diacriticDataExemptions)
		if gen {
			generated++
			continue
		}
		for _, h := range hits {
			perLane[h.lane]++
		}
	}

	for _, lane := range []string{laneDiacritic, laneWord, laneIdentifier} {
		assert.Positive(t, perLane[lane],
			"the %s lane found nothing in the whole repository. Either the migration "+
				"is complete — in which case %s is empty and this assertion should be "+
				"replaced by one that says so — or the lane is broken.",
			lane, turkishLedgerPath)
	}

	for _, root := range append(slices.Clone(scannedRoots), ".") {
		assert.Positive(t, perRoot[root],
			"no file was scanned under %q. The tree was moved or the walk is broken; "+
				"either way everything under it is being excused.", root)
	}

	// The counter above is not enough on its own, and the gap is worth naming:
	// it iterates the SAME list it is meant to be checking, so dropping a tree
	// from scannedRoots removes both the scan and the assertion that the scan
	// happened. A mutation proved it — deleting "plugins" left the suite green.
	//
	// So the roots are checked against the DISK instead: any top-level
	// directory holding files the scan would read must be listed.
	entries, err := os.ReadDir(repoRoot)
	require.NoError(t, err)

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") ||
			slices.Contains(skippedDirs, entry.Name()) {
			continue
		}

		scannable := 0
		walkErr := filepath.WalkDir(filepath.Join(repoRoot, entry.Name()),
			func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() && slices.Contains(skippedDirs, d.Name()) {
					return filepath.SkipDir
				}
				if !d.IsDir() && slices.Contains(scannedExtensions, filepath.Ext(path)) {
					scannable++
				}
				return nil
			})
		require.NoError(t, walkErr)

		if scannable == 0 {
			continue
		}
		assert.Contains(t, scannedRoots, entry.Name(),
			"%s/ holds %d file(s) the scan reads but is not in scannedRoots, so the "+
				"whole tree is excused without a single ledger line.", entry.Name(), scannable)
	}

	assert.Positive(t, generated,
		"no generated file was recognized. The generator header changed, so generated "+
			"code is now being scanned as if it were hand-written.")
}

// TestDetectorFindsPlantedTurkish is the positive control.
//
// [TestDetectorIsNotBlind] proves the lanes still fire on the repository, but
// the repository is mostly Turkish — a lane could keep firing on old files
// while being unable to catch anything NEW. This test plants a known sample in
// each lane and requires each to be caught, including the transliterated
// spelling that the letter lane cannot see.
func TestDetectorFindsPlantedTurkish(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hits, generated := scanSource("planted.go", []byte(tc.src), diacriticDataExemptions)
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
// as "rol", "reason" as "son", and the `x, ok` idiom that spells "yok".
func TestDetectorPassesEnglishSource(t *testing.T) {
	t.Parallel()

	const src = `package p

// The module resolves a reason from the JSON body and rolls back on failure.
func Rollback(modules []string) error {
	yok := len(modules) == 0
	if yok {
		return nil
	}
	return nil
}
`

	hits, generated := scanSource("english.go", []byte(src), diacriticDataExemptions)
	require.False(t, generated)
	assert.Empty(t, hits, "correct English source must not be flagged: %v", hits)
}

// TestDetectorExemptsOnlyItself proves the one hole in the content scan is
// exactly one file wide and still needed.
func TestDetectorExemptsOnlyItself(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile(filepath.Join(repoRoot, detectorFile))
	require.NoError(t, err, "%s must exist", detectorFile)

	hits, generated := scanSource(detectorFile, src, diacriticDataExemptions)
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

	hits, _ := scanSource("fixture.sql", []byte(fixture), nil)
	require.NotEmpty(t, hits, "without an exemption the fixture must be flagged")

	hits, _ = scanSource("fixture.sql", []byte(fixture),
		map[string][]string{"fixture.sql": {"Curaçao"}})
	assert.Empty(t, hits, "with the exemption in place the fixture must pass: %v", hits)
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
