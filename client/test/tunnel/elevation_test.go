package tunnel_test

import (
	"errors"
	"testing"

	"github.com/example/safelink/client/internal/tunnel"
)

func TestIsTUNAccessDenied(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("create TUN device: wintun CreateTUN: Error creating interface: Access is denied."), true},
		{errors.New("access denied"), true},
		{errors.New("拒绝访问"), true},
		{errors.New("connection refused"), false},
	}

	for _, tc := range cases {
		if got := tunnel.IsTUNAccessDenied(tc.err); got != tc.want {
			t.Fatalf("IsTUNAccessDenied(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
