package cli

import "testing"

func TestIsLoopbackSMTPHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		addr string
		want bool
	}{
		{"localhost:1025", true},
		{"LOCALHOST:587", true},
		{"127.0.0.1:1025", true},
		{"127.0.1.1:1025", true},
		{"[::1]:1025", true},
		{"mail.example.com:587", false},
		{"10.0.0.5:587", false},
		{"192.168.1.10:1025", false},
	}

	for _, tc := range cases {
		if got := isLoopbackSMTPHost(tc.addr); got != tc.want {
			t.Errorf("isLoopbackSMTPHost(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestParseSendmailAllowInsecureAuthFlag(t *testing.T) {
	t.Parallel()

	opts, err := parseSendmailArgs([]string{"--smtp", "mail.example.test:587", "--user", "u", "--password", "p", "--allow-insecure-auth", "a@b.test"}, defaultSendmailSMTP)
	if err != nil {
		t.Fatalf("parseSendmailArgs: %v", err)
	}
	if !opts.allowInsecureAuth {
		t.Fatalf("expected allowInsecureAuth=true")
	}
}
