package tls

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"sync"

	"proto.zip/studio/orbital/pkg/v8"
)

// A session crosses to JavaScript as one opaque buffer: a version byte, the ticket's length
// and the ticket, then crypto/tls's encoding of the session state. It is not the DER that
// OpenSSL writes, but scripts only hand it back to tls.connect.
const sessionVersion = 1

func encodeSession(ticket, state []byte) []byte {
	out := make([]byte, 0, 5+len(ticket)+len(state))
	out = append(out, sessionVersion)
	out = binary.BigEndian.AppendUint32(out, uint32(len(ticket)))
	out = append(out, ticket...)
	return append(out, state...)
}

var errBadSession = errors.New("tls: unusable session")

func decodeSession(data []byte) (*tls.ClientSessionState, error) {
	if len(data) < 5 || data[0] != sessionVersion {
		return nil, errBadSession
	}
	n := binary.BigEndian.Uint32(data[1:5])
	if uint64(n) > uint64(len(data)-5) {
		return nil, errBadSession
	}
	ticket := data[5 : 5+n]
	encoded := data[5+n:]
	// Only a client's session is usable here. crypto/tls reads a server's too, and resuming
	// from one would fail inside the handshake, so its type byte is checked first.
	const clientSession = 2
	if len(encoded) < 3 || encoded[2] != clientSession {
		return nil, errBadSession
	}
	state, err := tls.ParseSessionState(encoded)
	if err != nil {
		return nil, errBadSession
	}
	return tls.NewResumptionState(ticket, state)
}

// clientSessions is a connection's session cache. It offers the session the script passed,
// if any, and reports each new session to the script.
type clientSessions struct {
	mu    sync.Mutex
	offer *tls.ClientSessionState
	next  tls.ClientSessionCache
	onNew func(session, ticket []byte)
}

func (c *clientSessions) Get(key string) (*tls.ClientSessionState, bool) {
	c.mu.Lock()
	offer := c.offer
	c.mu.Unlock()
	if offer != nil {
		return offer, true
	}
	if c.next != nil {
		return c.next.Get(key)
	}
	return nil, false
}

func (c *clientSessions) Put(key string, cs *tls.ClientSessionState) {
	if c.next != nil {
		c.next.Put(key, cs)
	}
	if cs == nil {
		return
	}
	ticket, state, err := cs.ResumptionState()
	if err != nil || state == nil {
		return
	}
	encoded, err := state.Bytes()
	if err != nil {
		return
	}
	if c.onNew != nil {
		c.onNew(encodeSession(ticket, encoded), ticket)
	}
}

// useSessions installs the cache on a client's configuration. Sessions pass between the
// connection and the script only when the script's options are in charge of the
// configuration: with the embedder's, resumption comes from the embedder's own cache, if it
// has one.
func (t *TLS) useSessions(cfg *tls.Config, embedder bool, session string, onSession *v8.Value) {
	cache := &clientSessions{next: cfg.ClientSessionCache}
	if session != "" && !embedder {
		if data, err := base64.StdEncoding.DecodeString(session); err == nil {
			if state, err := decodeSession(data); err == nil {
				cache.offer = state
			}
		}
	}
	// Sessions made under the embedder's configuration stay in its cache: they can carry
	// the embedder's client identity, so the script never sees them.
	if onSession != nil && onSession.IsFunction() && !embedder {
		cache.onNew = func(session, ticket []byte) {
			t.deliver(func(ctx *v8.Context) {
				t.rt.MakeCallback(onSession, ctx.Undefined(),
					jsString(ctx, base64.StdEncoding.EncodeToString(session)),
					jsString(ctx, base64.StdEncoding.EncodeToString(ticket)))
			})
		}
	}
	cfg.ClientSessionCache = cache
}

// ticketKey turns Node's 48 bytes of ticket keys into the key crypto/tls takes.
func ticketKey(keys []byte) [32]byte {
	return sha256.Sum256(keys)
}

// newTicketKeys() returns 48 random bytes, as base64, for a new server's ticket keys.
func (t *TLS) newTicketKeysFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	keys := make([]byte, 48)
	if _, err := rand.Read(keys); err != nil {
		return info.Context().Throw("tls: no randomness for ticket keys")
	}
	return jsString(info.Context(), base64.StdEncoding.EncodeToString(keys))
}
