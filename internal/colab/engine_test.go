package colab_test

import (
	"testing"

	"github.com/LJMStark/Colab-Tools/internal/colab"
)

func TestStrictParsersHandleNamedSessionsAndRejectPartialUsage(t *testing.T) {
	// Partial usage must return false instead of silent zero
	if _, ok := colab.ParseUsage("Current balance: 120.5 compute units\n"); ok {
		t.Fatal("partial usage output must be rejected")
	}
	u, ok := colab.ParseUsage("Current balance: 2374.8 compute units\r\nUsage rate: 2.33/hr\r\nActive assignments: 1\r\n")
	if !ok || u.Balance != 2374.8 || u.RatePerHour != 2.33 || u.ActiveAssignments != 1 {
		t.Fatalf("unexpected parsed usage: %+v %v", u, ok)
	}

	// Sessions parser preserves session literally named `colab` and ignores log lines
	out := `[colab] Pruned 1 stale session
[colab] m-s-abc123 | Hardware: L4 | Shape: Standard | Variant: GPU
[?] m-s-orphan99 | Hardware: A100 | Shape: High-RAM | Variant: GPU
`
	sessions := colab.ParseSessions(out)
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].Name != "colab" || sessions[0].IsOrphan {
		t.Fatalf("unexpected first session: %+v", sessions[0])
	}
	if sessions[1].Name != "?" || !sessions[1].IsOrphan || sessions[1].Hardware != "A100" {
		t.Fatalf("unexpected orphan session: %+v", sessions[1])
	}

	who, ok := colab.ParseWhoami("Auth provider: oauth2\r\nEmail: demo@example.com\r\nExpires in: 55m\r\n")
	if !ok || who.Email != "demo@example.com" {
		t.Fatalf("unexpected whoami: %+v %v", who, ok)
	}
}
