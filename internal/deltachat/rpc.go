package deltachat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	chatmail "github.com/chatmail/rpc-client-go/v2/deltachat"
)

type RPCClient struct {
	accountsPath string
	serverCmd    string
	rpc          *chatmail.Rpc
	accountID    uint32
}

func NewRPCClient(serverCmd, accountsPath string) *RPCClient {
	return &RPCClient{
		serverCmd:    serverCmd,
		accountsPath: accountsPath,
	}
}

// ConfiguredAccountAddr returns the configured e-mail address of the sole account
// in accountsPath. It opens a short-lived RPC session (same as CLI helpers).
func (c *RPCClient) ConfiguredAccountAddr(ctx context.Context) (string, error) {
	rpc, transport, err := openRPC(ctx, c.accountsPath, c.serverCmd)
	if err != nil {
		return "", err
	}
	defer transport.Close()

	accountID, err := soleAccountID(rpc)
	if err != nil {
		return "", err
	}
	return configuredAccountAddr(rpc, accountID)
}

func (c *RPCClient) Run(ctx context.Context, handler EventHandler) error {
	rpc, transport, err := openRPC(ctx, c.accountsPath, c.serverCmd)
	if err != nil {
		return err
	}
	defer transport.Close()

	accountID, err := soleAccountID(rpc)
	if err != nil {
		return err
	}

	c.rpc = rpc
	c.accountID = accountID

	bot := chatmail.NewBot(rpc)
	handlerErr := make(chan error, 1)
	runErr := make(chan error, 1)

	bot.OnNewMsg(func(bot *chatmail.Bot, accID uint32, msgID uint32) {
		c.handleMessageEvent(ctx, bot, handlerErr, handler, MessageEventNew, accID, msgID)
	})
	bot.On(&chatmail.EventTypeMsgsChanged{}, func(bot *chatmail.Bot, accID uint32, event chatmail.EventType) {
		changed, ok := event.(*chatmail.EventTypeMsgsChanged)
		if !ok || changed.MsgId == 0 {
			return
		}
		c.handleMessageEvent(ctx, bot, handlerErr, handler, MessageEventUpdated, accID, changed.MsgId)
	})
	bot.On(&chatmail.EventTypeMsgDeleted{}, func(bot *chatmail.Bot, accID uint32, event chatmail.EventType) {
		deleted, ok := event.(*chatmail.EventTypeMsgDeleted)
		if !ok {
			return
		}
		msgEvent := MessageEvent{
			Kind:      MessageEventDeleted,
			ChatID:    strconv.FormatUint(uint64(deleted.ChatId), 10),
			MessageID: strconv.FormatUint(uint64(deleted.MsgId), 10),
		}
		if err := handler(ctx, msgEvent); err != nil {
			reportHandlerError(bot, handlerErr, err)
		}
	})
	bot.On(&chatmail.EventTypeLocationChanged{}, func(bot *chatmail.Bot, accID uint32, event chatmail.EventType) {
		changed, ok := event.(*chatmail.EventTypeLocationChanged)
		if !ok {
			return
		}
		c.handleLocationChangedEvent(ctx, bot, handlerErr, handler, accID, changed.ContactId)
	})

	go func() {
		runErr <- bot.Run()
	}()

	select {
	case <-ctx.Done():
		bot.Stop()
		return ctx.Err()
	case err := <-handlerErr:
		bot.Stop()
		return err
	case err := <-runErr:
		return err
	}
}

func (c *RPCClient) handleMessageEvent(ctx context.Context, bot *chatmail.Bot, handlerErr chan<- error, handler EventHandler, kind MessageEventKind, accID uint32, msgID uint32) {
	message, err := c.loadMessage(accID, msgID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skip deltachat message event: account_id=%d msg_id=%d kind=%s error=%v\n", accID, msgID, kind, err)
		return
	}
	if message.SenderID == "" {
		return
	}
	if err := handler(ctx, MessageEvent{
		Kind:      kind,
		Message:   message,
		ChatID:    message.ChatID,
		MessageID: message.ID,
	}); err != nil {
		reportHandlerError(bot, handlerErr, err)
	}
}

