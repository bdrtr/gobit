package link_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
)

// gecerliTanim testlerde başlangıç noktası olarak kullanılan geçerli tanımdır.
func gecerliTanim() link.LinkDefinition {
	return link.LinkDefinition{
		Name:        "product_price",
		From:        link.LinkSide{Module: "product", Field: "variant_id"},
		To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: link.OneToMany,
	}
}

func TestCardinalityString(t *testing.T) {
	assert.Equal(t, "one_to_one", link.OneToOne.String())
	assert.Equal(t, "one_to_many", link.OneToMany.String())
	assert.Equal(t, "many_to_many", link.ManyToMany.String())
	assert.Equal(t, "unknown(9)", link.Cardinality(9).String(),
		"tanımsız değer sessizce geçerli bir ada dönüşmemeli")

	// Sıfır değer EN KATI kısıt olmalı; aksi hâlde bildirilmemiş bir
	// kardinalite sessizce serbest ilişkiye izin verirdi.
	assert.Equal(t, link.OneToOne, link.Cardinality(0))
}

func TestCardinalityValid(t *testing.T) {
	assert.True(t, link.OneToOne.Valid())
	assert.True(t, link.OneToMany.Valid())
	assert.True(t, link.ManyToMany.Valid())
	assert.False(t, link.Cardinality(3).Valid())
	assert.False(t, link.Cardinality(255).Valid())
}

func TestLinkSideString(t *testing.T) {
	side := link.LinkSide{Module: "product", Field: "variant_id"}
	assert.Equal(t, "product.variant_id", side.String())
}

func TestLinkDefinitionString(t *testing.T) {
	assert.Equal(t,
		"product_price(product.variant_id -> pricing.price_set_id, one_to_many)",
		gecerliTanim().String())
}

func TestLinkDefinitionValidateAccepts(t *testing.T) {
	gecerli := []link.LinkDefinition{
		gecerliTanim(),
		{
			Name:        "a",
			From:        link.LinkSide{Module: "a", Field: "b"},
			To:          link.LinkSide{Module: "c", Field: "d"},
			Cardinality: link.OneToOne,
		},
		{
			// Aynı modülün kendi içindeki ilişkisi (örn. ilişkili ürünler)
			// geçerlidir: link tablosunda sütun adları sabit olduğu için iki
			// ucun aynı alan adını taşıması sorun değildir.
			Name:        "product_related",
			From:        link.LinkSide{Module: "product", Field: "product_id"},
			To:          link.LinkSide{Module: "product", Field: "product_id"},
			Cardinality: link.ManyToMany,
		},
		{
			Name:        strings.Repeat("a", 40),
			From:        link.LinkSide{Module: "product", Field: "variant_id"},
			To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
			Cardinality: link.ManyToMany,
		},
	}

	for _, def := range gecerli {
		t.Run(def.Name, func(t *testing.T) {
			require.NoError(t, def.Validate())

			table, err := link.TableName(def.Name)
			require.NoError(t, err)
			assert.Equal(t, "link_"+def.Name, table)
			assert.LessOrEqual(t, len(table+"_from_uniq"), 63,
				"türetilen en uzun ad PostgreSQL tanımlayıcı sınırını geçmemeli")
		})
	}
}

// TestLinkDefinitionValidateRejectsNames adın tablo adına çevrildiği tek yerin
// doğrulamadan geçtiğini kanıtlar. Enjeksiyon denemeleri bilinçli olarak
// listenin içindedir: bu adlar geçseydi doğrudan DDL metnine girerlerdi.
func TestLinkDefinitionValidateRejectsNames(t *testing.T) {
	kotuAdlar := map[string]string{
		"boş":                     "",
		"noktalı virgül":          "product; DROP TABLE customers",
		"yorum":                   "product--",
		"tırnak":                  `product" (x int); --`,
		"tek tırnak":              "product' OR '1'='1",
		"parantez":                "product)",
		"boşluk":                  "product price",
		"tab":                     "product\tprice",
		"satır sonu":              "product\nprice",
		"nokta":                   "pg_catalog.pg_tables",
		"büyük harf":              "Product",
		"rakamla başlıyor":        "1product",
		"alt çizgiyle başlıyor":   "_product",
		"tire":                    "product-price",
		"türkçe karakter":         "ürün_fiyat",
		"yıldız":                  "*",
		"çok uzun":                strings.Repeat("a", 41),
		"ayrılmış (defter tablo)": "definitions",
	}

	for ad, name := range kotuAdlar {
		t.Run(ad, func(t *testing.T) {
			def := gecerliTanim()
			def.Name = name

			err := def.Validate()

			require.Error(t, err, "geçersiz ad kabul edilmemeli: %q", name)
			assert.True(t, errors.IsInvalid(err),
				"hata sınıfı KindInvalid olmalı, %v alındı", errors.KindOf(err))
			assert.Equal(t, "link_name_invalid", errors.CodeOf(err))

			table, tableErr := link.TableName(name)
			require.Error(t, tableErr, "TableName aynı adı reddetmeli")
			assert.Empty(t, table, "geçersiz addan tablo adı üretilmemeli")
		})
	}
}

