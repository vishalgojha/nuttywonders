package main

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestIsSelfChat(t *testing.T) {
	own := types.NewJID("918108398025", types.DefaultUserServer)

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
			if got := isSelfChat(tc.chat, tc.own); got != tc.want {
				t.Fatalf("isSelfChat(%v) = %v, want %v", tc.chat, got, tc.want)
			}
		})
	}
}
