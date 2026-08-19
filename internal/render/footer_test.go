package render

import (
	"strings"
	"testing"

	"github.com/nemo/Tsumugi/internal/network"
)

// The key line held sixteen hints plus mode, proxy and version on one row, which pushed the two
// things worth reading at a glance off the right edge even on a 1080p terminal.
func TestFooterIsTwoLines(t *testing.T) {
	out := Footer("user", "v0.1.27", network.ProxyConfig{
		Kind:    network.ProxySOCKS5,
		Source:  network.SourceManual,
		Address: "127.0.0.1:1080",
	})

	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("footer has %d lines, want 2:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[1], "127.0.0.1:1080") {
		t.Fatalf("second line lacks the proxy address: %q", lines[1])
	}
	if !strings.Contains(lines[1], "v0.1.27") {
		t.Fatalf("second line lacks the version: %q", lines[1])
	}
}

// The reason this exists: with messages marked, a user hunting for "what now" pressed r expecting
// reply and got the reaction panel. The key line has to answer the question instead.
func TestFooterKeysSwitchToForwardingWhenMarked(t *testing.T) {
	plain := FooterKeys(FooterState{})
	marked := FooterKeys(FooterState{MarkedCount: 3})

	if plain == marked {
		t.Fatal("the key line must change once messages are marked")
	}
	if !strings.Contains(marked, "f forward") {
		t.Fatalf("marked key line lacks the forward key: %q", marked)
	}
	if !strings.Contains(marked, "F forward w/o author") {
		t.Fatalf("marked key line lacks the drop-author key: %q", marked)
	}
	if !strings.Contains(marked, "3 marked") {
		t.Fatalf("marked key line lacks the count: %q", marked)
	}
	if !strings.Contains(marked, "Esc clear") {
		t.Fatalf("marked key line lacks the way out: %q", marked)
	}
	// R would be a trap here: it is the reaction panel, not reply.
	if strings.Contains(marked, "R react") {
		t.Fatalf("marked key line should not advertise unrelated keys: %q", marked)
	}
}

// n and N do nothing until a search has run, so advertising them before that is a lie.
func TestFooterKeysAdvertiseSearchNavOnlyWithHits(t *testing.T) {
	if strings.Contains(FooterKeys(FooterState{}), "n/N results") {
		t.Fatal("n/N advertised with no search results")
	}
	if !strings.Contains(FooterKeys(FooterState{SearchHits: 4}), "n/N results") {
		t.Fatal("n/N not advertised once there are results")
	}
}

func TestFooterKeysAlwaysOfferAWayOut(t *testing.T) {
	for _, state := range []FooterState{{}, {MarkedCount: 1}, {SearchHits: 2}} {
		if !strings.Contains(FooterKeys(state), "q quit") {
			t.Errorf("state %+v has no quit hint", state)
		}
	}
}

// Each footer line must be a single logical line, because the footer is a two-row TextView with
// wrapping off: an embedded newline would silently push the status line out of view, which is what
// happened when the long key line was allowed to character-wrap.
func TestFooterLinesContainNoEmbeddedNewlines(t *testing.T) {
	states := []FooterState{{}, {MarkedCount: 3}, {SearchHits: 5}, {MarkedCount: 2, SearchHits: 9}}
	for _, state := range states {
		if strings.Contains(FooterKeys(state), "\n") {
			t.Errorf("FooterKeys(%+v) contains a newline", state)
		}
	}
	status := FooterStatus("user", "v0.1.29", network.ProxyConfig{
		Kind: network.ProxySOCKS5, Source: network.SourceManual, Address: "127.0.0.1:1080",
	})
	if strings.Contains(status, "\n") {
		t.Errorf("FooterStatus contains a newline: %q", status)
	}
}

// G jumps to the latest message in the message pane and opens a global search everywhere else. A
// hint naming the wrong one is worse than no hint, which is why the footer is focus-aware.
func TestFooterKeysDescribeGPerPane(t *testing.T) {
	inMessages := FooterKeys(FooterState{MessagePaneFocused: true})
	elsewhere := FooterKeys(FooterState{})

	if !strings.Contains(inMessages, "G latest") {
		t.Fatalf("message pane line = %q, want G described as jumping to the latest", inMessages)
	}
	if strings.Contains(inMessages, "G search all") {
		t.Fatalf("message pane line = %q, must not describe G as search", inMessages)
	}
	if !strings.Contains(elsewhere, "G search all") {
		t.Fatalf("chat list line = %q, want G described as global search", elsewhere)
	}
}

// The English base line is around 150 cells, so on the 80-column console the raw-terminal goal
// targets it was clipped and half the hints were unreadable.
func TestFooterKeysFitTheGivenWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80, 100, 200} {
		line := FooterKeys(FooterState{Width: width, MessagePaneFocused: true})
		if got := StringWidth(line); got > width {
			t.Errorf("width %d: line is %d cells: %q", width, got, line)
		}
	}
}

// Whatever is dropped, the way out survives: a user who can read nothing else still needs quit.
func TestFooterKeysAlwaysKeepQuitEvenWhenCramped(t *testing.T) {
	for _, width := range []int{10, 20, 40, 80} {
		for _, state := range []FooterState{{Width: width}, {Width: width, MarkedCount: 3}} {
			if line := FooterKeys(state); !strings.Contains(line, "q quit") {
				t.Errorf("width %d state %+v: %q has no quit hint", width, state, line)
			}
		}
	}
}

// Hints are dropped from the least useful end, so a narrow line is a prefix of a wide one.
func TestFooterKeysDropFromTheLeastUsefulEnd(t *testing.T) {
	wide := FooterKeys(FooterState{Width: 300, MessagePaneFocused: true})
	narrow := FooterKeys(FooterState{Width: 60, MessagePaneFocused: true})

	if !strings.Contains(wide, "Tab focus") || !strings.Contains(narrow, "Tab focus") {
		t.Fatalf("the most useful hint was dropped:\nwide=%q\nnarrow=%q", wide, narrow)
	}
	if strings.Contains(narrow, "? settings") {
		t.Fatalf("narrow line %q kept a low-priority hint", narrow)
	}
}

// Unknown width means the first draw has not happened yet; listing everything beats guessing.
func TestFooterKeysListEverythingWithoutAWidth(t *testing.T) {
	line := FooterKeys(FooterState{MessagePaneFocused: true})

	for _, want := range []string{"Tab focus", "? settings", "q quit"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q when the width is unknown", line, want)
		}
	}
}
