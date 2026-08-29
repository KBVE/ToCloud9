package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/walkline/ToCloud9/apps/groupserver/repo"
)

// Concurrent leaves on the same group raced: both goroutines could take the
// disband path (the second one panicked in the cache) or overwrite each
// other's member removal, leaving a group without members that its players
// were still mapped to - impossible to invite them ever again.
func TestGroupsServiceConcurrentLeaves(t *testing.T) {
	for i := 0; i < 50; i++ {
		cache := newWarmedUpCache(t)
		ctx := context.Background()

		assert.NoError(t, cache.Create(ctx, 1, newTwoMembersGroup()))
		assert.NoError(t, cache.AddMember(ctx, 1, &repo.GroupMember{GroupID: 1, MemberGUID: 3, MemberName: "Third", IsOnline: true}))

		s := NewGroupsService(cache, nil, noopGroupProducer{})

		var wg sync.WaitGroup
		start := make(chan struct{})
		leave := func(player uint64) {
			defer wg.Done()
			<-start
			assert.NoError(t, s.Leave(ctx, 1, player))
		}
		wg.Add(2)
		go leave(2)
		go leave(3)
		close(start)
		wg.Wait()

		// One leave shrinks the group to two members, the other one disbands it.
		group, err := s.GroupByID(ctx, 1, 1)
		assert.NoError(t, err)
		assert.Nil(t, group, "group should be disbanded")

		for _, player := range []uint64{1, 2, 3} {
			groupID, err := s.GroupIDByPlayer(ctx, 1, player)
			assert.NoError(t, err)
			assert.Zero(t, groupID, "player %d should not be mapped to a group anymore", player)
		}
	}
}

// staleInviteRepo hands out an invite pointing to a group that no longer exists.
type staleInviteRepo struct{ noopGroupsRepo }

func (staleInviteRepo) GetInviteByInvitedPlayer(ctx context.Context, realmID uint32, invitedPlayer uint64) (*repo.GroupInvite, error) {
	return &repo.GroupInvite{Inviter: 1, InviterName: "Leader", Invitee: invitedPlayer, InviteeName: "Late", GroupID: 999}, nil
}

// Invites are never deleted, only replaced, so accepting one that outlived its
// group used to dereference a nil group and crash the service.
func TestGroupsServiceAcceptStaleInvite(t *testing.T) {
	cache := NewInMemGroupsCache(staleInviteRepo{})
	assert.NoError(t, cache.Warmup(context.Background(), 1))

	s := NewGroupsService(cache, nil, noopGroupProducer{})

	assert.ErrorIs(t, s.AcceptInvite(context.Background(), 1, 2), ErrGroupNotFound)
}

// duplicateRowRepo models the characters database holding a group_member row
// the cache never learned about: the insert fails on the primary key until the
// row is deleted, exactly as MySQL does.
type duplicateRowRepo struct {
	noopGroupsRepo
	strandedRow uint64
	removed     bool
}

func (r *duplicateRowRepo) GetInviteByInvitedPlayer(ctx context.Context, realmID uint32, invitedPlayer uint64) (*repo.GroupInvite, error) {
	return &repo.GroupInvite{Inviter: 1, InviterName: "Leader", Invitee: invitedPlayer, InviteeName: "Late", GroupID: 1}, nil
}

func (r *duplicateRowRepo) RemoveMember(ctx context.Context, realmID uint32, memberGUID uint64) error {
	if memberGUID == r.strandedRow {
		r.removed = true
	}
	return nil
}

func (r *duplicateRowRepo) AddMember(ctx context.Context, realmID uint32, groupMember *repo.GroupMember) error {
	if groupMember.MemberGUID == r.strandedRow && !r.removed {
		return errors.New("Error 1062 (23000): Duplicate entry '3' for key 'group_member.PRIMARY'")
	}
	return nil
}

// A membership row that outlived its group -- a shard rolled while the party
// was alive, so nothing told the group service to clear it -- is invisible to
// the cache, so Invite lets the accept through and addMember then dies on the
// group_member primary key. That failure is not local to the one player: it
// fails the LFG formation for the whole party, and the matchmaking queue never
// forms a group again for anyone in it.
func TestGroupsServiceAcceptInviteClearsStrandedMembership(t *testing.T) {
	r := &duplicateRowRepo{strandedRow: 3}
	cache := NewInMemGroupsCache(r)
	ctx := context.Background()
	assert.NoError(t, cache.Warmup(ctx, 1))
	assert.NoError(t, cache.Create(ctx, 1, newTwoMembersGroup()))

	s := NewGroupsService(cache, nil, noopGroupProducer{})

	assert.NoError(t, s.AcceptInvite(ctx, 1, 3))
	assert.True(t, r.removed, "stranded membership row should be deleted before the insert")

	group, err := s.GroupByID(ctx, 1, 1)
	assert.NoError(t, err)
	assert.NotNil(t, group.MemberByGUID(3), "invitee should have joined the group")
}

// The same accept delivered twice must stay a no-op: the second one finds the
// player already in the group and must not delete their own membership row on
// the way through the stale-row path.
func TestGroupsServiceAcceptInviteTwiceKeepsMembership(t *testing.T) {
	r := &duplicateRowRepo{strandedRow: 3}
	cache := NewInMemGroupsCache(r)
	ctx := context.Background()
	assert.NoError(t, cache.Warmup(ctx, 1))
	assert.NoError(t, cache.Create(ctx, 1, newTwoMembersGroup()))

	s := NewGroupsService(cache, nil, noopGroupProducer{})

	assert.NoError(t, s.AcceptInvite(ctx, 1, 3))
	r.removed = false
	assert.NoError(t, s.AcceptInvite(ctx, 1, 3))
	assert.False(t, r.removed, "a retried accept must not touch the membership it already created")

	group, err := s.GroupByID(ctx, 1, 1)
	assert.NoError(t, err)
	assert.NotNil(t, group.MemberByGUID(3))
}
