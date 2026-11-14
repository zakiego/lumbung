package s3proto

// Request bodies: verifying readers for a signed payload hash and for the
// flexible checksums (x-amz-checksum-*), and the aws-chunked decoder with
// chunk signatures and trailers. Each reader checks at the end of the body
// and returns a *Error instead of io.EOF when something doesn't match, so
// the object is never committed.

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var crc64NVME = crc64.MakeTable(0x9a6c9329ac4bc9b5)

// crc64Hash adapts hash/crc64 with the NVMe table.
type crc64Hash struct{ v uint64 }

func (h *crc64Hash) Write(p []byte) (int, error) {
	h.v = crc64.Update(h.v, crc64NVME, p)
	return len(p), nil
}
func (h *crc64Hash) Sum(b []byte) []byte {
	return append(b, byte(h.v>>56), byte(h.v>>48), byte(h.v>>40), byte(h.v>>32), byte(h.v>>24), byte(h.v>>16), byte(h.v>>8), byte(h.v))
}
func (h *crc64Hash) Reset()         { h.v = 0 }
func (h *crc64Hash) Size() int      { return 8 }
func (h *crc64Hash) BlockSize() int { return 1 }

// NewChecksum returns the hash for a checksum algorithm name as it appears
// in headers (crc32, crc32c, crc64nvme, sha1, sha256), or nil.
func NewChecksum(alg string) hash.Hash {
	switch strings.ToLower(alg) {
	case "crc32":
		return crc32.NewIEEE()
	case "crc32c":
		return crc32.New(crc32.MakeTable(crc32.Castagnoli))
	case "crc64nvme":
		return &crc64Hash{}
	case "sha1":
		return sha1.New()
	case "sha256":
		return sha256.New()
	}
	return nil
}

// ChecksumAlgorithms are the supported names, lowercase.
var ChecksumAlgorithms = []string{"crc32", "crc32c", "crc64nvme", "sha1", "sha256"}

// Checksum is one declared checksum: algorithm and base64 value. The value
// may come later, from a trailer.
type Checksum struct {
	Alg   string
	Value string
}

// HeaderChecksums returns the x-amz-checksum-* headers of a request.
func HeaderChecksums(h http.Header) ([]Checksum, *Error) {
	var out []Checksum
	for _, alg := range ChecksumAlgorithms {
		if v := h.Get("X-Amz-Checksum-" + alg); v != "" {
			out = append(out, Checksum{Alg: alg, Value: v})
		}
	}
	for name := range h {
		l := strings.ToLower(name)
		if strings.HasPrefix(l, "x-amz-checksum-") && l != "x-amz-checksum-type" && l != "x-amz-checksum-algorithm" && l != "x-amz-checksum-mode" {
			if NewChecksum(strings.TrimPrefix(l, "x-amz-checksum-")) == nil {
				return nil, Errf("InvalidRequest", http.StatusBadRequest, "unsupported checksum %s", l)
			}
		}
	}
	return out, nil
}

// hashReader hashes what passes through and runs check at the end.
type hashReader struct {
	r     io.Reader
	hs    []hash.Hash
	check func() *Error
	done  bool
	err   error
}

func (h *hashReader) Read(p []byte) (int, error) {
	if h.err != nil {
		return 0, h.err
	}
	n, err := h.r.Read(p)
	for _, x := range h.hs {
		x.Write(p[:n])
	}
	if errors.Is(err, io.EOF) && !h.done {
		h.done = true
		if e := h.check(); e != nil {
			h.err = e
			return n, e
		}
	}
	if err != nil {
		h.err = err
	}
	return n, err
}

// VerifySHA256 checks the body's SHA-256 against a signed payload hash.
func VerifySHA256(r io.Reader, want string) io.Reader {
	h := sha256.New()
	return &hashReader{r: r, hs: []hash.Hash{h}, check: func() *Error {
		if hex.EncodeToString(h.Sum(nil)) != want {
			return Errf("XAmzContentSHA256Mismatch", http.StatusBadRequest, "the provided x-amz-content-sha256 header does not match what was computed")
		}
		return nil
	}}
}

