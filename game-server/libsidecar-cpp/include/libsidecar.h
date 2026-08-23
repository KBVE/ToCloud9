#ifndef __LIBSIDECAR_H__
#define __LIBSIDECAR_H__

#include <stdint.h>
#include <stdbool.h>

/* Export/import decoration for Windows DLL */
#ifdef _WIN32
    #ifdef TC9_BUILDING_DLL
        #define TC9_API __declspec(dllexport)
    #else
        #define TC9_API __declspec(dllimport)
    #endif
#else
    #ifdef TC9_BUILDING_DLL
        #define TC9_API __attribute__((visibility("default")))
    #else
        #define TC9_API
    #endif
#endif

/* Include all API headers */
#include "battleground-api.h"
#include "events-group.h"
#include "events-guild.h"
#include "events-servers-registry.h"
#include "monitoring.h"
#include "petition-api.h"
#include "player-interactions-api.h"
#include "player-items-api.h"
#include "player-money-api.h"
#include "player-guild-api.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Main library functions */
TC9_API void TC9InitLib(uint16_t port, uint32_t realmID, uint8_t isCrossRealm, char* availableMaps, uint32_t** assignedMaps, int* assignedMapsSize);
TC9_API void TC9GracefulShutdown();
TC9_API void TC9ProcessGRPCOrHTTPRequests();
TC9_API void TC9ProcessEventsHooks();

/* GUID generation */
TC9_API uint64_t TC9GetNextAvailableCharacterGuid(int realmID);
TC9_API uint64_t TC9GetNextAvailableItemGuid(int realmID);
TC9_API uint64_t TC9GetNextAvailableInstanceGuid(int realmID);

/* Map loading notification */
TC9_API void TC9ReadyToAcceptPlayersFromMaps(uint32_t* maps, int mapsLen);

/* Online status notifications for in-process sessions (e.g. server-side
 * bots). Sessions that log in through a gateway already get these events
 * published by the gateway itself — only call these for sessions WITHOUT
 * a gateway connection, otherwise events are duplicated. The sidecar
 * fills RealmID and uses its servers-registry ID as GatewayID so that
 * charserver purges these entries when this game server dies. */
TC9_API void TC9CharacterLoggedIn(uint64_t charGUID, const char* charName, uint8_t charRace, uint8_t charClass, uint8_t charGender, uint8_t charLevel, uint32_t charZone, uint32_t charMap, float charPosX, float charPosY, float charPosZ, uint32_t charGuildID, uint32_t accountID);
TC9_API void TC9CharacterLoggedOut(uint64_t charGUID, const char* charName, uint32_t charGuildID, uint32_t accountID);

/* Post-login field updates for in-process sessions. Batched and merged
 * per character (same barrier semantics as the gateway) and published as
 * gw.char.chars-updates so charserver (/who), guildserver and groupserver
 * caches stay fresh. Same rule as above: only call for sessions WITHOUT
 * a gateway connection. */
TC9_API void TC9CharacterZoneChanged(uint64_t charGUID, uint32_t mapID, uint32_t areaID, uint32_t zoneID);
TC9_API void TC9CharacterLevelChanged(uint64_t charGUID, uint8_t level);

/* Generic NATS pub/sub. Payloads are opaque bytes, subjects are arbitrary.
 * Subscription callbacks run on the thread draining TC9ProcessEventsHooks
 * (the world update thread), not on the NATS delivery thread. Both return
 * 0 on success, -1 on failure.
 *
 * Example — a mod broadcasting and consuming its own events:
 *
 *   // Publish (any thread):
 *   const char msg[] = "{\"zone\":1519,\"boss\":466}";
 *   TC9NatsPublish("mymod.boss.spawned", msg, sizeof(msg) - 1);
 *
 *   // Subscribe once at startup; the handler runs on the world update
 *   // thread, so it is safe to touch game state from it:
 *   void OnBossSpawned(const char* subject, const char* payload, int payloadLen)
 *   {
 *       std::string data(payload, payloadLen);  // payload is not NUL-terminated
 *       // ... react to the event ...
 *   }
 *   TC9NatsSubscribe("mymod.boss.spawned", &OnBossSpawned);
 */
typedef void (*TC9NatsMessageHandler)(const char* subject, const char* payload, int payloadLen);
TC9_API int TC9NatsPublish(const char* subject, const char* payload, int payloadLen);
TC9_API int TC9NatsSubscribe(const char* subject, TC9NatsMessageHandler handler);

