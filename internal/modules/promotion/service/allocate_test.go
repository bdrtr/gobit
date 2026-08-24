package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// toplam bir dilimin toplamını döner.
func toplam(values []int64) int64 {
	var sum int64
	for _, v := range values {
		sum += v
	}
	return sum
}

func TestAllocateAcrossToplamiBirebirDagitir(t *testing.T) {
	testler := []struct {
		ad      string
		total   int64
		lines   []allocLine
		bekleme []int64
	}{
		{
			ad:      "eşit satırlar, bölünmeyen toplam",
			total:   100,
			lines:   []allocLine{{"li_a", 1000}, {"li_b", 1000}, {"li_c", 1000}},
			bekleme: []int64{34, 33, 33},
		},
		{
			ad:      "orantılı dağıtım",
			total:   100,
			lines:   []allocLine{{"li_a", 3000}, {"li_b", 1000}},
			bekleme: []int64{75, 25},
		},
		{
			ad:      "iki kuruş artık iki AYRI satıra gider",
			total:   10,
			lines:   []allocLine{{"li_a", 100}, {"li_b", 100}, {"li_c", 100}, {"li_d", 100}},
			bekleme: []int64{3, 3, 2, 2},
		},
		{
			// Paylar 3⅓ ve 6⅔; artan tek kuruş, kesirli kalanı BÜYÜK olan
			// ikinci satıra gider (sırayla ya da kimlikle dağıtılsaydı ilk
			// satıra giderdi).
			ad:      "kesirli kalanı büyük olan satır artığı alır",
			total:   10,
			lines:   []allocLine{{"li_a", 10}, {"li_b", 20}},
			bekleme: []int64{3, 7},
		},
		{
			ad:      "sıfır tutarlı satır pay almaz",
			total:   50,
			lines:   []allocLine{{"li_a", 0}, {"li_b", 500}},
			bekleme: []int64{0, 50},
		},
		{
			ad:      "toplam tabanı aşarsa tabana kırpılır",
			total:   5000,
			lines:   []allocLine{{"li_a", 100}, {"li_b", 100}},
			bekleme: []int64{100, 100},
		},
		{
			ad:      "sıfır toplam hiçbir şey dağıtmaz",
			total:   0,
			lines:   []allocLine{{"li_a", 100}},
			bekleme: []int64{0},
		},
		{
			ad:      "negatif toplam hiçbir şey dağıtmaz",
			total:   -10,
			lines:   []allocLine{{"li_a", 100}},
			bekleme: []int64{0},
		},
		{
			ad:      "tüm satırlar sıfırsa dağıtılamaz",
			total:   100,
			lines:   []allocLine{{"li_a", 0}, {"li_b", 0}},
			bekleme: []int64{0, 0},
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			got := allocateAcross(tt.total, tt.lines)
			assert.Equal(t, tt.bekleme, got)

			var base int64
			for _, line := range tt.lines {
				if line.Amount > 0 {
					base += line.Amount
				}
			}
			beklenenToplam := min(tt.total, base)
			if beklenenToplam < 0 {
				beklenenToplam = 0
			}
			assert.Equal(t, beklenenToplam, toplam(got),
				"dağıtılan payların toplamı, dağıtılan toplamla BİREBİR tutmalı")
		})
	}
}

func TestAllocateAcrossKurusArtigiHicKaybolmaz(t *testing.T) {
	// Asal sayılarla kurulmuş, hiçbir payın tam bölünmediği bir küme: her
	// satırın payı aşağı yuvarlanır ve artık dağıtılmazsa toplam eksik kalır.
	lines := []allocLine{
		{"li_1", 101}, {"li_2", 103}, {"li_3", 107}, {"li_4", 109},
		{"li_5", 113}, {"li_6", 127}, {"li_7", 131},
	}
	for _, total := range []int64{1, 2, 7, 13, 99, 331, 790} {
		got := allocateAcross(total, lines)
		assert.Equal(t, total, toplam(got),
			"%d toplamı birebir dağıtılmalı; kuruş artığı kaybolamaz", total)
	}
}

func TestAllocateAcrossBosDilim(t *testing.T) {
	assert.Empty(t, allocateAcross(100, nil))
	assert.Empty(t, allocateAcross(100, []allocLine{}))
}

