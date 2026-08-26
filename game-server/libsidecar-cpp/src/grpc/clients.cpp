#include "clients.h"
#include "servers-registry/registry.grpc.pb.h"
#include "guid/guid.grpc.pb.h"
#include "matchmaking/matchmaking.grpc.pb.h"
#include "group/group.grpc.pb.h"
#include "guilds/guilds.grpc.pb.h"
#include "auctionhouse/auctionhouse.grpc.pb.h"
#include <spdlog/spdlog.h>

namespace tc9 {

namespace {
const char* LIB_VERSION = "libsidecar-cpp-v0.0.1";
}

GrpcClients::GrpcClients() {
    spdlog::debug("GrpcClients created");
}

GrpcClients::~GrpcClients() {
    Shutdown();
}

void GrpcClients::Connect(const std::string& registry_addr,
                          const std::string& guid_addr,
                          const std::string& matchmaking_addr,
                          const std::string& group_addr,
                          const std::string& guild_addr,
                          const std::string& auction_addr) {
    std::lock_guard<std::mutex> lock(mutex_);

    spdlog::info("Connecting to gRPC services:");
    spdlog::info("  - Registry: {}", registry_addr);
    spdlog::info("  - GUID: {}", guid_addr);
    spdlog::info("  - Matchmaking: {}", matchmaking_addr);
    spdlog::info("  - Group: {}", group_addr);
    spdlog::info("  - Guild: {}", guild_addr);
    spdlog::info("  - Auction: {}", auction_addr);

    // Create channels (using insecure credentials for now)
    registry_channel_ = grpc::CreateChannel(
        registry_addr, grpc::InsecureChannelCredentials());
    guid_channel_ = grpc::CreateChannel(
        guid_addr, grpc::InsecureChannelCredentials());
    matchmaking_channel_ = grpc::CreateChannel(
        matchmaking_addr, grpc::InsecureChannelCredentials());
    group_channel_ = grpc::CreateChannel(
        group_addr, grpc::InsecureChannelCredentials());
    guild_channel_ = grpc::CreateChannel(
        guild_addr, grpc::InsecureChannelCredentials());
    auction_channel_ = grpc::CreateChannel(
        auction_addr, grpc::InsecureChannelCredentials());

    // Create stubs
    registry_stub_ = v1::ServersRegistryService::NewStub(registry_channel_);
    guid_stub_ = v1::GuidService::NewStub(guid_channel_);
    matchmaking_stub_ = v1::MatchmakingService::NewStub(matchmaking_channel_);
    group_stub_ = v1::GroupService::NewStub(group_channel_);
    guild_stub_ = v1::GuildService::NewStub(guild_channel_);
    auction_stub_ = v1::AuctionHouseService::NewStub(auction_channel_);

    connected_ = true;
    spdlog::info("✅ All gRPC clients connected");
}

namespace {

void FillLFGResult(const std::string& instance_id,
                   int32_t status,
                   uint64_t group_id,
                   uint32_t dungeon_id,
                   uint32_t assigned_role,
                   int64_t queued_at_unix_milli,
                   GrpcClients::LFGResult& out) {
    out.instance_id = instance_id;
    out.status = status;
    out.group_id = group_id;
    out.dungeon_id = dungeon_id;
    out.assigned_role = assigned_role;
    out.queued_at_unix_milli = queued_at_unix_milli;
}

}  // namespace

bool GrpcClients::JoinLFG(uint32_t realm_id,
                          const std::string& request_id,
                          uint64_t leader_guid,
                          const std::vector<LFGMember>& members,
                          const std::vector<uint32_t>& selected_dungeons,
                          int64_t queued_at_unix_milli,
                          LFGResult& out) {
    if (!connected_ || !matchmaking_stub_) {
        spdlog::error("Matchmaking client not connected");
        return false;
    }

    v1::JoinLFGRequest request;
    request.set_api(LIB_VERSION);
    request.set_requestid(request_id);
    request.set_realmid(realm_id);
    request.set_leaderguid(leader_guid);
    request.set_queuedatunixmilli(queued_at_unix_milli);

    for (uint32_t dungeon : selected_dungeons) {
        request.add_selecteddungeonids(dungeon);
    }

    for (const LFGMember& member : members) {
        v1::LFGMember* entry = request.add_members();
        entry->set_realmid(member.realm_id);
        entry->set_playerguid(member.guid);
        entry->set_roles(member.roles);
        entry->set_level(member.level);
        entry->set_classid(member.class_id);
        entry->set_name(member.name);
        for (uint32_t dungeon : member.eligible_dungeons) {
            entry->add_eligibledungeonids(dungeon);
        }
    }

    v1::JoinLFGResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = matchmaking_stub_->JoinLFG(&context, request, &response);
    if (!status.ok()) {
        spdlog::error("JoinLFG failed for leader {}: {}", leader_guid, status.error_message());
        return false;
    }

    FillLFGResult(response.instanceid(), int32_t(response.status()), response.groupid(),
                  response.dungeonid(), uint32_t(response.assignedrole()), 0, out);
    return true;
}

bool GrpcClients::LeaveLFG(uint32_t realm_id, uint64_t player_guid) {
    if (!connected_ || !matchmaking_stub_) {
        spdlog::error("Matchmaking client not connected");
        return false;
    }

    v1::LeaveLFGRequest request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_playerguid(player_guid);

    v1::LeaveLFGResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = matchmaking_stub_->LeaveLFG(&context, request, &response);
    if (!status.ok()) {
        spdlog::error("LeaveLFG failed for {}: {}", player_guid, status.error_message());
        return false;
    }

    return true;
}

bool GrpcClients::GetLFGStatus(uint32_t realm_id, uint64_t player_guid, LFGResult& out) {
    if (!connected_ || !matchmaking_stub_) {
        spdlog::error("Matchmaking client not connected");
        return false;
    }

    v1::GetLFGStatusRequest request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_playerguid(player_guid);

    v1::GetLFGStatusResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = matchmaking_stub_->GetLFGStatus(&context, request, &response);
    if (!status.ok()) {
        spdlog::error("GetLFGStatus failed for {}: {}", player_guid, status.error_message());
        return false;
    }

    FillLFGResult(response.instanceid(), int32_t(response.status()), response.groupid(),
                  response.dungeonid(), uint32_t(response.assignedrole()),
                  response.queuedatunixmilli(), out);
    return true;
}

bool GrpcClients::InviteToGroup(uint32_t realm_id,
                                uint64_t inviter_guid,
                                uint64_t invited_guid,
                                const std::string& inviter_name,
                                const std::string& invited_name) {
    if (!connected_ || !group_stub_) {
        spdlog::error("Group client not connected");
        return false;
    }

    v1::InviteParams request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_inviter(inviter_guid);
    request.set_invited(invited_guid);
    request.set_invitername(inviter_name);
    request.set_invitedname(invited_name);

    v1::InviteResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = group_stub_->Invite(&context, request, &response);
    if (!status.ok()) {
        spdlog::error("InviteToGroup failed for {} -> {}: {}",
                      inviter_guid, invited_guid, status.error_message());
        return false;
    }

    if (response.status() != v1::InviteResponse::Ok) {
        spdlog::warn("InviteToGroup rejected for {} -> {}: status {}",
                     inviter_guid, invited_guid, int(response.status()));
        return false;
    }

    return true;
}

bool GrpcClients::AcceptGroupInvite(uint32_t realm_id, uint64_t player_guid) {
    if (!connected_ || !group_stub_) {
        spdlog::error("Group client not connected");
        return false;
    }

    v1::AcceptInviteParams request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_player(player_guid);

    v1::AcceptInviteResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = group_stub_->AcceptInvite(&context, request, &response);
    if (!status.ok()) {
        spdlog::error("AcceptGroupInvite failed for player {}: {}",
                      player_guid, status.error_message());
        return false;
    }

    if (response.status() != v1::AcceptInviteResponse::Ok) {
        spdlog::warn("AcceptGroupInvite rejected for player {}: status {}",
                     player_guid, int(response.status()));
        return false;
    }

    return true;
}

bool GrpcClients::LeaveGroup(uint32_t realm_id, uint64_t player_guid) {
    if (!connected_ || !group_stub_) {
        spdlog::error("Group client not connected");
        return false;
    }

    v1::GroupLeaveParams request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_player(player_guid);

    v1::GroupLeaveResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = group_stub_->Leave(&context, request, &response);
    if (!status.ok()) {
        spdlog::error("LeaveGroup failed for player {}: {}",
                      player_guid, status.error_message());
        return false;
    }

    return true;
}

void GrpcClients::Shutdown() {
    std::lock_guard<std::mutex> lock(mutex_);

    if (!connected_) {
        return;
    }

    spdlog::info("Shutting down gRPC clients");

    registry_stub_.reset();
    guid_stub_.reset();
    matchmaking_stub_.reset();
    group_stub_.reset();
    guild_stub_.reset();

    registry_channel_.reset();
    guid_channel_.reset();
    matchmaking_channel_.reset();
    group_channel_.reset();
    guild_channel_.reset();

    connected_ = false;
}

std::chrono::system_clock::time_point GrpcClients::Deadline(int seconds) {
    return std::chrono::system_clock::now() + std::chrono::seconds(seconds);
}

bool GrpcClients::RegisterGameServer(
    uint32_t game_port,
    uint32_t health_port,
    uint32_t grpc_port,
    uint32_t realm_id,
    bool is_cross_realm,
    const std::string& available_maps,
    const std::string& preferred_hostname,
    std::string& out_server_id,
    std::vector<uint32_t>& out_assigned_maps) {

    if (!connected_ || !registry_stub_) {
        spdlog::error("Registry client not connected");
        return false;
    }

    v1::RegisterGameServerRequest request;
    request.set_api(LIB_VERSION);
    request.set_gameport(game_port);
    request.set_healthport(health_port);
    request.set_grpcport(grpc_port);
    request.set_realmid(realm_id);
    request.set_iscrossrealm(is_cross_realm);
    request.set_availablemaps(available_maps);
    request.set_preferredhostname(preferred_hostname);

    v1::RegisterGameServerResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = registry_stub_->RegisterGameServer(&context, request, &response);

    if (!status.ok()) {
        spdlog::error("RegisterGameServer RPC failed: {} - {}",
                     status.error_code(), status.error_message());
        return false;
    }

    out_server_id = response.id();
    out_assigned_maps.clear();
    for (const auto& map_id : response.assignedmaps()) {
        out_assigned_maps.push_back(map_id);
    }

    spdlog::info("✅ Registered game server: ID={}, assigned {} maps",
                 out_server_id, out_assigned_maps.size());
    return true;
}

bool GrpcClients::GameServerMapsLoaded(
    const std::string& server_id,
    const std::vector<uint32_t>& maps_loaded) {

    if (!connected_ || !registry_stub_) {
        spdlog::error("Registry client not connected");
        return false;
    }

    v1::GameServerMapsLoadedRequest request;
    request.set_api(LIB_VERSION);
    request.set_gameserverid(server_id);
    for (const auto& map_id : maps_loaded) {
        request.add_mapsloaded(map_id);
    }

    v1::GameServerMapsLoadedResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = registry_stub_->GameServerMapsLoaded(&context, request, &response);

    if (!status.ok()) {
        spdlog::error("GameServerMapsLoaded RPC failed: {} - {}",
                     status.error_code(), status.error_message());
        return false;
    }

    spdlog::info("✅ Notified registry: {} maps loaded", maps_loaded.size());
    return true;
}

bool GrpcClients::RequestGUIDPool(
    uint32_t realm_id,
    int guid_type,
    uint64_t desired_pool_size,
    std::vector<std::pair<uint64_t, uint64_t>>& out_ranges) {

    if (!connected_ || !guid_stub_) {
        spdlog::error("GUID client not connected");
        return false;
    }

    v1::GetGUIDPoolRequest request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_guidtype(static_cast<v1::GuidType>(guid_type));
    request.set_desiredpoolsize(desired_pool_size);

    v1::GetGUIDPoolRequestResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = guid_stub_->GetGUIDPool(&context, request, &response);

    if (!status.ok()) {
        spdlog::error("RequestGUIDPool RPC failed: {} - {}",
                     status.error_code(), status.error_message());
        return false;
    }

    out_ranges.clear();
    for (const auto& range : response.receiverguid()) {
        out_ranges.push_back({range.start(), range.end()});
    }

    uint64_t total_guids = 0;
    for (const auto& [start, end] : out_ranges) {
        total_guids += (end - start + 1);
    }

    spdlog::info("✅ Received GUID pool: {} ranges, {} total GUIDs",
                 out_ranges.size(), total_guids);
    return true;
}

bool GrpcClients::AuctionSellItem(
    uint32_t realm_id,
    uint64_t player_guid,
    uint32_t house_id,
    uint32_t item_entry,
    uint64_t item_guid,
    uint32_t item_count,
    uint32_t start_bid,
    uint32_t buyout,
    uint32_t expire_time_secs,
    uint32_t deposit,
    uint32_t& out_auction_id) {

    if (!connected_ || !auction_stub_) {
        spdlog::error("Auction client not connected");
        return false;
    }

    v1::AuctionSellItemRequest request;
    request.set_realmid(realm_id);
    request.set_playerguid(player_guid);
    request.set_houseid(house_id);
    request.set_itementry(item_entry);
    request.set_itemguid(item_guid);
    request.set_itemcount(item_count);
    request.set_startbid(start_bid);
    request.set_buyout(buyout);
    request.set_expiretimesecs(expire_time_secs);
    request.set_deposit(deposit);

    v1::AuctionSellItemResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline(5));

