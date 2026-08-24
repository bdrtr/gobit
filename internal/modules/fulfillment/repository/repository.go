// Package repository fulfillment modülünün veritabanı erişimidir.
//
// SADECE bu modülün tablolarına dokunur (plan Bölüm 4). sqlc üretimi kod
// repository/fulfillmentdb altındadır ve elle düzenlenmez; bu paket onun
// üstüne iki şey ekler:
//
//   - Çeviri: pgtype ve üretilmiş satır tipleri BU PAKETİN DIŞINA ÇIKMAZ,
//     models tiplerine çevrilir.
//   - Sınıflandırma: sürücü hataları core/errors tipli hatalarına çevrilir;
//     satır bulunamaması NotFound, benzersizlik ihlali Conflict olur.
//
// # İşlem (transaction) taşınması
//
// [Repository.WithTx] bir işlem açar ve onu CONTEXT'e koyar; işlem boyunca
// çağrılan tüm repository metodları o context'i aldıkları sürece aynı işlemde
// çalışır. Bunun alternatifi, işlem tutamağını taşıyan ayrı bir arayüz tipini
// metot imzalarına koymaktı; o durumda servis kendi paketinde tanımladığı dar
// arayüzle bu paketi YAPISAL OLARAK eşleştiremezdi — Go'da imzadaki
// adlandırılmış tipler birebir aynı olmak zorundadır, yani servis repository'yi
// import etmek zorunda kalırdı. Context ile taşımak imzaları iki tarafın da
// paylaştığı tiplere (context.Context, models.*) indirger.
//
// Kilit alan metotlar (Lock...) işlem DIŞINDA çağrılırsa hata döner: FOR UPDATE
// kilidi işlem bitince serbest kalacağı için, işlemsiz bir kilit sessizce
// hiçbir şey korumazdı.
//
// # İki ayrı defter
//
// Bu paket iki farklı sahibin verisine hizmet eder: fulfillment modülünün alan
// tabloları (shipping_profiles, shipping_options, shipping_option_rules,
// fulfillments fulfillment_items) ve MANUEL SAĞLAYICININ kendi defteri
// (fulfillment_manual_shipments). İkincisi modülün alan verisi değildir;
// taklit edilen dış sistemin durumudur ve ona yalnızca manual paketi dokunur.
// Ayrım fiziksel olarak da korunur: servisin service.Store arayüzünde manuel
// defter metotları YOKTUR.
package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository/fulfillmentdb"
)

// rollbackTimeout iptal edilmiş bir bağlamda geri almaya tanınan süredir.
// Geri alma, çağıranın ctx'i dolmuş olsa da denenmelidir; aksi hâlde işlem
// bağlantı havuza dönene kadar açık kalırdı.
const rollbackTimeout = 5 * time.Second

// txKeyType context anahtarının tipidir; dışarıdan üretilemesin diye dışa
// açık değildir.
type txKeyType struct{}

// txKey işlem tutamağının context'teki anahtarıdır.
var txKey = txKeyType{}

// Repository fulfillment tablolarına erişimdir. Eşzamanlı kullanıma
// güvenlidir.
type Repository struct {
	pool *pgxpool.Pool
}

// New verilen havuz üzerinde çalışan bir Repository üretir.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// WithTx fn'i tek bir veritabanı işleminde çalıştırır.
//
// fn'e verilen context işlemi taşır; o context ile çağrılan tüm repository
// metodları aynı işlemde koşar. fn hata dönerse ya da panikler ise işlem geri
// alınır, hata (panikte panik) yukarı verilir.
//
// Çağrı iç içe gelirse yeni bir işlem AÇILMAZ, var olan kullanılır: iç içe
// işlem açmak PostgreSQL'de savepoint demektir ve dıştaki işlemin atomikliği
// konusunda yanıltıcı bir güven verirdi. Manuel sağlayıcı aynı depoyu
// paylaştığı için çağrıları servisin işlemine bu sayede KATILIR.
func (r *Repository) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return classify(err, codeTxBeginFailed, "işlem başlatılamadı")
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// Bağlamdan bağımsız kısa ömürlü bir context kullanılır: çağıranın
		// ctx'i iptal edilmişse onunla yapılan geri alma da anında düşerdi.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if err := fn(context.WithValue(ctx, txKey, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return classify(err, codeTxCommitFailed, "işlem tamamlanamadı")
	}
	committed = true
	return nil
}

// txFromContext context'teki işlem tutamağını döner.
func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey).(pgx.Tx)
	return tx, ok
}

// queries context'e uygun sorgu kümesini döner: işlem varsa ona, yoksa havuza
// bağlı olanı.
func (r *Repository) queries(ctx context.Context) *fulfillmentdb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return fulfillmentdb.New(tx)
	}
	return fulfillmentdb.New(r.pool)
}

// requireTx kilit alan metotların işlem içinde çağrıldığını doğrular.
func requireTx(ctx context.Context, op string) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"%s işlem (transaction) içinde çağrılmalı; işlemsiz bir FOR UPDATE kilidi hiçbir şeyi korumaz", op)
	}
	return nil
}
