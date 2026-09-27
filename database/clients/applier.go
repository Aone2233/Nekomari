package clients

// ClientInfoApplier adapts SaveClient to internal/bulk's Applier.
//
// It exists so bulk edits run through the *same* validation as a single-node edit rather
// than a second implementation of the rules, which is the whole reason `internal/bulk`
// takes an interface instead of a database handle. Two things about SaveClient make the
// adapter worth being explicit about rather than inlining a call:
//
//   - It **writes into the map it is given** — `updated_at` is set on it, and `expired_at`
//     is normalised in place. `bulk.Apply` hands each node its own copy for that reason, so
//     one node's timestamp cannot land on the next node's request.
//   - It returns the applier's own error text, which is what the per-node report shows. A
//     generic "failed" would make the operator re-derive the reason by hand.
type ClientInfoApplier struct{}

// SaveClientInfo applies one partial update. Named for the operation, not the receiver,
// so it satisfies bulk.Applier without the caller importing this package's shape.
func (ClientInfoApplier) SaveClientInfo(update map[string]interface{}) error {
	return SaveClient(update)
}
