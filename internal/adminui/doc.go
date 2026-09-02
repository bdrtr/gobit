// Package adminui gobit'in yönetim panelidir: sunucu tarafında üretilen HTML.
//
// # Ne çekirdek ne modül — DÖRDÜNCÜ ağaç
//
// Bu paket internal/workflows'un kardeşidir ve aynı sebeple oradadır (ADR
// 0011). Modül ağacına konsaydı üç duvara birden çarpardı ve üçü de ölçüldü:
// başka hiçbir modülü import edemez, api paketinde şablonu yazıcıya veremez ve
// şablonu alan üzerinden çalıştıran doğal Go yazımı muafiyet listesine
// YAZILAMAZ bile — çağrının adı çözülemediği için. Çekirdek altına konamaz
// (çekirdek modülleri tanımaz), bileşim kökü altına konamaz (orası yalnızca
// kablolamadır).
//
// Ağacın bedeli, ADR 0006'nın internal/workflows için ödediği bedelin
// aynısıdır: kurallar ağaç adına göre yazıldığı için bu ağaç kendiliğinden
// hiçbir kablolama kuralının kapsamında değildir. Bedel [FromContainer] ile ve
// internal/arch'taki kayıt denetiminin kapsamının buraya genişletilmesiyle
// ödenir.
//
// # Neyi bilmez
//
// Modülleri BİLMEZ ve hiçbirini import etmez. Veriyi Query katmanından, dar bir
// arayüzle ve container'dan ADLA çözerek alır (ADR 0001/0004/0006); sepet akışı
// aynı kalıbın kanıtlanmış örneğidir.
//
// Bugün YALNIZCA OKUR. Katalog yazma bilinçli olarak ertelenmiştir: modüllerin
// yönetim tarafına açılmış dar bir yüzeyi yok ve açmak, üç modüle derleyicisiz
// yeni sözleşmeler eklemek demektir (ADR 0011, Karar 6).
//
// # Yanıt gövdesi çekirdeğin yazıcısından geçer
//
// HTML doğrudan yazıcıya AKITILMAZ. Şablon önce belleğe üretilir, hata olursa
// corehttp.WriteError çağrılır, ancak başarılıysa corehttp.WriteHTML'e verilir.
// Ortada doğan bir hata aksi hâlde 200 durum kodlu YARIM bir sayfa bırakırdı.
//
// # Kimlik panelin kendi ağacında kalır
//
// Panel oturumu HttpOnly bir çerezle taşınır ve çerez YALNIZCA bu ağaçta
// geçerlidir. Yönetim API'si onu kabul etmez: API'nin CSRF bağışıklığı bir
// savunmadan değil, jetonun tarayıcının kendiliğinden eklemediği bir başlıkta
// durmasından gelir ve çerezi oraya açmak o bağışıklığı yok ederdi (ADR 0011,
// Karar 3).
package adminui
