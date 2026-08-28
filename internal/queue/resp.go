package queue

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// writeRESP encodes a Redis command as a RESP array of bulk strings.
// Example: PING → *1\r\n$4\r\nPING\r\n
//
// We still do not add a Redis client library: increment 0.1 already spoke
// a tiny dialect for PING. The queue needs bulk replies (BLPOP), so this
// file is a slightly stricter reader than store.readRESPLine.
func writeRESP(w io.Writer, args ...string) error {
	var b strings.Builder
	b.WriteByte('*')
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString("\r\n")
	for _, arg := range args {
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(len(arg)))
		b.WriteString("\r\n")
		b.WriteString(arg)
		b.WriteString("\r\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

type respKind int

const (
	respSimple respKind = iota
	respError
	respInt
	respBulk
	respArray
	respNull
)

type respValue struct {
	kind  respKind
	str   string
	n     int
	items []respValue
}

func readRESP(r *bufio.Reader) (respValue, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return respValue{}, err
	}
	switch prefix {
	case '+':
		s, err := readCRLFLine(r)
		if err != nil {
			return respValue{}, err
		}
		return respValue{kind: respSimple, str: s}, nil
	case '-':
		s, err := readCRLFLine(r)
		if err != nil {
			return respValue{}, err
		}
		return respValue{kind: respError, str: s}, nil
	case ':':
		s, err := readCRLFLine(r)
		if err != nil {
			return respValue{}, err
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return respValue{}, fmt.Errorf("resp integer: %w", err)
		}
		return respValue{kind: respInt, n: n}, nil
	case '$':
		s, err := readCRLFLine(r)
		if err != nil {
			return respValue{}, err
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return respValue{}, fmt.Errorf("resp bulk length: %w", err)
		}
		if n < 0 {
			return respValue{kind: respNull}, nil
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return respValue{}, err
		}
		if err := discardCRLF(r); err != nil {
			return respValue{}, err
		}
		return respValue{kind: respBulk, str: string(buf)}, nil
	case '*':
		s, err := readCRLFLine(r)
		if err != nil {
			return respValue{}, err
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return respValue{}, fmt.Errorf("resp array length: %w", err)
		}
		if n < 0 {
			return respValue{kind: respNull}, nil
		}
		items := make([]respValue, 0, n)
		for i := 0; i < n; i++ {
			item, err := readRESP(r)
			if err != nil {
				return respValue{}, err
			}
			items = append(items, item)
		}
		return respValue{kind: respArray, items: items}, nil
	default:
		return respValue{}, fmt.Errorf("resp: unknown prefix %q", prefix)
	}
}

func readCRLFLine(r *bufio.Reader) (string, error) {
	s, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r"), nil
}

func discardCRLF(r *bufio.Reader) error {
	b := make([]byte, 2)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	if b[0] != '\r' || b[1] != '\n' {
		return fmt.Errorf("resp: expected CRLF")
	}
	return nil
}