// VerifyChecksums checks declared checksums at the end of the body. value
// returns the expected value of each, read at the end so trailers can
// supply it.
func VerifyChecksums(r io.Reader, sums []Checksum, value func(alg string) string) io.Reader {
	if len(sums) == 0 {
		return r
	}
	hs := make([]hash.Hash, len(sums))
	for i, s := range sums {
		hs[i] = NewChecksum(s.Alg)
	}
	return &hashReader{r: r, hs: hs, check: func() *Error {
		for i, s := range sums {
			want := value(s.Alg)
			if want == "" {
				return Errf("InvalidRequest", http.StatusBadRequest, "the x-amz-checksum-%s value is missing", s.Alg)
			}
			got := base64.StdEncoding.EncodeToString(hs[i].Sum(nil))
			if got != want {
				return Errf("BadDigest", http.StatusBadRequest, "the %s you specified did not match the calculated checksum", strings.ToUpper(s.Alg))
			}
		}
		return nil
	}}
}

// ChunkSigner continues a signature over the chunks of a streaming upload.
type ChunkSigner struct {
	Key  []byte
	Time time.Time
	Scope
	Prev string // the previous signature, starting with the seed
}

func (c *ChunkSigner) next(chunkSHA string) string {
	sts := "AWS4-HMAC-SHA256-PAYLOAD\n" + c.Time.UTC().Format(TimeFormat) + "\n" + c.Scope.String() + "\n" + c.Prev + "\n" + EmptySHA256 + "\n" + chunkSHA
	c.Prev = Sign(c.Key, sts)
	return c.Prev
}

func (c *ChunkSigner) trailer(canonical string) string {
	sts := "AWS4-HMAC-SHA256-TRAILER\n" + c.Time.UTC().Format(TimeFormat) + "\n" + c.Scope.String() + "\n" + c.Prev + "\n" + sha256Hex(canonical)
	return Sign(c.Key, sts)
}

// ChunkedReader decodes an aws-chunked body. With a signer, every chunk's
// signature is checked (and a trailer signature, with trailers). Trailers
// declared in x-amz-trailer are collected and available from Trailer once
// the body has ended.
type ChunkedReader struct {
	br       *bufio.Reader
	signer   *ChunkSigner
	trailers map[string]bool // declared, lowercase
	got      map[string]string
	left     int64 // bytes left in the current chunk
	chunk    hash.Hash
	sig      string // the current chunk's declared signature
	done     bool
	err      error
}

// MaxChunkLine bounds a chunk header or trailer line.
const MaxChunkLine = 4096

// NewChunkedReader decodes body. trailerNames is the x-amz-trailer header.
func NewChunkedReader(body io.Reader, signer *ChunkSigner, trailerNames string) *ChunkedReader {
	c := &ChunkedReader{br: bufio.NewReaderSize(body, 64<<10), signer: signer, trailers: map[string]bool{}, got: map[string]string{}}
	for _, t := range strings.Split(trailerNames, ",") {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			c.trailers[t] = true
		}
	}
	return c
}

// Trailer returns a trailer's value once the body has ended.
func (c *ChunkedReader) Trailer(name string) string { return c.got[strings.ToLower(name)] }

func chunkErr(format string, args ...any) *Error {
	return Errf("IncompleteBody", http.StatusBadRequest, "the aws-chunked body is malformed: "+format, args...)
}

func (c *ChunkedReader) line() (string, *Error) {
	var b []byte
	for {
		part, isPrefix, err := c.br.ReadLine()
		if err != nil {
			return "", chunkErr("it ended early")
		}
		b = append(b, part...)
		if len(b) > MaxChunkLine {
			return "", chunkErr("a line is too long")
		}
		if !isPrefix {
			return string(b), nil
		}
	}
}

