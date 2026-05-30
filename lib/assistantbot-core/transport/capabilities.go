package transport

type Capability string

const (
	CapabilitySendText       Capability = "send_text"
	CapabilityEditMessage    Capability = "edit_message"
	CapabilityDeleteMessage  Capability = "delete_message"
	CapabilityReact          Capability = "react"
	CapabilityTyping         Capability = "typing"
	CapabilitySendMedia      Capability = "send_media"
	CapabilityLocationEvents Capability = "location_events"
)
