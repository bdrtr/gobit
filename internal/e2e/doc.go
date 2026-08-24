// Package e2e gerçek modüllerle uçtan uca sistem testlerini barındırır.
//
// Paketteki testlerin TAMAMI `//go:build integration` etiketlidir: gerçek bir
// PostgreSQL örneği (dolayısıyla Docker) gerektirirler. Çalıştırmak için:
// make test-integration
//
// Bu dosya bilinçli olarak ETİKETSİZDİR ve üretim kodu içermez. Sebebi
// tekniktir: paketteki her dosya etiketli olsaydı, etiket verilmeden
// derlenebilir tek bir dosya kalmaz ve `go vet ./...`, `go test ./...`,
// `golangci-lint run ./...` gibi depo geneli komutlar bu paket için
// "build constraints exclude all Go files" hatası verirdi.
//
// # Neden internal/workflows altında değil
//
// Buradaki testler GERÇEK modülleri kurar, yani internal/modules altındaki
// paketleri import eder. ADR 0006 internal/workflows altındaki her dosyaya
// modül import'unu yasaklar ve internal/arch bunu denetler; sistem testleri o
// kapsamın DIŞINDA durmalıdır. Amaçları zaten modüllerle akışların birlikte
// çalıştığını kanıtlamaktır.
package e2e