func (c *ChunkedReader) fail(e *Error) (int, error) {
	c.err = e
	return 0, e
}

func (c *ChunkedReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.done {
		return 0, io.EOF
	}
	if c.left == 0 {
		if err := c.startChunk(); err != nil {
			return c.fail(err)
		}
		if c.done {
			return 0, io.EOF
		}
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.br.Read(p)
	if n > 0 {
		c.left -= int64(n)
		if c.chunk != nil {
			c.chunk.Write(p[:n])
		}
	}
	if err != nil && n == 0 {
		return c.fail(chunkErr("it ended inside a chunk"))
	}
	if c.left == 0 {
		if e := c.endChunk(); e != nil {
			c.err = e
			return n, e
		}
	}
	return n, nil
}

// startChunk reads a chunk header; a zero-size chunk ends the body.
func (c *ChunkedReader) startChunk() *Error {
	line, err := c.line()
	if err != nil {
		return err
	}
	sizeHex, ext, _ := strings.Cut(line, ";")
	size, perr := strconv.ParseInt(strings.TrimSpace(sizeHex), 16, 64)
	if perr != nil || size < 0 || size > 1<<40 {
		return chunkErr("bad chunk size %q", sizeHex)
	}
	c.sig = ""
	if c.signer != nil {
		k, v, ok := strings.Cut(strings.TrimSpace(ext), "=")
		if !ok || k != "chunk-signature" || len(v) != 64 {
			return chunkErr("a chunk has no chunk-signature")
		}
		c.sig = v
		c.chunk = sha256.New()
	}
	if size == 0 {
		if c.signer != nil {
			if e := c.checkChunk(); e != nil {
				return e
			}
		}
		if e := c.readTrailers(); e != nil {
			return e
		}
		c.done = true
		return nil
	}
	c.left = size
	return nil
}

func (c *ChunkedReader) checkChunk() *Error {
	want := c.signer.next(hex.EncodeToString(c.chunk.Sum(nil)))
	if !hmac.Equal([]byte(want), []byte(c.sig)) {
		return Errf("SignatureDoesNotMatch", http.StatusForbidden, "a chunk signature doesn't match")
	}
	return nil
}

// endChunk checks a finished chunk's signature and reads its CRLF.
func (c *ChunkedReader) endChunk() *Error {
	if c.signer != nil {
		if e := c.checkChunk(); e != nil {
			return e
		}
	}
	line, err := c.line()
	if err != nil {
		return err
	}
	if line != "" {
		return chunkErr("a chunk is longer than its declared size")
	}
	return nil
}

// readTrailers reads trailer lines up to the empty line. Only declared
// trailers are accepted; a signed body's trailers end with
// x-amz-trailer-signature.
func (c *ChunkedReader) readTrailers() *Error {
	var canonical strings.Builder
	trailerSig := ""
	for {
		line, err := c.line()
		if err != nil {
			if len(c.trailers) == 0 && len(c.got) == 0 {
				return nil // some clients end right after the last chunk
			}
			return err
		}
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return chunkErr("bad trailer line")
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		if k == "x-amz-trailer-signature" {
			trailerSig = v
			continue
		}
		if !c.trailers[k] {
			return Errf("InvalidRequest", http.StatusBadRequest, "trailer %s wasn't declared in x-amz-trailer", k)
		}
		c.got[k] = v
		canonical.WriteString(k + ":" + v + "\n")
	}
	for t := range c.trailers {
		if _, ok := c.got[t]; !ok {
			return Errf("IncompleteBody", http.StatusBadRequest, "the declared trailer %s is missing", t)
		}
	}
	if c.signer != nil && len(c.trailers) > 0 {
		if !hmac.Equal([]byte(c.signer.trailer(canonical.String())), []byte(trailerSig)) {
			return Errf("SignatureDoesNotMatch", http.StatusForbidden, "the trailer signature doesn't match")
		}
	}
	return nil
}
