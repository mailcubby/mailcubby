package smtpd

import (
	"errors"

	"github.com/emersion/go-sasl"
)

// LoginMechanism is the SASL mechanism name for SMTP AUTH LOGIN.
const LoginMechanism = "LOGIN"

// LoginAuthenticator validates a username and password pair for AUTH LOGIN.
type LoginAuthenticator func(username, password string) error

type loginServer struct {
	auth     LoginAuthenticator
	step     int
	username string
}

// NewLoginServer returns a sasl.Server implementing SMTP AUTH LOGIN with optional
// initial response (SPEC FR-003, ARCHITECTURE 3.3).
func NewLoginServer(auth LoginAuthenticator) sasl.Server {
	return &loginServer{auth: auth}
}

func (s *loginServer) Next(response []byte) ([]byte, bool, error) {
	switch s.step {
	case 0:
		// If the client provided an initial response (AUTH LOGIN <base64 user>),
		// treat it as the username and proceed directly to the Password challenge.
		if response != nil {
			s.username = string(response)
			s.step = 2
			return []byte("Password:"), false, nil
		}
		s.step = 1
		return []byte("Username:"), false, nil
	case 1:
		s.username = string(response)
		s.step = 2
		return []byte("Password:"), false, nil
	case 2:
		password := string(response)
		s.step = 3
		if err := s.auth(s.username, password); err != nil {
			return nil, true, err
		}
		return nil, true, nil
	default:
		return nil, true, errors.New("smtpd: unexpected SASL LOGIN state")
	}
}
