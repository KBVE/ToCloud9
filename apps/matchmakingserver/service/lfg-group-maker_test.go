package service

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	"github.com/walkline/ToCloud9/apps/matchmakingserver/lfg"
	pbGroup "github.com/walkline/ToCloud9/gen/group/pb"
)

// fakeGroupClient models the group service as a player -> group id map. The
// embedded interface is nil on purpose: any RPC the maker starts calling
// without a stub here panics instead of passing silently.
type fakeGroupClient struct {
	pbGroup.GroupServiceClient

	groupOf map[uint64]uint32
	nextID  uint32

	leaves  []uint64
	invites []uint64
}

func newFakeGroupClient(groupOf map[uint64]uint32, nextID uint32) *fakeGroupClient {
	return &fakeGroupClient{groupOf: groupOf, nextID: nextID}
}

func (f *fakeGroupClient) GetGroupIDByPlayer(_ context.Context, in *pbGroup.GetGroupIDByPlayerRequest, _ ...grpc.CallOption) (*pbGroup.GetGroupIDByPlayerResponse, error) {
	return &pbGroup.GetGroupIDByPlayerResponse{GroupID: f.groupOf[in.Player]}, nil
}

func (f *fakeGroupClient) Leave(_ context.Context, in *pbGroup.GroupLeaveParams, _ ...grpc.CallOption) (*pbGroup.GroupLeaveResponse, error) {
	f.leaves = append(f.leaves, in.Player)
	delete(f.groupOf, in.Player)
	return &pbGroup.GroupLeaveResponse{}, nil
}

func (f *fakeGroupClient) Invite(_ context.Context, in *pbGroup.InviteParams, _ ...grpc.CallOption) (*pbGroup.InviteResponse, error) {
	if f.groupOf[in.Invited] != 0 {
		return &pbGroup.InviteResponse{Status: pbGroup.InviteResponse_AlreadyInGroup}, nil
	}

	f.invites = append(f.invites, in.Invited)
	f.groupOf[in.Inviter] = f.nextID
	return &pbGroup.InviteResponse{Status: pbGroup.InviteResponse_Ok}, nil
}

func (f *fakeGroupClient) AcceptInvite(_ context.Context, in *pbGroup.AcceptInviteParams, _ ...grpc.CallOption) (*pbGroup.AcceptInviteResponse, error) {
	f.groupOf[in.Player] = f.nextID
	return &pbGroup.AcceptInviteResponse{Status: pbGroup.AcceptInviteResponse_Ok}, nil
}

func member(guid uint64, name string) lfg.Member {
	return lfg.Member{PlayerKey: lfg.PlayerKey{RealmID: 1, GUID: guid}, Name: name}
}

// A match that mixes a premade party with a player queued alone used to die on
// the premade members' AlreadyInGroup, and the shard then built a group of its
// own with a locally allocated id.
func TestCreateGroupWithPremadeMembers(t *testing.T) {
	const premadeGroup = uint32(5)

	client := newFakeGroupClient(map[uint64]uint32{
		6:   premadeGroup,
		517: premadeGroup,
		593: premadeGroup,
	}, 9)

	maker := NewGroupServiceMaker(client)

	leader := lfg.PlayerKey{RealmID: 1, GUID: 4007}
	members := []lfg.Member{member(517, "Galan"), member(6, "Kbve"), member(593, "Sibooatha")}

	groupID, err := maker.CreateGroup(context.Background(), leader, "Faenia", members)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if groupID != 9 {
		t.Fatalf("group id = %d, want 9", groupID)
	}
	if len(client.invites) != len(members) {
		t.Fatalf("invited %d members, want %d", len(client.invites), len(members))
	}
	if len(client.leaves) != 3 {
		t.Fatalf("left %d groups, want 3 (the premade members)", len(client.leaves))
	}
}

// Nobody grouped means no Leave traffic at all: the lookup is the only cost.
func TestCreateGroupAllSolo(t *testing.T) {
	client := newFakeGroupClient(map[uint64]uint32{}, 4)
	maker := NewGroupServiceMaker(client)

	leader := lfg.PlayerKey{RealmID: 1, GUID: 100}
	members := []lfg.Member{member(101, "A"), member(102, "B")}

	groupID, err := maker.CreateGroup(context.Background(), leader, "Leader", members)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if groupID != 4 {
		t.Fatalf("group id = %d, want 4", groupID)
	}
	if len(client.leaves) != 0 {
		t.Fatalf("left %d groups, want 0", len(client.leaves))
	}
}

// The leader carrying the premade group is the case the member loop alone does
// not cover: leaving is what stops the new roster being grafted onto the old.
func TestCreateGroupLeaderInPremadeGroup(t *testing.T) {
	client := newFakeGroupClient(map[uint64]uint32{42: 7}, 8)
	maker := NewGroupServiceMaker(client)

	leader := lfg.PlayerKey{RealmID: 1, GUID: 42}
	members := []lfg.Member{member(43, "Solo")}

	if _, err := maker.CreateGroup(context.Background(), leader, "Leader", members); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if len(client.leaves) != 1 || client.leaves[0] != 42 {
		t.Fatalf("leaves = %v, want [42]", client.leaves)
	}
}
