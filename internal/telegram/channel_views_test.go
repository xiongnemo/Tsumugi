package telegram

import "testing"

func TestIsBroadcastChannel(t *testing.T) {
	if !IsBroadcastChannel("channel", "channel") {
		t.Fatal("broadcast channel expected")
	}
	if IsBroadcastChannel("channel", "group") {
		t.Fatal("megagroup should not be broadcast")
	}
	if IsBroadcastChannel("user", "private") {
		t.Fatal("user peer is not broadcast")
	}
}

func TestFormatViewCount(t *testing.T) {
	cases := map[int]string{
		0:       "",
		42:      "42",
		1500:    "1.5k",
		12000:   "12k",
		1500000: "1.5m",
	}
	for in, want := range cases {
		if got := FormatViewCount(in); got != want {
			t.Fatalf("FormatViewCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestChatIsBroadcast(t *testing.T) {
	if !ChatIsBroadcast(Chat{Kind: "channel", Subtitle: "channel | hello"}) {
		t.Fatal("expected broadcast chat")
	}
	if ChatIsBroadcast(Chat{Kind: "channel", Subtitle: "group | hello"}) {
		t.Fatal("megagroup chat should not be broadcast")
	}
}

func TestChatSupportsGroupReadMarks(t *testing.T) {
	if !ChatSupportsGroupReadMarks(Chat{Kind: "chat", Subtitle: "group | hello"}) {
		t.Fatal("basic group should support group read mark attempts")
	}
	if !ChatSupportsGroupReadMarks(Chat{Kind: "channel", Subtitle: "group | hello"}) {
		t.Fatal("megagroup should support group read mark attempts")
	}
	if ChatSupportsGroupReadMarks(Chat{Kind: "channel", Subtitle: "channel | hello"}) {
		t.Fatal("broadcast channel should not support group read marks")
	}
	if ChatSupportsGroupReadMarks(Chat{Kind: "user", Subtitle: "private"}) {
		t.Fatal("private chat should not support group read marks")
	}
}

func TestReactionsJSONRoundTrip(t *testing.T) {
	in := []ReactionSummary{{Emoji: "👍", Count: 2, Chosen: true}}
	raw := ReactionsJSON(in)
	out := ReactionsFromJSON(raw)
	if len(out) != 1 || out[0].Emoji != "👍" || out[0].Count != 2 || !out[0].Chosen {
		t.Fatalf("round trip = %+v", out)
	}
}
