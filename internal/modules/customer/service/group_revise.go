package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// CodeGroupMoved refuses a group's revision when the group is no longer named
// and ranked as the caller read it (ADR 0329).
const CodeGroupMoved = "customer_group_moved"

// ReviseGroup renames and re-ranks the group, writing both only while they are
// the ones the caller read, and refuses with [CodeGroupMoved] when another
// writer changed either since (ADR 0329). The name is trimmed and checked as
// a new group's is; any whole number is a rank (ADR 0049).
func (s *Service) ReviseGroup(
	ctx context.Context, id, readName string, readRank int32, name string, rank int32,
) (models.CustomerGroup, error) {
	if err := s.ready(); err != nil {
		return models.CustomerGroup{}, err
	}
	if err := requireID(id, models.CustomerGroupIDPrefix, "group id"); err != nil {
		return models.CustomerGroup{}, err
	}
	if err := requireText("group name", name); err != nil {
		return models.CustomerGroup{}, err
	}
	name = strings.TrimSpace(name)
	if err := checkLen("group name", name, models.MaxNameLen); err != nil {
		return models.CustomerGroup{}, err
	}

	group, revised, err := s.repo.ReviseGroup(ctx, id, readName, readRank, name, rank, s.clock())
	if err != nil || revised {
		return group, err
	}

	// Nothing was written: the group is gone, or it moved since it was read.
	current, err := s.repo.GetGroup(ctx, id)
	if err != nil {
		return models.CustomerGroup{}, err
	}

	return models.CustomerGroup{}, errors.Conflict(CodeGroupMoved,
		"customer group %s is %q at rank %d now, not %q at rank %d; draw the list again",
		id, current.Name, current.Rank, readName, readRank)
}
