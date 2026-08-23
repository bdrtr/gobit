// Package arch_test mimari değişmezleri OTOMATİK olarak denetler.
//
// Buradaki testler üretim kodu içermez; depoyu tarayıp plan Bölüm 2'deki
// Mimari Prensipleri ve Bölüm 8'deki konvansiyonları zorlar.
//
// Neden burada: bu ihlaller daha önce her fazda inceleme bulgusu olarak
// çıktı ve elle düzeltildi. Bir kural inceleme turunda yakalanıyorsa, testte
// yakalanabilir demektir; testte yakalanınca da bir daha hiç yazılmaz.
// golangci-lint'in yakalayamadığı iki sınıf özellikle burada: unexported
// godoc biçimi ve SQL dosyalarındaki cross-module foreign key.
package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	modulePath = "github.com/bdrtr/gobit"
	repoRoot   = "../.."
	modulesDir = "internal/modules"
)

// modulNames depodaki commerce modüllerinin adlarını döner.
func modulNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repoRoot, modulesDir))
	if err != nil {
		t.Fatalf("modüller okunamadı: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// goFiles verilen kök altındaki tüm .go dosyalarını döner.
func goFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s taranamadı: %v", root, err)
	}
	return out
}

// TestModullerBirbiriniImportEtmez Prensip 2.1/2.4 ve ADR 0001'i zorlar.
//
// depguard aynı kuralı golangci-lint tarafında da uyguluyor; bu test ikinci
// savunma hattıdır ve .golangci.yml'deki kural listesi bir modül eklenirken
// güncellenmeyi UNUTULURSA yine de yakalar.
func TestModullerBirbiriniImportEtmez(t *testing.T) {
	moduller := modulNames(t)
	if len(moduller) == 0 {
		t.Skip("henüz modül yok")
	}

	for _, mod := range moduller {
		root := filepath.Join(repoRoot, modulesDir, mod)
		for _, file := range goFiles(t, root) {
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("%s ayrıştırılamadı: %v", file, err)
			}
			for _, imp := range parsed.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				prefix := modulePath + "/" + modulesDir + "/"
				if !strings.HasPrefix(path, prefix) {
					continue
				}
				other := strings.SplitN(strings.TrimPrefix(path, prefix), "/", 2)[0]
				if other != mod {
					t.Errorf("%s: %q modülü %q modülünü import ediyor (Prensip 2.4 / ADR 0001).\n"+
						"Erişim, tüketicinin KENDİ paketinde tanımladığı dar arayüz ve container'dan "+
						"adla çözüm üzerinden olmalı.", file, mod, other)
				}
			}
		}
	}
}

// TestWorkflowlarModulleriImportEtmez ADR 0006'yı zorlar.
//
// internal/workflows çekirdek değildir (Prensip 2.4 onu bağlamaz) ve modül de
// değildir (depguard kuralları internal/modules içindir), yani hiçbir mevcut
// kural onu kısıtlamaz. Ama modülleri doğrudan import etseydi tüm modülleri
// tanıyan tek bir düğüme dönüşür, gerçek veritabanı olmadan test edilemez ve
// bir modülü ayrı servise çıkarmak workflow'ları derleme zamanında kırardı.
//
// Erişim, workflow'un KENDİ paketinde tanımladığı dar arayüz ve container'dan
// adla çözüm üzerinden olmalıdır.
func TestWorkflowlarModulleriImportEtmez(t *testing.T) {
	root := filepath.Join(repoRoot, "internal", "workflows")
	if _, err := os.Stat(root); err != nil {
		t.Skip("henüz workflow yok")
	}

	prefix := modulePath + "/" + modulesDir + "/"
	for _, file := range goFiles(t, root) {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s ayrıştırılamadı: %v", file, err)
		}
		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, prefix) {
				t.Errorf("%s: workflow %q modülünü import ediyor (ADR 0006).\n"+
					"Workflow, ihtiyaç duyduğu DAR yüzeyi kendi paketinde tanımlamalı ve "+
					"somut servisi container'dan ADLA çözmelidir.",
					file, strings.TrimPrefix(path, prefix))
			}
		}
	}
}

