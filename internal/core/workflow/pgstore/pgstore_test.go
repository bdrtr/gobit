package pgstore_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
)

// nulKacisi JSON'da NUL karakterini yazan kaçış dizisidir: ters bölü + u0000.
// Kaynağa kaçırılmış yazılır; kaçırılmasaydı derleyici onu gerçek NUL
// karakterine çevirir ve sınanan durum kalmazdı.
const nulKacisi = "\\u0000"

// gecerliYurutme testlerde kullanılan, doğrulamadan geçen bir yürütmedir.
func gecerliYurutme() *workflow.Execution {
	return &workflow.Execution{
		Workflow: "siparis_tamamla",
		Status:   workflow.StatusRunning,
		Input:    json.RawMessage(`{"cart_id":"cart_1"}`),
	}
}

// gecerliAdim testlerde kullanılan, doğrulamadan geçen bir adım kaydıdır.
func gecerliAdim() workflow.StepRecord {
	return workflow.StepRecord{
		Name:     "stok_rezerve",
		Index:    0,
		Status:   workflow.StepInvoked,
		Attempts: 1,
	}
}

// TestNewSozlesmeyiKarsilar dönen değerin workflow.Store olduğunu doğrular.
// Motor bu paketi import etmediği için uyum yalnızca burada denetlenebilir.
func TestNewSozlesmeyiKarsilar(t *testing.T) {
	t.Parallel()

	var donen any = pgstore.New(nil, nil)

	_, uyuyor := donen.(workflow.Store)
	assert.True(t, uyuyor, "New'in döndürdüğü tip workflow.Store'u karşılamalı")
}

// TestHavuzsuzDepoUnavailable havuz kurulmamışken her metodun tipli bir
// Unavailable hatası döndüğünü doğrular; panik ya da nil işaretçi kazası olmaz.
func TestHavuzsuzDepoUnavailable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := pgstore.New(nil, nil)

	tests := map[string]func() error{
		"Create": func() error {
			return store.Create(ctx, gecerliYurutme())
		},
		"FindByIdempotencyKey": func() error {
			_, err := store.FindByIdempotencyKey(ctx, "siparis_tamamla", "ord_1")
			return err
		},
		"AppendStep": func() error {
			return store.AppendStep(ctx, "wfx_1", gecerliAdim())
		},
		"UpdateStatus": func() error {
			return store.UpdateStatus(ctx, "wfx_1", workflow.StatusCompleted, nil, "")
		},
		"Get": func() error {
			_, err := store.Get(ctx, "wfx_1")
			return err
		},
	}

	for ad, cagir := range tests {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			err := cagir()

			require.Error(t, err)
			assert.Equal(t, errors.KindUnavailable, errors.KindOf(err),
				"havuz yokken Unavailable beklenir: %v", err)
			assert.Equal(t, "workflow_store_unavailable", errors.CodeOf(err))
		})
	}
}

