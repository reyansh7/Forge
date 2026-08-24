package store

import "testing"

func TestNewRedisPingerParsesLoopbackURL(t *testing.T) {
	// Path /0 is the Redis DB index; we ignore it in 0.1 but must still
	// parse host:port and treat a URL with no user as unauthenticated.
	p, err := NewRedisPinger("redis://127.0.0.1:6379/0")
	if err != nil {
		t.Fatal(err)
	}
	if p.addr != "127.0.0.1:6379" {
		t.Fatalf("addr = %q", p.addr)
	}
	if p.password != "" {
		t.Fatal("unexpected password")
	}
}

func TestNewRedisPingerRejectsUnknownScheme(t *testing.T) {
	_, err := NewRedisPinger("http://127.0.0.1:6379")
	if err == nil {
		t.Fatal("expected scheme error")
	}
}

func TestNewRedisPingerRejectsTLSInThisIncrement(t *testing.T) {
	// rediss:// is valid Redis URL syntax; we refuse it until TLS is designed.
	_, err := NewRedisPinger("rediss://127.0.0.1:6379")
	if err == nil {
		t.Fatal("expected rediss error")
	}
}