/* Group operations for in-process sessions (no gateway to call the group
 * service on their behalf). Blocking gRPC call, do not call from map update
 * threads. Returns 0 on success, -1 on error. */
TC9_API int TC9GroupInvite(uint64_t inviterGUID, uint64_t invitedGUID,
                           const char* inviterName, const char* invitedName);
TC9_API int TC9GroupAcceptInvite(uint64_t playerGUID);
TC9_API int TC9GroupLeave(uint64_t playerGUID);

/* Dungeon finder. The queue lives in the matchmaking service so that one
 * allocator mints every group id: each worldserver's GroupMgr counts from 1
 * independently, so two shards forming a party both call it group 1.
 *
 * eligibleDungeonIDs is the core's own verdict -- level, difficulty, expansion,
 * attunement, gear, lockout, deserter. Matchmaking only intersects these sets
 * and can never widen one, so anything the core does not list here is
 * unreachable. */
typedef struct {
    uint64_t playerGUID;
    uint32_t realmID;
    uint32_t roles; /* bitmask: 1 tank, 2 healer, 4 damage */
    uint32_t level;
    uint32_t classID;
    const char* name;
    const uint32_t* eligibleDungeonIDs;
    int32_t eligibleDungeonCount;
} TC9LFGMember;

enum TC9LFGStatus {
    TC9_LFG_STATUS_NONE = 0,
    TC9_LFG_STATUS_QUEUED = 1,
    TC9_LFG_STATUS_GROUPED = 2
};

typedef struct {
    int32_t status; /* TC9LFGStatus */
    uint64_t groupID;
    uint32_t dungeonID;
    uint32_t assignedRole;
    int64_t queuedAtUnixMilli;
} TC9LFGResult;

/* requestID makes the join idempotent: the queue is in memory only, so a
 * caller that sees instanceID change replays with the same requestID and the
 * original queuedAtUnixMilli instead of losing its place. Pass 0 for
 * queuedAtUnixMilli on a first attempt. */
TC9_API int TC9JoinLFG(const char* requestID,
                       uint64_t leaderGUID,
                       const TC9LFGMember* members, int32_t memberCount,
                       const uint32_t* selectedDungeonIDs, int32_t selectedDungeonCount,
                       int64_t queuedAtUnixMilli,
                       TC9LFGResult* out);
TC9_API int TC9LeaveLFG(uint64_t playerGUID);
TC9_API int TC9GetLFGStatus(uint64_t playerGUID, TC9LFGResult* out);

/* Guild operations for in-process sessions (no gateway to call the guild
 * service on their behalf). The guild service owns guild creation: it
 * allocates the guild id, inserts the guild, default ranks and leader rows,
 * hydrates its cache and publishes the guild.created event. Blocking gRPC
 * call, do not call from map update threads. Returns 0 on success and stores
 * the created guild id in *guildID, -1 on error. */
TC9_API int TC9GuildCreate(uint64_t leaderGUID, const char* name, uint64_t* guildID);

/* Accept a pending guild invite on behalf of an in-process session (no gateway
 * to relay SMSG_GUILD_INVITE / CMSG_GUILD_ACCEPT). The guild service records the
 * member and publishes guild.member.added, which sets the in-process guild id.
 * Blocking gRPC call, do not call from map update threads. 0 on success, -1 on error. */
TC9_API int TC9GuildAcceptInvite(uint64_t guid, const char* name, uint32_t lvl,
    uint32_t race, uint32_t classID, uint32_t gender, uint32_t areaID, uint64_t accountID);

/* Matchmaking notifications */
TC9_API void TC9PlayerLeftBattleground(uint64_t playerGUID, uint32_t realmID, uint32_t instanceID);
TC9_API void TC9BattlegroundStatusChanged(uint32_t instanceID, uint8_t status);

/* Query the queue slot assigned to an invited player (matchmaking owns the BG
 * queues cluster-wide; in-process sessions have no gateway to accept invites).
 * outIsAssignedToThisServer is 1 when the assigned battleground runs on THIS
 * worldserver. Blocking gRPC call, do not call from map update threads.
 * 0 on success, -1 on error or when no battleground is assigned yet. */
