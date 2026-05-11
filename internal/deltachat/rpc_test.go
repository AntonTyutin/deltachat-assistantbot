package deltachat

import (
	"fmt"
	"testing"

	chatmail "github.com/chatmail/rpc-client-go/v2/deltachat"
)

func TestSoleAccountID(t *testing.T) {
	id, err := soleAccountIDFromIDs([]uint32{3})
	if err != nil {
		t.Fatal(err)
	}
	if id != 3 {
		t.Fatalf("expected account 3, got %d", id)
	}
}

func TestSoleAccountIDMissing(t *testing.T) {
	_, err := soleAccountIDFromIDs(nil)
	if err == nil {
		t.Fatal("expected error for missing account")
	}
}

func TestSoleAccountIDMultiple(t *testing.T) {
	_, err := soleAccountIDFromIDs([]uint32{1, 2})
	if err == nil {
		t.Fatal("expected error for multiple accounts")
	}
}

func soleAccountIDFromIDs(ids []uint32) (uint32, error) {
	switch len(ids) {
	case 0:
		return 0, fmt.Errorf("no DeltaChat account; run `assistantbot setup-account`")
	case 1:
		return ids[0], nil
	default:
		return 0, fmt.Errorf("multiple DeltaChat accounts found; only one account per bot instance is supported")
	}
}

func TestRecoverRPCPanicConvertsPanicToError(t *testing.T) {
	err := callWithRecoveredRPCPanic()
	if err == nil {
		t.Fatal("expected recovered panic error")
	}
	if err.Error() != "deltachat rpc client panic: boom" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLatestLocationsByContactAllContactsUseLatestPerContact(t *testing.T) {
	locations := latestLocationsByContact([]chatmail.Location{
		{ContactId: 10, MsgId: 101, Latitude: 1, Longitude: 1, Timestamp: 1000},
		{ContactId: 10, MsgId: 102, Latitude: 2, Longitude: 2, Timestamp: 2000},
		{ContactId: 11, MsgId: 201, Latitude: 3, Longitude: 3, Timestamp: 1500},
		{ContactId: 11, MsgId: 202, Latitude: 4, Longitude: 4, Timestamp: 1200},
		{ContactId: 0, MsgId: 999, Latitude: 5, Longitude: 5, Timestamp: 5000},
	}, nil)
	if len(locations) != 2 {
		t.Fatalf("expected 2 locations, got %d", len(locations))
	}
	byContact := map[uint32]chatmail.Location{}
	for _, location := range locations {
		byContact[location.ContactId] = location
	}
	if byContact[10].MsgId != 102 {
		t.Fatalf("expected latest msg for contact 10, got %d", byContact[10].MsgId)
	}
	if byContact[11].MsgId != 201 {
		t.Fatalf("expected latest msg for contact 11, got %d", byContact[11].MsgId)
	}
}

func TestLatestLocationsByContactSingleContact(t *testing.T) {
	contactID := uint32(10)
	locations := latestLocationsByContact([]chatmail.Location{
		{ContactId: 10, MsgId: 101, Latitude: 1, Longitude: 1, Timestamp: 1000},
		{ContactId: 10, MsgId: 102, Latitude: 2, Longitude: 2, Timestamp: 2000},
		{ContactId: 11, MsgId: 201, Latitude: 3, Longitude: 3, Timestamp: 3000},
	}, &contactID)
	if len(locations) != 1 {
		t.Fatalf("expected 1 location, got %d", len(locations))
	}
	if locations[0].ContactId != 10 || locations[0].MsgId != 102 {
		t.Fatalf("unexpected selected location: %+v", locations[0])
	}
}

func callWithRecoveredRPCPanic() (err error) {
	defer recoverRPCPanic(&err)
	panic("boom")
}
