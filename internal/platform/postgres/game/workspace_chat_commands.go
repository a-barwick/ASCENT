package gamepostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"ascent/internal/chat"
	"ascent/internal/devices"
	"ascent/internal/identity"
	"ascent/internal/platform/ids"
	protocol "ascent/protocol/gen/go"
)

var panelIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)

type registerDevicePayload struct {
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
}

type panelSendPayload struct {
	DeviceID string `json:"deviceId"`
	PanelID  string `json:"panelId"`
	Message  string `json:"message"`
}

type chatSendPayload struct {
	ChannelID string `json:"channelId"`
	Body      string `json:"body"`
}

func (s *Service) registerDevice(
	ctx context.Context,
	transaction *sql.Tx,
	actor identity.Actor,
	command protocol.CommandEnvelope,
) (commandOutcome, *commandRejection, error) {
	if _, _, rejection, err := s.authorizeCompany(
		ctx,
		transaction,
		actor,
		command,
		"owner",
		"operator",
	); rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	var payload registerDevicePayload
	if rejection := decodePayload(command.Payload, &payload); rejection != nil {
		return commandOutcome{}, rejection, nil
	}
	payload.Name = strings.TrimSpace(payload.Name)
	if len(payload.Capabilities) != 1 || payload.Capabilities[0] != "panel.receive" {
		return commandOutcome{}, reject(
			"INVALID_DEVICE",
			"The MVP supports the panel.receive device capability.",
		), nil
	}
	deviceID, err := ids.NewUUID()
	if err != nil {
		return commandOutcome{}, nil, err
	}
	if err := devices.Validate(deviceID, payload.Name, devices.ClassDesktop); err != nil {
		return commandOutcome{}, reject("INVALID_DEVICE", "Enter a valid device name."), nil
	}
	viewID, err := ids.NewUUID()
	if err != nil {
		return commandOutcome{}, nil, err
	}
	now := s.clock()
	panelDefinitions, _ := json.Marshal([]map[string]string{{
		"id":   "operations",
		"name": "Operations",
	}})
	subscriptions, _ := json.Marshal(payload.Capabilities)
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO workspace.views (
			view_id, player_id, name, device_class,
			panel_definitions, subscriptions
		) VALUES ($1, $2, $3, 'desktop', $4, $5)`,
		viewID,
		actor.ID,
		payload.Name+" view",
		panelDefinitions,
		subscriptions,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("create device view: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO workspace.devices (
			device_id, player_id, device_key, name, device_class,
			status, registered_at, last_seen_at, active_view_id
		) VALUES ($1, $2, $3, $4, 'desktop', 'registered', $5, $5, $6)`,
		deviceID,
		actor.ID,
		"browser:"+command.CommandId,
		payload.Name,
		now,
		viewID,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("register device: %w", err)
	}
	event, err := appendEvent(
		ctx,
		transaction,
		&command.CommandId,
		"player:"+actor.ID,
		"DEVICE_REGISTERED",
		map[string]any{
			"deviceId":     deviceID,
			"name":         payload.Name,
			"capabilities": payload.Capabilities,
		},
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	return commandOutcome{payload: map[string]any{
		"deviceId":      deviceID,
		"viewId":        viewID,
		"eventSequence": event.Sequence,
	}}, nil, nil
}

