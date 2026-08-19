package fleet

import (
	"os"
	"strings"
)

// TokenEnv names the environment variable holding a Hugging Face access token.
//
// One variable for the whole program. The ingest needs it to read the gated
// source and the push needs it to write to the store, and it is the same
// account both times, so a second variable would only be a second thing to
// forget on a box that has been running for a week.
const TokenEnv = "HF_TOKEN"

// Token returns the token, trimmed.
//
// Trimmed because a token pasted out of a browser arrives with a newline on the
// end, and an Authorization header with a newline in it fails with a message
// about the header rather than about the token, which is an afternoon nobody
// needs to spend twice.
func Token() string { return strings.TrimSpace(os.Getenv(TokenEnv)) }
