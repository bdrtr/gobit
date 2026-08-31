// Package smoke uygulamayı GERÇEK bir süreç olarak açıp davranışını sınar.
//
// Paketteki testlerin TAMAMI `//go:build smoke` etiketlidir: ikiliyi derler,
// testcontainers ile Postgres ve Redis kaldırır ve gerçek süreçler başlatır.
// Çalıştırmak için: make smoke
//
// Bu dosya bilinçli olarak ETİKETSİZDİR ve üretim kodu içermez. Sebebi
// tekniktir: paketteki her dosya etiketli olsaydı, etiket verilmeden
// derlenebilir tek bir dosya kalmaz ve `go vet ./...`, `go test ./...`,
// `golangci-lint run ./...` gibi depo geneli komutlar bu paket için
// "build constraints exclude all Go files" hatası verirdi. Aynı çözüm
// internal/e2e/doc.go dosyasındadır.
//
// # Neden entegrasyon etiketine karıştırılmadı
//
// Buradaki her senaryo `go build` ile derlenmiş bir ikiliyi çalıştırır ve
// açılışın tamamını (migration'lar dahil) bekler. Entegrasyon etiketine
// eklemek, süreç başlatmayan yüzlerce testin de bu maliyeti her koşumda
// ödemesi demekti; ayrı etiket, ayrı Makefile hedefi ve ayrı CI işi bu yüzden.
package smoke
