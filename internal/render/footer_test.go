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
