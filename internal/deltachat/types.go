package deltachat

import "github.com/AntonTyutin/assistantbot-core/transport"

type Message = transport.Message
type OutboundMessage = transport.OutboundMessage
type MessageEdit = transport.MessageEdit
type MessageReaction = transport.MessageReaction
type TypingState = transport.TypingState
type MediaMessage = transport.MediaMessage
type NewMessageHandler = transport.NewMessageHandler
type MessageUpdatedHandler = transport.MessageUpdatedHandler
type MessageDeletedHandler = transport.MessageDeletedHandler
type LocationUpdatedHandler = transport.LocationUpdatedHandler
type EventHandlers = transport.EventHandlers
type Client = transport.Messenger