    grpc::Status status = auction_stub_->SellItem(&context, request, &response);

    if (!status.ok()) {
        spdlog::warn("AuctionSellItem RPC failed: {} - {}",
                     status.error_code(), status.error_message());
        return false;
    }

    // The service reports domain failures (not enough money for the deposit, a
    // item that is no longer sellable) in the response rather than the status,
    // so a non-zero error here is still an ok() RPC.
    if (response.error() != v1::AH_OK) {
        spdlog::debug("AuctionSellItem rejected for player {}: error {}",
                      player_guid, static_cast<int>(response.error()));
        return false;
    }

    out_auction_id = response.auctionid();
    return true;
}

bool GrpcClients::AuctionListItems(
    uint32_t realm_id,
    uint64_t player_guid,
    uint32_t house_id,
    uint32_t list_from,
    const std::string& searched_name,
    uint32_t item_class,
    uint32_t item_subclass,
    uint32_t quality,
    std::vector<AuctionListing>& out_listings,
    uint32_t& out_total) {

    if (!connected_ || !auction_stub_) {
        spdlog::error("Auction client not connected");
        return false;
    }

    v1::AuctionListItemsRequest request;
    request.set_realmid(realm_id);
    request.set_playerguid(player_guid);
    request.set_houseid(house_id);
    request.set_listfrom(list_from);
    request.set_searchedname(searched_name);
    request.set_itemclass(item_class);
    request.set_itemsubclass(item_subclass);
    request.set_quality(quality);

    v1::AuctionListItemsResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline(5));

    grpc::Status status = auction_stub_->ListItems(&context, request, &response);

    if (!status.ok()) {
        spdlog::warn("AuctionListItems RPC failed: {} - {}",
                     status.error_code(), status.error_message());
        return false;
    }

    out_listings.clear();
    for (const auto& item : response.items()) {
        AuctionListing listing;
        listing.auction_id = item.auctionid();
        listing.item_entry = item.itementry();
        listing.item_guid = item.itemguid();
        listing.item_count = item.itemcount();
        listing.owner_guid = item.ownerguid();
        listing.start_bid = item.startbid();
        listing.buyout = item.buyout();
        listing.current_bid = item.bid();
        listing.bidder_guid = item.bidderguid();
        out_listings.push_back(listing);
    }
    out_total = response.totalcount();
    return true;
}

