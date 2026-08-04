package may

import "testing"

// A token pasted out of a browser arrives with whitespace on it, and a bearer
// header with a newline in the middle is refused by the transport with a
// message about the header rather than about the token.
func TestTheTokenComesFromTheEnvironmentWithoutItsWhitespace(t *testing.T) {
	t.Setenv(TokenEnv, "  hf_padded\n")
	if got := Token(); got != "hf_padded" {
		t.Errorf("Token = %q", got)
	}
	t.Setenv(TokenEnv, "")
	if got := Token(); got != "" {
		t.Errorf("Token with nothing set = %q", got)
	}
}

// One variable for the whole program, because the ingest reads the gated source
// with it and the push writes the store with it, and they are one account.
func TestTheTokenVariableIsTheOneHuggingFaceUses(t *testing.T) {
	if TokenEnv != "HF_TOKEN" {
		t.Errorf("TokenEnv is %q, which is not what every other tool on the box reads", TokenEnv)
	}
}
