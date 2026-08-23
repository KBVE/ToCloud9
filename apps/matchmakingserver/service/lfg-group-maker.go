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