bool GrpcClients::AuctionPlaceBid(
    uint32_t realm_id,
    uint64_t player_guid,
    uint32_t house_id,
    uint32_t auction_id,
    uint32_t price) {

    if (!connected_ || !auction_stub_) {
        spdlog::error("Auction client not connected");
        return false;
    }

    v1::AuctionPlaceBidRequest request;
    request.set_realmid(realm_id);
    request.set_playerguid(player_guid);
    request.set_houseid(house_id);
    request.set_auctionid(auction_id);
    request.set_price(price);

    v1::AuctionPlaceBidResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline(5));

    grpc::Status status = auction_stub_->PlaceBid(&context, request, &response);

    if (!status.ok()) {
        spdlog::warn("AuctionPlaceBid RPC failed: {} - {}",
                     status.error_code(), status.error_message());
        return false;
    }

    if (response.error() != v1::AH_OK) {
        spdlog::debug("AuctionPlaceBid rejected for player {}: error {}",
                      player_guid, static_cast<int>(response.error()));
        return false;
    }

    return true;
}

bool GrpcClients::AuctionCancel(
    uint32_t realm_id,
    uint64_t player_guid,
    uint32_t house_id,
    uint32_t auction_id) {

    if (!connected_ || !auction_stub_) {
        spdlog::error("Auction client not connected");
        return false;
    }

    v1::AuctionCancelRequest request;
    request.set_realmid(realm_id);
    request.set_playerguid(player_guid);
    request.set_houseid(house_id);
    request.set_auctionid(auction_id);

    v1::AuctionCancelResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline(5));

    grpc::Status status = auction_stub_->CancelAuction(&context, request, &response);

    if (!status.ok()) {
        spdlog::warn("AuctionCancel RPC failed: {} - {}",
                     status.error_code(), status.error_message());
        return false;
    }

    if (response.error() != v1::AH_OK) {
        spdlog::debug("AuctionCancel rejected for player {}: error {}",
                      player_guid, static_cast<int>(response.error()));
        return false;
    }

    return true;
}

