// Package codec is the AES-256-GCM PayloadCodec behind the encrypting DataConverter, plus its keyring.
//
// PATTERN: Payload Encryption Codec — https://docs.temporal.io/payload-codec
// A DataConverter = a PayloadConverter (Go values <-> payloads; the default JSON/proto converter) + a chain
// of PayloadCodecs (payload bytes <-> payload bytes). This codec encrypts every payload client-side before
// it reaches the Temporal Service: workflow input/result, Activity inputs/results, Update arguments and
// results, Query results, failure details (through NewFailureConverter), and user-metadata summaries/details.
// The Temporal Service only ever stores ciphertext.
//
// NOT encrypted, because they are not payloads: Search Attributes, Workflow IDs, Activity IDs, Update
// names, type names, and failure messages. That is why none of them contain PII (account numbers are
// masked everywhere they could appear). PRODUCTION NOTE: to encrypt failure messages and stack traces too,
// set client.Options.FailureConverter to temporal.NewDefaultFailureConverter with EncodeCommonAttributes.
//
// PRODUCTION NOTE: keys come from a KMS/HSM with envelope encryption and are never committed. Rotation =
// add a key, make it active, keep old keys for decoding until every history that used them has aged past
// the namespace retention period.
package codec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/protobuf/proto"
)

const (
	// EncodingEncrypted marks a payload produced by this codec.
	EncodingEncrypted = "binary/encrypted"
	// MetadataKeyID names the key that encrypted the payload (any keyring key can decode it).
	MetadataKeyID = "encryption-key-id"
)

// Keyring is the JSON keyring file (config/keyring.json, mounted at CODEC_KEYRING_PATH).
type Keyring struct {
	ActiveKeyID string `json:"activeKeyId"`
	Keys        []Key  `json:"keys"`
}

// Key is one AES-256 key, base64-encoded.
type Key struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"createdAt"`
}

// ParseKeyring validates a keyring: every key is 32 bytes, ids are unique, and the active key exists.
func ParseKeyring(data []byte) (Keyring, error) {
	var k Keyring
	if err := json.Unmarshal(data, &k); err != nil {
		return Keyring{}, fmt.Errorf("keyring: %w", err)
	}
	if _, err := buildAEADs(k); err != nil {
		return Keyring{}, err
	}
	return k, nil
}

// Codec implements converter.PayloadCodec with the keyring's active key for Encode and any keyring key for
// Decode. The keyring can be swapped at runtime (see Watch) without restarting the process.
type Codec struct {
	mu     sync.RWMutex
	active string
	aeads  map[string]cipher.AEAD
}

var _ converter.PayloadCodec = (*Codec)(nil)

// New builds a codec from a keyring.
func New(k Keyring) (*Codec, error) {
	c := &Codec{}
	return c, c.Swap(k)
}

// Swap atomically replaces the keyring (used by hot reload). An invalid keyring is rejected and the current
// one stays in place.
func (c *Codec) Swap(k Keyring) error {
	aeads, err := buildAEADs(k)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.active, c.aeads = k.ActiveKeyID, aeads
	c.mu.Unlock()
	return nil
}

// ActiveKeyID is the key new payloads are encrypted with.
func (c *Codec) ActiveKeyID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active
}

// Encode encrypts each payload: the whole original Payload proto (metadata + data) is sealed with the active
// key and a fresh random 12-byte nonce; the key id is bound as GCM additional authenticated data, so a
// payload cannot be relabeled to claim a different key.
func (c *Codec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	c.mu.RLock()
	keyID, aead := c.active, c.aeads[c.active]
	c.mu.RUnlock()

	out := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		plain, err := proto.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("codec: marshal payload: %w", err)
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, fmt.Errorf("codec: nonce: %w", err)
		}
		out[i] = &commonpb.Payload{
			Metadata: map[string][]byte{
				converter.MetadataEncoding: []byte(EncodingEncrypted),
				MetadataKeyID:              []byte(keyID),
			},
			Data: aead.Seal(nonce, nonce, plain, []byte(keyID)), // nonce || ciphertext+tag
		}
	}
	return out, nil
}

// Decode reverses Encode. Payloads not produced by this codec pass through unchanged.
func (c *Codec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	out := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.GetMetadata()[converter.MetadataEncoding]) != EncodingEncrypted {
			out[i] = p
			continue
		}
		keyID := string(p.GetMetadata()[MetadataKeyID])
		c.mu.RLock()
		aead, ok := c.aeads[keyID]
		c.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("codec: unknown encryption key %q", keyID)
		}
		data := p.GetData()
		if len(data) < aead.NonceSize() {
			return nil, errors.New("codec: ciphertext too short")
		}
		plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(keyID))
		if err != nil {
			return nil, fmt.Errorf("codec: decrypt with key %q: %w", keyID, err)
		}
		var original commonpb.Payload
		if err := proto.Unmarshal(plain, &original); err != nil {
			return nil, fmt.Errorf("codec: unmarshal payload: %w", err)
		}
		out[i] = &original
	}
	return out, nil
}

// NewDataConverter is the encrypting DataConverter used by EVERY client and worker.
// PATTERN: Encrypting Data Converter — https://docs.temporal.io/dataconversion
func NewDataConverter(c *Codec) converter.DataConverter {
	return converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), c)
}

// NewFailureConverter serializes failure details (ApplicationError details, e.g. Core error bodies) with dc.
// WHY: when client.Options.FailureConverter is nil, the Go SDK uses a failure converter built on the DEFAULT
// DataConverter, so failure details would reach the Temporal Service in plaintext even with an encrypting dc.
// Every client and the replayer set this together with NewDataConverter.
func NewFailureConverter(dc converter.DataConverter) converter.FailureConverter {
	return temporal.NewDefaultFailureConverter(temporal.DefaultFailureConverterOptions{DataConverter: dc})
}

func buildAEADs(k Keyring) (map[string]cipher.AEAD, error) {
	if len(k.Keys) == 0 {
		return nil, errors.New("keyring: no keys")
	}
	aeads := make(map[string]cipher.AEAD, len(k.Keys))
	for _, key := range k.Keys {
		if key.ID == "" {
			return nil, errors.New("keyring: key without id")
		}
		if _, dup := aeads[key.ID]; dup {
			return nil, fmt.Errorf("keyring: duplicate key id %q", key.ID)
		}
		raw, err := base64.StdEncoding.DecodeString(key.Key)
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("keyring: key %q must be 32 bytes, base64-encoded", key.ID)
		}
		block, err := aes.NewCipher(raw)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block) // standard 12-byte nonce
		if err != nil {
			return nil, err
		}
		aeads[key.ID] = aead
	}
	if _, ok := aeads[k.ActiveKeyID]; !ok {
		return nil, fmt.Errorf("keyring: active key %q is not in the keyring", k.ActiveKeyID)
	}
	return aeads, nil
}