func (c *RPCClient) handleLocationChangedEvent(ctx context.Context, bot *chatmail.Bot, handlerErr chan<- error, handler EventHandler, accID uint32, contactID *uint32) {
	locations, err := c.rpc.GetLocations(accID, nil, contactID, 0, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skip deltachat location changed event: account_id=%d error=%v\n", accID, err)
		return
	}
	for _, location := range latestLocationsByContact(locations, contactID) {
		lat := location.Latitude
		lon := location.Longitude
		event := MessageEvent{
			Kind:          MessageEventLocationUpdate,
			ParticipantID: strconv.FormatUint(uint64(location.ContactId), 10),
			Latitude:      &lat,
			Longitude:     &lon,
		}
		if err := handler(ctx, event); err != nil {
			reportHandlerError(bot, handlerErr, err)
			return
		}
	}
}

func latestLocationsByContact(locations []chatmail.Location, contactID *uint32) []chatmail.Location {
	if contactID != nil {
		var (
			best      chatmail.Location
			found     bool
			bestStamp int64
		)
		for _, location := range locations {
			if location.ContactId != *contactID {
				continue
			}
			if !found || location.Timestamp > bestStamp {
				best = location
				bestStamp = location.Timestamp
				found = true
			}
		}
		if !found {
			return nil
		}
		return []chatmail.Location{best}
	}

	bestByContact := map[uint32]chatmail.Location{}
	for _, location := range locations {
		if location.ContactId == 0 {
			continue
		}
		best, ok := bestByContact[location.ContactId]
		if !ok || location.Timestamp > best.Timestamp {
			bestByContact[location.ContactId] = location
		}
	}
	result := make([]chatmail.Location, 0, len(bestByContact))
	for _, location := range bestByContact {
		result = append(result, location)
	}
	return result
}

func reportHandlerError(bot *chatmail.Bot, handlerErr chan<- error, err error) {
	select {
	case handlerErr <- err:
		bot.Stop()
	default:
	}
}

func (c *RPCClient) SendText(ctx context.Context, message OutboundMessage) (string, error) {
	if c.rpc == nil {
		return "", fmt.Errorf("deltachat client is not running")
	}
	chatID, err := parseUint32(message.ChatID)
	if err != nil {
		return "", err
	}
	text := message.Text
	data := chatmail.MessageData{Text: &text}
	if message.ReplyToID != "" {
		replyToID, err := parseUint32(message.ReplyToID)
		if err != nil {
			return "", err
		}
		data.QuotedMessageId = &replyToID
	}
	messageID, err := c.rpc.SendMsg(c.accountID, chatID, data)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(uint64(messageID), 10), nil
}

func (c *RPCClient) loadMessage(accountID uint32, msgID uint32) (message Message, err error) {
	defer recoverRPCPanic(&err)

	msg, err := c.rpc.GetMessage(accountID, msgID)
	if err != nil {
		return Message{}, err
	}
	if msg.FromId <= chatmail.ContactLastSpecial {
		return Message{}, nil
	}
	chat, err := c.rpc.GetBasicChatInfo(accountID, msg.ChatId)
	if err != nil {
		return Message{}, err
	}
	return convertMessage(msg, chat), nil
}

func recoverRPCPanic(err *error) {
	if recovered := recover(); recovered != nil {
		*err = fmt.Errorf("deltachat rpc client panic: %v", recovered)
	}
}

func SetupAccount(ctx context.Context, serverCmd, accountsPath, setupQR string) (string, error) {
	rpc, transport, err := openRPC(ctx, accountsPath, serverCmd)
	if err != nil {
		return "", err
	}
	defer transport.Close()

	accountID, err := firstOrCreateAccount(rpc)
	if err != nil {
		return "", err
	}

	configured, err := rpc.IsConfigured(accountID)
	if err != nil {
		return "", err
	}
	if !configured {
		botFlag := "1"
		if err := rpc.SetConfig(accountID, "bot", &botFlag); err != nil {
			return "", err
		}
		if err := rpc.AddTransportFromQr(accountID, setupQR); err != nil {
			return "", err
		}
	}

	return configuredAccountAddr(rpc, accountID)
}

func InviteLink(ctx context.Context, serverCmd, accountsPath string) (string, error) {
	rpc, transport, err := openRPC(ctx, accountsPath, serverCmd)
	if err != nil {
		return "", err
	}
	defer transport.Close()

	accountID, err := soleAccountID(rpc)
	if err != nil {
		return "", err
	}
	return rpc.GetChatSecurejoinQrCode(accountID, nil)
}

type BotProfileUpdate struct {
	Name      string
	Bio       string
	PhotoPath string
}

