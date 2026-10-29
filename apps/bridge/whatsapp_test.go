package main

import (
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func TestIsSelfChat(t *testing.T) {
	own := types.NewJID("918108398025", types.DefaultUserServer)
	ownLID := types.NewJID("244418099544183", types.HiddenUserServer)

	cases := []struct {
		name string
		chat types.JID
		own  *types.JID
		want bool
	}{
		{
			name: "own message chat",
			chat: types.NewJID("918108398025", types.DefaultUserServer),
			own:  &own,
			want: true,
		},
		{
			name: "customer chat",
			chat: types.NewJID("919999999999", types.DefaultUserServer),
			own:  &own,
			want: false,
		},
		{
			name: "group chat",
			chat: types.NewJID("918108398025", types.GroupServer),
			own:  &own,
			want: false,
		},
		{
			name: "not paired",
			chat: types.NewJID("918108398025", types.DefaultUserServer),
			own:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSelfChat(tc.chat, tc.own, nil); got != tc.want {
				t.Fatalf("isSelfChat(%v) = %v, want %v", tc.chat, got, tc.want)
			}
		})
	}

	if !isSelfChat(ownLID, &own, &ownLID) {
		t.Error("an account LID must be recognized as the self-chat")
	}
	if isSelfChat(types.NewJID("244418099544184", types.HiddenUserServer), &own, &ownLID) {
		t.Error("another LID must not be recognized as the self-chat")
	}
}

func TestMessageTextSupportsConversationAndExtendedText(t *testing.T) {
	plain := "HELP"
	if got := messageText(&waE2E.Message{Conversation: &plain}); got != plain {
		t.Fatalf("plain messageText() = %q, want %q", got, plain)
	}
	extended := "ORDERS"
	if got := messageText(&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &extended}}); got != extended {
		t.Fatalf("extended messageText() = %q, want %q", got, extended)
	}
}

// A phone that unlinks the device used to leave Studio showing "Offline" with
// no QR code at all until the bridge was restarted.
func TestLoggedOutRestartsPairing(t *testing.T) {
	fake := newFakePostgREST(t)
	w := &whatsapp{
		db:     fake.client(),
		log:    waLog.Stdout("whatsapp", "error", false),
		status: "connected",
		jid:    "918108398025:3@s.whatsapp.net",
		qr:     "stale",
		repair: make(chan struct{}, 1),
	}

	w.handleEvent(&events.LoggedOut{Reason: events.ConnectFailureLoggedOut})

	select {
	case <-w.repair:
	default:
		t.Error("logout did not queue a new pairing")
	}

	state := w.state()
	if state.Status != "disconnected" || state.JID != "" || state.QRCode != "" {
		t.Errorf("state after logout = %+v, want disconnected with no jid or qr", state)
	}
	// The expired code must not stay in the connection row looking scannable.
	if !strings.Contains(fake.bodies[len(fake.bodies)-1], `"qr_code":null`) {
		t.Errorf("persisted row kept the old qr code: %s", fake.bodies[len(fake.bodies)-1])
	}
}
