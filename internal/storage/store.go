package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	cipher *Cipher
}

func Open(ctx context.Context, path, secret string) (*Store, error) {
	cipher, err := NewCipher(secret)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, cipher: cipher}
	if err := store.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS participants (
			id TEXT PRIMARY KEY,
			payload BLOB NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS chat_names (
			chat_id TEXT NOT NULL,
			participant_id TEXT NOT NULL,
			payload BLOB NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (chat_id, participant_id)
		);`,
		`CREATE TABLE IF NOT EXISTS chats (
			id TEXT PRIMARY KEY,
			payload BLOB NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			chat_id TEXT NOT NULL,
			id TEXT NOT NULL,
			sender_id TEXT NOT NULL,
			reply_to_id TEXT,
			sent_at TEXT NOT NULL,
			payload BLOB NOT NULL,
			PRIMARY KEY (chat_id, id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_sent_at ON messages(chat_id, sent_at DESC);`,
		`CREATE TABLE IF NOT EXISTS topics (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			payload BLOB NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_topics_chat_updated_at ON topics(chat_id, updated_at DESC);`,
		`CREATE TABLE IF NOT EXISTS message_topics (
			chat_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			topic_id TEXT NOT NULL,
			PRIMARY KEY (chat_id, message_id)
		);`,
		`CREATE TABLE IF NOT EXISTS daily_summaries (
			chat_id TEXT NOT NULL,
			date TEXT NOT NULL,
			payload BLOB NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (chat_id, date)
		);`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertChat(ctx context.Context, chat Chat) error {
	chat.UpdatedAt = chat.UpdatedAt.UTC()
	payload, err := s.cipher.SealJSON(chat)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO chats(id, payload, updated_at)
		VALUES(?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`,
		chat.ID, payload, chat.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListChats(ctx context.Context) ([]Chat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM chats ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chats []Chat
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var chat Chat
		if err := s.cipher.OpenJSON(payload, &chat); err != nil {
			return nil, err
		}
		chats = append(chats, chat)
	}
	return chats, rows.Err()
}

func (s *Store) UpsertProfile(ctx context.Context, profile ParticipantProfile) error {
	if profile.Names == nil {
		profile.Names = map[string]string{}
	}
	if profile.Expertise == nil {
		profile.Expertise = map[string]string{}
	}
	profile.UpdatedAt = profile.UpdatedAt.UTC()
	payload, err := s.cipher.SealJSON(profile)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO participants(id, payload, updated_at)
		VALUES(?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`,
		profile.ID, payload, profile.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetProfile(ctx context.Context, id string) (ParticipantProfile, bool, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM participants WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return ParticipantProfile{}, false, nil
	}
	if err != nil {
		return ParticipantProfile{}, false, err
	}
	var profile ParticipantProfile
	if err := s.cipher.OpenJSON(payload, &profile); err != nil {
		return ParticipantProfile{}, false, err
	}
	return profile, true, nil
}

func (s *Store) SetChatName(ctx context.Context, chatID, participantID, name string) error {
	payload, err := s.cipher.SealJSON(map[string]string{"name": name})
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `INSERT INTO chat_names(chat_id, participant_id, payload, updated_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(chat_id, participant_id) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`,
		chatID, participantID, payload, now)
	return err
}

func (s *Store) ChatName(ctx context.Context, chatID, participantID string) (string, bool, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM chat_names WHERE chat_id = ? AND participant_id = ?`, chatID, participantID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var decoded map[string]string
	if err := s.cipher.OpenJSON(payload, &decoded); err != nil {
		return "", false, err
	}
	return decoded["name"], true, nil
}

func (s *Store) UpsertMessage(ctx context.Context, message Message) error {
	if message.SentAt.IsZero() {
		message.SentAt = time.Now()
	}
	message.SentAt = message.SentAt.UTC()
	payload, err := s.cipher.SealJSON(message)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(chat_id, id, sender_id, reply_to_id, sent_at, payload)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, id) DO UPDATE SET sender_id = excluded.sender_id, reply_to_id = excluded.reply_to_id, sent_at = excluded.sent_at, payload = excluded.payload`,
		message.ChatID, message.ID, message.SenderID, nullable(message.ReplyToID), message.SentAt.Format(time.RFC3339Nano), payload); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages
		WHERE chat_id = ? AND id NOT IN (
			SELECT id FROM messages WHERE chat_id = ? ORDER BY sent_at DESC LIMIT 20
		)`, message.ChatID, message.ChatID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetMessage(ctx context.Context, chatID, messageID string) (Message, bool, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM messages WHERE chat_id = ? AND id = ?`, chatID, messageID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	var message Message
	if err := s.cipher.OpenJSON(payload, &message); err != nil {
		return Message{}, false, err
	}
	return message, true, nil
}

func (s *Store) DeleteMessage(ctx context.Context, chatID, messageID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_topics WHERE chat_id = ? AND message_id = ?`, chatID, messageID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE chat_id = ? AND id = ?`, chatID, messageID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) TopicIDForMessage(ctx context.Context, chatID, messageID string) (string, bool, error) {
	var topicID string
	err := s.db.QueryRowContext(ctx, `SELECT topic_id FROM message_topics WHERE chat_id = ? AND message_id = ?`, chatID, messageID).Scan(&topicID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return topicID, true, nil
}

func (s *Store) RecentMessages(ctx context.Context, chatID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM messages WHERE chat_id = ? ORDER BY sent_at DESC LIMIT ?`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reverse []Message
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var message Message
		if err := s.cipher.OpenJSON(payload, &message); err != nil {
			return nil, err
		}
		reverse = append(reverse, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(reverse)-1; i < j; i, j = i+1, j-1 {
		reverse[i], reverse[j] = reverse[j], reverse[i]
	}
	return reverse, nil
}

func (s *Store) TopicMessages(ctx context.Context, chatID, topicID string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT messages.payload
		FROM messages
		INNER JOIN message_topics ON message_topics.chat_id = messages.chat_id AND message_topics.message_id = messages.id
		WHERE messages.chat_id = ? AND message_topics.topic_id = ?
		ORDER BY messages.sent_at ASC`, chatID, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.decodeMessages(rows)
}

func (s *Store) ParticipantMessages(ctx context.Context, participantID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM messages WHERE sender_id = ? ORDER BY sent_at DESC LIMIT ?`, participantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages, err := s.decodeMessages(rows)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

func (s *Store) decodeMessages(rows *sql.Rows) ([]Message, error) {
	var messages []Message
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var message Message
		if err := s.cipher.OpenJSON(payload, &message); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) UpsertTopic(ctx context.Context, topic Topic) error {
	topic.UpdatedAt = topic.UpdatedAt.UTC()
	payload, err := s.cipher.SealJSON(topic)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO topics(id, chat_id, payload, updated_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET chat_id = excluded.chat_id, payload = excluded.payload, updated_at = excluded.updated_at`,
		topic.ID, topic.ChatID, payload, topic.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListTopics(ctx context.Context, chatID string, limit int) ([]Topic, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM topics WHERE chat_id = ? ORDER BY updated_at DESC LIMIT ?`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []Topic
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var topic Topic
		if err := s.cipher.OpenJSON(payload, &topic); err != nil {
			return nil, err
		}
		topics = append(topics, topic)
	}
	return topics, rows.Err()
}

func (s *Store) TopicForReply(ctx context.Context, chatID, replyToID string) (Topic, bool, error) {
	if replyToID == "" {
		return Topic{}, false, nil
	}
	var topicID string
	err := s.db.QueryRowContext(ctx, `SELECT topic_id FROM message_topics WHERE chat_id = ? AND message_id = ?`, chatID, replyToID).Scan(&topicID)
	if errors.Is(err, sql.ErrNoRows) {
		return Topic{}, false, nil
	}
	if err != nil {
		return Topic{}, false, err
	}
	return s.GetTopic(ctx, topicID)
}

func (s *Store) GetTopic(ctx context.Context, id string) (Topic, bool, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM topics WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return Topic{}, false, nil
	}
	if err != nil {
		return Topic{}, false, err
	}
	var topic Topic
	if err := s.cipher.OpenJSON(payload, &topic); err != nil {
		return Topic{}, false, err
	}
	return topic, true, nil
}

func (s *Store) AttachMessageToTopic(ctx context.Context, chatID, messageID, topicID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO message_topics(chat_id, message_id, topic_id)
		VALUES(?, ?, ?)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET topic_id = excluded.topic_id`,
		chatID, messageID, topicID)
	return err
}

func (s *Store) SaveDailySummary(ctx context.Context, summary DailySummary) error {
	if summary.CreatedAt.IsZero() {
		summary.CreatedAt = time.Now()
	}
	summary.CreatedAt = summary.CreatedAt.UTC()
	payload, err := s.cipher.SealJSON(summary)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO daily_summaries(chat_id, date, payload, created_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(chat_id, date) DO UPDATE SET payload = excluded.payload, created_at = excluded.created_at`,
		summary.ChatID, summary.Date, payload, summary.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func NewTopicID(chatID string, now time.Time) string {
	return fmt.Sprintf("%s:%d", chatID, now.UTC().UnixNano())
}
