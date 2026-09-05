package repository

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// TestBenzersizlikIhlaliKisitAdinaGoreKodlanir yarışı kaybeden isteğin,
// servisin "önce oku" denetimine takılan istekle AYNI kodu aldığını doğrular.
//
// İki yol aynı durumu anlatır: ülkeye ikinci kök bölge, bölgeye ikinci
// varsayılan oran. Kod ayrışsaydı, koda göre dallanan bir yönetim arayüzü
// eşzamanlı iki istekte aynı durumu iki farklı koddan görür ve mesajı yanlış
// eşlerdi.
//
// Test gerçek bir veritabanı gerektirmez: kanıtlanan şey SQLSTATE 23505'in
// kısıt ADINA göre eşlenmesidir ve kısıt adlarının gerçekten bunlar olduğu
// entegrasyon testlerinde ayrıca gösterilir.
func TestBenzersizlikIhlaliKisitAdinaGoreKodlanir(t *testing.T) {
	tests := map[string]struct {
		constraint string
		wantCode   string
	}{
		"ülkenin ikinci kök bölgesi": {
			constraint: constraintRegionCountryRoot,
			wantCode:   service.CodeRootExists,
		},
		"bölgenin ikinci varsayılan oranı": {
			constraint: constraintRateDefault,
			wantCode:   service.CodeDefaultExists,
		},
		"servis karşılığı olmayan kısıt": {
			constraint: "tax_rate_rule_uniq",
			wantCode:   CodeDuplicate,
		},
		"kısıt adı bildirilmemiş": {
			constraint: "",
			wantCode:   CodeDuplicate,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := wrapDB(&pgconn.PgError{
				Code:           sqlstateUniqueViolation,
				ConstraintName: tc.constraint,
			}, "kayıt yazılamadı")

			require.Error(t, err)
			assert.True(t, errors.IsConflict(err), "benzersizlik ihlali ÇAKIŞMADIR")
			assert.Equal(t, tc.wantCode, errors.CodeOf(err))
			assert.Contains(t, err.Error(), tc.constraint,
				"mesaj hangi kuralın çiğnendiğini yazmalı")
		})
	}
}
