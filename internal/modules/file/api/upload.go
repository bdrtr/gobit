package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// sniffBoyutu içerik tipi tespiti için okunan bayt sayısıdır.
//
// 512, [net/http.DetectContentType]'ın baktığı azami uzunluktur; daha fazlası
// hiçbir şey eklemez, daha azı bazı imzaları kaçırırdı.
const sniffBoyutu = 512

// zarfPayi multipart zarfının (sınırlayıcılar ve parça başlıkları) gövdeye
// eklediği paydır.
//
// Boyut sınırı DOSYAYA konur ama [net/http.MaxBytesReader] isteğin TAMAMINI
// sayar. Pay bırakılmasaydı tam sınırdaki bir dosya, sırf zarfı yüzünden
// reddedilir ve hata "dosyan çok büyük" derdi — oysa dosya sınırın tam
// kendisiydi. Payın kendisi de sınırsız değildir: dosyanın gerçek boyutunu
// servisteki sayaç ayrıca ve TAM olarak zorlar (bkz. service.sinirliOkuyucu),
// yani buradaki gevşeklik yalnızca zarfa tanınır.
const zarfPayi int64 = 8 << 10 // 8 KiB

// createUpload POST /admin/v1/uploads handler'ıdır.
//
// # Gövde JSON DEĞİL multipart/form-data'dır
//
// Bu, depoda istemciden RASTGELE BAYT kabul edilen tek yoldur ve akışın her
// adımı bu yüzden açıkça yazılmıştır:
//
//  1. Gövde [net/http.MaxBytesReader] ile SARILIR. Sınırsız bir gövde, tek
//     istekle diski (ve belleği) doldurmanın en ucuz yoludur.
//  2. Ayrıştırma AKIŞLA yapılır ([net/http.Request.MultipartReader]), form
//     ayrıştırıcısıyla değil. r.ParseMultipartForm bellekte tutamadığı
//     parçaları GEÇİCİ DOSYALARA yazar — yani henüz hiçbir denetimden
//     geçmemiş baytları diske indirir. Kaçınmaya çalıştığımız şeyin ta
//     kendisi.
//  3. İlk 512 bayt okunur ve içerik tipi İÇERİKTEN tespit edilir. İstemcinin
//     Content-Type başlığı bir İDDİADIR, olgu değil: "image/png" diye
//     gönderilen bir HTML dosyası, ona güvenen bir izin listesinden geçer ve
//     sunulduğunda tarayıcıda çalışır.
//  4. Okunan baştaki baytlar [io.MultiReader] ile akışın önüne geri konur;
//     yoksa dosyanın ilk 512 baytı kaybolurdu.
//  5. İzin listesi servis katmanında, depoya TEK BAYT yazılmadan uygulanır.
func (h *Handler) createUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Sınır, servisin sınırıyla AYNI kaynaktan okunur; iki yerde ayrı ayrı
	// yazmak ikisinin sessizce ayrışması demek olurdu.
	r.Body = http.MaxBytesReader(w, r.Body, h.svc.MaxUploadBytes()+zarfPayi)

	parcalar, err := r.MultipartReader()
	if err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"istek gövdesi multipart/form-data olmalı ve %q alanını taşımalıdır", fieldFile))

		return
	}

	dosya, err := dosyaParcasi(parcalar)
	if err != nil {
		corehttp.WriteError(ctx, w, boyutHatasi(err))

		return
	}

	bas, err := basiOku(dosya)
	if err != nil {
		corehttp.WriteError(ctx, w, boyutHatasi(err))

		return
	}

	kayit, err := h.svc.Upload(ctx, service.UploadInput{
		ContentType: icerikTipi(bas),
		Body:        io.MultiReader(bytes.NewReader(bas), dosya),
		// [mime/multipart.Part.FileName] adı RFC 7578 §4.2 gereği zaten
		// [path/filepath.Base]'den geçirir, yani "../../etc/passwd" buraya
		// "passwd" olarak ulaşır. Bu KORUMAMIZ DEĞİLDİR ve ona
		// güvenilmiyor: korumamız, adın hiçbir yol ifadesine hiç girmemesi.
		// Ayrım önemlidir — stdlib'in davranışına dayanan bir tasarım,
		// istemci adını başka bir yoldan (örn. bir JSON alanından) alan ilk
		// değişiklikte sessizce çöker.
		OriginalName: dosya.FileName(),
		UploadedBy:   cagiranKimligi(r),
	})
	if err != nil {
		corehttp.WriteError(ctx, w, boyutHatasi(err))

		return
	}

	// Fazladan parça, yüklenen dosyayı GERİ ALIR.
	//
	// Sessizce yok saymak, istemcinin gönderdiğini sandığı ikinci dosyanın
	// hiçbir yere gitmemesi demek olurdu ve bu ancak "ikinci görselim nerede"
	// diye aranınca fark edilirdi. Denetim yüklemeden SONRA yapılır çünkü
	// multipart akışında bir sonraki parçanın varlığı ancak öncekinin tamamı
	// okunduktan sonra bilinir.
	if err := h.fazlaParcayiReddet(r, parcalar, kayit.ID); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	writeItem(w, r, http.StatusCreated, toUploadDTO(kayit))
}

