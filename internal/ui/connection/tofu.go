package connection

import "io.github/murmel/internal/certs"

// tofuAction is what the controller does for a TOFU verification outcome.
type tofuAction int

const (
	// tofuProceed continues the handshake silently (pinned fingerprint
	// matches and was not declined earlier in this session).
	tofuProceed tofuAction = iota
	// tofuAskTrust shows the first-use fingerprint trust dialog.
	tofuAskTrust
	// tofuAskChanged shows the old-vs-new fingerprint warning.
	tofuAskChanged
	// tofuBlocked refuses the handshake: the user declined exactly this
	// fingerprint earlier in this session (guard against the pin that
	// certs.VerifyServer records on first sight).
	tofuBlocked
)

// tofuActionFor maps a verification result to the controller's action.
// declined is the fingerprint the user refused earlier this session for
// this address ("" when none was refused).
func tofuActionFor(result certs.VerifyResult, declined string) tofuAction {
	switch result {
	case certs.VerifyMatch:
		if declined != "" {
			return tofuBlocked
		}
		return tofuProceed
	case certs.VerifyNew:
		return tofuAskTrust
	case certs.VerifyChanged:
		return tofuAskChanged
	}
	return tofuBlocked
}