func TestMulDivMod(t *testing.T) {
	testler := []struct {
		ad           string
		a, b, d      int64
		bolum, kalan int64
	}{
		{ad: "tam bölünen", a: 100, b: 50, d: 10, bolum: 500, kalan: 0},
		{ad: "kalanlı", a: 10, b: 3, d: 4, bolum: 7, kalan: 2},
		{ad: "sıfır bölen", a: 10, b: 3, d: 0, bolum: 0, kalan: 0},
		{ad: "sıfır çarpan", a: 0, b: 3, d: 4, bolum: 0, kalan: 0},
		{ad: "negatif girdi", a: -1, b: 3, d: 4, bolum: 0, kalan: 0},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			bolum, kalan := mulDivMod(tt.a, tt.b, tt.d)
			assert.Equal(t, tt.bolum, bolum)
			assert.Equal(t, tt.kalan, kalan)
		})
	}
}

func TestMulDivModAraCarpimInt64eSigmasaBileDogruSonucVerir(t *testing.T) {
	// 10^12 × 10^12 = 10^24; int64'ün üst sınırı 9.22×10^18'dir. Düz çarpma
	// sarardı ve sonuç sessizce yanlış (hatta negatif) çıkardı.
	a, b, d := models.MaxAmount, models.MaxAmount, models.MaxAmount

	bolum, kalan := mulDivMod(a, b, d)

	assert.Equal(t, models.MaxAmount, bolum, "10^24 / 10^12 = 10^12")
	assert.Zero(t, kalan)
	assert.Positive(t, bolum, "düz int64 çarpması burada negatif bir sonuç verirdi")
}

func TestMulDivModOnKosulKirilirsaSifirDoner(t *testing.T) {
	// a > d: bölüm int64'e sığmayabilir. Fonksiyon panik ÜRETMEZ, sıfır döner
	// ve çağıran bunu "indirim yok" sayar.
	bolum, kalan := mulDivMod(math.MaxInt64, math.MaxInt64, 1)

	assert.Zero(t, bolum, "ön koşul kırıldığında hesaplanamamış bir indirim verilmez")
	assert.Zero(t, kalan)
}

func TestPercentageOf(t *testing.T) {
	testler := []struct {
		ad      string
		amount  int64
		bps     int64
		bekleme int64
	}{
		{ad: "tam yüzde", amount: 10000, bps: 2000, bekleme: 2000},
		{ad: "aşağı yuvarlar", amount: 999, bps: 2000, bekleme: 199},
		{ad: "tam yarım aşağı yuvarlar", amount: 5, bps: 5000, bekleme: 2},
		{ad: "yüzde yüz", amount: 1234, bps: 10000, bekleme: 1234},
		{ad: "sıfır oran", amount: 1234, bps: 0, bekleme: 0},
		{ad: "sıfır tutar", amount: 0, bps: 5000, bekleme: 0},
		{ad: "negatif tutar", amount: -100, bps: 5000, bekleme: 0},
		{ad: "oran üst sınıra kırpılır", amount: 100, bps: 99999, bekleme: 100},
		{ad: "tutar üst sınıra kırpılır", amount: models.MaxAmount + 1, bps: 10000, bekleme: models.MaxAmount},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			assert.Equal(t, tt.bekleme, percentageOf(tt.amount, tt.bps))
		})
	}
}

func TestPercentageOfAzamiTutardaTasmaz(t *testing.T) {
	got := percentageOf(models.MaxAmount, models.BasisPointDenominator)

	require.Positive(t, got)
	assert.Equal(t, models.MaxAmount, got,
		"ara çarpım 10^16'dır ve int64'e sığar; taşma olsaydı sonuç negatif çıkardı")
}

func TestLineStateChargeKalaniAsmaz(t *testing.T) {
	line := &lineState{id: "li_1", amount: 1000, quantity: 1}

	assert.Equal(t, int64(400), line.charge(400))
	assert.Equal(t, int64(600), line.charge(900), "kalan 600'dür; fazlası kırpılır")
	assert.Equal(t, int64(1000), line.discount)
	assert.Zero(t, line.charge(1), "tükenen satıra daha fazla indirim yazılmaz")
	assert.Zero(t, line.remaining())
}