bool GrpcClients::PlayerLeftBattleground(
    uint32_t realm_id,
    uint64_t player_guid,
    uint32_t instance_id,
    bool is_cross_realm) {

    if (!connected_ || !matchmaking_stub_) {
        spdlog::error("Matchmaking client not connected");
        return false;
    }

    v1::PlayerLeftBattlegroundRequest request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_playerguid(player_guid);
    request.set_instanceid(instance_id);
    request.set_iscrossrealm(is_cross_realm);

    v1::PlayerLeftBattlegroundResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline(2));  // Shorter timeout for async notification

    grpc::Status status = matchmaking_stub_->PlayerLeftBattleground(&context, request, &response);

    if (!status.ok()) {
        spdlog::warn("PlayerLeftBattleground RPC failed: {} - {}",
                    status.error_code(), status.error_message());
        return false;
    }

    spdlog::debug("Notified matchmaking: player {} left BG instance {}",
                 player_guid, instance_id);
    return true;
}

bool GrpcClients::BattlegroundQueueDataForPlayer(
    uint32_t realm_id,
    uint64_t player_guid,
    uint32_t& out_bg_type_id,
    uint32_t& out_instance_id,
    uint32_t& out_map_id,
    std::string& out_gameserver_address) {

    if (!connected_ || !matchmaking_stub_) {
        spdlog::error("Matchmaking client not connected");
        return false;
    }

    v1::BattlegroundQueueDataForPlayerRequest request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_playerguid(player_guid);

    v1::BattlegroundQueueDataForPlayerResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = matchmaking_stub_->BattlegroundQueueDataForPlayer(&context, request, &response);

    if (!status.ok()) {
        spdlog::warn("BattlegroundQueueDataForPlayer RPC failed: {} - {}",
                    status.error_code(), status.error_message());
        return false;
    }

    if (response.slots_size() == 0 || !response.slots(0).has_assignedbattlegrounddata()) {
        return false;
    }

    const auto& slot = response.slots(0);
    const auto& bg_data = slot.assignedbattlegrounddata();
    out_bg_type_id = slot.bgtypeid();
    out_instance_id = bg_data.assignedbattlegroundinstanceid();
    out_map_id = bg_data.mapid();
    out_gameserver_address = bg_data.gameserveraddress();
    return true;
}

