package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum"

	"github.com/AntonTyutin/assistantbot-core/reminders"
)

type Store struct {
	mu     sync.Mutex
	db     *sql.DB
	opts   Options
	secret string
}

func Open(ctx context.Context, path, secret string, opts Options) (*Store, error) {
	opts = opts.withDefaults()
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("database encryption key is required")
	}
	dsn := fmt.Sprintf("file:%s?vfs=adiantum&_pragma=temp_store(memory)", url.PathEscape(filepath.ToSlash(path)))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, opts: opts, secret: secret}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA textkey = '%s'", escapeSQLString(secret))); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set encryption key: %w", err)
	}
	if err := store.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

func (s *Store) RecentMessagesLimit() int {
	return s.opts.RecentMessagesLimit
}

func (s *Store) Migrate(ctx context.Context) error {
	dim := s.opts.EmbeddingDimensions
	statements := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS participants (
			id TEXT PRIMARY KEY,
			names TEXT NOT NULL DEFAULT '{}',
			city TEXT NOT NULL DEFAULT '',
			address TEXT NOT NULL DEFAULT '',
			timezone TEXT NOT NULL DEFAULT '',
			style TEXT NOT NULL DEFAULT '',
			verbosity TEXT NOT NULL DEFAULT '',
			expertise TEXT NOT NULL DEFAULT '{}',
			interests TEXT NOT NULL DEFAULT '[]',
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS chat_names (
			chat_id TEXT NOT NULL,
			participant_id TEXT NOT NULL,
			name TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (chat_id, participant_id)
		);`,
		`CREATE TABLE IF NOT EXISTS chats (
			id TEXT PRIMARY KEY,
			is_group INTEGER NOT NULL DEFAULT 0,
			title TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			rowid INTEGER PRIMARY KEY,
			chat_id TEXT NOT NULL,
			id TEXT NOT NULL,
			sender_id TEXT NOT NULL,
			sender TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL,
			is_group INTEGER NOT NULL DEFAULT 0,
			is_from_self INTEGER NOT NULL DEFAULT 0,
			reply_to_id TEXT,
			topic_id TEXT,
			sent_at TEXT NOT NULL,
			UNIQUE(chat_id, id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_sent_at ON messages(chat_id, sent_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_topic ON messages(chat_id, topic_id, sent_at);`,
		`CREATE TABLE IF NOT EXISTS topics (
			rowid INTEGER PRIMARY KEY,
			id TEXT NOT NULL UNIQUE,
			chat_id TEXT NOT NULL,
			title TEXT NOT NULL,
			summary TEXT NOT NULL,
			decisions TEXT NOT NULL DEFAULT '[]',
			open_questions TEXT NOT NULL DEFAULT '[]',
			active_participants TEXT NOT NULL DEFAULT '[]',
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_topics_chat_updated_at ON topics(chat_id, updated_at DESC);`,
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_messages USING vec0(
			embedding float[%d],
			chat_id TEXT partition key
		);`, dim),
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_topics USING vec0(
			embedding float[%d],
			chat_id TEXT partition key
		);`, dim),
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_lists USING vec0(
			embedding float[%d],
			chat_id TEXT partition key
		);`, dim),
		`CREATE TABLE IF NOT EXISTS lists (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_lists_chat ON lists(chat_id, updated_at DESC);`,
		`CREATE TABLE IF NOT EXISTS list_items (
			id TEXT PRIMARY KEY,
			list_id TEXT NOT NULL,
			text TEXT NOT NULL,
			created_by TEXT NOT NULL,
			done INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_list_items_list ON list_items(list_id, created_at);`,
		`CREATE TABLE IF NOT EXISTS reminders (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			requester_id TEXT NOT NULL,
			due_at TEXT NOT NULL,
			text TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_reminders_due ON reminders(status, due_at);`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if err := s.migrateReminderColumns(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) migrateReminderColumns(ctx context.Context) error {
	columns := map[string]string{
		"anchor_at":        "TEXT",
		"recurrence_json":  "TEXT",
		"occurrence_count": "INTEGER NOT NULL DEFAULT 0",
	}
	existing, err := s.reminderColumnNames(ctx)
	if err != nil {
		return err
	}
	for name, ddl := range columns {
		if existing[name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE reminders ADD COLUMN %s %s", name, ddl)); err != nil {
			return fmt.Errorf("migrate reminders.%s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) reminderColumnNames(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(reminders)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (s *Store) UpsertChat(ctx context.Context, chat Chat) error {
	chat.UpdatedAt = chat.UpdatedAt.UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO chats(id, is_group, title, updated_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET is_group = excluded.is_group, title = excluded.title, updated_at = excluded.updated_at`,
		chat.ID, boolInt(chat.IsGroup), chat.Title, chat.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListChats(ctx context.Context) ([]Chat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, is_group, title, updated_at FROM chats ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chats []Chat
	for rows.Next() {
		var chat Chat
		var isGroup int
		var updatedAt string
		if err := rows.Scan(&chat.ID, &isGroup, &chat.Title, &updatedAt); err != nil {
			return nil, err
		}
		chat.IsGroup = isGroup != 0
		chat.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
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
	if profile.Interests == nil {
		profile.Interests = []string{}
	}
	profile.UpdatedAt = profile.UpdatedAt.UTC()
	namesJSON, err := json.Marshal(profile.Names)
	if err != nil {
		return err
	}
	expertiseJSON, err := json.Marshal(profile.Expertise)
	if err != nil {
		return err
	}
	interestsJSON, err := json.Marshal(profile.Interests)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO participants(id, names, city, address, timezone, style, verbosity, expertise, interests, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			names = excluded.names,
			city = excluded.city,
			address = excluded.address,
			timezone = excluded.timezone,
			style = excluded.style,
			verbosity = excluded.verbosity,
			expertise = excluded.expertise,
			interests = excluded.interests,
			updated_at = excluded.updated_at`,
		profile.ID, string(namesJSON), profile.City, profile.Address, profile.Timezone,
		profile.Style, profile.Verbosity, string(expertiseJSON), string(interestsJSON),
		profile.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetProfile(ctx context.Context, id string) (ParticipantProfile, bool, error) {
	var profile ParticipantProfile
	var namesJSON, expertiseJSON, interestsJSON, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT names, city, address, timezone, style, verbosity, expertise, interests, updated_at
		FROM participants WHERE id = ?`, id).Scan(
		&namesJSON, &profile.City, &profile.Address, &profile.Timezone,
		&profile.Style, &profile.Verbosity, &expertiseJSON, &interestsJSON, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ParticipantProfile{}, false, nil
	}
	if err != nil {
		return ParticipantProfile{}, false, err
	}
	profile.ID = id
	if err := json.Unmarshal([]byte(namesJSON), &profile.Names); err != nil {
		return ParticipantProfile{}, false, err
	}
	if err := json.Unmarshal([]byte(expertiseJSON), &profile.Expertise); err != nil {
		return ParticipantProfile{}, false, err
	}
	if err := json.Unmarshal([]byte(interestsJSON), &profile.Interests); err != nil {
		return ParticipantProfile{}, false, err
	}
	profile.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return ParticipantProfile{}, false, err
	}
	return profile, true, nil
}

func (s *Store) SetChatName(ctx context.Context, chatID, participantID, name string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO chat_names(chat_id, participant_id, name, updated_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(chat_id, participant_id) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`,
		chatID, participantID, name, now)
	return err
}

func (s *Store) ChatName(ctx context.Context, chatID, participantID string) (string, bool, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM chat_names WHERE chat_id = ? AND participant_id = ?`, chatID, participantID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return name, err == nil, err
}

func (s *Store) UpsertMessage(ctx context.Context, message Message, embedding []float32) error {
	if message.SentAt.IsZero() {
		message.SentAt = time.Now()
	}
	message.SentAt = message.SentAt.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var rowid int64
	err = tx.QueryRowContext(ctx, `SELECT rowid FROM messages WHERE chat_id = ? AND id = ?`, message.ChatID, message.ID).Scan(&rowid)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, err := tx.ExecContext(ctx, `INSERT INTO messages(chat_id, id, sender_id, sender, text, is_group, is_from_self, reply_to_id, topic_id, sent_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			message.ChatID, message.ID, message.SenderID, message.Sender, message.Text,
			boolInt(message.IsGroup), boolInt(message.IsFromSelf), nullable(message.ReplyToID), nullable(message.TopicID),
			message.SentAt.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		rowid, err = res.LastInsertId()
		if err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET sender_id = ?, sender = ?, text = ?, is_group = ?, is_from_self = ?, reply_to_id = ?, topic_id = ?, sent_at = ?
			WHERE rowid = ?`,
			message.SenderID, message.Sender, message.Text, boolInt(message.IsGroup), boolInt(message.IsFromSelf),
			nullable(message.ReplyToID), nullable(message.TopicID), message.SentAt.Format(time.RFC3339Nano), rowid); err != nil {
			return err
		}
	}

	if len(embedding) > 0 {
		vecArg, err := embeddingArg(embedding)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM vec_messages WHERE rowid = ?`, rowid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO vec_messages(rowid, embedding, chat_id) VALUES(?, ?, ?)`,
			rowid, vecArg, message.ChatID); err != nil {
			return err
		}
	}

	limit := s.opts.RecentMessagesLimit
	staleSubquery := `SELECT rowid FROM messages WHERE chat_id = ? ORDER BY sent_at DESC LIMIT -1 OFFSET ?`
	if _, err := tx.ExecContext(ctx, `DELETE FROM vec_messages WHERE rowid IN (`+staleSubquery+`)`, message.ChatID, limit); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE rowid IN (`+staleSubquery+`)`, message.ChatID, limit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetMessage(ctx context.Context, chatID, messageID string) (Message, bool, error) {
	var message Message
	var isGroup, isFromSelf int
	var replyToID, topicID sql.NullString
	var sentAt string
	err := s.db.QueryRowContext(ctx, `SELECT id, chat_id, sender_id, sender, text, is_group, is_from_self, reply_to_id, topic_id, sent_at
		FROM messages WHERE chat_id = ? AND id = ?`, chatID, messageID).Scan(
		&message.ID, &message.ChatID, &message.SenderID, &message.Sender, &message.Text,
		&isGroup, &isFromSelf, &replyToID, &topicID, &sentAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	message.IsGroup = isGroup != 0
	message.IsFromSelf = isFromSelf != 0
	if replyToID.Valid {
		message.ReplyToID = replyToID.String
	}
	if topicID.Valid {
		message.TopicID = topicID.String
	}
	message.SentAt, err = time.Parse(time.RFC3339Nano, sentAt)
	if err != nil {
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
	var rowid int64
	err = tx.QueryRowContext(ctx, `SELECT rowid FROM messages WHERE chat_id = ? AND id = ?`, chatID, messageID).Scan(&rowid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM vec_messages WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) TopicIDForMessage(ctx context.Context, chatID, messageID string) (string, bool, error) {
	var topicID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT topic_id FROM messages WHERE chat_id = ? AND id = ?`, chatID, messageID).Scan(&topicID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !topicID.Valid || topicID.String == "" {
		return "", false, nil
	}
	return topicID.String, true, nil
}

func (s *Store) RecentMessages(ctx context.Context, chatID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = s.opts.RecentMessagesLimit
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, chat_id, sender_id, sender, text, is_group, is_from_self, reply_to_id, topic_id, sent_at
		FROM messages WHERE chat_id = ? ORDER BY sent_at DESC LIMIT ?`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reverse, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(reverse)-1; i < j; i, j = i+1, j-1 {
		reverse[i], reverse[j] = reverse[j], reverse[i]
	}
	return reverse, nil
}

func (s *Store) TopicMessages(ctx context.Context, chatID, topicID string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, chat_id, sender_id, sender, text, is_group, is_from_self, reply_to_id, topic_id, sent_at
		FROM messages WHERE chat_id = ? AND topic_id = ? ORDER BY sent_at ASC`, chatID, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *Store) NearestMessages(ctx context.Context, chatID string, embedding []float32, limit int) ([]NearestMessage, error) {
	if limit <= 0 || len(embedding) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vecQuery, err := embeddingArg(embedding)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// vec0 MATCH/k panics in ncruces WASM (vec0Filter OOB) even with SerializeFloat32 and CTE.
	// Brute-force distance over the chat partition is acceptable while RECENT_MESSAGES_LIMIT is small.
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.chat_id, m.sender_id, m.sender, m.text, m.is_group, m.is_from_self, m.reply_to_id, m.topic_id, m.sent_at,
		vec_distance_L2(v.embedding, ?) AS distance
		FROM vec_messages v
		INNER JOIN messages m ON m.rowid = v.rowid
		WHERE v.chat_id = ?
		ORDER BY distance
		LIMIT ?`, vecQuery, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NearestMessage
	for rows.Next() {
		var message Message
		var isGroup, isFromSelf int
		var replyToID, topicID sql.NullString
		var sentAt string
		var distance float64
		if err := rows.Scan(&message.ID, &message.ChatID, &message.SenderID, &message.Sender, &message.Text,
			&isGroup, &isFromSelf, &replyToID, &topicID, &sentAt, &distance); err != nil {
			return nil, err
		}
		message.IsGroup = isGroup != 0
		message.IsFromSelf = isFromSelf != 0
		if replyToID.Valid {
			message.ReplyToID = replyToID.String
		}
		if topicID.Valid {
			message.TopicID = topicID.String
		}
		message.SentAt, err = time.Parse(time.RFC3339Nano, sentAt)
		if err != nil {
			return nil, err
		}
		out = append(out, NearestMessage{Message: message, Distance: distance})
	}
	return out, rows.Err()
}

func (s *Store) NearestTopics(ctx context.Context, chatID string, embedding []float32, limit int) ([]NearestTopic, error) {
	if limit <= 0 || len(embedding) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vecQuery, err := embeddingArg(embedding)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.chat_id, t.title, t.summary, t.decisions, t.open_questions, t.active_participants, t.updated_at,
		vec_distance_L2(v.embedding, ?) AS distance
		FROM vec_topics v
		INNER JOIN topics t ON t.rowid = v.rowid
		WHERE v.chat_id = ?
		ORDER BY distance
		LIMIT ?`, vecQuery, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NearestTopic
	for rows.Next() {
		topic, distance, err := scanTopicRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, NearestTopic{Topic: topic, Distance: distance})
	}
	return out, rows.Err()
}

func (s *Store) UpsertTopic(ctx context.Context, topic Topic, embedding []float32) error {
	topic.UpdatedAt = topic.UpdatedAt.UTC()
	if topic.Decisions == nil {
		topic.Decisions = []string{}
	}
	if topic.OpenQuestions == nil {
		topic.OpenQuestions = []string{}
	}
	if topic.ActiveParticipants == nil {
		topic.ActiveParticipants = []string{}
	}
	decisionsJSON, err := json.Marshal(topic.Decisions)
	if err != nil {
		return err
	}
	openQuestionsJSON, err := json.Marshal(topic.OpenQuestions)
	if err != nil {
		return err
	}
	participantsJSON, err := json.Marshal(topic.ActiveParticipants)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var rowid int64
	err = tx.QueryRowContext(ctx, `SELECT rowid FROM topics WHERE id = ?`, topic.ID).Scan(&rowid)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, err := tx.ExecContext(ctx, `INSERT INTO topics(id, chat_id, title, summary, decisions, open_questions, active_participants, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			topic.ID, topic.ChatID, topic.Title, topic.Summary, string(decisionsJSON), string(openQuestionsJSON),
			string(participantsJSON), topic.UpdatedAt.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		rowid, err = res.LastInsertId()
		if err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if _, err := tx.ExecContext(ctx, `UPDATE topics SET chat_id = ?, title = ?, summary = ?, decisions = ?, open_questions = ?, active_participants = ?, updated_at = ?
			WHERE rowid = ?`,
			topic.ChatID, topic.Title, topic.Summary, string(decisionsJSON), string(openQuestionsJSON),
			string(participantsJSON), topic.UpdatedAt.Format(time.RFC3339Nano), rowid); err != nil {
			return err
		}
	}

	if len(embedding) > 0 {
		vecArg, err := embeddingArg(embedding)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM vec_topics WHERE rowid = ?`, rowid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO vec_topics(rowid, embedding, chat_id) VALUES(?, ?, ?)`,
			rowid, vecArg, topic.ChatID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListTopics(ctx context.Context, chatID string, limit int) ([]Topic, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, chat_id, title, summary, decisions, open_questions, active_participants, updated_at
		FROM topics WHERE chat_id = ? ORDER BY updated_at DESC LIMIT ?`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var topics []Topic
	for rows.Next() {
		topic, _, err := scanTopicRowNoDistance(rows)
		if err != nil {
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
	var topicID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT topic_id FROM messages WHERE chat_id = ? AND id = ?`, chatID, replyToID).Scan(&topicID)
	if errors.Is(err, sql.ErrNoRows) || !topicID.Valid || topicID.String == "" {
		return Topic{}, false, nil
	}
	if err != nil {
		return Topic{}, false, err
	}
	return s.GetTopic(ctx, topicID.String)
}

func (s *Store) GetTopic(ctx context.Context, id string) (Topic, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, chat_id, title, summary, decisions, open_questions, active_participants, updated_at
		FROM topics WHERE id = ?`, id)
	topic, err := scanTopic(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Topic{}, false, nil
	}
	if err != nil {
		return Topic{}, false, err
	}
	return topic, true, nil
}

func (s *Store) UpsertList(ctx context.Context, list List, embedding []float32) error {
	list.UpdatedAt = list.UpdatedAt.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `INSERT INTO lists(id, chat_id, kind, title, updated_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, title = excluded.title, updated_at = excluded.updated_at`,
		list.ID, list.ChatID, string(list.Kind), list.Title, list.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	var rowid int64
	if err := tx.QueryRowContext(ctx, `SELECT rowid FROM lists WHERE id = ?`, list.ID).Scan(&rowid); err != nil {
		return err
	}
	if len(embedding) > 0 {
		vecArg, err := embeddingArg(embedding)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM vec_lists WHERE rowid = ?`, rowid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO vec_lists(rowid, embedding, chat_id) VALUES(?, ?, ?)`,
			rowid, vecArg, list.ChatID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) NearestLists(ctx context.Context, chatID string, embedding []float32, limit int) ([]NearestList, error) {
	if limit <= 0 || len(embedding) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vecQuery, err := embeddingArg(embedding)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT l.id, l.chat_id, l.kind, l.title, l.updated_at,
		vec_distance_L2(v.embedding, ?) AS distance
		FROM vec_lists v
		INNER JOIN lists l ON l.rowid = v.rowid
		WHERE v.chat_id = ?
		ORDER BY distance
		LIMIT ?`, vecQuery, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NearestList
	for rows.Next() {
		var list List
		var kindRaw, updatedAt string
		var distance float64
		if err := rows.Scan(&list.ID, &list.ChatID, &kindRaw, &list.Title, &updatedAt, &distance); err != nil {
			return nil, err
		}
		list.Kind = ListKind(kindRaw)
		list.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, NearestList{List: list, Distance: distance})
	}
	return out, rows.Err()
}

func (s *Store) ListLists(ctx context.Context, chatID string, kind ListKind) ([]List, error) {
	query := `SELECT id, chat_id, kind, title, updated_at FROM lists WHERE chat_id = ?`
	args := []any{chatID}
	if kind != "" {
		query += ` AND kind = ?`
		args = append(args, string(kind))
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lists []List
	for rows.Next() {
		var list List
		var kindRaw, updatedAt string
		if err := rows.Scan(&list.ID, &list.ChatID, &kindRaw, &list.Title, &updatedAt); err != nil {
			return nil, err
		}
		list.Kind = ListKind(kindRaw)
		list.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, err
		}
		lists = append(lists, list)
	}
	return lists, rows.Err()
}

func (s *Store) GetList(ctx context.Context, listID string) (List, bool, error) {
	var list List
	var kindRaw, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id, chat_id, kind, title, updated_at FROM lists WHERE id = ?`, listID).
		Scan(&list.ID, &list.ChatID, &kindRaw, &list.Title, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return List{}, false, nil
	}
	if err != nil {
		return List{}, false, err
	}
	list.Kind = ListKind(kindRaw)
	list.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return List{}, false, err
	}
	return list, true, nil
}

func (s *Store) FindListByTitle(ctx context.Context, chatID, title string, kind ListKind) (List, bool, error) {
	var list List
	var kindRaw, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id, chat_id, kind, title, updated_at FROM lists
		WHERE chat_id = ? AND lower(title) = lower(?) AND kind = ?`, chatID, title, string(kind)).
		Scan(&list.ID, &list.ChatID, &kindRaw, &list.Title, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return List{}, false, nil
	}
	if err != nil {
		return List{}, false, err
	}
	list.Kind = ListKind(kindRaw)
	list.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return List{}, false, err
	}
	return list, true, nil
}

func (s *Store) AddListItem(ctx context.Context, item ListItem) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	item.CreatedAt = item.CreatedAt.UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO list_items(id, list_id, text, created_by, done, created_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET text = excluded.text, done = excluded.done`,
		item.ID, item.ListID, item.Text, item.CreatedBy, boolInt(item.Done), item.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListItems(ctx context.Context, listID string) ([]ListItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, list_id, text, created_by, done, created_at
		FROM list_items WHERE list_id = ? ORDER BY created_at ASC`, listID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ListItem
	for rows.Next() {
		var item ListItem
		var done int
		var createdAt string
		if err := rows.Scan(&item.ID, &item.ListID, &item.Text, &item.CreatedBy, &done, &createdAt); err != nil {
			return nil, err
		}
		item.Done = done != 0
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteListItems(ctx context.Context, listID string, itemIDs []string) (int, error) {
	if len(itemIDs) == 0 {
		return 0, nil
	}
	query, args := inClauseQuery(`DELETE FROM list_items WHERE list_id = ? AND id IN `, listID, itemIDs)
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (s *Store) SetListItemsDone(ctx context.Context, listID string, itemIDs []string, done bool) (int, error) {
	if len(itemIDs) == 0 {
		return 0, nil
	}
	query, args := inClauseQuery(`UPDATE list_items SET done = ? WHERE list_id = ? AND id IN `, boolInt(done), listID, itemIDs)
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func inClauseQuery(prefix string, leadingArgs ...any) (string, []any) {
	var ids []string
	switch last := leadingArgs[len(leadingArgs)-1].(type) {
	case []string:
		ids = last
		leadingArgs = leadingArgs[:len(leadingArgs)-1]
	default:
		panic("inClauseQuery: last argument must be []string")
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(leadingArgs)+len(ids))
	args = append(args, leadingArgs...)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	return prefix + "(" + strings.Join(placeholders, ", ") + ")", args
}

func (s *Store) UpsertReminder(ctx context.Context, reminder Reminder) error {
	if reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = time.Now()
	}
	reminder.CreatedAt = reminder.CreatedAt.UTC()
	reminder.DueAt = reminder.DueAt.UTC()
	if !reminder.AnchorAt.IsZero() {
		reminder.AnchorAt = reminder.AnchorAt.UTC()
	}
	if reminder.Status == "" {
		reminder.Status = ReminderPending
	}
	recurrenceJSON, err := marshalRecurrence(reminder.Recurrence)
	if err != nil {
		return err
	}
	anchorAt := nullableTime(reminder.AnchorAt)
	_, err = s.db.ExecContext(ctx, `INSERT INTO reminders(id, chat_id, requester_id, due_at, text, status, created_at, anchor_at, recurrence_json, occurrence_count)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET due_at = excluded.due_at, text = excluded.text, status = excluded.status,
			anchor_at = excluded.anchor_at, recurrence_json = excluded.recurrence_json, occurrence_count = excluded.occurrence_count`,
		reminder.ID, reminder.ChatID, reminder.RequesterID, reminder.DueAt.Format(time.RFC3339Nano),
		reminder.Text, string(reminder.Status), reminder.CreatedAt.Format(time.RFC3339Nano),
		anchorAt, recurrenceJSON, reminder.OccurrenceCount)
	return err
}

func marshalRecurrence(rule *reminders.RecurrenceRule) (any, error) {
	if rule == nil {
		return nil, nil
	}
	raw, err := json.Marshal(rule)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (s *Store) DueReminders(ctx context.Context, now time.Time, limit int) ([]Reminder, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, chat_id, requester_id, due_at, text, status, created_at, anchor_at, recurrence_json, occurrence_count
		FROM reminders WHERE status = 'pending' AND due_at <= ? ORDER BY due_at ASC LIMIT ?`,
		now.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReminders(rows)
}

func (s *Store) GetReminder(ctx context.Context, chatID, reminderID string) (Reminder, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, chat_id, requester_id, due_at, text, status, created_at, anchor_at, recurrence_json, occurrence_count
		FROM reminders WHERE id = ? AND chat_id = ? AND status = 'pending'`, reminderID, chatID)
	reminder, err := scanReminderRow(row)
	if err == sql.ErrNoRows {
		return Reminder{}, false, nil
	}
	if err != nil {
		return Reminder{}, false, err
	}
	return reminder, true, nil
}

func (s *Store) ListReminders(ctx context.Context, chatID string, status ReminderStatus) ([]Reminder, error) {
	query := `SELECT id, chat_id, requester_id, due_at, text, status, created_at, anchor_at, recurrence_json, occurrence_count FROM reminders WHERE chat_id = ?`
	args := []any{chatID}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, string(status))
	}
	query += ` ORDER BY due_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReminders(rows)
}

func (s *Store) CancelReminder(ctx context.Context, chatID, reminderID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE reminders SET status = 'cancelled' WHERE id = ? AND chat_id = ? AND status = 'pending'`, reminderID, chatID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("reminder not found")
	}
	return nil
}

func (s *Store) AdvanceReminder(ctx context.Context, reminderID string, nextDue *time.Time, occurrenceCount int) error {
	if nextDue == nil {
		_, err := s.db.ExecContext(ctx, `UPDATE reminders SET status = 'delivered' WHERE id = ?`, reminderID)
		return err
	}
	due := nextDue.UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `UPDATE reminders SET due_at = ?, occurrence_count = ?, status = 'pending' WHERE id = ?`,
		due, occurrenceCount, reminderID)
	return err
}

func (s *Store) MarkReminderDelivered(ctx context.Context, reminderID string) error {
	return s.AdvanceReminder(ctx, reminderID, nil, 0)
}

func escapeSQLString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func NewTopicID(chatID string, now time.Time) string {
	return fmt.Sprintf("%s:%d", chatID, now.UTC().UnixNano())
}

func NewListID(chatID string, now time.Time) string {
	return fmt.Sprintf("list:%s:%d", chatID, now.UTC().UnixNano())
}

func NewListItemID(listID string, now time.Time) string {
	return fmt.Sprintf("item:%s:%d", listID, now.UTC().UnixNano())
}

func NewReminderID(chatID string, now time.Time) string {
	return fmt.Sprintf("reminder:%s:%d", chatID, now.UTC().UnixNano())
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	var messages []Message
	for rows.Next() {
		var message Message
		var isGroup, isFromSelf int
		var replyToID, topicID sql.NullString
		var sentAt string
		if err := rows.Scan(&message.ID, &message.ChatID, &message.SenderID, &message.Sender, &message.Text,
			&isGroup, &isFromSelf, &replyToID, &topicID, &sentAt); err != nil {
			return nil, err
		}
		message.IsGroup = isGroup != 0
		message.IsFromSelf = isFromSelf != 0
		if replyToID.Valid {
			message.ReplyToID = replyToID.String
		}
		if topicID.Valid {
			message.TopicID = topicID.String
		}
		var err error
		message.SentAt, err = time.Parse(time.RFC3339Nano, sentAt)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func scanTopicRow(rows *sql.Rows) (Topic, float64, error) {
	var topic Topic
	var decisionsJSON, openQuestionsJSON, participantsJSON, updatedAt string
	var distance float64
	if err := rows.Scan(&topic.ID, &topic.ChatID, &topic.Title, &topic.Summary, &decisionsJSON, &openQuestionsJSON,
		&participantsJSON, &updatedAt, &distance); err != nil {
		return Topic{}, 0, err
	}
	if err := json.Unmarshal([]byte(decisionsJSON), &topic.Decisions); err != nil {
		return Topic{}, 0, err
	}
	if err := json.Unmarshal([]byte(openQuestionsJSON), &topic.OpenQuestions); err != nil {
		return Topic{}, 0, err
	}
	if err := json.Unmarshal([]byte(participantsJSON), &topic.ActiveParticipants); err != nil {
		return Topic{}, 0, err
	}
	var err error
	topic.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Topic{}, 0, err
	}
	return topic, distance, nil
}

func scanTopicRowNoDistance(rows *sql.Rows) (Topic, float64, error) {
	var topic Topic
	var decisionsJSON, openQuestionsJSON, participantsJSON, updatedAt string
	if err := rows.Scan(&topic.ID, &topic.ChatID, &topic.Title, &topic.Summary, &decisionsJSON, &openQuestionsJSON,
		&participantsJSON, &updatedAt); err != nil {
		return Topic{}, 0, err
	}
	if err := json.Unmarshal([]byte(decisionsJSON), &topic.Decisions); err != nil {
		return Topic{}, 0, err
	}
	if err := json.Unmarshal([]byte(openQuestionsJSON), &topic.OpenQuestions); err != nil {
		return Topic{}, 0, err
	}
	if err := json.Unmarshal([]byte(participantsJSON), &topic.ActiveParticipants); err != nil {
		return Topic{}, 0, err
	}
	var err error
	topic.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Topic{}, 0, err
	}
	return topic, 0, nil
}

func scanTopic(row *sql.Row) (Topic, error) {
	var topic Topic
	var decisionsJSON, openQuestionsJSON, participantsJSON, updatedAt string
	if err := row.Scan(&topic.ID, &topic.ChatID, &topic.Title, &topic.Summary, &decisionsJSON, &openQuestionsJSON,
		&participantsJSON, &updatedAt); err != nil {
		return Topic{}, err
	}
	if err := json.Unmarshal([]byte(decisionsJSON), &topic.Decisions); err != nil {
		return Topic{}, err
	}
	if err := json.Unmarshal([]byte(openQuestionsJSON), &topic.OpenQuestions); err != nil {
		return Topic{}, err
	}
	if err := json.Unmarshal([]byte(participantsJSON), &topic.ActiveParticipants); err != nil {
		return Topic{}, err
	}
	var err error
	topic.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Topic{}, err
	}
	return topic, nil
}

func scanReminders(rows *sql.Rows) ([]Reminder, error) {
	var reminders []Reminder
	for rows.Next() {
		reminder, err := scanReminderRow(rows)
		if err != nil {
			return nil, err
		}
		reminders = append(reminders, reminder)
	}
	return reminders, rows.Err()
}

func scanReminderRow(scanner interface {
	Scan(dest ...any) error
}) (Reminder, error) {
	var reminder Reminder
	var status, dueAt, createdAt string
	var anchorAt, recurrenceJSON sql.NullString
	if err := scanner.Scan(&reminder.ID, &reminder.ChatID, &reminder.RequesterID, &dueAt, &reminder.Text, &status, &createdAt,
		&anchorAt, &recurrenceJSON, &reminder.OccurrenceCount); err != nil {
		return Reminder{}, err
	}
	reminder.Status = ReminderStatus(status)
	var err error
	reminder.DueAt, err = time.Parse(time.RFC3339Nano, dueAt)
	if err != nil {
		return Reminder{}, err
	}
	reminder.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Reminder{}, err
	}
	if anchorAt.Valid && anchorAt.String != "" {
		reminder.AnchorAt, err = time.Parse(time.RFC3339Nano, anchorAt.String)
		if err != nil {
			return Reminder{}, err
		}
	}
	if recurrenceJSON.Valid && recurrenceJSON.String != "" {
		var rule reminders.RecurrenceRule
		if err := json.Unmarshal([]byte(recurrenceJSON.String), &rule); err != nil {
			return Reminder{}, err
		}
		reminder.Recurrence = &rule
	}
	return reminder, nil
}
