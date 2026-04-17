package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oluu-web/lennut/internal/utils"
)

type TunnelHandler struct {
	DB *sql.DB
	Domain string
}

type createTunnelRequest struct {
	TargetPort int `json:"target_port"`
	Protocol   string `json:"protocol"`
}

type tunnelResponse struct {
	ID         string    `json:"id"`
	Hostname   string    `json:"hostname"`
	Protocol   string    `json:"protocol"`
	TargetPort int       `json:"target_port"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

var (
	errTunnelLimitReached = fmt.Errorf("tunnel limit reached")
	errDuplicatePort      = fmt.Errorf("duplicate port")
)

func (h *TunnelHandler) CreateTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		utils.WriteJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	claims, ok := AuthClaimsFromContext(r.Context())
	if !ok {
		utils.WriteJSONError(w, http.StatusUnauthorized, "missing auth context")
		return
	}

	var req createTunnelRequest

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(&req); err != nil {
		utils.WriteJSONError(w, http.StatusBadRequest, "invalid json body first")
		return
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
    utils.WriteJSONError(w, http.StatusBadRequest, "request body must contain only one json object")
    return
}

	req.Protocol = strings.ToLower(strings.TrimSpace(req.Protocol))
	if req.Protocol == "" {
		req.Protocol = "http"
	}

	if req.Protocol != "http" {
		utils.WriteJSONError(w, http.StatusBadRequest, "protocol must be http")
		return
	}

	if req.TargetPort < 1 || req.TargetPort > 65535 {
		utils.WriteJSONError(w, http.StatusBadRequest, "target_port must be between 1 and 65535")
		return
	}

	hostname := fmt.Sprintf("%s.%s", uuid.NewString()[:8], h.Domain)

	tunnel, err := h.createTunnelTx(r.Context(), claims.UserID, hostname, req.Protocol, req.TargetPort)
	if err != nil {
		switch err {
		case errTunnelLimitReached:
			utils.WriteJSONError(w, http.StatusConflict, "active tunnel limit reached")
		case errDuplicatePort:
			utils.WriteJSONError(w, http.StatusUnprocessableEntity, "tunnel already exists for this port")
		default:
			slog.Error("create tunnel", "err", err)
			utils.WriteJSONError(w, http.StatusInternalServerError, "failed to create tunnel")
		}
		return
	}

	utils.WriteJSON(w, http.StatusCreated, tunnel)
}

func (h *TunnelHandler) ListTunnels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		utils.WriteJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	claims, ok := AuthClaimsFromContext(r.Context())
	if !ok {
		utils.WriteJSONError(w, http.StatusUnauthorized, "missing auth context")
		return
	}

	tunnels, err := h.listTunnels(r, claims.UserID)
	if err != nil {
		utils.WriteJSONError(w, http.StatusInternalServerError, "failed to list tunnels")
		return
	}

	utils.WriteJSON(w, http.StatusOK, tunnels)
}

func (h *TunnelHandler) DeleteTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		utils.WriteJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	claims, ok := AuthClaimsFromContext(r.Context())
	if !ok {
		utils.WriteJSONError(w, http.StatusUnauthorized, "missing auth context")
		return
	}

	tunnelID := strings.TrimPrefix(r.URL.Path, "/tunnels/")
	if tunnelID == "" || tunnelID == r.URL.Path {
		utils.WriteJSONError(w, http.StatusBadRequest, "missing tunnel id")
		return
	}

	if err := h.closeTunnel(r, claims.UserID, tunnelID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			utils.WriteJSONError(w, http.StatusNotFound, "tunnel not found")
			return
		}
		utils.WriteJSONError(w, http.StatusInternalServerError, "failed to delete tunnel")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *TunnelHandler) createTunnelTx(ctx context.Context, userID, hostname, protocol string, targetPort int) (*tunnelResponse, error) {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID)
	if err != nil {
		return nil, fmt.Errorf("lock user row: %w", err)
	}

	var count int
	err = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tunnels WHERE user_id = $1 AND status IN ('pending', 'active')`,
		userID,
	).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("count tunnels: %w", err)
	}

	if count >= 2 {
		return nil, errTunnelLimitReached
	}

	var tunnel tunnelResponse
	err = tx.QueryRowContext(ctx, `
		INSERT INTO tunnels (user_id, hostname, protocol, target_port, status)
		VALUES ($1, $2, $3, $4, 'pending')
		RETURNING id, hostname, protocol, target_port, status, created_at`,
		userID, hostname, protocol, targetPort,
	).Scan(
		&tunnel.ID,
		&tunnel.Hostname,
		&tunnel.Protocol,
		&tunnel.TargetPort,
		&tunnel.Status,
		&tunnel.CreatedAt,
	)

	if err != nil {
		if isDuplicateTunnelPort(err) {
			return nil, errDuplicatePort
		}
		return nil, fmt.Errorf("insert tunnel: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &tunnel, nil
}

func (h *TunnelHandler) listTunnels(r *http.Request, userID string) ([]tunnelResponse, error) {
	rows, err := h.DB.QueryContext(
		r.Context(),
		`
		SELECT id, hostname, protocol, target_port, status, created_at
		FROM tunnels
		WHERE user_id = $1
		ORDER BY created_at DESC
		`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tunnels := make([]tunnelResponse, 0)

	for rows.Next() {
		var t tunnelResponse
		if err := rows.Scan(
			&t.ID,
			&t.Hostname,
			&t.Protocol,
			&t.TargetPort,
			&t.Status,
			&t.CreatedAt,
		); err != nil {
			return nil, err
		}
		tunnels = append(tunnels, t)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tunnels, nil
}

func (h *TunnelHandler) closeTunnel(r *http.Request, userID, tunnelID string) error {
	row := h.DB.QueryRowContext(
		r.Context(),
		`
		UPDATE tunnels
		SET status = 'closed',
		    closed_at = now()
		WHERE id = $1
		  AND user_id = $2
		  AND status IN ('pending', 'active')
		RETURNING id
		`,
		tunnelID,
		userID,
	)

	var id string
	if err := row.Scan(&id); err != nil {
		return err
	}

	return nil
}

func isDuplicateTunnelPort(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == pgerrcode.UniqueViolation
}