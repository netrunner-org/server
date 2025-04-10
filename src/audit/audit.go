package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"covalence/src/db/postgres"
	"covalence/src/db/postgres/sqlc"
)

// Trace represents a full request trace with all related data
type Trace struct {
	RequestID         string
	UserID            string
	Model             string
	Inputs            []map[string]interface{}
	Response          map[string]interface{}
	RequestParameters map[string]interface{}
	FirewallInfo      []FirewallEvent
	ClientIP          string
	RiskScore         float64
	Blocked           bool
	BlockedReason     string
}

type FirewallEvent struct {
	RequestID     string
	FirewallID    string
	FirewallType  string
	Blocked       bool
	BlockedReason string
	RiskScore     float64
}

type Request struct {
	UserID     string
	APIKeyID   string
	Model      string
	TargetURL  string
	Inputs     []map[string]interface{}
	Parameters map[string]interface{}
	ClientIP   string
}

// LogRequest creates a request log entry
func LogRequest(ctx context.Context, r Request, db *postgres.DB) (string, error) {

	db.Mu.Lock()
	defer db.Mu.Unlock()

	// Messages parameters to JSON
	// Convert each message to JSON and store in a list
	var inputBytesList [][]byte
	for _, input := range r.Inputs {
		inputBytes, err := json.Marshal(input)
		if err != nil {
			return "", fmt.Errorf("invalid messages: %w", err)
		}
		inputBytesList = append(inputBytesList, inputBytes)
	}

	// Convert parameters to JSON
	paramsBytes, err := json.Marshal(r.Parameters)
	if err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	// Parse IP if provided
	var clientIP *netip.Addr
	if r.ClientIP != "" {
		ip, err := netip.ParseAddr(r.ClientIP)
		if err != nil {
			return "", fmt.Errorf("invalid IP: %w", err)
		}
		clientIP = &ip
	}

	// Prepare pgtype values
	var userUUID, apiKeyUUID pgtype.UUID
	userUUID.Scan(r.UserID)
	apiKeyUUID.Scan(r.APIKeyID)

	// Execute insert
	req, err := db.Queries.InsertRequestLog(ctx, sqlc.InsertRequestLogParams{
		UserID:     userUUID,
		ApiKeyID:   apiKeyUUID,
		Model:      r.Model,
		TargetUrl:  r.TargetURL,
		Inputs:     inputBytesList,
		Parameters: paramsBytes,
		ClientIp:   clientIP,
	})

	if err != nil {
		return "", err
	}

	return req.RequestID.String(), nil
}

type Response struct {
	RequestID string
	Response  map[string]interface{}
	LatencyMs int64
}

// LogResponse records a response to an existing request
func LogResponse(ctx context.Context, r Response, db *postgres.DB) error {

	db.Mu.Lock()
	defer db.Mu.Unlock()

	var reqUUID pgtype.UUID
	reqUUID.Scan(r.RequestID)

	var pgLatency pgtype.Int4
	pgLatency.Scan(r.LatencyMs)

	// Turn Parameters into bytes json
	responseBytes, err := json.Marshal(r.Response)
	if err != nil {
		return fmt.Errorf("invalid response: %w", err)
	}

	_, err = db.Queries.InsertResponseLog(ctx, sqlc.InsertResponseLogParams{
		RequestID: reqUUID,
		Response:  responseBytes,
		LatencyMs: pgLatency,
	})

	return err
}

// LogFirewall records a firewall event for a request
func LogFirewallEvent(ctx context.Context, fe FirewallEvent, db *postgres.DB) error {

	db.Mu.Lock()
	defer db.Mu.Unlock()

	// Convert request ID
	var reqUUID pgtype.UUID
	err := reqUUID.Scan(fe.RequestID)
	if err != nil {
		return fmt.Errorf("invalid request ID: %w", err)
	}

	var blocked pgtype.Bool
	err = blocked.Scan(fe.Blocked)
	if err != nil {
		return fmt.Errorf("invalid blocked value: %w", err)
	}

	var blockedReason pgtype.Text
	err = blockedReason.Scan(fe.BlockedReason)
	if err != nil {
		return fmt.Errorf("invalid blocked reason: %w", err)
	}

	var riskScore pgtype.Numeric
	err = riskScore.Scan(fmt.Sprintf("%f", fe.RiskScore))
	if err != nil {
		return fmt.Errorf("invalid risk score: %w", err)
	}

	_, err = db.Queries.InsertFirewallEvent(ctx, sqlc.InsertFirewallEventParams{
		RequestID:     reqUUID,
		FirewallID:    fe.FirewallID,
		FirewallType:  fe.FirewallType,
		Blocked:       blocked,
		BlockedReason: blockedReason,
		RiskScore:     riskScore,
	})

	return err
}

// GetTrace retrieves the full trace for a request
func GetTrace(ctx context.Context, requestID string, db *postgres.DB) (Trace, error) {
	db.Mu.Lock()
	defer db.Mu.Unlock()

	var reqUUID pgtype.UUID
	reqUUID.Scan(requestID)

	rows, err := db.Queries.GetRequestFullTrace(ctx, reqUUID)
	if err != nil {
		return Trace{}, err
	}

	if len(rows) == 0 {
		return Trace{}, fmt.Errorf("request not found")
	}

	// Create basic trace from first row
	row := rows[0]

	// Parse parameters
	var params map[string]interface{}
	json.Unmarshal(row.Parameters, &params) // Ignoring error, empty map is fine

	// Parse parameters
	var inputs []map[string]interface{}
	for _, input := range row.Inputs {
		var msg map[string]interface{}
		err := json.Unmarshal(input, &msg)
		if err != nil {
			return Trace{}, fmt.Errorf("invalid messages: %w", err)
		}
		inputs = append(inputs, msg)
	}

	var response map[string]interface{}
	err = json.Unmarshal(row.Response, &response)
	if err != nil {
		return Trace{}, fmt.Errorf("invalid response: %w", err)
	}

	trace := Trace{
		RequestID:         row.RequestID.String(),
		UserID:            row.UserID.String(),
		Model:             row.Model,
		Inputs:            inputs,
		Response:          response,
		RequestParameters: params,
		ClientIP:          "", // Will be populated if client IP exists
		RiskScore:         0,  // Will be populated if risk score exists
		Blocked:           row.Blocked.Bool,
		BlockedReason:     row.BlockedReason.String,
	}

	// Add optional fields if they exist
	if row.ClientIp != nil {
		trace.ClientIP = row.ClientIp.String()
	}

	// Add risk score if valid
	if row.RiskScore.Valid {
		score, err := row.RiskScore.Float64Value()
		if err != nil {
			return Trace{}, fmt.Errorf("invalid risk score: %w", err)
		}
		trace.RiskScore = score.Float64
	}

	// Add firewall events
	events := []FirewallEvent{}
	for _, r := range rows {
		if r.FirewallID.Valid {

			riskScore, err := r.RiskScore.Float64Value()
			if err != nil {
				return Trace{}, fmt.Errorf("invalid risk score: %w", err)
			}

			events = append(events, FirewallEvent{
				RequestID:     r.RequestID.String(),
				FirewallID:    r.FirewallID.String,
				FirewallType:  r.FirewallType.String,
				Blocked:       r.Blocked.Bool,
				BlockedReason: r.BlockedReason.String,
				RiskScore:     riskScore.Float64,
			})
		}
	}
	trace.FirewallInfo = events

	return trace, nil
}

// NewUUID generates a new UUID string
func NewUUID() string {
	return uuid.New().String()
}
