package queue

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func redisURLForTest(t *testing.T) string {
	t.Helper()
	if os.Getenv("FORGE_SKIP_REDIS") != "" {
		t.Skip("FORGE_SKIP_REDIS is set")
	}
	u := strings.TrimSpace(os.Getenv("FORGE_REDIS_URL"))
	if u == "" {
		u = redisURLFromDotEnv("../../.env")
	}
	if u == "" {
		t.Skip("FORGE_REDIS_URL is not set")
	}
	return u
}

func redisURLFromDotEnv(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "FORGE_REDIS_URL" {
			continue
		}
		return strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return ""
}

func TestRedisEnqueueDequeueAgainstDocker(t *testing.T) {
	rawURL := redisURLForTest(t)
	key := "forge:jobs:test:" + strings.ReplaceAll(t.Name(), "/", "_")
	q, err := NewRedis(rawURL, key)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := q.Ping(ctx); err != nil {
		t.Skipf("redis not reachable: %v", err)
	}

	job := Job{ID: "itest", Type: TypeExample, Payload: []byte(`{}`)}
	if err := q.Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}
	got, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "itest" || got.Type != TypeExample {
		t.Fatalf("got %+v", got)
	}
}

func TestReadRESPErrorLine(t *testing.T) {
	v, err := readRESP(bufio.NewReader(strings.NewReader("-ERR nope\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if v.kind != respError || v.str != "ERR nope" {
		t.Fatalf("%+v", v)
	}
}
