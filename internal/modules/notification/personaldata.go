package notification

import "github.com/bdrtr/gobit/core/personaldata"

// PersonalData declares what the delivery log may hold about a person
// (ADR 0278).
//
// The log keeps no recipient address by design, and a failed send's error has
// every address taken out before it is kept (D191). What a provider wrote
// about a person in other words can still be in it, so the error is declared
// as open text. The log cannot attribute a row to a person — it names an
// order, not a customer — and offers no disclosure and no erasure; the sweep
// reports it as keeping what is listed here.
func (m *Module) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{
		Holder: ModuleName,
		Holdings: []personaldata.Holding{
			{
				Table: "notification_deliveries", Column: "error", Kind: personaldata.Open,
				Why:       "the provider's words for a failed send, with every address taken out; other words about the recipient may remain",
				OnErasure: personaldata.Kept,
			},
		},
	}
}

var _ personaldata.Declarer = (*Module)(nil)
