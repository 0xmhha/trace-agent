package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
	"github.com/0xmhha/trace-agent/internal/usage/service"
)

var _ service.ExtensionControl = (*extensionControl)(nil)

const (
	// installationKeySize is the only key length the Keychain loader accepts.
	installationKeySize = 32
	// usageEventsTokenLabel separates the extension credential from the
	// credentials the trace receivers derive from the same installation key.
	usageEventsTokenLabel = "usage-events-authorization/v1"
	maxTCPPort            = 65535
	// extensionCloseTimeout bounds draining the endpoint's requests in flight.
	extensionCloseTimeout = 3 * time.Second
)

var (
	errInstallationKeySize = errors.New("installation key must be 32 bytes")
	errExtensionShutDown   = errors.New("extension endpoint is shut down")
)

// ensureInstallationKey returns the installation key, creating and saving it
// first when no trace source has been attached yet. A new key reaches memory
// only after the vault accepted it; a credential derived from an unsaved key
// would stop matching after a restart.
func (c *collector) ensureInstallationKey(random io.Reader) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.config.Key) != 0 {
		return slices.Clone(c.config.Key), nil
	}
	key := make([]byte, installationKeySize)
	if _, err := io.ReadFull(random, key); err != nil {
		return nil, fmt.Errorf("generate installation key: %w", err)
	}
	updated := c.config
	updated.Key = key
	if err := c.vault.Save(updated); err != nil {
		return nil, fmt.Errorf("save installation key: %w", err)
	}
	c.config = updated
	return slices.Clone(key), nil
}

// usageEventsToken derives the bearer credential of the extension endpoint.
// It refuses a key of any other length than the one the Keychain stores.
func usageEventsToken(installationKey []byte) (string, error) {
	if len(installationKey) != installationKeySize {
		return "", errInstallationKeySize
	}
	h := hmac.New(sha256.New, installationKey)
	h.Write([]byte(usageEventsTokenLabel))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extensionControl is the one place that starts and stops the extension
// event endpoint. It implements service.ExtensionControl and is safe for
// concurrent use.
type extensionControl struct {
	sink   usage.ExtSink
	listen usageListenFunc
	key    func() ([]byte, error)

	mu       sync.Mutex
	receiver usageReceiver
	bearer   string
	lastPort int
	shutDown bool
}

// newExtensionControl returns a stopped control. lastPort is the port the
// endpoint was bound to in an earlier run, or zero.
func newExtensionControl(sink usage.ExtSink, listen usageListenFunc, key func() ([]byte, error), lastPort int) *extensionControl {
	if lastPort < 0 || lastPort > maxTCPPort {
		lastPort = 0
	}
	return &extensionControl{sink: sink, listen: listen, key: key, lastPort: lastPort}
}

// SetEnabled starts or stops the endpoint and returns the port it is bound
// to, or the last bound port when it is stopped. Starting a running endpoint
// and stopping a stopped one change nothing.
func (c *extensionControl) SetEnabled(ctx context.Context, enabled bool) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !enabled {
		c.stop(ctx)
		return c.lastPort, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if c.shutDown {
		return 0, errExtensionShutDown
	}
	if c.receiver != nil {
		return c.lastPort, nil
	}
	key, err := c.key()
	if err != nil {
		return 0, fmt.Errorf("installation key: %w", err)
	}
	bearer, err := usageEventsToken(key)
	if err != nil {
		return 0, err
	}
	receiver, err := c.listen(c.sink, c.lastPort, bearer)
	if err != nil && c.lastPort != 0 {
		// Senders were told the previous port, so it is tried first; when
		// another process holds it now, any free port is better than no
		// endpoint.
		receiver, err = c.listen(c.sink, 0, bearer)
	}
	if err != nil {
		return 0, fmt.Errorf("listen for extension events: %w", err)
	}
	c.receiver, c.bearer, c.lastPort = receiver, bearer, receiver.Port()
	return c.lastPort, nil
}

// Endpoint reports the live endpoint without its credential.
func (c *extensionControl) Endpoint() usage.ExtEndpoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.receiver == nil {
		return usage.ExtEndpoint{}
	}
	return usage.ExtEndpoint{Enabled: true, URL: c.receiver.URL()}
}

// credential returns the port and bearer of the running endpoint. The bearer
// must stay on the native side.
func (c *extensionControl) credential() (port int, bearer string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.receiver == nil {
		return 0, "", false
	}
	return c.lastPort, c.bearer, true
}

// shutdown stops the endpoint for good; later attempts to start it fail.
func (c *extensionControl) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shutDown = true
	c.stop(context.Background())
}

// stop closes the running endpoint. The caller holds c.mu. Stopping narrows
// what the process accepts, so it goes ahead even when the caller's context
// is already cancelled; draining gets its own bounded time instead.
func (c *extensionControl) stop(ctx context.Context) {
	if c.receiver == nil {
		return
	}
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), extensionCloseTimeout)
	defer cancel()
	// A close error means requests in flight did not drain in time; the
	// listener is released either way, and a stop the user asked for must
	// be recorded as stopped.
	_ = c.receiver.Close(closeCtx)
	c.receiver, c.bearer = nil, ""
}
