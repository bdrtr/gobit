package link

import "github.com/bdrtr/gobit/internal/core/errors"

// DefineForTest tanımı YALNIZCA süreç içi kayıt defterine yazar; veritabanına
// dokunmaz ve tablo oluşturmaz.
//
// Yalnızca testler içindir (bu dosya üretim derlemesine girmez). Veritabanı
// gerektirmeyen yolların — doğrulama sırası, tanımsız link kapısı, boş küme
// kısa devresi — Docker olmadan sınanabilmesi için vardır; gerçek Define
// davranışı entegrasyon testlerinde doğrulanır.
func DefineForTest(svc LinkService, def LinkDefinition) error {
	s, ok := svc.(*service)
	if !ok {
		return errors.Internal("link_test_helper", "beklenen somut tip *service değil")
	}
	if err := def.Validate(); err != nil {
		return err
	}
	lt, err := newLinkTable(def)
	if err != nil {
		return err
	}
	s.defs.put(lt)
	return nil
}