func TestLinkDefinitionValidateRejectsSides(t *testing.T) {
	bozukUclar := map[string]link.LinkDefinition{
		"From modülü boş": {
			Name: "x", From: link.LinkSide{Field: "a"},
			To: link.LinkSide{Module: "b", Field: "c"},
		},
		"From alanı boş": {
			Name: "x", From: link.LinkSide{Module: "a"},
			To: link.LinkSide{Module: "b", Field: "c"},
		},
		"To modülü boş": {
			Name: "x", From: link.LinkSide{Module: "a", Field: "b"},
			To: link.LinkSide{Field: "c"},
		},
		"To alanı boş": {
			Name: "x", From: link.LinkSide{Module: "a", Field: "b"},
			To: link.LinkSide{Module: "c"},
		},
		"From modülünde enjeksiyon": {
			Name: "x", From: link.LinkSide{Module: "a; DROP TABLE t", Field: "b"},
			To: link.LinkSide{Module: "c", Field: "d"},
		},
		"To alanında enjeksiyon": {
			Name: "x", From: link.LinkSide{Module: "a", Field: "b"},
			To: link.LinkSide{Module: "c", Field: `d" --`},
		},
	}

	for ad, def := range bozukUclar {
		t.Run(ad, func(t *testing.T) {
			def.Cardinality = link.OneToOne

			err := def.Validate()

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err),
				"hata sınıfı KindInvalid olmalı, %v alındı", errors.KindOf(err))
			assert.Equal(t, "link_side_invalid", errors.CodeOf(err))
		})
	}
}

func TestLinkDefinitionValidateRejectsUnknownCardinality(t *testing.T) {
	def := gecerliTanim()
	def.Cardinality = link.Cardinality(7)

	err := def.Validate()

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, "link_cardinality_invalid", errors.CodeOf(err))
}

// TestUndefinedLinkIsNotFound tanımlanmamış bir link üzerinde yapılan her
// çağrının, veritabanına hiç gitmeden teşhis edilebilir bir NotFound ürettiğini
// doğrular. Bu kapı aynı zamanda güvenlik sınırıdır: tanımsız bir ad SQL'e
// asla ulaşmaz.
func TestUndefinedLinkIsNotFound(t *testing.T) {
	ctx := context.Background()
	svc := link.New(nil, nil)

	t.Run("Create", func(t *testing.T) {
		bekleneniDogrula(t, svc.Create(ctx, "yok", "a", "b"))
	})
	t.Run("Delete", func(t *testing.T) {
		bekleneniDogrula(t, svc.Delete(ctx, "yok", "a", "b"))
	})
	t.Run("List", func(t *testing.T) {
		ids, err := svc.List(ctx, "yok", "a")
		assert.Nil(t, ids)
		bekleneniDogrula(t, err)
	})
	t.Run("ListMany", func(t *testing.T) {
		ids, err := svc.ListMany(ctx, "yok", []string{"a"})
		assert.Nil(t, ids)
		bekleneniDogrula(t, err)
	})
	t.Run("Definition", func(t *testing.T) {
		def, err := svc.Definition(ctx, "yok")
		assert.Equal(t, link.LinkDefinition{}, def)
		bekleneniDogrula(t, err)
	})
}

// bekleneniDogrula tanımsız link hatasının sınıfını ve kodunu doğrular.
func bekleneniDogrula(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err),
		"hata sınıfı KindNotFound olmalı, %v alındı", errors.KindOf(err))
	assert.Equal(t, "link_not_defined", errors.CodeOf(err))
	assert.Contains(t, err.Error(), "tanımlı linkler", "mesaj tanımlı adları saymalı")
}