// TestGirdiDogrulamasi geçersiz girdinin veritabanına GİTMEDEN Invalid olarak
// döndüğünü doğrular. Havuz nil olduğu için sorguya ulaşan bir çağrı
// Unavailable dönerdi; Invalid görmek doğrulamanın önce çalıştığının kanıtıdır.
func TestGirdiDogrulamasi(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := pgstore.New(nil, nil)
	uzunAd := strings.Repeat("a", 200)

	tests := map[string]func() error{
		"nil yürütme": func() error {
			return store.Create(ctx, nil)
		},
		"boş workflow adı": func() error {
			exec := gecerliYurutme()
			exec.Workflow = "   "
			return store.Create(ctx, exec)
		},
		"aşırı uzun workflow adı": func() error {
			exec := gecerliYurutme()
			exec.Workflow = uzunAd
			return store.Create(ctx, exec)
		},
		"aşırı uzun kimlik": func() error {
			exec := gecerliYurutme()
			exec.ID = strings.Repeat("x", 200)
			return store.Create(ctx, exec)
		},
		"aşırı uzun idempotency anahtarı": func() error {
			exec := gecerliYurutme()
			exec.IdempotencyKey = strings.Repeat("k", 300)
			return store.Create(ctx, exec)
		},
		"geçersiz girdi JSON'u": func() error {
			exec := gecerliYurutme()
			exec.Input = json.RawMessage(`{bozuk`)
			return store.Create(ctx, exec)
		},
		"geçersiz çıktı JSON'u": func() error {
			exec := gecerliYurutme()
			exec.Output = json.RawMessage(`{bozuk`)
			return store.Create(ctx, exec)
		},
		"yalnızca boşluktan oluşan idempotency anahtarı": func() error {
			exec := gecerliYurutme()
			exec.IdempotencyKey = "   "
			return store.Create(ctx, exec)
		},
		"JSONB'nin çeviremediği girdi": func() error {
			exec := gecerliYurutme()
			exec.Input = json.RawMessage(`{"not":"a` + nulKacisi + `b"}`)
			return store.Create(ctx, exec)
		},
		"NUL baytlı workflow adı": func() error {
			exec := gecerliYurutme()
			exec.Workflow = "siparis\x00tamamla"
			return store.Create(ctx, exec)
		},
		"geçersiz UTF-8 taşıyan adım adı": func() error {
			rec := gecerliAdim()
			rec.Name = "stok\xff"
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"boş anahtarla arama": func() error {
			_, err := store.FindByIdempotencyKey(ctx, "siparis_tamamla", "  ")
			return err
		},
		"boş workflow adıyla arama": func() error {
			_, err := store.FindByIdempotencyKey(ctx, "", "ord_1")
			return err
		},
		"boş kimlikle okuma": func() error {
			_, err := store.Get(ctx, "")
			return err
		},
		"boş kimlikle adım yazma": func() error {
			return store.AppendStep(ctx, "", gecerliAdim())
		},
		"adı boş adım": func() error {
			rec := gecerliAdim()
			rec.Name = ""
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"durumu boş adım": func() error {
			rec := gecerliAdim()
			rec.Status = ""
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"negatif adım sırası": func() error {
			rec := gecerliAdim()
			rec.Index = -1
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"negatif deneme sayısı": func() error {
			rec := gecerliAdim()
			rec.Attempts = -3
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"geçersiz adım çıktısı": func() error {
			rec := gecerliAdim()
			rec.Output = json.RawMessage(`[1,`)
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"boş durumla güncelleme": func() error {
			return store.UpdateStatus(ctx, "wfx_1", "", nil, "")
		},
		"boş kimlikle güncelleme": func() error {
			return store.UpdateStatus(ctx, " ", workflow.StatusCompleted, nil, "")
		},
		"geçersiz çıktıyla güncelleme": func() error {
			return store.UpdateStatus(ctx, "wfx_1", workflow.StatusCompleted, json.RawMessage(`}`), "")
		},
	}

	for ad, cagir := range tests {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			err := cagir()

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err),
				"doğrulama hatası Invalid olmalı: %v", err)
			assert.Equal(t, "workflow_store_invalid", errors.CodeOf(err))
		})
	}
}

// TestGecerliGirdiDogrulamayiGecer sınır değerlerinin doğrulamayı GEÇTİĞİNİ
// doğrular: sıfır sıra, sıfır deneme, boş anahtar, nil JSON ve sıfır zamanlar
// geçerli girdilerdir; hata Unavailable'da (havuz yok) olmalıdır.
func TestGecerliGirdiDogrulamayiGecer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := pgstore.New(nil, nil)

	tests := map[string]func() error{
		"anahtarsız ve durumsuz yürütme": func() error {
			return store.Create(ctx, &workflow.Execution{Workflow: "siparis_tamamla"})
		},
		"nil JSON alanları": func() error {
			return store.Create(ctx, &workflow.Execution{
				Workflow: "siparis_tamamla",
				Status:   workflow.StatusRunning,
				Input:    nil,
				Output:   nil,
			})
		},
		"JSON null değeri": func() error {
			return store.Create(ctx, &workflow.Execution{
				Workflow: "siparis_tamamla",
				Input:    json.RawMessage(`null`),
			})
		},
		"sıfır zamanlı adım": func() error {
			rec := gecerliAdim()
			rec.Attempts = 0
			rec.StartedAt = time.Time{}
			rec.EndedAt = time.Time{}
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		// Arıza açıklaması tanı metnidir: yazılamaz baytları REDDEDİLMEZ,
		// temizlenir. Reddedilseydi yürütmenin uç durumu hiç yazılamaz, kayıt
		// sonsuza dek "running" kalırdı.
		"bozuk baytlar taşıyan arıza açıklaması": func() error {
			exec := gecerliYurutme()
			exec.Failure = "stok\x00 servisi \xff yanıt vermedi"
			return store.Create(ctx, exec)
		},
		"bozuk baytlar taşıyan adım açıklaması": func() error {
			rec := gecerliAdim()
			rec.Failure = "stok\x00 servisi \xff yanıt vermedi"
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"bozuk baytlar taşıyan uç durum açıklaması": func() error {
			return store.UpdateStatus(ctx, "wfx_1", workflow.StatusFailed, nil,
				"stok\x00 servisi \xff yanıt vermedi")
		},
	}

	for ad, cagir := range tests {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			err := cagir()

			require.Error(t, err)
			assert.Equal(t, errors.KindUnavailable, errors.KindOf(err),
				"geçerli girdi doğrulamayı geçmeli, hata yalnızca havuzdan gelmeli: %v", err)
		})
	}
}

// TestMigrationsCagriBasinaAyniKok Migrations'ın her çağrıda aynı dosyaları
// döndüğünü doğrular; çekirdek onu birden çok kez çağırabilir.
func TestMigrationsCagriBasinaAyniKok(t *testing.T) {
	t.Parallel()

	ilk := pgstore.Migrations()
	ikinci := pgstore.Migrations()

	require.NotNil(t, ilk)
	assert.Equal(t, ilk, ikinci)
}
