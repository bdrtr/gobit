package pgstore

import (
	"embed"
	"io/fs"
)

// MigrationOwner bu şemanın sahibi olan çekirdek bileşenin adıdır.
//
// db.Migrate sürüm defterini sahip adına göre ayırdığı için (owner başına ayrı
// <owner>_schema_migrations tablosu) sabit tek bir yerde durur: çağıran
// tarafların elle "workflow" yazması, bir gün adın değişmesi hâlinde sürüm
// defterinin sessizce ikiye bölünmesi demekti.
const MigrationOwner = "workflow"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir.
// db.Migrate kaynağı iofs.New(src, ".") ile açar, yani KÖKÜ dosyaların
// bulunduğu dizin olmalıdır.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Migrations workflow şemasının migration dosyalarını döner.
//
// Çekirdek bunları db.Migrate(ctx, dsn, pgstore.Migrations(), pgstore.MigrationOwner)
// ile uygular; geri alma db.MigrateDown ile aynı kaynağı kullanır.
func Migrations() fs.FS {
	return migrationsRoot
}

// mustSub alt dizini açar; açılamazsa panikler.
//
// Panik burada güvenlidir: dizin adı derleme zamanında sabittir ve go:embed
// dosyaların varlığını derleme zamanında zaten doğrulamıştır. Hata dönmek,
// çağıranı asla gerçekleşmeyecek bir dalı ele almaya zorlardı.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("pgstore: gömülü migration dizini açılamadı: " + err.Error())
	}
	return sub
}