// TestDefineWithoutPoolIsUnavailable havuzsuz bir servisin panik yerine tipli
// hata döndürdüğünü doğrular; kurulum sırası hataları böyle görünür olur.
func TestDefineWithoutPoolIsUnavailable(t *testing.T) {
	err := link.New(nil, nil).Define(context.Background(), gecerliTanim())

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"hata sınıfı KindUnavailable olmalı, %v alındı", errors.KindOf(err))
	assert.Equal(t, "link_db_unavailable", errors.CodeOf(err))
}

// TestDefineValidatesBeforeTouchingDatabase geçersiz tanımın havuz olmadan da
// doğrulama hatası verdiğini, yani doğrulamanın veritabanından ÖNCE çalıştığını
// gösterir.
func TestDefineValidatesBeforeTouchingDatabase(t *testing.T) {
	def := gecerliTanim()
	def.Name = "product; DROP TABLE customers"

	err := link.New(nil, nil).Define(context.Background(), def)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err),
		"doğrulama hatası, havuz hatasından ÖNCE dönmeli; %v alındı", errors.KindOf(err))
	assert.Equal(t, "link_name_invalid", errors.CodeOf(err))
}

// TestListManyWithNoIDsSkipsQuery boş kümenin veritabanına hiç gitmediğini
// gösterir: havuz olmamasına rağmen çağrı başarılıdır.
func TestListManyWithNoIDsSkipsQuery(t *testing.T) {
	svc := tanimliServis(t)

	result, err := svc.ListMany(context.Background(), gecerliTanim().Name, nil)

	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestDefinitionReturnsDeclaredDefinition Query katmanının bir linkin hangi
// modüle çözüldüğünü öğrenebildiğini doğrular (ADR 0004).
func TestDefinitionReturnsDeclaredDefinition(t *testing.T) {
	def, err := tanimliServis(t).Definition(context.Background(), gecerliTanim().Name)

	require.NoError(t, err)
	assert.Equal(t, gecerliTanim(), def)
}

// TestDefinitionHonorsCanceledContext bellekten okunan yolun bile bağlam
// bütçesine uyduğunu doğrular (plan Bölüm 8).
func TestDefinitionHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tanimliServis(t).Definition(ctx, gecerliTanim().Name)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"hata sınıfı KindUnavailable olmalı, %v alındı", errors.KindOf(err))
	assert.Equal(t, "link_canceled", errors.CodeOf(err))
	assert.True(t, errors.Is(err, context.Canceled))
}

// TestIDValidationRunsBeforeDatabase kimlik doğrulamasının havuza gitmeden
// çalıştığını gösterir: havuzsuz serviste bile hata KindInvalid'dir.
func TestIDValidationRunsBeforeDatabase(t *testing.T) {
	ctx := context.Background()
	name := gecerliTanim().Name
	svc := tanimliServis(t)
	uzunID := strings.Repeat("x", 256)

	cagrilar := map[string]func() error{
		"Create fromID boş":      func() error { return svc.Create(ctx, name, "", "b") },
		"Create fromID boşluk":   func() error { return svc.Create(ctx, name, "   ", "b") },
		"Create toID boş":        func() error { return svc.Create(ctx, name, "a", "") },
		"Create toID çok uzun":   func() error { return svc.Create(ctx, name, "a", uzunID) },
		"Delete fromID boş":      func() error { return svc.Delete(ctx, name, "", "b") },
		"Delete toID boş":        func() error { return svc.Delete(ctx, name, "a", "") },
		"List fromID boş":        func() error { _, err := svc.List(ctx, name, ""); return err },
		"ListMany fromID boş":    func() error { _, err := svc.ListMany(ctx, name, []string{"a", ""}); return err },
		"ListMany ID çok uzun":   func() error { _, err := svc.ListMany(ctx, name, []string{uzunID}); return err },
		"List fromID çok uzun":   func() error { _, err := svc.List(ctx, name, uzunID); return err },
		"Create fromID çok uzun": func() error { return svc.Create(ctx, name, uzunID, "b") },

		// Baş/son boşluk taşıyan kimlikler: kırpılmış hâlleri boş olmadığı için
		// eskiden doğrulamadan geçip veritabanına yazılırdı.
		"Create fromID sonunda boşluk": func() error { return svc.Create(ctx, name, "var_1 ", "ps_1") },
		"Create fromID başında boşluk": func() error { return svc.Create(ctx, name, " var_1", "ps_1") },
		"Create toID satır sonu":       func() error { return svc.Create(ctx, name, "var_1", "ps_1\n") },
		"Delete fromID sekme":          func() error { return svc.Delete(ctx, name, "var_1\t", "ps_1") },
		"List fromID sonunda boşluk":   func() error { _, err := svc.List(ctx, name, "var_1 "); return err },
		"ListMany ID satır sonu": func() error {
			_, err := svc.ListMany(ctx, name, []string{"var_1", "var_2\n"})
			return err
		},
		"ListManyByTo ID başında boşluk": func() error {
			_, err := svc.ListManyByTo(ctx, name, []string{" ps_1"})
			return err
		},
	}

	for ad, cagri := range cagrilar {
		t.Run(ad, func(t *testing.T) {
			err := cagri()

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err),
				"kimlik doğrulaması havuz kontrolünden ÖNCE çalışmalı; %v alındı", errors.KindOf(err))
			assert.Equal(t, "link_id_invalid", errors.CodeOf(err))
		})
	}
}