TC9_API int TC9BattlegroundQueueDataForLocalPlayer(uint64_t playerGUID, uint32_t* outBgTypeID,
    uint32_t* outInstanceID, uint32_t* outMapID, int* outIsAssignedToThisServer);

/* Confirm to matchmaking that an in-process player entered the battleground
 * (the gateway does this for real players after AddPlayersToBattleground).
 * Blocking gRPC call, do not call from map update threads. 0 on success. */
TC9_API int TC9PlayerJoinedBattleground(uint64_t playerGUID, uint32_t instanceID);

/* Enqueue a solo in-process player into a battleground queue — the same RPC
 * the gateway issues for real players (pvpTeamID: 1 alliance, 2 horde).
 * Blocking gRPC call, do not call from map update threads. 0 on success. */
TC9_API int TC9EnqueueLocalPlayerToBattleground(uint64_t playerGUID, uint32_t playerLvl,
    uint32_t bgTypeID, uint32_t pvpTeamID);

/* Event hooks registration */
TC9_API void TC9SetOnGroupCreatedHook(OnGroupCreatedHook h);
TC9_API void TC9SetOnGroupMemberAddedHook(OnGroupMemberAddedHook h);
TC9_API void TC9SetOnGroupMemberRemovedHook(OnGroupMemberRemovedHook h);
TC9_API void TC9SetOnGroupDisbandedHook(OnGroupDisbandedHook h);
TC9_API void TC9SetOnGroupLootTypeChangedHook(OnGroupLootTypeChangedHook h);
TC9_API void TC9SetOnGroupDungeonDifficultyChangedHook(OnGroupDungeonDifficultyChangedHook h);
TC9_API void TC9SetOnGroupRaidDifficultyChangedHook(OnGroupRaidDifficultyChangedHook h);
TC9_API void TC9SetOnGroupConvertedToRaidHook(OnGroupConvertedToRaidHook h);

TC9_API void TC9SetOnGuildMemberAddedHook(OnGuildMemberAddedHook h);
TC9_API void TC9SetOnGuildMemberRemovedHook(OnGuildMemberRemovedHook h);
TC9_API void TC9SetOnGuildMemberLeftHook(OnGuildMemberLeftHook h);
TC9_API void TC9SetOnGuildCreatedHook(OnGuildCreatedHook h);

TC9_API void TC9SetOnMapsReassignedHook(OnMapsReassignedHook h);

/* Handler registration for gRPC requests */
TC9_API void TC9SetBattlegroundStartHandler(BattlegroundStartHandler h);
TC9_API void TC9SetBattlegroundAddPlayersHandler(BattlegroundAddPlayersHandler h);
TC9_API void TC9SetCanPlayerJoinBattlegroundQueueHandler(CanPlayerJoinBattlegroundQueueHandler h);
TC9_API void TC9SetCanPlayerTeleportToBattlegroundHandler(CanPlayerTeleportToBattlegroundHandler h);
TC9_API void TC9SetCanTurnInGuildPetitionHandler(CanTurnInGuildPetitionHandler h);

TC9_API void TC9SetMonitoringDataCollectorHandler(MonitoringDataCollectorHandler h);

TC9_API void TC9SetCanPlayerInteractWithNPCAndFlagsHandler(CanPlayerInteractWithNPCAndFlagsHandler h);
TC9_API void TC9SetCanPlayerInteractWithGOAndTypeHandler(CanPlayerInteractWithGOAndTypeHandler h);

TC9_API void TC9SetGetPlayerItemsByGuidsHandler(GetPlayerItemsByGuidsHandler h);
TC9_API void TC9SetGetPlayerItemByPosHandler(GetPlayerItemByPosHandler h);
TC9_API void TC9SetRemoveItemsWithGuidsFromPlayerHandler(RemoveItemsWithGuidsFromPlayerHandler h);
TC9_API void TC9SetAddExistingItemToPlayerHandler(AddExistingItemToPlayerHandler h);

TC9_API void TC9SetGetMoneyForPlayerHandler(GetMoneyForPlayerHandler h);
TC9_API void TC9SetModifyMoneyForPlayerHandler(ModifyMoneyForPlayerHandler h);

TC9_API void TC9SetSetPlayerGuildFieldsHandler(SetPlayerGuildFieldsHandler h);

#ifdef __cplusplus
}
#endif

#endif /* __LIBSIDECAR_H__ */
