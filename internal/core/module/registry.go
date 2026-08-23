package module

import (
	"context"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
)

// MigrateFunc bir modülün migration'larını uygulayan işlevdir.
//
// Registry'nin internal/core/db paketine doğrudan bağlanmaması için işlev
// olarak dışarıdan verilir; böylece registry veritabanı olmadan test edilebilir.
// owner, versiyon tablosunu ayırmak için kullanılan modül adıdır.
type MigrateFunc func(ctx context.Context, src fs.FS, owner string) error

// Registry tüm modülleri tutar ve sırayla Register/Migrate/Routes çağırır
// (plan Bölüm 5.1).
type Registry struct {
	modules []Module
	migrate MigrateFunc
	log     *slog.Logger
}

// NewRegistry boş bir modül kaydı oluşturur.
// migrate nil ise migration adımı atlanır (testlerde kullanışlıdır).
func NewRegistry(log *slog.Logger, migrate MigrateFunc) *Registry {
	if log == nil {
		log = slog.Default()
	}
	return &Registry{migrate: migrate, log: log}
}

// Add bir modülü kayda ekler. Modüller eklenme sırasına göre işlenir.
func (r *Registry) Add(mod Module) {
	r.modules = append(r.modules, mod)
}

// Modules kayıtlı modülleri eklenme sırasıyla döner.
func (r *Registry) Modules() []Module {
	return append([]Module(nil), r.modules...)
}

// Bootstrap modülleri sırayla ayağa kaldırır:
// önce adları doğrular, sonra tüm modüllerin Register'ını, ardından
// migration'larını, en son route'larını çalıştırır.
//
// Sıra bilinçlidir: TÜM modüller Register olmadan hiçbiri route bağlamaz,
// böylece bir modülün handler'ı başka modülün servisini güvenle çözebilir.
func (r *Registry) Bootstrap(ctx context.Context, c *container.Container, router chi.Router) error {
	if err := r.validateNames(); err != nil {
		return err
	}
	if err := r.registerAll(ctx, c); err != nil {
		return err
	}
	if err := r.migrateAll(ctx); err != nil {
		return err
	}
	r.mountRoutes(router)

	r.log.InfoContext(ctx, "modüller ayağa kaldırıldı", "sayi", len(r.modules))
	return nil
}

// validateNames modül adlarının boş olmadığını ve tekrarlanmadığını doğrular.
func (r *Registry) validateNames() error {
	seen := make(map[string]struct{}, len(r.modules))
	for _, mod := range r.modules {
		name := mod.Name()
		if name == "" {
			return errors.Invalid("module_name_empty", "modül adı boş olamaz")
		}
		if _, dup := seen[name]; dup {
			return errors.Conflict("module_name_duplicate", "modül adı tekrarlandı: %s", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// registerAll her modülün servislerini container'a kaydeder.
func (r *Registry) registerAll(ctx context.Context, c *container.Container) error {
	for _, mod := range r.modules {
		if err := mod.Register(ctx, c); err != nil {
			return errors.Wrap(err, errors.KindOf(err), "module_register_failed",
				"%s modülü kaydedilemedi", mod.Name())
		}
		r.log.DebugContext(ctx, "modül kaydedildi", "modul", mod.Name())
	}
	return nil
}

// migrateAll her modülün migration'larını kendi versiyon tablosuna uygular.
func (r *Registry) migrateAll(ctx context.Context) error {
	if r.migrate == nil {
		r.log.DebugContext(ctx, "migration işlevi verilmedi, migration atlanıyor")
		return nil
	}
	for _, mod := range r.modules {
		src := mod.Migrations()
		if src == nil {
			continue
		}
		if err := r.migrate(ctx, src, mod.Name()); err != nil {
			return errors.Wrap(err, errors.KindOf(err), "module_migrate_failed",
				"%s modülünün migration'ları uygulanamadı", mod.Name())
		}
		r.log.DebugContext(ctx, "modül migration'ları uygulandı", "modul", mod.Name())
	}
	return nil
}

// mountRoutes her modülün route'larını router'a bağlar.
func (r *Registry) mountRoutes(router chi.Router) {
	if router == nil {
		return
	}
	for _, mod := range r.modules {
		mod.Routes(router)
	}
}
