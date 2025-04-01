// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.28.0

package sqlc

import (
	"net/netip"

	"github.com/jackc/pgx/v5/pgtype"
)

type AuditArchive struct {
	ArchiveID   pgtype.UUID
	RequestID   pgtype.UUID
	S3Path      string
	ArchivedAt  pgtype.Timestamptz
	ArchiveHash pgtype.Text
}

type FirewallEvent struct {
	FirewallEventID pgtype.UUID
	RequestID       pgtype.UUID
	FirewallID      string
	FirewallType    string
	Blocked         pgtype.Bool
	BlockedReason   pgtype.Text
	RiskScore       pgtype.Numeric
	EvaluatedAt     pgtype.Timestamptz
}

type RequestLog struct {
	RequestID  pgtype.UUID
	UserID     pgtype.UUID
	ApiKeyID   pgtype.UUID
	Model      string
	TargetUrl  string
	Inputs     [][]byte
	Parameters []byte
	ReceivedAt pgtype.Timestamptz
	ClientIp   *netip.Addr
	Archived   pgtype.Bool
}

type ResponseLog struct {
	ResponseID pgtype.UUID
	RequestID  pgtype.UUID
	Response   []byte
	CreatedAt  pgtype.Timestamptz
	LatencyMs  pgtype.Int4
}
