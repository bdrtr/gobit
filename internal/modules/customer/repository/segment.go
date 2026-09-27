package repository

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// segmentCountLockClass is the advisory lock class of the segment count (ADR
// 0217); the key space is one across the database, and internal/arch holds the
// classes apart.
const segmentCountLockClass int64 = 6

// SegmentCountLockKey is the one lock of its class: the count is one number.
// It is exported for the test that holds it from a second transaction.
const SegmentCountLockKey int64 = segmentCountLockClass << 32

// segmentManaged is the refusal of a hand edit of a segment's members.
func segmentManaged(groupID string) error {
	return errors.Conflict(models.CodeSegmentManaged,
		"group %s is a segment; its rule decides its members", groupID)
}

// SetGroupSegment gives a group the rule, or a new one, as of now (ADR 0217).
//
// A group that is not a segment yet becomes one only while fewer than limit
// other groups are; the writers that could each add one are serialized first,
// so the count is the one they all see.
func (r *Repo) SetGroupSegment(
	ctx context.Context, id string, rule models.SegmentRule, limit int64, now time.Time,
) (models.CustomerGroup, error) {
	body, err := json.Marshal(rule)
	if err != nil {
		return models.CustomerGroup{}, errors.Wrap(err, errors.KindInternal, CodeGroupNotFound,
			"the segment rule could not be encoded")
	}
	var out models.CustomerGroup
	err = r.inTx(ctx, func(q *customerdb.Queries) error {
		if err := q.LockSegmentCount(ctx, SegmentCountLockKey); err != nil {
			return wrapDB(err, "the segments could not be counted")
		}
		others, err := q.CountSegments(ctx, id)
		if err != nil {
			return wrapDB(err, "the segments could not be counted")
		}
		if others >= limit {
			return errors.Conflict(models.CodeSegmentLimit,
				"%d groups are segments already, the most there can be", others)
		}
		row, err := q.SetGroupSegment(ctx, customerdb.SetGroupSegmentParams{
			ID: id, Segment: body, SetAt: fromTime(now),
		})
		if err != nil {
			return notFoundOr(err, CodeGroupNotFound, "customer group not found: %s", id)
		}
		out, err = toGroup(row)

		return err
	})
	if err != nil {
		return models.CustomerGroup{}, err
	}
	return out, nil
}

// ClearGroupSegment hands a segment back to the operator; its members stay.
func (r *Repo) ClearGroupSegment(ctx context.Context, id string, now time.Time) (models.CustomerGroup, error) {
	if err := r.ready(); err != nil {
		return models.CustomerGroup{}, err
	}
	row, err := r.q.ClearGroupSegment(ctx, customerdb.ClearGroupSegmentParams{ID: id, UpdatedAt: fromTime(now)})
	if err != nil {
		return models.CustomerGroup{}, notFoundOr(err, CodeGroupNotFound, "customer group not found: %s", id)
	}
	return toGroup(row)
}

// ListSegments reads the live segments in id order, at most limit of them.
func (r *Repo) ListSegments(ctx context.Context, limit int32) ([]models.CustomerGroup, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.q.ListSegments(ctx, limit)
	if err != nil {
		return nil, wrapDB(err, "the segments could not be read")
	}
	return toGroups(rows)
}

// ListSegmentFacts pages the live customers after afterID in id order with
// what a segment rule reads of their record.
func (r *Repo) ListSegmentFacts(ctx context.Context, afterID string, limit int32) ([]models.SegmentFacts, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.q.ListSegmentFacts(ctx, customerdb.ListSegmentFactsParams{AfterID: afterID, RowLimit: limit})
	if err != nil {
		return nil, wrapDB(err, "the customers could not be read for the segments")
	}
	out := make([]models.SegmentFacts, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.SegmentFacts{
			CustomerID: row.ID, HasAccount: row.HasAccount, CreatedAt: toTime(row.CreatedAt),
			CountryCode: derefString(row.CountryCode),
		})
	}
	return out, nil
}

// ApplySegmentPage writes a segment's members among the customer ids in
// (afterID, lastID] — to the end of the ids when lastID is empty — under the
// group's lock: the given members are in, every other member in the range is
// out. It writes nothing and says so when the segment's rule is no longer the
// one set at setAt.
func (r *Repo) ApplySegmentPage(
	ctx context.Context, groupID string, setAt time.Time, afterID, lastID string, members []string, now time.Time,
) (added, removed int64, applied bool, err error) {
	if members == nil {
		members = []string{}
	}
	err = r.inTx(ctx, func(q *customerdb.Queries) error {
		current, err := q.LockSegment(ctx, groupID)
		if err != nil {
			if stderrors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return wrapDB(err, "segment %s could not be locked", groupID)
		}
		if !toTime(current).Equal(setAt) {
			return nil
		}
		removed, err = q.RemoveSegmentStrays(ctx, customerdb.RemoveSegmentStraysParams{
			GroupID: groupID, AfterID: afterID, LastID: lastID, Members: members,
		})
		if err != nil {
			return wrapDB(err, "segment %s's strays could not be removed", groupID)
		}
		added, err = q.AddSegmentMembers(ctx, customerdb.AddSegmentMembersParams{
			GroupID: groupID, CreatedAt: fromTime(now), Members: members,
		})
		if err != nil {
			return wrapDB(err, "segment %s's members could not be added", groupID)
		}
		applied = true

		return nil
	})
	if err != nil {
		return 0, 0, false, err
	}
	return added, removed, applied, nil
}

// FinishSegment records that a pass wrote the members of the rule set at setAt,
// only while it is still the segment's rule, and says whether it did.
func (r *Repo) FinishSegment(ctx context.Context, groupID string, setAt, evaluatedAt time.Time) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	n, err := r.q.FinishSegment(ctx, customerdb.FinishSegmentParams{
		ID: groupID, SetAt: fromTime(setAt), EvaluatedAt: fromTime(evaluatedAt),
	})
	if err != nil {
		return false, wrapDB(err, "segment %s could not be marked evaluated", groupID)
	}
	return n > 0, nil
}
