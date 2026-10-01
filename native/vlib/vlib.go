package vlib

// Tier bit flags. Match the C ndec_lookup_tier enum.
const (
	TierNone   uint32 = 0
	TierWindow uint32 = 1 << 0
	TierGperf  uint32 = 1 << 1
	TierHand   uint32 = 1 << 2
	TierTable  uint32 = 1 << 3

	TiersAll     = TierWindow | TierGperf | TierHand | TierTable
	TiersPerfect = TierWindow | TierGperf | TierHand
)

// Error codes returned by Build as negative values.
const (
	ErrNullArg         = -1
	ErrKeysEmpty       = -2
	ErrKeysTooMany     = -3
	ErrKeyEmpty        = -4
	ErrKeyTooLong      = -5
	ErrKeyInvalidByte  = -6
	ErrKeyDuplicate    = -7
	ErrStorageTooSmall = -8
	ErrNoTierMatches   = -9
)

// TierName resolves a tier flag returned by Build to its ASCII name
// (window/gperf/hand/table/none).
func TierName(t uint32) string {
	switch t {
	case TierWindow:
		return "window"
	case TierGperf:
		return "gperf"
	case TierHand:
		return "hand"
	case TierTable:
		return "table"
	default:
		return "none"
	}
}