// createTableRe ve referencesRe migration dosyalarındaki tablo bildirimlerini
// ve foreign key hedeflerini yakalar.
var (
	createTableRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?"?([a-z_][a-z0-9_]*)"?`)
	referencesRe  = regexp.MustCompile(`(?is)REFERENCES\s+"?([a-z_][a-z0-9_]*)"?`)
	// sqlYorumRe satır ve blok yorumlarını yakalar. Yorumlar taramadan ÖNCE
	// silinir: "-- başka modülün tablosuna REFERENCES verilmez" gibi bir açıklama
	// aksi hâlde ihlal sanılırdı.
	sqlSatirYorumRe = regexp.MustCompile(`--[^\n]*`)
	sqlBlokYorumRe  = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// sqlYorumlariSil SQL gövdesinden yorumları çıkarır.
func sqlYorumlariSil(body string) string {
	body = sqlBlokYorumRe.ReplaceAllString(body, " ")
	return sqlSatirYorumRe.ReplaceAllString(body, " ")
}

// TestCrossModuleForeignKeyYok Prensip 2.2'yi zorlar.
//
// Bir modülün migration'ı yalnızca KENDİ oluşturduğu tablolara foreign key
// verebilir. Başka bir modülün tablosuna REFERENCES vermek, modülü ayrı bir
// servise çıkarmayı imkânsız kılar ve ilişkinin Module Links yerine
// veritabanı kısıtıyla kurulması demektir.
func TestCrossModuleForeignKeyYok(t *testing.T) {
	for _, mod := range modulNames(t) {
		migDir := filepath.Join(repoRoot, modulesDir, mod, "migrations")
		if _, err := os.Stat(migDir); err != nil {
			continue
		}

		sqls, err := filepath.Glob(filepath.Join(migDir, "*.sql"))
		if err != nil {
			t.Fatalf("%s taranamadı: %v", migDir, err)
		}

		// Önce modülün SAHİP olduğu tabloları topla.
		owned := map[string]bool{}
		var bodies []string
		for _, f := range sqls {
			raw, readErr := os.ReadFile(f)
			if readErr != nil {
				t.Fatalf("%s okunamadı: %v", f, readErr)
			}
			body := sqlYorumlariSil(string(raw))
			bodies = append(bodies, body)
			for _, m := range createTableRe.FindAllStringSubmatch(body, -1) {
				owned[strings.ToLower(m[1])] = true
			}
		}

		for i, body := range bodies {
			for _, m := range referencesRe.FindAllStringSubmatch(body, -1) {
				target := strings.ToLower(m[1])
				if !owned[target] {
					t.Errorf("%s: %q modülü kendi sahibi OLMADIĞI %q tablosuna REFERENCES veriyor "+
						"(Prensip 2.2: cross-module FK yasak; ilişki Module Links ile kurulur).",
						sqls[i], mod, target)
				}
			}
		}
	}
}

// TestMigrationlarGeriAlinabilir plan Bölüm 8'deki up/down şartını zorlar.
func TestMigrationlarGeriAlinabilir(t *testing.T) {
	for _, mod := range modulNames(t) {
		migDir := filepath.Join(repoRoot, modulesDir, mod, "migrations")
		ups, err := filepath.Glob(filepath.Join(migDir, "*.up.sql"))
		if err != nil {
			t.Fatalf("%s taranamadı: %v", migDir, err)
		}
		for _, up := range ups {
			down := strings.TrimSuffix(up, ".up.sql") + ".down.sql"
			if _, statErr := os.Stat(down); statErr != nil {
				t.Errorf("%s: down çifti yok (%s). Plan Bölüm 8 migration'ların geri "+
					"alınabilir olmasını şart koşuyor.", up, filepath.Base(down))
			}
		}
	}
}

// paraAlanlari para tuttuğu varsayılan alan adlarıdır.
var paraAlanlari = regexp.MustCompile(`(?i)^(amount|price|total|subtotal|cost|fee|discount|tax|shipping)([A-Za-z]*)?$`)

// TestParaTamSayidir plan Bölüm 8'i zorlar: para minor unit TAM SAYI saklanır.
//
// Kayan noktalı para, toplama sırasında sessiz yuvarlama hatası üretir ve
// hatayı ancak mutabakat sırasında görürsünüz.
func TestParaTamSayidir(t *testing.T) {
	for _, mod := range modulNames(t) {
		root := filepath.Join(repoRoot, modulesDir, mod)
		for _, file := range goFiles(t, root) {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatalf("%s ayrıştırılamadı: %v", file, err)
			}

			ast.Inspect(parsed, func(n ast.Node) bool {
				st, ok := n.(*ast.StructType)
				if !ok || st.Fields == nil {
					return true
				}
				for _, f := range st.Fields.List {
					ident, ok := f.Type.(*ast.Ident)
					if !ok || (ident.Name != "float32" && ident.Name != "float64") {
						continue
					}
					for _, name := range f.Names {
						if paraAlanlari.MatchString(name.Name) {
							t.Errorf("%s:%d: %q alanı %s tipinde. Plan Bölüm 8: para TAM SAYI "+
								"minor unit (kuruş/cent) olarak saklanır, float ASLA.",
								file, fset.Position(name.Pos()).Line, name.Name, ident.Name)
						}
					}
				}
				return true
			})
		}
	}
}

// godocVirgul bir godoc ilk satırının addan hemen sonra virgülle devam edip
// etmediğini yakalar.
var godocVirgul = regexp.MustCompile(`^// ([A-Za-z_][A-Za-z0-9_]*), `)

// TestGodocBicimi godoc'un tanımlayıcı adıyla başlayıp virgülsüz devam etmesini
// zorlar.
//
// revive'ın "exported" kuralı yalnızca DIŞA AÇIK tanımlayıcıları denetler;
// unexported olanlarda aynı hata sessizce kalır. Bu test ikisini de kapsar ve
// ayrıca godoc bloğunun YANLIŞ tanımlayıcıya bağlanmasını yakalar — bloğun ilk
// satırındaki ad, bağlandığı tanımın adıyla eşleşmiyorsa hata verir.
func TestGodocBicimi(t *testing.T) {
	roots := []string{
		filepath.Join(repoRoot, "internal"),
		filepath.Join(repoRoot, "cmd"),
	}

	for _, root := range roots {
		for _, file := range goFiles(t, root) {
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
			if err != nil {
				t.Fatalf("%s ayrıştırılamadı: %v", file, err)
			}
			// Üretilmiş kod (sqlc) bu kuralın dışındadır.
			if len(parsed.Comments) > 0 && strings.Contains(parsed.Comments[0].Text(), "Code generated by") {
				continue
			}

			for _, decl := range parsed.Decls {
				var (
					doc  *ast.CommentGroup
					name string
				)
				switch d := decl.(type) {
				case *ast.FuncDecl:
					doc, name = d.Doc, d.Name.Name
				case *ast.GenDecl:
					doc = d.Doc
					if len(d.Specs) == 1 {
						switch sp := d.Specs[0].(type) {
						case *ast.TypeSpec:
							name = sp.Name.Name
						case *ast.ValueSpec:
							if len(sp.Names) > 0 {
								name = sp.Names[0].Name
							}
						}
					}
				}
				// "var _ Iface = (*T)(nil)" deyiminin adı "_"dır; godoc'u doğal
				// olarak bir adı değil, doğrulanan sözleşmeyi anlatır.
				if doc == nil || name == "" || name == "_" || len(doc.List) == 0 {
					continue
				}

				first := doc.List[0].Text
				if m := godocVirgul.FindStringSubmatch(first); m != nil {
					t.Errorf("%s:%d: godoc ilk satırı %q — addan hemen sonra VİRGÜL var.\n"+
						"Doğrusu: \"// %s ...\". revive bu kuralı yalnızca dışa açık "+
						"tanımlayıcılarda uyguluyor.",
						file, fset.Position(doc.List[0].Pos()).Line, strings.TrimSpace(first), m[1])
					continue
				}
				// Bloğun bağlandığı tanımın adıyla başlamıyorsa, büyük ihtimalle
				// godoc yanlış tanımlayıcıya yapışmıştır.
				prefix := "// " + name
				if !strings.HasPrefix(first, prefix) && !strings.HasPrefix(first, "// Package ") &&
					!strings.HasPrefix(first, "//nolint") && !strings.HasPrefix(first, "//go:") {
					t.Errorf("%s:%d: %q tanımının godoc'u %q ile başlıyor.\n"+
						"godoc bloğu bağlandığı tanımın ADIYLA başlamalı; bu blok büyük "+
						"olasılıkla araya boş satır konmadığı için yanlış tanıma yapışmış.",
						file, fset.Position(doc.List[0].Pos()).Line, name, strings.TrimSpace(first))
				}
			}
		}
	}
}
