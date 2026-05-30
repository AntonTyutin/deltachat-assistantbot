package transport

import (
	"errors"
	"fmt"
)

var (
	ErrUnsupportedCapability = errors.New("unsupported transport capability")
	ErrTemporary             = errors.New("temporary transport error")
	ErrPermanent             = errors.New("permanent transport error")
	ErrRateLimited           = errors.New("transport rate limited")
)

type UnsupportedCapabilityError struct {
	Capability Capability
	Transport  string
}

func (e UnsupportedCapabilityError) Error() string {
	if e.Transport == "" {
		return fmt.Sprintf("%v: %s", ErrUnsupportedCapability, e.Capability)
	}
	return fmt.Sprintf("%v: %s on %s", ErrUnsupportedCapability, e.Capability, e.Transport)
}

func (e UnsupportedCapabilityError) Unwrap() error {
	return ErrUnsupportedCapability
}
