package payment

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// The payment module declares, discloses and answers an erasure for what it
// keeps about a person (ADR 0277); the lists and the reads are in the service
// package, beside the rows they describe.
var (
	_ personaldata.Declarer  = (*Module)(nil)
	_ personaldata.Discloser = (*Module)(nil)
	_ personaldata.Eraser    = (*Module)(nil)
)

// codeNotRegistered refuses an answer asked of a module Register has not
// wired: "nothing found" from a module that never looked would be a false
// sentence in a document handed to a person.
const codeNotRegistered = "payment_module_not_registered"

// PersonalData declares where this module keeps something about a person. It
// needs no database and no Register: a declaration is a property of the code.
func (m *Module) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{Holder: ModuleName, Holdings: service.PersonalDataHoldings()}
}

// PersonalDataOf shows one person what this module holds about them.
func (m *Module) PersonalDataOf(ctx context.Context, s personaldata.Subject) (personaldata.Disclosure, error) {
	if m.personal == nil {
		return personaldata.Disclosure{}, errors.Internal(codeNotRegistered,
			"the %s module was asked what it holds about a person before Register ran; nothing was read "+
				"and the dossier must not report it as empty", ModuleName)
	}

	return m.personal.Disclose(ctx, s)
}

// Erase answers an erasure request: everything is kept, and the answer says
// what and why.
func (m *Module) Erase(ctx context.Context, s personaldata.Subject) (personaldata.Result, error) {
	if m.personal == nil {
		return personaldata.Result{}, errors.Internal(codeNotRegistered,
			"the %s module was asked to erase before Register ran, so it cannot say what it holds", ModuleName)
	}

	return m.personal.Erase(ctx, s)
}
