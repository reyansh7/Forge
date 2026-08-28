package queue

import (
	"bufio"
	"bytes"
	"errors"
	"testing"
)

func TestJobRoundTrip(t *testing.T) {
	j := Job{ID: "abc", Type: TypeExample, Payload: []byte(`{"ok":true}`)}
	raw, err := j.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseJob(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "abc" || got.Type != TypeExample {
		t.Fatalf("got %+v", got)
	}
}

func TestParseJobRejectsInvalidJSON(t *testing.T) {
	if _, err := ParseJob([]byte(`{`)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseJobRejectsMissingType(t *testing.T) {
	if _, err := ParseJob([]byte(`{"id":"x"}`)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("err = %v", err)
	}
}

func TestAllowedType(t *testing.T) {
	if !AllowedType(TypeExample) {
		t.Fatal("example must be allowed")
	}
	if AllowedType("shell") || AllowedType("") {
		t.Fatal("arbitrary types must be rejected")
	}
}

func TestWriteAndReadSimplePONG(t *testing.T) {
	var buf bytes.Buffer
	if err := writeRESP(&buf, "PING"); err != nil {
		t.Fatal(err)
	}
	// Simulate Redis +PONG without a server.
	reply := bytes.NewBufferString("+PONG\r\n")
	v, err := readRESP(bufio.NewReader(reply))
	if err != nil {
		t.Fatal(err)
	}
	if v.kind != respSimple || v.str != "PONG" {
		t.Fatalf("%+v", v)
	}
}

func TestReadRESPNullArray(t *testing.T) {
	v, err := readRESP(bufio.NewReader(bytes.NewBufferString("*-1\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if v.kind != respNull {
		t.Fatalf("kind = %v", v.kind)
	}
}

func TestReadRESPBLPopArray(t *testing.T) {
	raw := "*2\r\n$10\r\nforge:jobs\r\n$27\r\n{\"id\":\"a\",\"type\":\"example\"}\r\n"
	v, err := readRESP(bufio.NewReader(bytes.NewBufferString(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if v.kind != respArray || len(v.items) != 2 {
		t.Fatalf("%+v", v)
	}
	job, err := ParseJob([]byte(v.items[1].str))
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != "a" {
		t.Fatalf("id = %q", job.ID)
	}
}
