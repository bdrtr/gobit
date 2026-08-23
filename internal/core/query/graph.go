package query

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	codeInvalidSpec      = "query_invalid_spec"
	codeNoContainer      = "query_container_missing"
	codeNoLinkService    = "query_link_service_missing"
	codeProviderNotFound = "query_provider_not_found"
	codeProviderInvalid  = "query_provider_invalid"
	codeProviderMismatch = "query_provider_entity_mismatch"
	codeProviderFailed   = "query_provider_failed"
	codeLinkDefFailed    = "query_link_definition_failed"
	codeLinkMismatch     = "query_link_entity_mismatch"
	codeLinkFailed       = "query_link_failed"
	codeCardinality      = "query_unknown_cardinality"
	codeMissingID        = "query_record_id_missing"
	codeCanceled         = "query_canceled"
)

// Hata ayrıntılarında (errors.Error.Details) kullanılan anahtarlar. Sabit
// tutulmaları, çağıranın ayrıntılara güvenle bakabilmesi içindir.
const (
	detailEntity = "entity"
	detailLink   = "link"
	detailName   = "aranan_ad"
	detailField  = "alan"
)

// run tek bir [Query.Graph] çağrısının durumudur.
//
// Tanım ve sağlayıcı çözümleri çağrı boyunca önbelleklenir: aynı link ya da
// aynı entity birden çok seviyede geçse bile container ve link servisi bir kez
// sorgulanır. Önbellek ÇAĞRI BOYUNCADIR, iki Graph çağrısı arasında
// paylaşılmaz; böylece sonradan tanımlanan bir link bir sonraki çağrıda görülür.
type run struct {
	res       *resolver
	defs      map[string]link.LinkDefinition
	providers map[string]Provider
}

// Graph spec'e göre kök kayıtları çeker ve genişletmeleri uygular.
//
// Kök kayıt yoksa boş (nil olmayan) dilim ve nil hata döner. Herhangi bir
// seviyedeki hata tüm çağrıyı düşürür; kısmi sonuç dönmez.
//
// Genişletme ağacı VERİ GETİRİLMEDEN önce çözülür (bkz. plan), dönen ağaçtaki
// kayıtlar ise çağırana aittir (bkz. ownRecords).
func (r *resolver) Graph(ctx context.Context, spec GraphSpec) ([]Record, error) {
	if err := ctxErr(ctx, "sorgu"); err != nil {
		return nil, err
	}
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	if r.c == nil {
		return nil, errors.Internal(codeNoContainer,
			"query container olmadan kurulmuş; %q sağlayıcısı çözülemez", spec.Entity+ProviderSuffix)
	}

	rn := &run{
		res:       r,
		defs:      make(map[string]link.LinkDefinition),
		providers: make(map[string]Provider),
	}

	provider, err := rn.provider(spec.Entity)
	if err != nil {
		return nil, err
	}

	// Ağaç VERİDEN ÖNCE çözülür. İki sebeple:
	//   1. Bozuk bir spec (bilinmeyen link, kayıtsız hedef sağlayıcı) kök
	//      sorgusunun maliyetini ödetmeden hata verir.
	//   2. Deterministik spec hatası, sağlayıcıdan gelen geçici bir hatanın
	//      ARKASINDA GİZLENMEZ. Aksi hâlde "veritabanı erişilemez" hatası,
	//      aslında düzeltilmesi gereken bir link adı yazım hatasını maskelerdi.
	nodes, err := rn.plan(ctx, spec.Entity, spec.Expand)
	if err != nil {
		return nil, err
	}

	roots, err := provider.List(ctx, ListOptions{
		Fields:  fieldsWithID(spec.Fields, len(spec.Expand) > 0),
		Filters: spec.Filters,
		Limit:   spec.Limit,
		Offset:  spec.Offset,
	})
	if err != nil {
		return nil, wrapCallErr(err, codeProviderFailed,
			"%q sağlayıcısının List çağrısı başarısız oldu", spec.Entity)
	}
	if len(roots) == 0 {
		return []Record{}, nil
	}

	roots = ownRecords(roots)
	if err := rn.expand(ctx, spec.Entity, roots, nodes); err != nil {
		return nil, err
	}
	return roots, nil
}

// planNode çözümlenmiş tek bir genişletmedir: hedef entity, gidilen yön ve
// sonucun şekli VERİYE BAKILMADAN belirlenir.
type planNode struct {
	exp      Expansion
	def      link.LinkDefinition
	target   string
	reverse  bool
	many     bool
	children []planNode
}