bool GrpcClients::EnqueueToBattleground(
    uint32_t realm_id,
    uint64_t player_guid,
    uint32_t player_lvl,
    uint32_t bg_type_id,
    uint32_t team_id) {

    if (!connected_ || !matchmaking_stub_) {
        spdlog::error("Matchmaking client not connected");
        return false;
    }

    v1::EnqueueToBattlegroundRequest request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_leaderguid(player_guid);
    request.set_leaderslvl(player_lvl);
    request.set_bgtypeid(bg_type_id);
    request.set_teamid(static_cast<v1::PVPTeamID>(team_id));

    v1::EnqueueToBattlegroundResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = matchmaking_stub_->EnqueueToBattleground(&context, request, &response);

    if (!status.ok()) {
        spdlog::warn("EnqueueToBattleground RPC failed: {} - {}",
                    status.error_code(), status.error_message());
        return false;
    }

    spdlog::debug("Enqueued player {} to BG type {} (team {})",
                 player_guid, bg_type_id, team_id);
    return true;
}

bool GrpcClients::PlayerJoinedBattleground(
    uint32_t realm_id,
    uint64_t player_guid,
    uint32_t instance_id,
    bool is_cross_realm) {

    if (!connected_ || !matchmaking_stub_) {
        spdlog::error("Matchmaking client not connected");
        return false;
    }

    v1::PlayerJoinedBattlegroundRequest request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_playerguid(player_guid);
    request.set_instanceid(instance_id);
    request.set_iscrossrealm(is_cross_realm);

    v1::PlayerJoinedBattlegroundResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = matchmaking_stub_->PlayerJoinedBattleground(&context, request, &response);

    if (!status.ok()) {
        spdlog::warn("PlayerJoinedBattleground RPC failed: {} - {}",
                    status.error_code(), status.error_message());
        return false;
    }

    spdlog::debug("Notified matchmaking: player {} joined BG instance {}",
                 player_guid, instance_id);
    return true;
}

