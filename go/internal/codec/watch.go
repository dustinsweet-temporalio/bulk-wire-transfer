// Keyring file loading and hot reload. The Control Center rotates keys by writing a new keyring file
// (temp file + rename); every process polls the file and swaps keys in place, so rotation needs no restart.
package codec

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// LoadFile reads, validates, and builds a codec from a keyring file.
func LoadFile(path string) (*Codec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	k, err := ParseKeyring(data)
	if err != nil {
		return nil, err
	}
	return New(k)
}

// Watch polls the keyring file every interval and swaps in a changed, VALID keyring. A malformed or
// half-written file never replaces a good keyring: it is logged and the last good one stays active.
// Returns when ctx is done.
func (c *Codec) Watch(ctx context.Context, path string, interval time.Duration, log *slog.Logger) {
	var lastMod time.Time
	var lastSize int64
	if fi, err := os.Stat(path); err == nil {
		lastMod, lastSize = fi.ModTime(), fi.Size()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fi, err := os.Stat(path)
		if err != nil {
			log.Warn("keyring: cannot stat file; keeping current keys", "path", path, "err", err)
			continue
		}
		if fi.ModTime().Equal(lastMod) && fi.Size() == lastSize {
			continue
		}
		lastMod, lastSize = fi.ModTime(), fi.Size()
		data, err := os.ReadFile(path)
		if err != nil {
			log.Warn("keyring: cannot read file; keeping current keys", "err", err)
			continue
		}
		k, err := ParseKeyring(data)
		if err == nil {
			err = c.Swap(k)
		}
		if err != nil {
			log.Error("keyring: invalid file ignored; keeping current keys", "err", err)
			continue
		}
		log.Info("keyring: reloaded", "activeKeyId", k.ActiveKeyID, "keys", len(k.Keys))
	}
}