func UpdateBotProfile(ctx context.Context, serverCmd, accountsPath string, update BotProfileUpdate) error {
	rpc, transport, err := openRPC(ctx, accountsPath, serverCmd)
	if err != nil {
		return err
	}
	defer transport.Close()

	accountID, err := soleAccountID(rpc)
	if err != nil {
		return err
	}

	if strings.TrimSpace(update.Name) != "" {
		name := update.Name
		if err := rpc.SetConfig(accountID, "displayname", &name); err != nil {
			return fmt.Errorf("set display name: %w", err)
		}
	}
	if strings.TrimSpace(update.Bio) != "" {
		bio := update.Bio
		if err := rpc.SetConfig(accountID, "selfstatus", &bio); err != nil {
			return fmt.Errorf("set bio: %w", err)
		}
	}
	if strings.TrimSpace(update.PhotoPath) != "" {
		absPhotoPath, err := filepath.Abs(update.PhotoPath)
		if err != nil {
			return fmt.Errorf("resolve photo path: %w", err)
		}
		photo := absPhotoPath
		if err := rpc.SetConfig(accountID, "selfavatar", &photo); err != nil {
			return fmt.Errorf("set photo: %w", err)
		}
	}
	return nil
}

func openRPC(ctx context.Context, accountsPath, serverCmd string) (*chatmail.Rpc, *chatmail.IOTransport, error) {
	trans := chatmail.NewIOTransport()
	if serverCmd != "" {
		trans.Cmd = serverCmd
	}
	trans.AccountsDir = accountsPath
	if err := trans.Open(); err != nil {
		return nil, nil, err
	}
	return &chatmail.Rpc{Context: ctx, Transport: trans}, trans, nil
}

func firstOrCreateAccount(rpc *chatmail.Rpc) (uint32, error) {
	ids, err := rpc.GetAllAccountIds()
	if err != nil {
		return 0, err
	}
	if len(ids) > 1 {
		return 0, fmt.Errorf("multiple DeltaChat accounts found; only one account per bot instance is supported")
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	return rpc.AddAccount()
}

func soleAccountID(rpc *chatmail.Rpc) (uint32, error) {
	ids, err := rpc.GetAllAccountIds()
	if err != nil {
		return 0, err
	}
	switch len(ids) {
	case 0:
		return 0, fmt.Errorf("no DeltaChat account; run `assistantbot setup-account`")
	case 1:
		return ids[0], nil
	default:
		return 0, fmt.Errorf("multiple DeltaChat accounts found; only one account per bot instance is supported")
	}
}

func configuredAccountAddr(rpc *chatmail.Rpc, accountID uint32) (string, error) {
	account, err := rpc.GetAccountInfo(accountID)
	if err != nil {
		return "", err
	}
	configured, ok := account.(*chatmail.AccountConfigured)
	if !ok || configured.Addr == nil || strings.TrimSpace(*configured.Addr) == "" {
		return "", fmt.Errorf("configured DeltaChat account %d has no address", accountID)
	}
	return *configured.Addr, nil
}

func convertMessage(msg chatmail.Message, chat chatmail.BasicChat) Message {
	replyToID := ""
	if msg.ParentId != nil {
		replyToID = strconv.FormatUint(uint64(*msg.ParentId), 10)
	}
	sentAt := time.Unix(msg.Timestamp, 0)
	if msg.Timestamp == 0 {
		sentAt = time.Unix(msg.ReceivedTimestamp, 0)
	}
	return Message{
		ID:         strconv.FormatUint(uint64(msg.Id), 10),
		ChatID:     strconv.FormatUint(uint64(msg.ChatId), 10),
		SenderID:   strconv.FormatUint(uint64(msg.FromId), 10),
		Sender:     contactName(msg.Sender),
		Text:       msg.Text,
		IsGroup:    chat.ChatType == chatmail.ChatTypeGroup || chat.ChatType == chatmail.ChatTypeMailinglist,
		IsFromSelf: msg.FromId == chatmail.ContactSelf || msg.State >= chatmail.MsgStateOutPreparing,
		ReplyToID:  replyToID,
		SentAt:     sentAt,
	}
}

func contactName(contact chatmail.Contact) string {
	if contact.Name != "" {
		return contact.Name
	}
	if contact.DisplayName != "" {
		return contact.DisplayName
	}
	return contact.NameAndAddr
}

func parseUint32(value string) (uint32, error) {
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid DeltaChat numeric id %q: %w", value, err)
	}
	return uint32(parsed), nil
}
