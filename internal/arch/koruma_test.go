package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// atlamaCagrilari bir testi KOŞMADAN "geçmiş" gösteren testing metotlarının
// adlarıdır.
//
// Üçü de aynı sonucu verir: koşu çıktısında paket "ok" yazar ve denetim hiç
// çalışmamış olur. Ayrım yalnızca çağrının biçimindedir, sonucunda değil.
var atlamaCagrilari = map[string]bool{
	"Skip":    true,
	"Skipf":   true,
	"SkipNow": true,
}

// TestYapisalDenetimlerAtlanmaz internal/arch altında hiçbir denetimin
// kendini ATLAYARAK geçmediğini zorlar.
//
// # Hangi arıza sınıfı
//
// Bu paketteki testlerin ortak ölüm biçimi, ihlali kaçırmak değil GİRDİYİ
// kaybetmektir: gezinti bir gün hiçbir şey bulmaz ve test sessizce yeşil
// kalır. O kayboluşun en ucuz ve en görünmez biçimi bir atlamadır —
// "modül yoksa atla", "ağaç yoksa atla" — çünkü atlanan bir test, koşu
// özetinde geçen bir testten ayırt edilemez (go test -v olmadan SKIP satırı
// hiç yazılmaz) ve niyeti masumdur: yazıldığı gün gerçekten henüz o ağaç
// yoktur. Ağaç geldikten sonra satırı kimse silmez; silinmediği için de
// ağacın bir gün TAŞINMASI, kuralın kalkmasıyla aynı şeye dönüşür.
//
// Bu depoda üç örneği vardı ve üçü de tam olarak böyle yazılmıştı: modül,
// workflow ve eklenti ağaçları yokken atlayan üç import değişmezi. Ağaçlar
// geldikten sonra üç satır da yerinde kaldı — zararsız göründükleri için. Oysa
// o satırlar, dizin adının kaydığı gün üç denetimi birden sessizce
// susturacaktı; kural kalkmayacak, yalnızca kimse bakmayacaktı.
//
// # Neden yasak, neden "gerekçeli muafiyet" değil
//
// Bu paketin girdisi DEPONUN KENDİSİDİR ve depo her zaman vardır. Bir
// yapısal denetimin koşamayacağı bir durum yoktur: aradığı şey yoksa cevap
// "atla" değil, "kayıp" olmalıdır — çünkü aradığının kaybolması, tam olarak
// bu testlerin haber vermesi gereken olaydır. Bu yüzden bir kapı da
// açılmadı: bugün meşru tek bir örneği bile olmayan bir muafiyet
// mekanizması, yalnızca ilk sıkışan kişiye hazır bir çıkış yolu bırakırdı.
// Gerçekten meşru bir atlama gerekirse doğru hamle, bu denetimi gerekçeli
// bir muafiyet listesiyle GENİŞLETMEKTİR; gerekçe o zaman kod incelemesinde
// görünür.
//
// # Bu değişmez neyi GARANTİ ETMEZ
//
// "Yeni yazılan her yapısal testin bir körlük koruması vardır" DEMEZ ve
// diyemez. Yalnızca bir kaçış yolunu — atlamayı — kapatır. Girdi kümesi
// boşalınca hiçbir şey yapmadan biten bir `for` döngüsü buradan sorunsuz
// geçer; onu yakalamanın tek yolu, gezintinin ne saydığını BİLMEKTİR ve o
// bilgi testin kendisinde durur, dışarıdan okunamaz. Denenebilecek
// sözdizimsel vekiller (gövdede bir require.NotEmpty ARANMASI gibi) tek
// satırla susturulabildikleri için ölçtükleri şeyi ölçmez; kendi kendini
// karşılayan bir kural, bu paketin kapatmaya çalıştığı sınıfın ta kendisidir.
// Gerçek güvence mutasyondadır: bir korumanın işe yaradığı, ancak gezinti
// körleştirilip testin DÜŞTÜĞÜ görülerek bilinir ve bunu otomatikleştirmenin
// yolu, testleri kasten bozan bir koşum yazmaktan geçer — o da bu paketin
// değil, ayrı bir aracın işidir.
//
// # Yanlış pozitifi
//
// Eşleşme ADA bakar: "Skip" adlı bir metodu olan başka bir tip (bir
// ayrıştırıcı, bir tarayıcı) bu paketin içinde kullanılsaydı haksız yere
// düşerdi. Tip çözümü yapılmıyor çünkü bu paketin hiçbir denetimi go/types'a
// bağlı değildir ve bu tek vaka için bağlanması, tarama maliyetini kuralın
// değerinden büyük yapardı. Yanlış pozitif GÜRÜLTÜLÜDÜR — birinin bakması ve
// kuralı genişletmesi gerekir, sessizce geçmez.
func TestYapisalDenetimlerAtlanmaz(t *testing.T) {
	t.Parallel()

	dizin := filepath.Join(repoRoot, archDizini)
	girdiler, err := os.ReadDir(dizin)
	require.NoError(t, err, "%s okunamadı", archDizini)

	fset := token.NewFileSet()
	taranan, gorulenCagri := 0, 0

	for _, girdi := range girdiler {
		if girdi.IsDir() || !strings.HasSuffix(girdi.Name(), "_test.go") {
			continue
		}

		// Derleme etiketleri UMURSANMAZ: entegrasyon etiketinin arkasındaki bir
		// denetim de bu kurala tabidir ve etikete bakan bir tarama onu etiketsiz
		// koşuda görmezden gelirdi.
		yol := filepath.Join(dizin, girdi.Name())
		agac, ayristirmaHatasi := parser.ParseFile(fset, yol, nil, parser.SkipObjectResolution)
		require.NoError(t, ayristirmaHatasi, "%s ayrıştırılamadı", yol)
		taranan++

		ast.Inspect(agac, func(n ast.Node) bool {
			cagri, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			gorulenCagri++

			sec, ok := cagri.Fun.(*ast.SelectorExpr)
			if !ok || !atlamaCagrilari[sec.Sel.Name] {
				return true
			}
			t.Errorf("%s: %s.%s çağrısı — bir yapısal denetim kendini ATLIYOR.\n"+
				"Atlanan test, koşu özetinde geçen bir testten ayırt edilemez: SKIP satırı "+
				"yalnızca -v ile görünür, paket yine \"ok\" yazar. Aranan ağaç, dosya ya da "+
				"sabit bulunamıyorsa bu bir atlama sebebi DEĞİL, denetimin haber vermesi "+
				"gereken olayın ta kendisidir — koşul gerçekleştiği gün kural kalkmış olmaz, "+
				"yalnızca denetimi biter.\n"+
				"Koşulu bir require'a çevirin ve mesajında NEYİN kaybolmuş olabileceğini "+
				"yazın (dizin taşındı, imza değişti, konvansiyon kaydı). Atlama gerçekten "+
				"meşruysa bu denetim gerekçeli bir muafiyetle genişletilmelidir.",
				fset.Position(sec.Sel.Pos()), aliciAdi(sec.X), sec.Sel.Name)

			return true
		})
	}

	require.Positive(t, taranan,
		"%s altında hiç _test.go dosyası bulunamadı; bu denetim KÖR kalmış olmalı — "+
			"yapısal testler başka bir pakete taşındıysa archDizini de taşınmalıdır.",
		archDizini)
	require.Positive(t, gorulenCagri,
		"%s altındaki test dosyalarında hiç fonksiyon çağrısı bulunamadı; gezinti "+
			"bozulmuş olmalı. Çağrı göremeyen bir tarama, atlama çağrısını da göremez ve "+
			"her koşuda temiz rapor verir.", archDizini)
}

// aliciAdi bir çağrının alıcı ifadesini hata mesajı için kısa metne çevirir;
// okunamayan ifade için "?" döner.
//
// Yalnızca teşhis içindir: alıcının adı ("t", "b") kuralı değiştirmez ama
// mesajı kaynakta aranabilir kılar.
func aliciAdi(ifade ast.Expr) string {
	if id, ok := ifade.(*ast.Ident); ok {
		return id.Name
	}

	return "?"
}
