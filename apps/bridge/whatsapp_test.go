package main

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
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
