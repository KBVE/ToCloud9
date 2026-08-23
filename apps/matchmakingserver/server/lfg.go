package server

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	matchmaking "github.com/walkline/ToCloud9/apps/matchmakingserver"
	"github.com/walkline/ToCloud9/apps/matchmakingserver/lfg"
	"github.com/walkline/ToCloud9/gen/matchmaking/pb"
)

func (s *MatchmakingServer) JoinLFG(ctx context.Context, req *pb.JoinLFGRequest) (*pb.JoinLFGResponse, error) {
	if s.lfgService == nil {
		return nil, status.Error(codes.Unavailable, "lfg is not enabled")
	}

	// Callers that do not know their battlegroup send 0. Resolving it here
	// rather than defaulting keeps two realms from sharing one dungeon queue
	// when they were never put in the same battlegroup, and keeps the queue key
	// out of the realm-id namespace, where a realm 1 and a battlegroup 1 would
	// otherwise collide.
	battlegroupID := req.BattlegroupID
	if battlegroupID == 0 {
		resolved, err := s.battlegroups.BattleGroupIDByRealmID(ctx, req.RealmID)
		if err != nil {
			return nil, err
		}
		battlegroupID = resolved
	}

	entry := &lfg.Entry{
		RequestID:     req.RequestID,
		BattlegroupID: battlegroupID,
		Leader:        lfg.PlayerKey{RealmID: req.RealmID, GUID: req.LeaderGUID},
		Members:       make([]lfg.Member, 0, len(req.Members)),
	}
	if req.QueuedAtUnixMilli > 0 {
		entry.QueuedAt = unixMilli(req.QueuedAtUnixMilli)
	}

	entry.SelectedDungeons = make(map[uint32]struct{}, len(req.SelectedDungeonIDs))
	for _, dungeon := range req.SelectedDungeonIDs {
		entry.SelectedDungeons[dungeon] = struct{}{}
	}

	for _, member := range req.Members {
		eligible := make(map[uint32]struct{}, len(member.EligibleDungeonIDs))
		for _, dungeon := range member.EligibleDungeonIDs {
			eligible[dungeon] = struct{}{}
		}
		entry.Members = append(entry.Members, lfg.Member{
			PlayerKey:        lfg.PlayerKey{RealmID: member.RealmID, GUID: member.PlayerGUID},
			Name:             member.Name,
			Roles:            lfg.Role(member.Roles),
			Level:            uint8(member.Level),
			Class:            uint8(member.ClassID),
			EligibleDungeons: eligible,
		})
	}

	playerStatus, err := s.lfgService.Join(ctx, entry)
	if err != nil {
		switch {
		case errors.Is(err, lfg.ErrInvalidEntry):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, lfg.ErrPlayerAlreadyQueued):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, err
	}

	return &pb.JoinLFGResponse{
		Api:          matchmaking.Ver,
		InstanceID:   s.lfgService.InstanceID(),
		Status:       lfgStatusToPB(playerStatus.Status),
		GroupID:      playerStatus.GroupID,
		DungeonID:    playerStatus.DungeonID,
		AssignedRole: pb.LFGRole(playerStatus.AssignedRole),
	}, nil
}

func (s *MatchmakingServer) LeaveLFG(_ context.Context, req *pb.LeaveLFGRequest) (*pb.LeaveLFGResponse, error) {
	if s.lfgService == nil {
		return nil, status.Error(codes.Unavailable, "lfg is not enabled")
	}

	err := s.lfgService.Leave(lfg.PlayerKey{RealmID: req.RealmID, GUID: req.PlayerGUID})
	// Leaving a queue you are not in is the normal result of a race with a
	// match forming, not something the caller can act on.
	if err != nil && !errors.Is(err, lfg.ErrPlayerNotQueued) {
		return nil, err
	}

	return &pb.LeaveLFGResponse{
		Api:        matchmaking.Ver,
		InstanceID: s.lfgService.InstanceID(),
	}, nil
}

func (s *MatchmakingServer) GetLFGStatus(_ context.Context, req *pb.GetLFGStatusRequest) (*pb.GetLFGStatusResponse, error) {
	if s.lfgService == nil {
		return nil, status.Error(codes.Unavailable, "lfg is not enabled")
	}

	playerStatus := s.lfgService.Status(lfg.PlayerKey{RealmID: req.RealmID, GUID: req.PlayerGUID})

	resp := &pb.GetLFGStatusResponse{
		Api:          matchmaking.Ver,
		InstanceID:   s.lfgService.InstanceID(),
		Status:       lfgStatusToPB(playerStatus.Status),
		GroupID:      playerStatus.GroupID,
		DungeonID:    playerStatus.DungeonID,
		AssignedRole: pb.LFGRole(playerStatus.AssignedRole),
	}
	if !playerStatus.QueuedAt.IsZero() {
		resp.QueuedAtUnixMilli = playerStatus.QueuedAt.UnixMilli()
	}
	return resp, nil
}

func lfgStatusToPB(s lfg.Status) pb.LFGQueueStatus {
	switch s {
	case lfg.StatusQueued:
		return pb.LFGQueueStatus_LFGQueueStatusQueued
	case lfg.StatusGrouped:
		return pb.LFGQueueStatus_LFGQueueStatusGrouped
	}
	return pb.LFGQueueStatus_LFGQueueStatusNone
}

func unixMilli(v int64) time.Time { return time.UnixMilli(v) }