// TestPaddedIDIsRejectedNotTrimmed kimliğin sessizce KIRPILMADIĞINI, baştan
// reddedildiğini sabitler.
//
// Kırpma da bir seçenekti; reddetme seçildi çünkü kırpma çağıranın gönderdiği
// kimlikle sakladığımız kimliği ayırır ve fark yalnızca veri bozulduktan sonra
// görünür. Reddetme, kaymayı ilk çağrıda ve tipli bir hatayla bildirir.
//
// İç boşluk KISITLANMAZ: kimlik serbest bir dizgedir, kural yalnızca dış
// kaynaktan (CSV, HTTP başlığı, JSON) bulaşan baş/son boşluğu hedefler.
func TestPaddedIDIsRejectedNotTrimmed(t *testing.T) {
	ctx := context.Background()
	name := gecerliTanim().Name
	svc := tanimliServis(t)

	err := svc.Create(ctx, name, "var_1 ", "ps_1")

	require.Error(t, err, "kırpılmış hâli dolu olsa da baş/son boşluklu kimlik kabul edilmemeli")
	assert.True(t, errors.IsInvalid(err),
		"boşluk kayması veri hatasıdır, yeniden denenecek bir altyapı hatası değil; %v alındı",
		errors.KindOf(err))
	assert.Equal(t, "link_id_invalid", errors.CodeOf(err))
	assert.Contains(t, err.Error(), "fromID", "hata hangi ucun kusurlu olduğunu söylemeli")

	// İç boşluk hâlâ geçerlidir: çağrı doğrulamayı geçip havuz kontrolüne
	// düştüğü için hata KindUnavailable olmalı, KindInvalid değil.
	icBosluklu := svc.Create(ctx, name, "var 1", "ps_1")

	require.Error(t, icBosluklu)
	assert.True(t, errors.HasKind(icBosluklu, errors.KindUnavailable),
		"iç boşluk yasaklanmamalı; %v alındı", errors.KindOf(icBosluklu))
}

// TestWritePathsWithoutPoolAreUnavailable doğrulamayı geçen ama havuz
// bulamayan çağrıların tipli KindUnavailable döndüğünü doğrular.
func TestWritePathsWithoutPoolAreUnavailable(t *testing.T) {
	ctx := context.Background()
	name := gecerliTanim().Name
	svc := tanimliServis(t)

	cagrilar := map[string]func() error{
		"Create":   func() error { return svc.Create(ctx, name, "a", "b") },
		"Delete":   func() error { return svc.Delete(ctx, name, "a", "b") },
		"List":     func() error { _, err := svc.List(ctx, name, "a"); return err },
		"ListMany": func() error { _, err := svc.ListMany(ctx, name, []string{"a"}); return err },
	}

	for ad, cagri := range cagrilar {
		t.Run(ad, func(t *testing.T) {
			err := cagri()

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindUnavailable),
				"hata sınıfı KindUnavailable olmalı, %v alındı", errors.KindOf(err))
			assert.Equal(t, "link_db_unavailable", errors.CodeOf(err))
		})
	}
}

// tanimliServis havuzsuz ama tanımı kayıtlı bir servis üretir; veritabanı
// gerektirmeyen yolları sınamak için yeterlidir.
func tanimliServis(t *testing.T) link.LinkService {
	t.Helper()

	svc := link.New(nil, nil)
	require.NoError(t, link.DefineForTest(svc, gecerliTanim()))
	return svc
}