// dosyaParcasi multipart akışından dosya parçasını bulur.
//
// Beklenmeyen bir alan adı REDDEDİLİR, atlanmaz. Sessizce atlamak, adını
// yanlış yazmış bir istemciye "dosya alanı yok" yerine daha da kafa
// karıştırıcı bir hata verirdi; üstelik bu modülün okuduğu tek alan zaten
// budur ve okunmayan bir alanı kabul etmek, çalışmayan bir vaat olurdu.
func dosyaParcasi(parcalar *multipart.Reader) (*multipart.Part, error) {
	parca, err := parcalar.NextPart()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, coreerrors.Invalid(codeInvalidRequest,
				"istek gövdesinde %q alanı yok", fieldFile)
		}

		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"multipart gövdesi çözümlenemedi")
	}

	if parca.FormName() != fieldFile {
		return nil, coreerrors.Invalid(codeInvalidRequest,
			"beklenmeyen alan %q; yalnızca %q alanı okunur", parca.FormName(), fieldFile)
	}

	return parca, nil
}

// fazlaParcayiReddet dosya parçasından sonra başka parça olmadığını doğrular;
// varsa yüklenen kaydı siler ve hata döner.
func (h *Handler) fazlaParcayiReddet(r *http.Request, parcalar *multipart.Reader, uploadID string) error {
	_, err := parcalar.NextPart()
	if errors.Is(err, io.EOF) {
		return nil
	}

	// Geri alma, isteğin bağlamı iptal edilmiş olsa bile denenmelidir; aksi
	// hâlde reddedilen her istek depoda bir dosya bırakırdı.
	if delErr := h.svc.DeleteUpload(context.WithoutCancel(r.Context()), uploadID); delErr != nil {
		corehttp.LoggerFromContext(r.Context()).ErrorContext(r.Context(),
			"reddedilen yükleme geri alınamadı",
			"error", delErr,
			"upload_id", uploadID,
			"anlami", "depoda kaydı silinmemiş bir dosya kaldı; elle temizlenmeli")
	}

	if err != nil {
		return boyutHatasi(coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"multipart gövdesi çözümlenemedi"))
	}

	return coreerrors.Invalid(codeInvalidRequest,
		"istek gövdesi tek bir %q alanı taşımalıdır", fieldFile)
}

// basiOku içerik tipi tespiti için gövdenin başını okur.
//
// Dosya 512 bayttan küçükse eksik okuma bir HATA DEĞİLDİR; tespit elde olan
// baytlarla yapılır. Hiç bayt okunamaması ise hatadır: sıfır baytlık bir
// yüklemenin ne tipi tespit edilebilir ne de sunulacak bir içeriği vardır.
func basiOku(r io.Reader) ([]byte, error) {
	bas := make([]byte, sniffBoyutu)

	n, err := io.ReadFull(r, bas)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"dosya okunamadı")
	}

	if n == 0 {
		return nil, coreerrors.Invalid(codeInvalidRequest, "dosya boş olamaz")
	}

	return bas[:n], nil
}

// icerikTipi baştaki baytlardan içerik tipini tespit eder.
//
// Parametreler (örn. "; charset=utf-8") ATILIR: [net/http.DetectContentType]
// metin tiplerine karakter kümesi ekler ve ham dize izin listesindeki
// "image/png" gibi çıplak tiplerle hiçbir zaman eşleşmezdi. Ayrıştırma
// başarısız olursa ham değer olduğu gibi döner ve izin listesinden geçemez —
// tanınmayan bir tipin doğru cevabı zaten reddedilmektir.
func icerikTipi(bas []byte) string {
	ham := http.DetectContentType(bas)

	tip, _, err := mime.ParseMediaType(ham)
	if err != nil {
		return ham
	}

	return tip
}

// boyutHatasi gövde sınırının aşıldığı hatalarını tipli bir istemci hatasına
// çevirir.
//
// [net/http.MaxBytesReader]'ın hatası zincirin herhangi bir yerinde olabilir:
// multipart ayrıştırıcısı ya da sağlayıcı onu sarmalayarak döndürür. Tipiyle
// aranmasının sebebi budur.
//
// # Neden 413 değil 422
//
// Status kodunu handler SEÇMEZ (plan Bölüm 2.7): kod, hatanın sınıfından
// türetilir ve çekirdeğin sınıf kümesinde 413'ün karşılığı yoktur. Tek bir uç
// için o kuralı delmek, hata sınıflandırmasının tek yerde durması ilkesini
// bozardı — ve istemcinin gerçekten dallanacağı şey status değil,
// makine tarafından okunabilen koddur ([service.CodeTooLarge]).
func boyutHatasi(err error) error {
	var buyuk *http.MaxBytesError
	if errors.As(err, &buyuk) {
		return coreerrors.Wrap(err, coreerrors.KindInvalid, service.CodeTooLarge,
			"istek gövdesi en fazla %d bayt olabilir", buyuk.Limit)
	}

	return err
}

// cagiranKimligi isteği yapan çağıranın kimliğini döner; yoksa boş dize.
//
// Kimlik defterde SERBEST METİNDİR, foreign key değildir (Prensip 2.2): auth
// modülünün tablosuna bağlanmak modül izolasyonunu kırardı. Boş kalması da
// mümkündür — bu uç korumalı olduğu için normal akışta olmaz, ama gömülü
// kullanımda handler doğrudan çağrılabilir ve o durumda "kim yükledi"
// bilinmiyor demek, uydurmaktan iyidir.
func cagiranKimligi(r *http.Request) string {
	kimlik, ok := corehttp.PrincipalFromContext(r.Context())
	if !ok {
		return ""
	}

	return kimlik.ID
}
