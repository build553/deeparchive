// Device authorization: lets a CLI with no browser of its own (backup-cli)
// authenticate the same way `gh auth login` does — print a short code,
// have the person enter it on a page they open themselves, and poll until
// that page's owner approves it. Modeled on RFC 8628 (OAuth 2.0 Device
// Authorization Grant); the wire shapes below intentionally echo it
// (device_code/user_code/interval, "authorization_pending"/"slow_down"
// error codes) since that vocabulary is already what CLI authors expect.
package identity

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"deeparchive/db/generated"
	"deeparchive/internal/audit"
	"deeparchive/internal/dbtypes"
)

const (
	deviceAuthTTL      = 15 * time.Minute
	deviceAuthInterval = 5 * time.Second
	// A poll faster than (interval - grace) gets "slow_down" rather than
	// being silently allowed — a well-behaved client never hits this; it
	// exists for one that ignores `interval` and hammers the endpoint.
	deviceAuthPollGrace = time.Second
)

var (
	ErrDeviceAuthPending  = errors.New("device authorization pending")
	ErrDeviceAuthSlowDown = errors.New("polling too fast")
	ErrDeviceAuthDenied   = errors.New("device authorization denied")
	ErrDeviceAuthExpired  = errors.New("device authorization expired or already used")
)

// userCodeAlphabet excludes 0/O and 1/I/L — characters that look alike
// when read off a phone screen and typed on a different keyboard.
const userCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func generateUserCode() (string, error) {
	var b strings.Builder
	for i := 0; i < 8; i++ {
		if i == 4 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(userCodeAlphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(userCodeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// normalizeUserCode accepts what a person actually types — lowercase,
// extra spaces, a missing or misplaced dash — and reduces it to the
// canonical "XXXX-XXXX" form stored in the database.
func normalizeUserCode(raw string) string {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	raw = strings.ReplaceAll(raw, " ", "")
	raw = strings.ReplaceAll(raw, "-", "")
	if len(raw) != 8 {
		return raw
	}
	return raw[:4] + "-" + raw[4:]
}

type DeviceAuthStart struct {
	DeviceCode string
	UserCode   string
	ExpiresAt  time.Time
	Interval   int
}

// StartDeviceAuth begins the flow: clientName (e.g. "backup-cli") is
// shown on the approval page so a person knows what they're authorizing.
func (s *Service) StartDeviceAuth(ctx context.Context, clientName string) (*DeviceAuthStart, error) {
	deviceCode, err := generateOpaqueToken()
	if err != nil {
		return nil, fmt.Errorf("generating device code: %w", err)
	}
	userCode, err := generateUserCode()
	if err != nil {
		return nil, fmt.Errorf("generating user code: %w", err)
	}
	expiresAt := time.Now().Add(deviceAuthTTL)

	if _, err := s.queries.CreateDeviceAuthRequest(ctx, generated.CreateDeviceAuthRequestParams{
		DeviceCodeHash: hashToken(deviceCode),
		UserCode:       userCode,
		ClientName:     clientName,
		ExpiresAt:      dbtypes.Timestamptz(expiresAt),
	}); err != nil {
		return nil, fmt.Errorf("storing device auth request: %w", err)
	}

	return &DeviceAuthStart{
		DeviceCode: deviceCode,
		UserCode:   userCode,
		ExpiresAt:  expiresAt,
		Interval:   int(deviceAuthInterval.Seconds()),
	}, nil
}

// PollDeviceAuth redeems an approved request for a real session, exactly
// like a completed Login. Called repeatedly by the CLI until it returns
// something other than ErrDeviceAuthPending/ErrDeviceAuthSlowDown.
func (s *Service) PollDeviceAuth(ctx context.Context, deviceCode, userAgent, remoteAddr string) (*IssuedSession, error) {
	req, err := s.queries.GetDeviceAuthRequestByCodeHash(ctx, hashToken(deviceCode))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDeviceAuthExpired
		}
		return nil, fmt.Errorf("looking up device auth request: %w", err)
	}

	if req.LastPolledAt.Valid && time.Since(req.LastPolledAt.Time) < deviceAuthInterval-deviceAuthPollGrace {
		return nil, ErrDeviceAuthSlowDown
	}
	if err := s.queries.TouchDeviceAuthRequestPoll(ctx, req.ID); err != nil {
		s.logger.Error("touch_device_auth_poll_error", "error", err)
	}

	switch req.Status {
	case "denied":
		_ = s.queries.DeleteDeviceAuthRequest(ctx, req.ID)
		return nil, ErrDeviceAuthDenied
	case "approved":
		if !req.UserID.Valid {
			return nil, fmt.Errorf("device auth request %s approved with no user_id", dbtypes.FromUUID(req.ID))
		}
		// Single-use: delete now so a stolen/replayed device_code can't
		// be redeemed twice, same reasoning as a password-reset token.
		if err := s.queries.DeleteDeviceAuthRequest(ctx, req.ID); err != nil {
			return nil, fmt.Errorf("consuming device auth request: %w", err)
		}
		userID := dbtypes.FromUUID(req.UserID)
		s.audit.Record(ctx, audit.Event{
			ActorUserID: &userID, EventType: "auth.device_code_redeemed", TargetType: "user", TargetID: &userID,
			Metadata: map[string]any{"client_name": req.ClientName}, IPAddress: remoteAddr,
		})
		return s.createSession(ctx, userID, uuid.New(), userAgent, remoteAddr)
	default: // "pending"
		return nil, ErrDeviceAuthPending
	}
}

type DeviceAuthInfo struct {
	ClientName string
	ExpiresAt  time.Time
}

// VerifyDeviceUserCode looks up a code for the approval page to display
// — called by an authenticated browser session, never by the CLI.
func (s *Service) VerifyDeviceUserCode(ctx context.Context, userCode string) (*DeviceAuthInfo, error) {
	req, err := s.queries.GetPendingDeviceAuthRequestByUserCode(ctx, normalizeUserCode(userCode))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDeviceAuthExpired
		}
		return nil, fmt.Errorf("looking up device auth request: %w", err)
	}
	return &DeviceAuthInfo{ClientName: req.ClientName, ExpiresAt: dbtypes.FromTimestamptz(req.ExpiresAt)}, nil
}

