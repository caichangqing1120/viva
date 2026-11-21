//go:build linux

// Package fcitx speaks the Fcitx5 input-context protocol on the session bus.
// A Client is driven by a single event loop; it runs no goroutine.
package fcitx5

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vivaui/viva/internal/dbus"
)

const (
	service          = "org.fcitx.Fcitx5"
	portalService    = "org.freedesktop.portal.Fcitx"
	methodPath       = "/org/freedesktop/portal/inputmethod"
	methodInterface  = "org.fcitx.Fcitx.InputMethod1"
	contextInterface = "org.fcitx.Fcitx.InputContext1"
	busService       = "org.freedesktop.DBus"
	busPath          = "/org/freedesktop/DBus"
	requestTimeout   = 2 * time.Second
	queueLimit       = 128
	updateLimit      = 512
)

type Capability uint64

const (
	Preedit          Capability = 1 << 1
	FormattedPreedit Capability = 1 << 4
	SurroundingText  Capability = 1 << 6
	RelativeRect     Capability = 1 << 24
	ReportKeyRepeat  Capability = 1 << 38
	ClientSidePanel  Capability = 1 << 39
)

var (
	ErrServiceChanged = errors.New("fcitx: input method service owner changed")
	ErrTimeout        = errors.New("fcitx: daemon did not answer")
	ErrClosed         = errors.New("fcitx: client closed")
	ErrQueueFull      = errors.New("fcitx: input queue full")
	ErrFocus          = errors.New("fcitx: input does not own the queued focus")
)

type transport interface {
	Send(destination, path, iface, member, signature string, body []byte) (uint32, error)
	Receive() ([]*dbus.Message, error)
	FD() int
	Close()
}

type Key struct {
	Symbol, Code, State, Time uint32
	Release                   bool
	Tag                       uint64 // local identifier, not sent to the daemon
}

type Delivery uint8

const (
	// Unsent and Declined can fall back in the original editor. Unknown must
	// not be replayed: the daemon may already have run the key.
	Unsent Delivery = iota
	Declined
	Handled
	Unknown
)

type Result struct {
	Key      Key
	Delivery Delivery
	Events   []Event
}

// Update belongs to the editor token passed when its context took focus. A
// ready update has neither an Event nor a Result.
type Update struct {
	Owner  uint64
	Event  *Event
	Result *Result
}

type stage uint8

const (
	settingOut stage = iota + 1
	creating
	settingCaps
	settingIn
	destroying
	processing
	controlling
)

type action struct {
	owner       uint64
	caps        Capability
	suspend     bool
	key         *Key
	method, sig string
	body        []byte
}

type Client struct {
	conn               transport
	name, daemon       string
	program, display   string
	now                func() time.Time
	queue              []action
	updates            []Update
	queuedOwner, owner uint64
	path, outgoing     string
	ready              bool
	phase              stage
	serial             uint32
	deadline           time.Time
	current            action
	err                error
}

// Dial connects to an already running Fcitx service without activating one.
// Calls after Dial use Send and are completed through Dispatch.
func Dial(program, display string) (*Client, error) {
	conn, err := dbus.DialSession()
	if err != nil {
		return nil, err
	}
	for _, name := range []string{portalService, service} {
		var rule, serviceName dbus.Encoder
		rule.String("type='signal',sender='" + busService + "',interface='" + busService + "',member='NameOwnerChanged',arg0='" + name + "'")
		reply, err := conn.Call(busService, busPath, busService, "AddMatch", "s", rule.Bytes())
		if err != nil {
			conn.Close()
			return nil, err
		}
		reply.CloseFDs()
		serviceName.String(name)
		reply, err = conn.Call(busService, busPath, busService, "GetNameOwner", "s", serviceName.Bytes())
		if err != nil {
			var wireError *dbus.Error
			if errors.As(err, &wireError) && wireError.Name == busService+".Error.NameHasNoOwner" {
				continue
			}
			conn.Close()
			return nil, err
		}
		args := reply.Args()
		owner := args.String()
		reply.CloseFDs()
		if reply.Sender != busService || reply.Signature != "s" || args.Err() != nil || !args.Done() || !strings.HasPrefix(owner, ":") || len(owner) < 2 {
			conn.Close()
			return nil, errors.New("fcitx: invalid service owner")
		}
		return newClient(conn, name, owner, program, display, time.Now), nil
	}
	conn.Close()
	return nil, errors.New("fcitx: no running input method service")
}

