package service

import (
	"context"
	"fmt"

	matchmaking "github.com/walkline/ToCloud9/apps/matchmakingserver"
	"github.com/walkline/ToCloud9/apps/matchmakingserver/lfg"
	pbGroup "github.com/walkline/ToCloud9/gen/group/pb"
)

// GroupServiceMaker forms an LFG match into a real group by driving the same
// Invite/AcceptInvite pair a manual party invite uses. Reusing that path is
// deliberate: the group service allocates the id, and the shards' existing
// OnGroupCreated hooks fire without knowing LFG was involved.
type GroupServiceMaker struct {
	client pbGroup.GroupServiceClient
}

func NewGroupServiceMaker(client pbGroup.GroupServiceClient) *GroupServiceMaker {
	return &GroupServiceMaker{client: client}
}

func (m *GroupServiceMaker) CreateGroup(ctx context.Context, leader lfg.PlayerKey, leaderName string, members []lfg.Member) (uint64, error) {
	// A match routinely mixes a premade party with players queued alone, so some
	// members arrive already in a group and Invite answers AlreadyInGroup. That
	// used to fail the whole formation, and the shard fell back to building a
	// group of its own with a locally allocated id -- two groups for one party,
	// each shard's anchor addressing a different one. Everyone leaves their
	// current group first, the leader included, so the invite chain always
	// starts from an empty roster.
	if err := m.leaveCurrentGroup(ctx, leader.RealmID, leader.GUID); err != nil {
		return 0, err
	}
	for _, member := range members {
		if err := m.leaveCurrentGroup(ctx, member.RealmID, member.GUID); err != nil {
			return 0, err
		}
	}

	for _, member := range members {
		invite, err := m.client.Invite(ctx, &pbGroup.InviteParams{
			Api:         matchmaking.SupportedGroupServiceVer,
			RealmID:     leader.RealmID,
			Inviter:     leader.GUID,
			Invited:     member.GUID,
			InviterName: leaderName,
			InvitedName: member.Name,
		})
		if err != nil {
			return 0, fmt.Errorf("lfg: invite %d: %w", member.GUID, err)
		}
		if invite.Status != pbGroup.InviteResponse_Ok {
			return 0, fmt.Errorf("lfg: invite %d rejected: %s", member.GUID, invite.Status)
		}

		accept, err := m.client.AcceptInvite(ctx, &pbGroup.AcceptInviteParams{
			Api:     matchmaking.SupportedGroupServiceVer,
			RealmID: member.RealmID,
			Player:  member.GUID,
		})
		if err != nil {
			return 0, fmt.Errorf("lfg: accept for %d: %w", member.GUID, err)
		}
		if accept.Status != pbGroup.AcceptInviteResponse_Ok {
			return 0, fmt.Errorf("lfg: accept for %d rejected: %s", member.GUID, accept.Status)
		}
	}

	// The id only exists once the group does, so it is read back rather than
	// predicted. This is also the check that the group really formed.
	resp, err := m.client.GetGroupIDByPlayer(ctx, &pbGroup.GetGroupIDByPlayerRequest{
		Api:     matchmaking.SupportedGroupServiceVer,
		RealmID: leader.RealmID,
		Player:  leader.GUID,
	})
	if err != nil {
		return 0, fmt.Errorf("lfg: read back group id: %w", err)
	}
	if resp.GroupID == 0 {
		return 0, fmt.Errorf("lfg: group service reported no group for leader %d", leader.GUID)
	}
	return uint64(resp.GroupID), nil
}

// leaveCurrentGroup drops one player from whatever group they are in. A player
// who is in none is left alone, so this costs a single lookup in the common
// case of an all-solo match.
func (m *GroupServiceMaker) leaveCurrentGroup(ctx context.Context, realmID uint32, guid uint64) error {
	current, err := m.client.GetGroupIDByPlayer(ctx, &pbGroup.GetGroupIDByPlayerRequest{
		Api:     matchmaking.SupportedGroupServiceVer,
		RealmID: realmID,
		Player:  guid,
	})
	if err != nil {
		return fmt.Errorf("lfg: read group id for %d: %w", guid, err)
	}
	if current.GroupID == 0 {
		return nil
	}

	if _, err = m.client.Leave(ctx, &pbGroup.GroupLeaveParams{
		Api:     matchmaking.SupportedGroupServiceVer,
		RealmID: realmID,
		Player:  guid,
	}); err != nil {
		return fmt.Errorf("lfg: leave group %d for %d: %w", current.GroupID, guid, err)
	}

	return nil
}