// ApproveDeviceAuth binds a pending request to actorUserID — the CLI's
// next poll then gets a session for that user.
func (s *Service) ApproveDeviceAuth(ctx context.Context, userCode string, actorUserID uuid.UUID) error {
	req, err := s.queries.GetPendingDeviceAuthRequestByUserCode(ctx, normalizeUserCode(userCode))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDeviceAuthExpired
		}
		return fmt.Errorf("looking up device auth request: %w", err)
	}
	if err := s.queries.ApproveDeviceAuthRequest(ctx, generated.ApproveDeviceAuthRequestParams{
		ID: req.ID, UserID: dbtypes.UUID(actorUserID),
	}); err != nil {
		return fmt.Errorf("approving device auth request: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &actorUserID, EventType: "auth.device_code_approved",
		Metadata: map[string]any{"client_name": req.ClientName},
	})
	return nil
}

func (s *Service) DenyDeviceAuth(ctx context.Context, userCode string, actorUserID uuid.UUID) error {
	req, err := s.queries.GetPendingDeviceAuthRequestByUserCode(ctx, normalizeUserCode(userCode))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDeviceAuthExpired
		}
		return fmt.Errorf("looking up device auth request: %w", err)
	}
	if err := s.queries.DenyDeviceAuthRequest(ctx, req.ID); err != nil {
		return fmt.Errorf("denying device auth request: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &actorUserID, EventType: "auth.device_code_denied",
		Metadata: map[string]any{"client_name": req.ClientName},
	})
	return nil
}