func newClient(conn transport, name, owner, program, display string, now func() time.Time) *Client {
	return &Client{conn: conn, name: name, daemon: owner, program: program, display: display, now: now}
}

// ServiceOwner identifies the well-known service and the unique owner this
// client connected to, even after it has been closed.
func (c *Client) ServiceOwner() (name, owner string) { return c.name, c.daemon }

func (c *Client) FD() int {
	if c.err != nil {
		return -1
	}
	return c.conn.FD()
}

func (c *Client) Deadline() (time.Time, bool) { return c.deadline, c.serial != 0 }

// Expire closes a stalled connection. A new client must negotiate its own
// contexts once the daemon is available again.
func (c *Client) Expire() error {
	if c.err == nil && c.serial != 0 && !c.now().Before(c.deadline) {
		c.fail(ErrTimeout)
	}
	return c.err
}

func (c *Client) Close() { c.fail(ErrClosed) }

// Updates takes ownership of updates in wire order, freeing the output buffer
// for the next dispatch. Call it after Dispatch, Expire and state changes.
func (c *Client) Updates() []Update {
	updates := c.updates
	c.updates = nil
	return updates
}

// Focus requests a fresh context for this editor generation. Zero blurs the
// editor. Keeping the unfocused predecessor until the successor exists lets
// the daemon apply its own per-program mode policy.
func (c *Client) Focus(owner uint64, caps Capability) error {
	return c.focus(action{owner: owner, caps: caps})
}

// Suspend takes keyboard focus away without destroying the mode donor. Use
// it while a modal dialog owns input, then Focus or blur via Focus(0, 0).
func (c *Client) Suspend() error {
	return c.focus(action{suspend: true})
}

func (c *Client) focus(item action) error {
	if err := c.checkQueue(); err != nil {
		return err
	}
	c.queuedOwner = item.owner
	c.queue = append(c.queue, item)
	c.pump()
	return nil
}

// Key accepts a key for the last requested focus. Once accepted it produces
// exactly one Result even when dispatch or sending fails immediately.
func (c *Client) Key(owner uint64, key Key) error {
	if err := c.checkQueue(); err != nil {
		return err
	}
	if owner == 0 || owner != c.queuedOwner {
		return ErrFocus
	}
	c.queue = append(c.queue, action{owner: owner, key: &key})
	c.pump()
	return nil
}

func (c *Client) control(owner uint64, method, sig string, body []byte) error {
	if err := c.checkQueue(); err != nil {
		return err
	}
	if owner == 0 || owner != c.queuedOwner {
		return ErrFocus
	}
	c.queue = append(c.queue, action{owner: owner, method: method, sig: sig, body: body})
	c.pump()
	return nil
}

func (c *Client) checkQueue() error {
	if c.err != nil {
		return c.err
	}
	if len(c.queue) >= queueLimit || len(c.updates) >= updateLimit {
		return ErrQueueFull
	}
	return nil
}

func (c *Client) pump() {
	for c.err == nil && c.serial == 0 && len(c.queue) != 0 {
		item := c.queue[0]
		c.queue[0] = action{}
		c.queue = c.queue[1:]
		c.current = item
		if item.key != nil {
			if !c.ready || item.owner != c.owner {
				c.result(item, Unsent, nil)
				continue
			}
			var e dbus.Encoder
			e.Uint32(item.key.Symbol)
			e.Uint32(item.key.Code)
			e.Uint32(item.key.State)
			e.Bool(item.key.Release)
			e.Uint32(item.key.Time)
			c.send(processing, c.path, contextInterface, "ProcessKeyEventBatch", "uuubu", e.Bytes())
			return
		}
		if item.method != "" {
			if c.ready && c.owner == item.owner {
				c.send(controlling, c.path, contextInterface, item.method, item.sig, item.body)
				return
			}
			continue
		}
		c.ready = false
		c.owner = 0
		if c.path != "" {
			c.outgoing, c.path = c.path, ""
			c.send(settingOut, c.outgoing, contextInterface, "FocusOut", "", nil)
			return
		}
		if item.owner != 0 {
			c.create()
			return
		}
		if c.outgoing != "" && !item.suspend {
			c.destroy()
			return
		}
	}
}

func (c *Client) create() {
	var e dbus.Encoder
	mark := e.BeginArray(8)
	for _, option := range [][2]string{{"program", c.program}, {"display", c.display}} {
		e.Align(8)
		e.String(option[0])
		e.String(option[1])
	}
	e.EndArray(mark)
	c.send(creating, methodPath, methodInterface, "CreateInputContext", "a(ss)", e.Bytes())
}

