#ifndef __KBVE_AUCTION_API__
#define __KBVE_AUCTION_API__

/*
 * KBVE extension: auction house access from a worldserver.
 *
 * ToCloud9 moved the auction house out of the worldserver into a cluster-wide
 * service, so the in-process AuctionHouseMgr an AzerothCore module would
 * normally drive writes a view nothing reads -- and two shards doing it
 * independently diverge. These calls are the only correct path from a shard to
 * the real auctions.
 *
 * Deliberately NOT named auction-api.h and deliberately not using the TC9
 * prefix. Upstream AzerothCore now maintains deps/libsidecar itself (clustering
 * landed in azerothcore#16832), so if it grows its own auction API the two can
 * sit side by side and this one gets deleted on purpose rather than fighting a
 * same-named file on every rebase.
 *
 * Versioned separately from TC9_VERSION_* for the same reason: bumping theirs
 * would make this fork claim an ABI it does not implement.
 *
 * Symbols resolve when the shared library loads, so a worldserver built against
 * these headers but running on a stock upstream libsidecar would fail at load
 * rather than link. Call KBVEAuctionAvailable() first and disable the feature
 * when it returns false.
 */

#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>

#define KBVE_AUCTION_ABI_VERSION 1

#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
    uint32_t auctionID;
    uint32_t itemEntry;
    uint64_t itemGuid;
    uint32_t itemCount;
    uint64_t ownerGuid;
    uint32_t startBid;
    uint32_t buyout;
    uint32_t bid;
    uint64_t bidderGuid;
} KBVEAuctionListing;

/* Non-zero when this library actually implements the auction extension. */
int KBVEAuctionAvailable(void);
int KBVEAuctionAbiVersion(void);

/* All return 0 on success and -1 on failure. A rejection by the auction
 * service (deposit unaffordable, item gone, outbid) is a failure here, not a
 * separate result: the caller has nothing different to do either way. */
int KBVEAuctionSellItem(
    uint64_t playerGUID,
    uint32_t houseID,
    uint32_t itemEntry,
    uint64_t itemGuid,
    uint32_t itemCount,
    uint32_t startBid,
    uint32_t buyout,
    uint32_t expireTimeSecs,
    uint32_t deposit,
    uint32_t* outAuctionID);

/* Fills up to maxListings entries and writes how many were produced to
 * outCount; outTotal carries the full result size so a caller can page. */
int KBVEAuctionListItems(
    uint64_t playerGUID,
    uint32_t houseID,
    uint32_t listFrom,
    const char* searchedName,
    uint32_t itemClass,
    uint32_t itemSubClass,
    uint32_t quality,
    KBVEAuctionListing* outListings,
    uint32_t maxListings,
    uint32_t* outCount,
    uint32_t* outTotal);

int KBVEAuctionPlaceBid(
    uint64_t playerGUID,
    uint32_t houseID,
    uint32_t auctionID,
    uint32_t price);

int KBVEAuctionCancel(
    uint64_t playerGUID,
    uint32_t houseID,
    uint32_t auctionID);

#ifdef __cplusplus
}
#endif

#endif  // __KBVE_AUCTION_API__
