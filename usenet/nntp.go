package usenet

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"net/url"

	"golang.org/x/sync/errgroup"
)

// https://datatracker.ietf.org/doc/html/rfc3977
// https://datatracker.ietf.org/doc/html/rfc4643

type Pool struct {
	URL   string
	Conns chan *Conn
}

type Conn struct {
	*textproto.Conn
}

type Msg struct {
	ID     string
	Offset int64
	Data   []byte
}

func NewPool(providerURL string, n int) (*Pool, error) {
	g, p := errgroup.Group{}, &Pool{
		URL:   providerURL,
		Conns: make(chan *Conn, n),
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

func (p *Pool) NewConn() (*Conn, error) {
	u, err := url.Parse(p.URL)
	if err != nil {
		return nil, err
	}
	host, user := u.Host, u.User.Username()
	pass, _ := u.User.Password()
	tc, err := tls.Dial("tcp", host, nil)
	if err != nil {
		return nil, err
	}
	c := &Conn{textproto.NewConn(tc)}
	if _, _, err := c.ReadCodeLine(200); err != nil {
		c.Close()
		return nil, fmt.Errorf("connect (expected 200): %w", err)
	}
	if err := c.Cmd(381, "authinfo user %s", user); err != nil {
		return nil, fmt.Errorf("authinfo user (expected 381): %w", err)
	} else if err := c.Cmd(281, "authinfo pass %s", pass); err != nil {
		return nil, fmt.Errorf("authinfo pass (expected 281): %w", err)
	}
	return c, nil
}

func (c *Conn) Cmd(code int, format string, args ...any) error {
	id, err := c.Conn.Cmd(format, args...)
	if err != nil {
		return io.EOF
	}
	c.StartResponse(id)
	defer c.EndResponse(id)
	_, _, err = c.ReadCodeLine(code)
	return err
}

func (p *Pool) Close() error {
	errs := []error{}
	for c := range p.Conns {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	close(p.Conns)
	return errors.Join(errs...)
}

func (c *Conn) Close() error {
	if err := c.Cmd(205, "quit"); err != nil {
		return err
	}
	return c.Conn.Close()
}

func (m *Msg) ReadAt(bs []byte, off int64) (int, error) {
	i := int(off - m.Offset)
	if i >= len(m.Data) {
		return 0, io.EOF
	}
	return copy(bs, m.Data[i:]), nil
}

func (p *Pool) GetMsg(id string) (*Msg, error) {
	c := <-p.Conns
	if c == nil {
		return nil, fmt.Errorf("pool is closed")
	}
	defer func() { p.Conns <- c }()
	if err := c.Cmd(222, "body <%s>", id); err == io.EOF {
		if c, err = p.NewConn(); err != nil {
			return nil, fmt.Errorf("failed to not re-open conn: %w", err)
		} else if err := c.Cmd(222, "body <%s>", id); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if bs, err := io.ReadAll(c.DotReader()); err != nil {
		return nil, err
	} else if offset, bs, err := Decode(bs); err != nil {
		return nil, err
	} else {
		return &Msg{id, int64(offset), bs}, nil
	}
}