bool GrpcClients::FindGameServerAddressByID(
    const std::string& server_id,
    std::string& out_address) {

    if (!connected_ || !registry_stub_) {
        spdlog::error("Registry client not connected");
        return false;
    }

    v1::ListAllGameServersRequest request;
    request.set_api(LIB_VERSION);

    v1::ListGameServersResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = registry_stub_->ListAllGameServers(&context, request, &response);

    if (!status.ok()) {
        spdlog::warn("ListAllGameServers RPC failed: {} - {}",
                    status.error_code(), status.error_message());
        return false;
    }

    for (const auto& server : response.gameservers()) {
        if (server.id() == server_id) {
            out_address = server.address();
            return true;
        }
    }

    return false;
}

bool GrpcClients::BattlegroundStatusChanged(
    uint32_t realm_id,
    uint32_t instance_id,
    bool is_cross_realm,
    uint8_t status) {

    if (!connected_ || !matchmaking_stub_) {
        spdlog::error("Matchmaking client not connected");
        return false;
    }

    v1::BattlegroundStatusChangedRequest request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_instanceid(instance_id);
    request.set_iscrossrealm(is_cross_realm);
    request.set_status(static_cast<v1::BattlegroundStatusChangedRequest_Status>(status));

    v1::BattlegroundStatusChangedResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline(2));  // Shorter timeout for async notification

    grpc::Status status_result = matchmaking_stub_->BattlegroundStatusChanged(&context, request, &response);

    if (!status_result.ok()) {
        spdlog::warn("BattlegroundStatusChanged RPC failed: {} - {}",
                    status_result.error_code(), status_result.error_message());
        return false;
    }

    spdlog::debug("Notified matchmaking: BG instance {} status changed to {}",
                 instance_id, status);
    return true;
}

bool GrpcClients::CreateGuild(
    uint32_t realm_id,
    uint64_t leader_guid,
    const std::string& name,
    uint64_t& out_guild_id) {

    if (!connected_ || !guild_stub_) {
        spdlog::error("Guild client not connected");
        return false;
    }

    v1::CreateGuildParams request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);
    request.set_leaderguid(leader_guid);
    request.set_name(name);

    v1::CreateGuildResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = guild_stub_->CreateGuild(&context, request, &response);
    if (!status.ok()) {
        spdlog::error("CreateGuild failed for leader {}: {} - {}",
                      leader_guid, status.error_code(), status.error_message());
        return false;
    }

    out_guild_id = response.guildid();
    spdlog::info("✅ Guild '{}' created with id {} (leader {})",
                 name, out_guild_id, leader_guid);
    return true;
}

bool GrpcClients::AcceptGuildInvite(
    uint32_t realm_id,
    uint64_t guid,
    const std::string& name,
    uint32_t lvl,
    uint32_t race,
    uint32_t class_id,
    uint32_t gender,
    uint32_t area_id,
    uint64_t account_id) {

    if (!connected_ || !guild_stub_) {
        spdlog::error("Guild client not connected");
        return false;
    }

    v1::InviteAcceptedParams request;
    request.set_api(LIB_VERSION);
    request.set_realmid(realm_id);

    auto* character = request.mutable_character();
    character->set_guid(guid);
    character->set_name(name);
    character->set_lvl(lvl);
    character->set_race(race);
    character->set_classid(class_id);
    character->set_gender(gender);
    character->set_areaid(area_id);
    character->set_accountid(account_id);

    v1::InviteAcceptedResponse response;
    grpc::ClientContext context;
    context.set_deadline(Deadline());

    grpc::Status status = guild_stub_->InviteAccepted(&context, request, &response);
    if (!status.ok()) {
        spdlog::error("AcceptGuildInvite failed for player {}: {} - {}",
                      guid, status.error_code(), status.error_message());
        return false;
    }

    return true;
}

}  // namespace tc9
