package platform

import "testing"

func TestShellQuote(t *testing.T) {
	if got := shellQuote("/Applications/User's VPN.app/helper"); got != `'/Applications/User'\''s VPN.app/helper'` {
		t.Fatalf("shellQuote() = %q", got)
	}
}
