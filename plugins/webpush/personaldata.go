package webpush

import (
	"context"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
)

// This file is the plugin's answer to ADR 0029's third obligation, and it was
// missing until 2026-09-07.
//
// # What was wrong
//
// webpush_subscription holds a push endpoint, the device's two keys and a
// customer id, and the module declared NONE of it — there was no occurrence of
// the personal-data vocabulary anywhere under plugins/. Two things followed, and
// neither raised anything:
//
//   - A data-subject DISCLOSURE listed every holder except this one, so a person
//     asking what is held about them was told about their orders and not about
//     the devices this framework can push a message to.
//   - An ERASURE swept every holder except this one, so the row survived the
//     sweep and the subscription kept working.
//
// It was found by ADR 0051, which asks what a storefront may accept from a party
// it cannot identify, and noticed on the way that the audit which should have
// caught this cannot see a plugin at all: registeredModules in
// internal/app/personaldata_test.go builds its registry from an empty
// config.Config, so no plugin module ever enters the walk. That blindness is why
// the gate for this file lives in this package (see personaldata_test.go here),
// which is the shape ADR 0018 already chose for this plugin's rollback test.

// tableSubscription is the table this module keeps people in.
const tableSubscription = "webpush_subscription"

// codeNotWired reports that the module was asked to act before Register.
const codeNotWired = "webpush_not_wired"

// PersonalData says where this module keeps personal data (ADR 0029).
//
// # Why an endpoint is personal data
//
// It is the least obvious of the four and the most important to get right. The
// push endpoint is a URL the browser minted for ONE installation of ONE browser
// on ONE device, it is UNIQUE in this table by construction, and anybody holding
// it can send that device a message. It identifies a person the way a phone
// number does — not by naming them, but by reaching them — and a declaration
// that left it out while naming customer_id would be answering the easy half.
//
// # Why the keys are declared too
//
// p256dh and auth are the device's public key and its secret. They name nobody
// on their own, and they are useless to anybody who does not also hold the
// endpoint; but they are held ABOUT a person, in the same row, and a controller
// answering "what do you hold" has to be able to say so.
//
// # Why locale and vapid_fingerprint are NOT declared
//
// locale is a preference and vapid_fingerprint identifies the SERVER key the
// subscription was minted against, not the person. Declaring them would make the
// disclosure longer and less true.
func (m *webpushModule) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{
		Holder: ModuleName,
		Holdings: []personaldata.Holding{
			{
				Table: tableSubscription, Column: "endpoint", Kind: personaldata.Named,
				Why: "the push service URL of one browser on one device. It names nobody and " +
					"REACHES somebody, which is the same thing for the purpose of a disclosure",
			},
			{
				Table: tableSubscription, Column: "p256dh", Kind: personaldata.Named,
				Why: "the device's public key, held about the person the device belongs to",
			},
			{
				Table: tableSubscription, Column: "auth", Kind: personaldata.Named,
				Why: "the device's secret, held about the person the device belongs to",
			},
			{
				Table: tableSubscription, Column: "customer_id", Kind: personaldata.Named,
				Why: "the customer this device was bound to when it subscribed, empty for a " +
					"device that subscribed before signing in",
			},
		},
	}
}

// Erase removes every subscription bound to the subject.
//
// # Why DELETED and not anonymized
//
// The other holders in this repository anonymize, because their rows are a sale
// or a document that has to keep existing with the person taken out of it. A
// push subscription is not a record of anything — it is a live capability to
// reach a device, and a subscription with the person removed is either useless
// or dangerous. So the row goes.
//
// # Why an e-mail-only subject erases NOTHING, and says so
//
// This table has no address column: a device is bound by customer id or by
// nothing. A subject carrying only an e-mail therefore matches no row here, and
// the honest answer is Deleted with a count of zero rather than an error — the
// sweep asked a holder that has nothing under that handle, which is not a fault.
//
// What it must NOT do is report success while a row survives, so a subject with
// no customer id at all is answered with zero and the Why says which handle this
// holder can be asked by.
func (m *webpushModule) Erase(
	ctx context.Context, subject personaldata.Subject,
) (personaldata.Result, error) {
	result := personaldata.Result{Holder: ModuleName, Outcome: personaldata.Deleted}

	if subject.CustomerID == "" {
		result.Why = "this holder binds a device by customer id and holds no address, so a " +
			"subject naming only an e-mail matches nothing here"

		return result, nil
	}

	if m.store == nil {
		// Register was not called, so nothing was read. Reporting Deleted here
		// would tell a controller this holder is clean when it was never asked.
		return personaldata.Result{}, coreerrors.Internal(codeNotWired,
			"the %s module was asked to erase before Register wired its store; nothing was "+
				"read and the report must not count this holder as done", ModuleName)
	}

	removed, err := m.store.deleteByCustomer(ctx, subject.CustomerID)
	if err != nil {
		return personaldata.Result{}, err
	}

	result.Rows = int(removed)

	return result, nil
}