// plan genişletme ağacını veri getirilmeden ÖNCE çözer.
//
// Her seviye için link tanımı okunur, yön (targetSide) ve sonucun şekli
// (writesMany) belirlenir, hedef sağlayıcının container'da kayıtlı olduğu
// doğrulanır. Böylece hatalı bir link adı, kök entity'ye bağlanmayan bir link,
// tanınmayan bir kardinalite ve unutulmuş bir sağlayıcı kaydı VERİDEN BAĞIMSIZ
// olarak raporlanır: üst seviye hiç kayıt getirmese de aynı hata deterministik
// biçimde döner. Aksi hâlde bozuk bir sorgu tanımı boş fixture'lı testlerden
// geçip ilk gerçek veriyle birlikte patlardı.
//
// Tanımlar ve sağlayıcılar çağrı boyunca önbelleklendiği için bu ön geçiş
// genişletme sırasında ek tur üretmez.
func (rn *run) plan(ctx context.Context, entity string, exps []Expansion) ([]planNode, error) {
	if len(exps) == 0 {
		return nil, nil
	}

	nodes := make([]planNode, 0, len(exps))
	for _, exp := range exps {
		if err := ctxErr(ctx, "genişletme "+exp.Link); err != nil {
			return nil, err
		}

		def, err := rn.definition(ctx, exp.Link)
		if err != nil {
			return nil, err
		}
		target, reverse, err := targetSide(def, entity)
		if err != nil {
			return nil, err
		}
		many, err := writesMany(def, reverse)
		if err != nil {
			return nil, err
		}
		if _, err := rn.provider(target); err != nil {
			return nil, err
		}
		children, err := rn.plan(ctx, target, exp.Expand)
		if err != nil {
			return nil, err
		}

		nodes = append(nodes, planNode{
			exp: exp, def: def, target: target, reverse: reverse, many: many, children: children,
		})
	}
	return nodes, nil
}

// expand parents dilimindeki TÜM kayıtlara plan düğümlerini uygular.
//
// Her genişletme için sırasıyla: bu seviyedeki tüm kayıtların kimlikleri tek
// turda toplanır, link'ler tek turda çözülür ve hedef modülden TEK FetchByIDs
// yapılır. Kayıt başına sağlayıcı çağrısı YOKTUR; iç içe seviyelerde de aynı
// kural geçerlidir.
//
// Genişletilen kayıtlar üst kayda REFERANSLA yazılır (Record bir haritadır),
// bu yüzden bir alt seviye genişletmesi üst seviyedeki kopyayı da günceller;
// ayrıca bir birleştirme adımı gerekmez. Yazılan haritalar sağlayıcıdan gelen
// haritalar değil, ownRecords ile alınmış çağrıya ait kopyalardır.
func (rn *run) expand(ctx context.Context, entity string, parents []Record, nodes []planNode) error {
	// Düğümler link tanımını taşıdığı için kopyalanmadan, adresle gezilir.
	for i := range nodes {
		node := &nodes[i]

		if err := ctxErr(ctx, "genişletme "+node.exp.Link); err != nil {
			return err
		}

		ids, byParent, err := collectIDs(parents, entity, node.exp.Link)
		if err != nil {
			return err
		}

		related, err := rn.resolveLinks(ctx, node.def, ids, node.reverse)
		if err != nil {
			return err
		}

		children, err := rn.fetchRelated(ctx, node.target, related, node.exp)
		if err != nil {
			return err
		}
		byID, err := indexByID(children, node.target)
		if err != nil {
			return err
		}

		key := outputKey(node.exp)
		for id, records := range byParent {
			value := shape(related[id], byID, node.many)
			for _, rec := range records {
				rec[key] = value
			}
		}

		rn.res.log.Debug("genişletme çözüldü",
			"link", node.exp.Link, "kok_entity", entity, "hedef_entity", node.target,
			"ters_yon", node.reverse, "kok_kayit", len(parents), "getirilen_kayit", len(children),
			"anahtar", key)

		if len(node.children) > 0 && len(children) > 0 {
			if err := rn.expand(ctx, node.target, children, node.children); err != nil {
				return err
			}
		}
	}
	return nil
}

// fetchRelated ilgili kimliklerin tamamını hedef sağlayıcıdan TEK çağrıda getirir.
// Hiç ilgili kimlik yoksa sağlayıcıya hiç gidilmez.
//
// Getirilen kayıtlar sonuç ağacına girmeden önce ownRecords ile kopyalanır;
// gerekçesi orada yazılıdır.
func (rn *run) fetchRelated(ctx context.Context, target string, related map[string][]string, exp Expansion) ([]Record, error) {
	ids := uniqueValues(related)
	if len(ids) == 0 {
		return nil, nil
	}

	provider, err := rn.provider(target)
	if err != nil {
		return nil, err
	}

	records, err := provider.FetchByIDs(ctx, ids, fieldsWithID(exp.Fields, true))
	if err != nil {
		return nil, wrapCallErr(err, codeProviderFailed,
			"%q sağlayıcısının FetchByIDs çağrısı başarısız oldu (link: %q, %d kimlik)",
			target, exp.Link, len(ids))
	}
	return ownRecords(records), nil
}

