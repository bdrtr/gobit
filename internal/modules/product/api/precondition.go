package api

import (
	"net/http"
	"strconv"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// headerIfMatch and headerETag carry a product's version (ADR 0222).
const (
	headerIfMatch = "If-Match"
	headerETag    = "ETag"
)

// codeIfMatchInvalid reports an If-Match header that is not one strong tag of
// a version, nor *.
const codeIfMatchInvalid = "product_if_match_invalid"

// productPreconditions is the middleware of every route whose write revises a
// product (ADR 0222).
//
// An If-Match header naming one version, `"7"`, makes the write refused with
// 412 unless the product is at that version when its row lock is taken; `*`,
// or no header, asks nothing. A successful write answers the version the
// product is at after it in an ETag header, whatever the route's body is — a
// variant's, an image's or the product's own.
func productPreconditions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if raw := strings.TrimSpace(r.Header.Get(headerIfMatch)); raw != "" && raw != "*" {
			version, err := parseETag(raw)
			if err != nil {
				corehttp.WriteError(ctx, w, err)
				return
			}
			ctx = service.ExpectVersion(ctx, version)
		}
		ctx, read := service.WithVersionSink(ctx)
		etag := func() (string, bool) {
			version, ok := read()
			return etagOf(version), ok
		}
		next.ServeHTTP(corehttp.HeaderOnSuccess(w, headerETag, etag), r.WithContext(ctx))
	})
}

// parseETag reads one strong tag of a version.
func parseETag(raw string) (int64, error) {
	unquoted, ok := strings.CutPrefix(raw, `"`)
	if ok {
		unquoted, ok = strings.CutSuffix(unquoted, `"`)
	}
	version, err := strconv.ParseInt(unquoted, 10, 64)
	if !ok || err != nil || version < 0 {
		return 0, coreerrors.Invalid(codeIfMatchInvalid,
			"If-Match takes one quoted version the product was read at, such as \"7\", or *; %q given", raw)
	}
	return version, nil
}

// etagOf is a version as an ETag.
func etagOf(version int64) string {
	return `"` + strconv.FormatInt(version, 10) + `"`
}

// ifMatchParameter describes the header on a revising write.
func ifMatchParameter() openapi.Parameter {
	return openapi.Parameter{
		Name: headerIfMatch, In: "header",
		Schema: map[string]any{schemaType: typeString},
		Description: "The product's version as it was read, quoted (\"7\"), from `version` or the ETag. " +
			"The write is refused with 412 `" + service.CodeVersionMismatch + "` when the product has " +
			"been written since; `*` or no header asks nothing. The answer's ETag is the version after the write.",
	}
}
