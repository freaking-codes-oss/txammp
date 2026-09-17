// Package fastcgi implements the subset of the FastCGI wire protocol that
// txampp needs, with no external dependencies:
//
//   - a *client* used for PHP-FPM health checks (the "ping" endpoint), and
//   - a *responder* used by the txampp test tooling (hack/mockphp).
//
// Reference: "FastCGI 1.0 specification" (fastcgi.com, 1996).
package fastcgi

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Record types.
const (
	TypeBeginRequest = 1
	TypeAbortRequest = 2
	TypeEndRequest   = 3
	TypeParams       = 4
	TypeStdin        = 5
	TypeStdout       = 6
	TypeStderr       = 7
	TypeData         = 8
	TypeGetValues    = 9
	TypeGetValuesRes = 10
	TypeUnknownType  = 11
)

// Roles.
const (
	RoleResponder  = 1
	RoleAuthorizer = 2
	RoleFilter     = 3
)

// Protocol status codes for end requests.
const (
	StatusRequestComplete = 0
	StatusCantMpxConn     = 1
	StatusOverloaded      = 2
	StatusUnknownRole     = 3
)

// Header size on the wire.
const headerLen = 8

// maxContent is the maximum content length of a single record.
const maxContent = 65535

const (
	fcgiVersion = 1
	// flagKeepConn = 1 // txampp always closes; keep simple.
)

// record is a decoded FastCGI record.
type record struct {
	typ     byte
	reqID   uint16
	content []byte
}

// encodeHeader writes an 8 byte FastCGI record header.
func encodeHeader(dst []byte, typ byte, reqID uint16, contentLen, paddingLen int) {
	dst[0] = fcgiVersion
	dst[1] = typ
	binary.BigEndian.PutUint16(dst[2:], reqID)
	binary.BigEndian.PutUint16(dst[4:], uint16(contentLen))
	dst[6] = byte(paddingLen)
	dst[7] = 0
}

// readRecord reads one record from r.
func readRecord(r *bufio.Reader) (record, error) {
	var hdr [headerLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return record{}, err
	}
	if hdr[0] != fcgiVersion {
		return record{}, fmt.Errorf("fastcgi: bad protocol version %d", hdr[0])
	}
	rec := record{
		typ:   hdr[1],
		reqID: binary.BigEndian.Uint16(hdr[2:]),
	}
	clen := int(binary.BigEndian.Uint16(hdr[4:]))
	plen := int(hdr[6])
	if clen > 0 {
		rec.content = make([]byte, clen)
		if _, err := io.ReadFull(r, rec.content); err != nil {
			return record{}, err
		}
	}
	if plen > 0 {
		if _, err := io.CopyN(io.Discard, r, int64(plen)); err != nil {
			return record{}, err
		}
	}
	return rec, nil
}

// writeRecord writes one record with the given content, splitting into
// multiple records when needed (content is chunked to maxContent). An empty
// content still writes a single empty record (stream terminator).
func writeRecord(w io.Writer, typ byte, reqID uint16, content []byte) error {
	first := true
	for len(content) > 0 || first {
		n := len(content)
		if n > maxContent {
			n = maxContent
		}
		chunk := content[:n]
		content = content[n:]
		pad := (8 - n%8) % 8
		buf := make([]byte, headerLen+n+pad)
		encodeHeader(buf[:headerLen], typ, reqID, n, pad)
		copy(buf[headerLen:], chunk)
		if _, err := w.Write(buf); err != nil {
			return err
		}
		first = false
	}
	return nil
}

// encodeParams serializes FastCGI PARAMS name/value pairs.
func encodeParams(b []byte, params map[string]string) []byte {
	// Deterministic order not required by the spec; sort for tests.
	for _, k := range sortedKeys(params) {
		v := params[k]
		b = appendLen(b, len(k))
		b = appendLen(b, len(v))
		b = append(b, k...)
		b = append(b, v...)
	}
	return b
}

func appendLen(b []byte, n int) []byte {
	if n < 128 {
		return append(b, byte(n))
	}
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(n)|0x80000000)
	return append(b, l[:]...)
}

// decodeParams parses a PARAMS content block.
func decodeParams(content []byte) (map[string]string, error) {
	out := map[string]string{}
	for len(content) > 0 {
		klen, rest, err := readLen(content)
		if err != nil {
			return nil, err
		}
		vlen, rest, err := readLen(rest)
		if err != nil {
			return nil, err
		}
		if int(klen)+int(vlen) > len(rest) {
			return nil, errors.New("fastcgi: bad params encoding")
		}
		k := string(rest[:klen])
		v := string(rest[klen : klen+vlen])
		out[k] = v
		content = rest[klen+vlen:]
	}
	return out, nil
}

func readLen(b []byte) (int, []byte, error) {
	if len(b) == 0 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	if b[0] < 128 {
		return int(b[0]), b[1:], nil
	}
	if len(b) < 4 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	n := binary.BigEndian.Uint32(b[:4]) & 0x7fffffff
	return int(n), b[4:], nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