// provider entity'nin sağlayıcısını container'dan adla çözer ve önbelleğe alır.
//
// Kayıt yoksa dönen hata ARANAN ADI içerir; ADR 0004'ün teşhis şartı budur.
func (rn *run) provider(entity string) (Provider, error) {
	if p, ok := rn.providers[entity]; ok {
		return p, nil
	}

	name := entity + ProviderSuffix
	p, err := container.Resolve[Provider](rn.res.c, name)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, errors.Wrap(err, errors.KindNotFound, codeProviderNotFound,
				"%q entity'si için sorgu sağlayıcısı bulunamadı; container'da %q adı arandı (modül Register sırasında kaydediyor mu?)",
				entity, name).
				WithDetails(map[string]any{detailEntity: entity, detailName: name})
		}
		return nil, errors.Wrap(err, errors.KindOf(err), codeProviderInvalid,
			"%q entity'sinin sorgu sağlayıcısı çözülemedi (aranan ad: %q)", entity, name).
			WithDetails(map[string]any{detailEntity: entity, detailName: name})
	}

	// Sağlayıcı yanlış adla kaydedilmişse birleştirme sessizce yanlış modülden
	// veri çekerdi; bu yüzden kayıt adı ile sunulan entity tutmak zorundadır.
	if got := p.Entity(); got != entity {
		return nil, errors.Invalid(codeProviderMismatch,
			"%q adıyla kayıtlı sağlayıcı %q entity'sini sunuyor, %q bekleniyordu", name, got, entity).
			WithDetails(map[string]any{detailName: name, "beklenen": entity, "gelen": got})
	}

	rn.providers[entity] = p
	return p, nil
}

// definition link tanımını çözer ve çağrı boyunca önbelleğe alır.
//
// Tanımsız bir link adı için [link.LinkService.Definition] errors.NotFound
// döner ve bu sınıf korunarak yukarı taşınır.
func (rn *run) definition(ctx context.Context, name string) (link.LinkDefinition, error) {
	if def, ok := rn.defs[name]; ok {
		return def, nil
	}
	if rn.res.links == nil {
		return link.LinkDefinition{}, errors.Internal(codeNoLinkService,
			"query link servisi olmadan kurulmuş; %q link'i çözülemez", name)
	}

	def, err := rn.res.links.Definition(ctx, name)
	if err != nil {
		return link.LinkDefinition{}, wrapCallErr(err, codeLinkDefFailed,
			"%q link tanımı okunamadı", name).
			WithDetails(map[string]any{detailLink: name})
	}

	rn.defs[name] = def
	return def, nil
}

// resolveLinks bu seviyedeki TÜM kimlikler için link'leri çözer ve kök kimlikten
// ilgili kimliklere eşleyen haritayı döner.
//
// İki yön de sözleşmedeki TOPLU metotlarla çözülür: ileri yönde
// [link.LinkService.ListMany], ters yönde [link.LinkService.ListManyByTo].
// Tek çağrı, N+1 yok.
func (rn *run) resolveLinks(ctx context.Context, def link.LinkDefinition, ids []string, reverse bool) (map[string][]string, error) {
	links := rn.res.links
	if links == nil {
		return nil, errors.Internal(codeNoLinkService,
			"query link servisi olmadan kurulmuş; %q link'i çözülemez", def.Name)
	}

	if reverse {
		return rn.resolveReverse(ctx, links, def, ids)
	}

	related, err := links.ListMany(ctx, def.Name, ids)
	if err != nil {
		return nil, wrapLinkErr(err, def.Name, len(ids))
	}
	return related, nil
}

// resolveReverse link'i ters yönde (To -> From) TOPLU çözer.
//
// [link.LinkService.ListManyByTo] sözleşmenin parçasıdır; ileri yöndeki
// ListMany ile simetriktir ve kayıt başına sorgu üretmez.
func (rn *run) resolveReverse(ctx context.Context, links link.LinkService, def link.LinkDefinition, ids []string) (map[string][]string, error) {
	related, err := links.ListManyByTo(ctx, def.Name, ids)
	if err != nil {
		return nil, wrapLinkErr(err, def.Name, len(ids))
	}
	return related, nil
}

// wrapLinkErr link servisinden gelen hatayı sınıfını koruyarak sarar.
func wrapLinkErr(err error, name string, count int) error {
	return wrapCallErr(err, codeLinkFailed,
		"%q link'i çözülemedi (%d kimlik)", name, count).
		WithDetails(map[string]any{detailLink: name})
}

// wrapCallErr sağlayıcıdan ya da link servisinden dönen hatayı tipli hataya
// sarar ve İPTALİ ayrı sınıflandırır.
//
// Tipli gelen hatanın sınıfı olduğu gibi korunur. Ham context.Canceled /
// context.DeadlineExceeded — pgx'in doğrudan döndürdüğü hata budur — tipli
// değildir; errors.KindOf onu güvenli varsayılan olan KindInternal'a çevirir ve
// HTTP katmanı bütçesi dolan bir isteğe 503 yerine opak (mesajı bastırılmış) bir
// 500 verirdi. Bu yüzden iptal, ctxErr ve link.wrapDB ile AYNI biçimde
// KindUnavailable + codeCanceled'a eşlenir. err nil ise nil döner.
func wrapCallErr(err error, code, format string, a ...any) *errors.Error {
	var typed *errors.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &typed):
		return errors.Wrap(err, typed.Kind, code, format, a...)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.Wrap(err, errors.KindUnavailable, codeCanceled,
			format+" (bağlam iptal edildi)", a...)
	default:
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}
}
