package gamepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"ascent/internal/identity"
	"ascent/internal/platform/ids"
	protocol "ascent/protocol/gen/go"
)

const authorizedTopicsCTE = `
	WITH authorized_topics(topic) AS (
		SELECT 'public'::text
		UNION ALL SELECT 'global'::text
		UNION ALL SELECT 'player:' || $1::text
		UNION ALL SELECT 'player.' || $1::text
		UNION ALL SELECT 'actor:' || $1::text
		UNION ALL SELECT 'actor.' || $1::text
		UNION ALL
		SELECT 'company:' || membership.company_id::text
		FROM companies.memberships AS membership
		JOIN companies.companies AS company
		  ON company.company_id = membership.company_id
		 AND company.status = 'active'
		WHERE membership.player_id = $1::uuid
		  AND membership.left_at IS NULL
		UNION ALL
		SELECT 'company.' || membership.company_id::text
		FROM companies.memberships AS membership
		JOIN companies.companies AS company
		  ON company.company_id = membership.company_id
		 AND company.status = 'active'
		WHERE membership.player_id = $1::uuid
		  AND membership.left_at IS NULL
		UNION ALL
		SELECT 'market:' || market.market_id::text
		FROM markets.markets AS market
		WHERE market.status <> 'closed'
		UNION ALL
		SELECT 'contract:' || contract.contract_id::text
		FROM freight.contracts AS contract
		WHERE EXISTS (
			SELECT 1
			FROM companies.memberships AS membership
			WHERE membership.player_id = $1::uuid
			  AND membership.left_at IS NULL
			  AND membership.company_id IN (
				contract.shipper_company_id,
				contract.carrier_company_id
			  )
		)
		UNION ALL
		SELECT 'chat:' || channel.channel_id::text
		FROM chat.channels AS channel
		WHERE channel.status <> 'archived'
		  AND (
			channel.channel_type = 'global'
			OR EXISTS (
				SELECT 1
				FROM chat.channel_members AS channel_member
				WHERE channel_member.channel_id = channel.channel_id
				  AND channel_member.player_id = $1::uuid
				  AND channel_member.left_at IS NULL
			)
			OR EXISTS (
				SELECT 1
				FROM companies.memberships AS membership
				WHERE membership.player_id = $1::uuid
				  AND membership.left_at IS NULL
				  AND membership.company_id = channel.company_id
			)
			OR EXISTS (
				SELECT 1
				FROM freight.contracts AS contract
				JOIN companies.memberships AS membership
				  ON membership.company_id IN (
					contract.shipper_company_id,
					contract.carrier_company_id
				  )
				 AND membership.player_id = $1::uuid
				 AND membership.left_at IS NULL
				WHERE contract.contract_id = channel.contract_id
			)
		  )
	)`

// Events returns only public facts and facts scoped to the authenticated player
// or one of their current companies. The global sequence remains the recovery
// cursor while topicSequence preserves ordering inside each authorized topic.
func (s *Service) Events(ctx context.Context, actor identity.Actor, after int64) ([]protocol.EventEnvelope, error) {
	if s == nil || s.database == nil {
		return nil, errors.New("load game events: database is required")
	}
	if !ids.IsUUID(actor.ID) {
		return nil, errors.New("load game events: actor ID must be a UUID")
	}
	if after < 0 {
		return nil, errors.New("load game events: cursor must be nonnegative")
	}
	rows, err := s.database.QueryContext(ctx, authorizedTopicsCTE+`
		SELECT
			event.sequence,
			event.event_id::text,
			event.protocol_version,
			event.command_id::text,
			event.topic,
			event.topic_sequence,
			event.event_type,
			event.occurred_at,
			event.payload
		FROM platform.event_outbox AS event
		WHERE event.sequence > $2
		  AND EXISTS (
			SELECT 1
			FROM identity.players AS player
			WHERE player.player_id = $1::uuid
			  AND player.status = 'active'
		  )
		  AND EXISTS (
			SELECT 1
			FROM authorized_topics AS authorized
			WHERE event.topic = authorized.topic
			   OR event.topic LIKE authorized.topic || '.%'
			   OR event.topic LIKE authorized.topic || ':%'
		  )
		ORDER BY event.sequence
		LIMIT 500`, actor.ID, after)
	if err != nil {
		return nil, fmt.Errorf("load game events: query: %w", err)
	}
	defer rows.Close()

	events := make([]protocol.EventEnvelope, 0)
	for rows.Next() {
		var (
			event         protocol.EventEnvelope
			eventID       string
			commandID     sql.NullString
			topicSequence int64
			payload       []byte
		)
		if err := rows.Scan(
			&event.Sequence,
			&eventID,
			&event.ProtocolVersion,
			&commandID,
			&event.Topic,
			&topicSequence,
			&event.Type,
			&event.OccurredAt,
			&payload,
		); err != nil {
			return nil, fmt.Errorf("load game events: scan: %w", err)
		}
		event.EventId = stringPointer(eventID)
		event.TopicSequence = int64Pointer(topicSequence)
		if commandID.Valid {
			event.CommandId = stringPointer(commandID.String)
		}
		event.Payload = append(event.Payload[:0], payload...)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load game events: rows: %w", err)
	}
	return events, nil
}

func authorizedSequence(ctx context.Context, transaction *sql.Tx, actorID string) (int64, error) {
	var sequence int64
	err := transaction.QueryRowContext(ctx, authorizedTopicsCTE+`
		SELECT COALESCE(max(event.sequence), 0)
		FROM platform.event_outbox AS event
		WHERE EXISTS (
			SELECT 1
			FROM authorized_topics AS authorized
			WHERE event.topic = authorized.topic
			   OR event.topic LIKE authorized.topic || '.%'
			   OR event.topic LIKE authorized.topic || ':%'
		)`, actorID).Scan(&sequence)
	if err != nil {
		return 0, fmt.Errorf("load authorized event sequence: %w", err)
	}
	return sequence, nil
}

func topicAuthorized(topic, actorID string, companyIDs []string) bool {
	publicPrefixes := []string{"public", "global", "player:" + actorID, "player." + actorID, "actor:" + actorID, "actor." + actorID}
	for _, prefix := range publicPrefixes {
		if topicHasPrefix(topic, prefix) {
			return true
		}
	}
	for _, companyID := range companyIDs {
		if topicHasPrefix(topic, "company:"+companyID) || topicHasPrefix(topic, "company."+companyID) {
			return true
		}
	}
	return false
}

func topicHasPrefix(topic, prefix string) bool {
	return topic == prefix || strings.HasPrefix(topic, prefix+".") || strings.HasPrefix(topic, prefix+":")
}

func int64Pointer(value int64) *int64 {
	return &value
}