func (c *Client) send(next stage, path, iface, method, signature string, body []byte) {
	c.phase = next
	serial, err := c.conn.Send(c.daemon, path, iface, method, signature, body)
	if err != nil {
		// A transport can fail after writing some or all of the message.
		c.fail(err)
		return
	}
	c.serial, c.deadline = serial, c.now().Add(requestTimeout)
}

func (c *Client) Dispatch() error {
	if c.err != nil {
		return c.err
	}
	messages, err := c.conn.Receive()
	if err != nil {
		c.fail(err)
		return c.err
	}
	for _, message := range messages {
		message.CloseFDs()
		if c.err != nil {
			continue
		}
		if message.Type == dbus.TypeSignal {
			c.signal(message)
		} else if c.serial != 0 && message.ReplySerial == c.serial {
			c.reply(message)
		}
	}
	return c.err
}

func (c *Client) signal(message *dbus.Message) {
	if message.Sender == busService && message.Path == busPath && message.Interface == busService &&
		message.Member == "NameOwnerChanged" && message.Signature == "sss" {
		d := message.Args()
		name, old, next := d.String(), d.String(), d.String()
		if d.Err() == nil && d.Done() && name == c.name && old == c.daemon && old != next {
			c.fail(ErrServiceChanged)
		}
		return
	}
	if !c.ready || message.Sender != c.daemon || message.Path != c.path || message.Interface != contextInterface {
		return
	}
	event, ok := decodeSignal(message)
	if !ok {
		return
	}
	if len(c.updates) >= updateLimit {
		c.fail(ErrQueueFull)
		return
	}
	c.updates = append(c.updates, Update{Owner: c.owner, Event: &event})
}

func (c *Client) reply(m *dbus.Message) {
	if m.Type == dbus.TypeError && (m.Sender == c.daemon || m.Sender == busService) {
		c.fail(fmt.Errorf("fcitx: %s", m.ErrorName))
		return
	}
	if m.Type != dbus.TypeReturn || m.Sender != c.daemon {
		return
	}
	state := c.phase
	if state != creating && state != processing && (m.Signature != "" || len(m.Body) != 0) {
		c.fail(errors.New("fcitx: unexpected reply body"))
		return
	}
	c.serial = 0
	switch state {
	case settingOut:
		if c.current.owner != 0 {
			c.create()
			return
		}
		if !c.current.suspend {
			c.destroy()
			return
		}
	case creating:
		path, ok := decodeContext(m)
		if !ok || path == c.outgoing {
			c.fail(errors.New("fcitx: invalid input context"))
			return
		}
		c.path = path
		var e dbus.Encoder
		e.Uint64(uint64(c.current.caps))
		c.send(settingCaps, path, contextInterface, "SetCapability", "t", e.Bytes())
		return
	case settingCaps:
		c.send(settingIn, c.path, contextInterface, "FocusIn", "", nil)
		return
	case settingIn:
		c.owner, c.ready = c.current.owner, true
		c.updates = append(c.updates, Update{Owner: c.owner})
		if c.outgoing != "" {
			c.destroy()
			return
		}
	case destroying:
		c.outgoing = ""
		if c.path == "" {
			c.owner = 0
		}
	case processing:
		result, ok := decodeBatch(m)
		if !ok {
			c.fail(errors.New("fcitx: invalid key reply"))
			return
		}
		c.result(c.current, result.Delivery, result.Events)
	}
	c.current = action{}
	c.phase = 0
	c.pump()
}

func (c *Client) destroy() {
	c.send(destroying, c.outgoing, contextInterface, "DestroyIC", "", nil)
}

func (c *Client) result(item action, delivery Delivery, events []Event) {
	c.updates = append(c.updates, Update{Owner: item.owner, Result: &Result{Key: *item.key, Delivery: delivery, Events: events}})
}

func (c *Client) fail(err error) {
	if c.err != nil {
		return
	}
	c.err = err
	c.conn.Close()
	if c.phase == processing {
		c.result(c.current, Unknown, nil)
	}
	for _, item := range c.queue {
		if item.key != nil {
			c.result(item, Unsent, nil)
		}
	}
	c.serial, c.ready, c.phase = 0, false, 0
	c.queue, c.current = nil, action{}
}