func (s *Service) sendPanel(
	ctx context.Context,
	transaction *sql.Tx,
	actor identity.Actor,
	command protocol.CommandEnvelope,
) (commandOutcome, *commandRejection, error) {
	if _, _, rejection, err := s.authorizeCompany(
		ctx,
		transaction,
		actor,
		command,
		"owner",
		"operator",
	); rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	var payload panelSendPayload
	if rejection := decodePayload(command.Payload, &payload); rejection != nil {
		return commandOutcome{}, rejection, nil
	}
	message, err := chat.NormalizeMessage(payload.Message)
	if err != nil || len(message) > 500 || !ids.IsUUID(payload.DeviceID) ||
		!panelIDPattern.MatchString(payload.PanelID) {
		return commandOutcome{}, reject(
			"INVALID_PANEL",
			"Choose a valid target panel and enter a short plain-text message.",
		), nil
	}
	route := "/terminal/panels/" + payload.PanelID
	if err := devices.ValidatePanelRoute(route); err != nil {
		return commandOutcome{}, reject("INVALID_PANEL", "The target panel route is invalid."), nil
	}
	var targetStatus string
	err = transaction.QueryRowContext(
		ctx,
		`SELECT status
		   FROM workspace.devices
		  WHERE device_id = $1 AND player_id = $2
		  FOR SHARE`,
		payload.DeviceID,
		actor.ID,
	).Scan(&targetStatus)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && targetStatus != "registered") {
		return commandOutcome{}, reject("DEVICE_UNAVAILABLE", "That target device is unavailable."), nil
	}
	if err != nil {
		return commandOutcome{}, nil, fmt.Errorf("load target device: %w", err)
	}

	senderID, err := ensureSenderDevice(ctx, transaction, actor.ID, payload.DeviceID, command.CommandId, s.clock())
	if err != nil {
		return commandOutcome{}, nil, err
	}
	deliveryID, err := ids.NewUUID()
	if err != nil {
		return commandOutcome{}, nil, err
	}
	now := s.clock()
	event, err := appendEvent(
		ctx,
		transaction,
		&command.CommandId,
		"player:"+actor.ID,
		"PANEL_QUEUED",
		map[string]any{
			"deliveryId": deliveryID,
			"deviceId":   payload.DeviceID,
			"panelId":    payload.PanelID,
			"message":    message,
		},
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	panelPayload, _ := json.Marshal(map[string]string{
		"panelId": payload.PanelID,
		"message": message,
	})
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO workspace.panel_deliveries (
			panel_delivery_id, command_id, player_id, sender_device_id,
			target_device_id, route, panel_payload, status, event_id, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'queued', $8, $9)`,
		deliveryID,
		command.CommandId,
		actor.ID,
		senderID,
		payload.DeviceID,
		route,
		panelPayload,
		*event.EventId,
		now.Add(10*time.Minute),
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("queue panel delivery: %w", err)
	}
	return commandOutcome{payload: map[string]any{
		"deliveryId":    deliveryID,
		"eventSequence": event.Sequence,
		"status":        "queued",
	}}, nil, nil
}

func ensureSenderDevice(
	ctx context.Context,
	transaction *sql.Tx,
	playerID, targetDeviceID, commandID string,
	now time.Time,
) (string, error) {
	var senderID string
	err := transaction.QueryRowContext(
		ctx,
		`SELECT device_id
		   FROM workspace.devices
		  WHERE player_id = $1
		    AND device_id <> $2
		    AND status = 'registered'
		  ORDER BY last_seen_at DESC, device_id
		  LIMIT 1
		  FOR SHARE`,
		playerID,
		targetDeviceID,
	).Scan(&senderID)
	if err == nil {
		return senderID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("load panel sender device: %w", err)
	}
	senderID, err = ids.NewUUID()
	if err != nil {
		return "", err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO workspace.devices (
			device_id, player_id, device_key, name, device_class,
			status, registered_at, last_seen_at
		) VALUES ($1, $2, $3, 'Current browser', 'desktop',
		          'registered', $4, $4)`,
		senderID,
		playerID,
		"sender:"+commandID,
		now,
	); err != nil {
		return "", fmt.Errorf("register panel sender: %w", err)
	}
	return senderID, nil
}

func (s *Service) sendChat(
	ctx context.Context,
	transaction *sql.Tx,
	actor identity.Actor,
	command protocol.CommandEnvelope,
) (commandOutcome, *commandRejection, error) {
	if _, _, rejection, err := s.authorizeCompany(
		ctx,
		transaction,
		actor,
		command,
		"owner",
		"operator",
		"trader",
		"analyst",
		"viewer",
	); rejection != nil || err != nil {
		return commandOutcome{}, rejection, err
	}
	var payload chatSendPayload
	if rejection := decodePayload(command.Payload, &payload); rejection != nil {
		return commandOutcome{}, rejection, nil
	}
	body, err := chat.NormalizeMessage(payload.Body)
	if err != nil {
		return commandOutcome{}, reject(
			"INVALID_MESSAGE",
			"Enter a non-empty plain-text message of at most 1,000 characters.",
		), nil
	}
	var (
		channelID string
		status    string
	)
	channelQuery := `SELECT channel.channel_id, channel.status
	                   FROM chat.channels AS channel
	                   JOIN chat.channel_members AS member
	                     ON member.channel_id = channel.channel_id
	                  WHERE member.player_id = $1
	                    AND member.left_at IS NULL
	                    AND channel.company_id = $2
	                    AND channel.channel_type = 'company'
	                    AND (
	                      channel.channel_id::text = $3
	                      OR channel.name = $3
	                      OR $3 = 'company-operations'
	                    )
	                  FOR SHARE OF channel, member`
	err = transaction.QueryRowContext(
		ctx,
		channelQuery,
		actor.ID,
		*command.CompanyId,
		payload.ChannelID,
	).Scan(&channelID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return commandOutcome{}, reject(
			"CHANNEL_UNAVAILABLE",
			"You are not an active member of that company channel.",
		), nil
	}
	if err != nil {
		return commandOutcome{}, nil, fmt.Errorf("resolve chat channel: %w", err)
	}
	if status != "active" {
		return commandOutcome{}, reject("CHANNEL_LOCKED", "That channel is not accepting messages."), nil
	}
	messageID, err := ids.NewUUID()
	if err != nil {
		return commandOutcome{}, nil, err
	}
	now := s.clock()
	event, err := appendEvent(
		ctx,
		transaction,
		&command.CommandId,
		"chat:"+channelID,
		"CHAT_MESSAGE_SENT",
		map[string]any{
			"messageId": messageID,
			"channelId": payload.ChannelID,
			"actorId":   actor.ID,
			"actorName": actor.DisplayName,
			"body":      body,
		},
		now,
	)
	if err != nil {
		return commandOutcome{}, nil, err
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO chat.messages (
			message_id, command_id, channel_id, sender_player_id,
			body, event_id, sent_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		messageID,
		command.CommandId,
		channelID,
		actor.ID,
		body,
		*event.EventId,
		now,
	); err != nil {
		return commandOutcome{}, nil, fmt.Errorf("store chat message: %w", err)
	}
	return commandOutcome{payload: map[string]any{
		"messageId":     messageID,
		"eventSequence": event.Sequence,
	}}, nil, nil
}
