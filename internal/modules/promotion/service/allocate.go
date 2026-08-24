package service

import (
	"cmp"
	"math"
	"math/bits"
	"slices"
)

// allocLine tahsise giren tek bir satırdır.
type allocLine struct {
	// ID satırın kimliğidir; kuruş artığının sahibi belirlenirken son
	// belirleyicidir.
	ID string
	// Amount satırın tutarıdır (minor unit); pay bu tutarla orantılıdır.
	Amount int64
}

// allocateAcross verilen toplamı satırlara tutarlarıyla ORANTILI dağıtır.
//
// Dağıtılan payların toplamı, girilen toplamla BİREBİR tutar (kuruş artığı
// dâhil); tek istisna, tüm satırların tutarının sıfır olduğu durumdur ve orada
// dağıtılacak bir taban yoktur.
//
// # Yöntem: en büyük kalan (largest remainder)
//
// Her satırın payı önce AŞAĞI yuvarlanarak hesaplanır:
//
//	pay_i = floor(toplam × tutar_i / taban),  taban = Σ tutar_i
//
// Aşağı yuvarlama yüzünden payların toplamı toplamdan `artık` kadar eksik
// kalır ve artık, pay alan satır sayısından küçüktür. Artık BİRER BİRER,
// kesirli kalanı EN BÜYÜK olan satırlara dağıtılır.
//
// Basit alternatifler bilinçle reddedilmiştir: artığı ilk satıra ya da en
// büyük satıra topluca vermek, iki kuruşluk artığı tek bir kaleme yığar ve o
// kalemin indirimini orantısal hakkından iki kuruş fazla gösterir.
//
// # Kuruş artığı kime gider
//
// Sıralama ölçütleri şunlardır; ilk FARK kazananı belirler:
//
//  1. Kesirli kalan (büyük kazanır) — orantısal hakkı en çok kırpılan satır.
//  2. Tutar (büyük kazanır) — eşit kalanda büyük satır, bir kuruşu oransal
//     olarak daha az bozar.
//  3. Kimlik (küçük kazanır) — kalan her durumda sonuç BELİRLENİMCİDİR ve
//     satırların GELİŞ SIRASINDAN bağımsızdır. Kimlikler zaman sıralı olduğu
//     için bu, "önce eklenen kalem" demektir.
//
// Belirlenimcilik süsleme değildir: aynı sepet iki kez hesaplandığında aynı
// satıra aynı kuruş düşmelidir, aksi hâlde iki hesap arasında satır tutarları
// oynar ve mutabakat imkânsızlaşır.
//
// # Sınırlar
//
// total, tabanı AŞAMAZ; aşıyorsa tabana kırpılır — bir tahsis, dağıttığı
// tabandan fazlasını dağıtamaz. Tutarı pozitif OLMAYAN satır pay ALMAZ: sıfır
// tutarlı bir kaleme kuruş yazmak, indirimi olmayan bir kaleme indirim
// göstermek olurdu.
func allocateAcross(total int64, lines []allocLine) []int64 {
	out := make([]int64, len(lines))
	if total <= 0 || len(lines) == 0 {
		return out
	}

	var base int64
	for i := range lines {
		if lines[i].Amount > 0 {
			base += lines[i].Amount
		}
	}
	if base <= 0 {
		return out
	}
	if total > base {
		total = base
	}

	// share bir satırın payı ve kesirli kalanıdır; artık dağıtımı buna göre
	// sıralanır.
	type share struct {
		index     int
		remainder int64
		amount    int64
		id        string
	}

	shares := make([]share, 0, len(lines))
	var assigned int64
	for i := range lines {
		if lines[i].Amount <= 0 {
			continue
		}
		quotient, remainder := mulDivMod(total, lines[i].Amount, base)
		out[i] = quotient
		assigned += quotient
		shares = append(shares, share{
			index:     i,
			remainder: remainder,
			amount:    lines[i].Amount,
			id:        lines[i].ID,
		})
	}

	slices.SortFunc(shares, func(a, b share) int {
		if c := cmp.Compare(b.remainder, a.remainder); c != 0 {
			return c
		}
		if c := cmp.Compare(b.amount, a.amount); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})

	leftover := total - assigned
	for i := 0; i < len(shares) && leftover > 0; i++ {
		out[shares[i].index]++
		leftover--
	}
	return out
}

// mulDivMod a×b/d bölümünü ve kalanını 128 bit ara sonuç üzerinden döner.
//
// Ara çarpım int64'e SIĞMAYABİLİR: tahsiste a ve b'nin ikisi de
// [models.MaxAmount] (10^12) büyüklüğünde olabilir ve çarpımları 10^24'e
// ulaşır. math/bits'in 128 bitlik çarpma/bölmesi bu yüzden zorunludur; float'a
// geçmek plan Bölüm 8'in yasakladığı şeydir ve zaten kuruş düzeyinde sessiz
// hata üretirdi.
//
// Bölüm AŞAĞI yuvarlanır (tam sayı bölmesi) ve kalan, artık dağıtımının
// sıralama anahtarıdır.
//
// Ön koşul: 0 ≤ a ≤ d, 0 ≤ b ≤ d, d > 0. Bu koşul altında 128 bitlik bölmenin
// kendi ön koşulu (yüksek kelime < bölen) kendiliğinden sağlanır ve bölüm
// int64'e sığar. Koşulun ihlal edildiği durumda sıfır dönülür — yani indirim
// verilmez. Yön bilinçlidir: bir aritmetik ön koşulu kırıldığında müşteriye
// hesaplanamamış bir indirim vermektense hiç indirim vermemek yeğdir ve durum
// toplamlarda görünür kalır.
func mulDivMod(a, b, d int64) (quotient, remainder int64) {
	if a <= 0 || b <= 0 || d <= 0 {
		return 0, 0
	}

	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi >= uint64(d) {
		return 0, 0
	}

	q, r := bits.Div64(hi, lo, uint64(d))
	// Kalan bölenden küçüktür ve bölen int64 olduğu için kalan da sığar; bölüm
	// ise ön koşul altında b'yi aşamaz. İkisi de yine de denetlenir — sınırın
	// YEREL olarak kanıtlanması, uzaktaki bir değişikliğin sessizce sarma
	// üretmesini imkânsız kılar.
	if q > math.MaxInt64 || r > math.MaxInt64 {
		return 0, 0
	}
	return int64(q), int64(r)
}
