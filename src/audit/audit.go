package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"netrunner/src/db/postgres"
)

// Store handles database operations with a simplified interface
type Store struct {
	pool    *pgxpool.Pool
	queries *postgres.Queries
	mu      sync.Mutex
}

// Trace represents a full request trace with all related data
type Trace struct {
	RequestID     string
	UserID        string
	Model         string
	Messages      []map[string]interface{}
	Response      string
	Parameters    map[string]interface{}
	FirewallInfo  []FirewallEvent
	ClientIP      string
	RiskScore     float64
	Blocked       bool
	BlockedReason string
}

type FirewallEvent struct {
	RequestID     string
	FirewallID    string
	FirewallType  string
	Blocked       bool
	BlockedReason string
	RiskScore     float64
}

// New creates a database store with connection pooling
func New(ctx context.Context, connString string) (*Store, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("database connection failed: %w", err)
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database ping failed: %w", err)
	}

	return &Store{
		pool:    pool,
		queries: postgres.New(pool),
	}, nil
}

type Request struct {
	UserID     string
	APIKeyID   string
	Model      string
	Endpoint   string
	Messages   []map[string]interface{}
	Parameters map[string]interface{}
	ClientIP   string
}

// LogRequest creates a request log entry
func (s *Store) LogRequest(ctx context.Context, r Request) (string, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	// Messages parameters to JSON
	// Convert each message to JSON and store in a list
	var messageBytesList [][]byte
	for _, message := range r.Messages {
		messageBytes, err := json.Marshal(message)
		if err != nil {
			return "", fmt.Errorf("invalid messages: %w", err)
		}
		messageBytesList = append(messageBytesList, messageBytes)
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
	req, err := s.queries.InsertRequestLog(ctx, postgres.InsertRequestLogParams{
		UserID:     userUUID,
		ApiKeyID:   apiKeyUUID,
		Model:      r.Model,
		Endpoint:   r.Endpoint,
		Messages:   messageBytesList,
		Parameters: paramsBytes,
		ClientIp:   clientIP,
	})

	if err != nil {
		return "", err
	}

	return req.RequestID.String(), nil
}

type Response struct {
	RequestID    string
	Response     string
	LatencyMs    int
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// LogResponse records a response to an existing request
func (s *Store) LogResponse(ctx context.Context, r Response) error {

	s.mu.Lock()
	defer s.mu.Unlock()

	var reqUUID pgtype.UUID
	reqUUID.Scan(r.RequestID)

	var pgLatency, pgInput, pgOutput, pgTotal pgtype.Int4
	pgLatency.Scan(r.LatencyMs)
	pgInput.Scan(r.InputTokens)
	pgOutput.Scan(r.OutputTokens)
	pgTotal.Scan(r.TotalTokens)

	_, err := s.queries.InsertResponseLog(ctx, postgres.InsertResponseLogParams{
		RequestID:    reqUUID,
		Response:     r.Response,
		LatencyMs:    pgLatency,
		InputTokens:  pgInput,
		OutputTokens: pgOutput,
		TotalTokens:  pgTotal,
	})

	return err
}

// LogFirewall records a firewall event for a request
func (s *Store) LogFirewallEvent(ctx context.Context, fe FirewallEvent) error {

	s.mu.Lock()
	defer s.mu.Unlock()

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

	_, err = s.queries.InsertFirewallEvent(ctx, postgres.InsertFirewallEventParams{
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
func (s *Store) GetTrace(ctx context.Context, requestID string) (Trace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var reqUUID pgtype.UUID
	reqUUID.Scan(requestID)

	rows, err := s.queries.GetRequestFullTrace(ctx, reqUUID)
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
	var messages []map[string]interface{}
	for _, message := range row.Messages {
		var msg map[string]interface{}
		err := json.Unmarshal(message, &msg)
		if err != nil {
			return Trace{}, fmt.Errorf("invalid messages: %w", err)
		}
		messages = append(messages, msg)
	}

	trace := Trace{
		RequestID:     row.RequestID.String(),
		UserID:        row.UserID.String(),
		Model:         row.Model,
		Messages:      messages,
		Response:      row.Response.String,
		Parameters:    params,
		ClientIP:      "", // Will be populated if client IP exists
		RiskScore:     0,  // Will be populated if risk score exists
		Blocked:       row.Blocked.Bool,
		BlockedReason: row.BlockedReason.String,
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

// Close closes the database connection pool
func (s *Store) Close() {
	s.pool.Close()
}

// NewUUID generates a new UUID string
func NewUUID() string {
	return uuid.New().String()
}
