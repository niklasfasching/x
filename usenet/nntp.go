package usenet

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"net/url"
	"syscall"

	"golang.org/x/sync/errgroup"
)

// https://datatracker.ietf.org/doc/html/rfc3977
// https://datatracker.ietf.org/doc/html/rfc4643

type Pool struct {
	URL   string
	Conns chan *Conn
	context.Context
	Close func()
}

type Conn struct {
	*textproto.Conn
}

type Msg struct {
	ID     string
	Offset int64
	Data   []byte
}

type Err struct{ error }

func NewPool(ctx context.Context, providerURL string, n int) (*Pool, error) {
	ctx, cancel := context.WithCancel(ctx)
	g, p := errgroup.Group{}, &Pool{
		URL:     providerURL,
		Conns:   make(chan *Conn, n),
		Context: ctx,
		Close:   cancel,
	}
	for i := 0; i < n; i++ {
		g.Go(func() error {
			c, err := p.NewConn()
			if err != nil {
				return err
			}
			p.Conns <- c
			return nil
		})
	}
	return p, g.Wait()
}

func (p *Pool) NewConn() (c *Conn, err error) {
	u, err := url.Parse(p.URL)
	if err != nil {
		return nil, err
	}
	host, user := u.Host, u.User.Username()
	pass, _ := u.User.Password()
	tc, err := new(tls.Dialer).DialContext(p, "tcp", host)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()
	c = &Conn{textproto.NewConn(tc)}
	if _, _, err := c.ReadCodeLine(200); err != nil {
		return nil, fmt.Errorf("connect (expected 200): %w", err)
	} else if err := c.Cmd(381, "authinfo user %s", user); err != nil {
		return nil, fmt.Errorf("authinfo user (expected 381): %w", err)
	} else if err := c.Cmd(281, "authinfo pass %s", pass); err != nil {
		return nil, fmt.Errorf("authinfo pass (expected 281): %w", err)
	}
	return c, nil
}

func (c *Conn) Cmd(code int, format string, args ...any) error {
	id, err := c.Conn.Cmd(format, args...)
	if err != nil {
		return err
	}
	c.StartResponse(id)
	defer c.EndResponse(id)
	_, _, err = c.ReadCodeLine(code)
	return err
}

func (m *Msg) ReadAt(bs []byte, off int64) (int, error) {
	i := int(off - m.Offset)
	if i >= len(m.Data) {
		return 0, io.EOF
	}
	return copy(bs, m.Data[i:]), nil
}

func (m *Msg) Bytes(off int64, l int) []byte {
	relOff := max(0, min(int(off-m.Offset), len(m.Data)))
	relLen := max(relOff, min(int(off-m.Offset)+l, len(m.Data)))
	return m.Data[relOff:relLen]
}

func (p *Pool) StatMsg(ctx context.Context, id string) error {
	return p.WithConn(ctx, func(c *Conn) error {
		if err := c.Cmd(223, "stat <%s>", id); err != nil {
			return Err{fmt.Errorf("stat <%s>: %w", id, err)}
		}
		return nil
	})
}

func (p *Pool) GetMsg(ctx context.Context, id string) (m *Msg, err error) {
	return m, p.WithConn(ctx, func(c *Conn) error {
		if err := c.Cmd(222, "body <%s>", id); err != nil {
			return Err{fmt.Errorf("body <%s>: %w", id, err)}
		} else if bs, err := io.ReadAll(c.DotReader()); err != nil {
			return fmt.Errorf("dot reader: %w", err)
		} else if offset, bs, err := Decode(bs); err != nil {
			return Err{fmt.Errorf("yenc: %w", err)}
		} else {
			m = &Msg{id, int64(offset), bs}
			return nil
		}
	})
}

func (p *Pool) WithConn(ctx context.Context, f func(*Conn) error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c := <-p.Conns:
		if c == nil {
			return fmt.Errorf("pool is closed")
		}
		defer func() { p.Conns <- c }()
		if err := f(c); errors.Is(err, syscall.EPIPE) || errors.Is(err, io.EOF) {
			if c, err = p.NewConn(); err != nil {
				return fmt.Errorf("failed to re-open conn: %w", err)
			}
			return f(c)
		} else {
			return err
		}
	}
}

func (e Err) Error() string { return e.error.Error() }

func (e Err) Unwrap() error { return e.error }
