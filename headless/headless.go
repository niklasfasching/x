package headless

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"time"
)

type H struct {
	cmd       *exec.Cmd
	pipe      pipe
	nextID    int
	pending   map[int]chan message
	sessions  map[string]*Session
	UserAgent string
	sync.Mutex
}

type Params map[string]any
type Response struct {
	Status        int
	Url, MimeType string
	Headers       map[string]string
}

type pipe struct {
	r *os.File
	w *os.File
	*bufio.Reader
}

type message struct {
	ID        int
	SessionID string
	Method    string
	Params    json.RawMessage
	Result    json.RawMessage
	Error     json.RawMessage
}

var Executable = "chromium-browser"

var debugPrefix = os.Getenv("DEBUG_PREFIX")
var debug = os.Getenv("DEBUG") != ""
var defaultBrowserArgs = map[string]any{
	"--remote-debugging-pipe": true,
	"--temp-profile":          true,
	"--headless":              true,
	// TODO: dynamic (https://intoli.com/blog/not-possible-to-block-chrome-headless/chrome-headless-test.html)
	"--user-agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"--disable-component-extensions-with-background-pages": true,
}

func init() {
	if e := os.Getenv("HEADLESS_EXECUTABLE"); e != "" {
		Executable = e
	}
}

func Start(args map[string]any) (*H, error) {
	m, mergedArgs := map[string]any{}, []string{}
	for k, v := range defaultBrowserArgs {
		m[k] = v
	}
	for k, v := range args {
		m[k] = v
	}
	for a, v := range m {
		if enable, isBool := v.(bool); isBool && enable {
			mergedArgs = append(mergedArgs, a)
		} else if v, isString := v.(string); isString {
			mergedArgs = append(mergedArgs, fmt.Sprintf("%s=%s", a, v))
		}
	}
	ir, iw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	or, ow, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(Executable, mergedArgs...)
	cmd.ExtraFiles = append(cmd.ExtraFiles, ir, ow)
	if debug {
		cmd.Stderr = os.Stderr
	}
	h := &H{
		pipe:     pipe{or, iw, bufio.NewReader(or)},
		cmd:      cmd,
		pending:  map[int]chan message{},
		sessions: map[string]*Session{},
	}
	if err := h.cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		if err := h.loop(); err != nil {
			panic(err)
		}
	}()
	return h, nil
}

func (h *H) Stop() error {
	h.Lock()
	cmd, pipe := h.cmd, h.pipe
	h.cmd = nil
	h.Unlock()
	if cmd == nil {
		return nil
	}
	if err := pipe.r.Close(); err != nil {
		return err
	} else if err := pipe.w.Close(); err != nil {
		return err
	} else if err := cmd.Process.Kill(); err != nil {
		return err
	}
	_, err := cmd.Process.Wait()
	return err
}

func (h *H) Open(url string, f func(*Session) error) (*Session, error) {
	cr := struct{ TargetId string }{}
	if err := h.Exec("Target.createTarget", Params{"url": url}, &cr); err != nil {
		return nil, err
	}
	ar := struct{ SessionId string }{}
	if err := h.Exec("Target.attachToTarget", Params{"targetId": cr.TargetId, "flatten": true}, &ar); err != nil {
		return nil, err
	}
	s := &Session{
		id:        ar.SessionId,
		targetID:  cr.TargetId,
		contextID: 1,
		h:         h,
		handlers:  map[string][]*Handler{},
		bindings:  map[string]reflect.Value{},
		Err:       make(chan error),
	}
	h.Lock()
	h.sessions[ar.SessionId] = s
	h.Unlock()
	c := make(chan error)
	if f == nil {
		close(c)
	} else {
		go func() { c <- f(s) }()
	}
	return s, errors.Join(s.Init(), <-c)
}

func (s *Session) Init() error {
	for _, domain := range []string{"Page", "Runtime"} {
		if err := s.Exec(domain+".enable", nil, nil); err != nil {
			return err
		}
	}
	s.Handle("Runtime.bindingCalled", s.onBindingCalled)
	s.Handle("Runtime.exceptionThrown", func(m json.RawMessage) { go func() { s.Err <- fmt.Errorf(FormatException(m)) }() })
	type ExecutionContextCreated struct {
		Context struct {
			Id      int
			AuxData struct {
				FrameId   string
				IsDefault bool
			}
		}
	}
	s.Handle("Runtime.executionContextCreated", func(p ExecutionContextCreated) {
		if p.Context.AuxData.FrameId == s.targetID && p.Context.AuxData.IsDefault {
			s.contextID = p.Context.Id
		}
	})
	return nil
}

func (h *H) Capture(ctx context.Context, url string) (*Response, string, error) {
	s, err := h.Open(url, nil)
	if err != nil {
		return nil, "", err
	}
	defer s.Close()
	if err := s.Exec("Page.setLifecycleEventsEnabled", Params{"enabled": true}, nil); err != nil {
		return nil, "", err
	}
	if err := s.Await(ctx, "Page.loadEventFired"); err != nil {
		return nil, "", err
	}
	interactiveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	s.Handle("Page.lifecycleEvent", func(v struct{ Name string }) {
		if v.Name == "InteractiveTime" {
			cancel()
		}
	})
	<-interactiveCtx.Done()
	r := struct{ Data string }{}
	// https://issues.chromium.org/issues/40495614
	s.Eval(`
      await Promise.all([...document.querySelectorAll("img[loading=lazy]")].map(img => {
        img.loading = "eager";
        if (img.complete) return;
        return new Promise((res, rej) => { img.onload = res, img.onerror = rej });
      }));
      document.querySelectorAll("script[type*='ld+json']").forEach(el => {
        document.head.append(Object.assign(document.createElement("meta"), {
          name: el.type, content: el.innerText,
        }));
    })`, nil)
	err = s.Exec("Page.captureSnapshot", nil, &r)
	return &s.Response, r.Data, err
}

func (h *H) Close(s *Session) error {
	h.Lock()
	delete(h.sessions, s.id)
	h.Unlock()
	r := struct{ Success bool }{}
	err := h.Exec("Target.closeTarget", Params{"targetId": s.targetID}, &r)
	if err != nil {
		return err
	} else if !r.Success {
		return fmt.Errorf("error closing target: browser says no success")
	}
	return nil
}

func (h *H) Exec(method string, params, v interface{}) error {
	return h.exec("", method, params, v)
}

func (h *H) exec(sessionID, method string, params, v interface{}) error {
	h.Lock()
	id, c := h.nextID, make(chan message, 1)
	h.nextID++
	h.pending[id] = c
	h.Unlock()
	m := map[string]interface{}{
		"method":    method,
		"params":    params,
		"id":        id,
		"sessionId": sessionID,
	}
	bs, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if debug {
		log.Println("->", string(bs))
	} else if debugPrefix != "" && strings.HasPrefix(method, debugPrefix) {
		log.Println("->", string(bs))
	}
	if err := h.send(bs); err != nil {
		return err
	}
	r := <-c
	if r.Error != nil {
		e := map[string]interface{}{}
		if err := json.Unmarshal(r.Error, &e); err != nil {
			return fmt.Errorf("%s", string(r.Error))
		}
		return fmt.Errorf("%v: %v (%v)", e["code"], e["message"], e["data"])
	}
	e := struct {
		Description      string
		ExceptionDetails struct {
			LineNumber, ColumnNumber int
			Exception                struct {
				Description string
			}
		}
	}{}
	if err := json.Unmarshal(r.Result, &e); err == nil && e.ExceptionDetails.Exception.Description != "" {
		d := e.ExceptionDetails
		return fmt.Errorf("%d:%d: %s", d.LineNumber, d.ColumnNumber, d.Exception.Description)
	}
	if v == nil {
		return nil
	} else if err := json.Unmarshal(r.Result, v); err != nil {
		return fmt.Errorf("could not unmarshal '%s' into %T", string(r.Result), v)
	}
	return nil
}

func (h *H) send(bs []byte) error {
	h.Lock()
	defer h.Unlock()
	_, err := h.pipe.w.Write(append(bs, 0))
	return err
}

func (h *H) loop() error {
	for {
		bs, err := h.pipe.ReadBytes(0)
		if err != nil {
			h.Lock()
			cmd := h.cmd
			h.Unlock()
			if cmd == nil {
				return nil
			}
			return fmt.Errorf("could not read from pipe: %s", err)
		}
		if len(bs) == 0 {
			continue
		}
		m, bs := message{}, bs[:len(bs)-1]
		if err := json.Unmarshal(bs, &m); err != nil {
			return fmt.Errorf("bad message: %s: '%s'", err, string(bs))
		}
		if debug {
			log.Println("<-", string(bs))
		} else if debugPrefix != "" && strings.HasPrefix(m.Method, debugPrefix) {
			log.Println("<-", string(bs))
		}
		if m.Method != "" {
			h.Lock()
			s := h.sessions[m.SessionID]
			h.Unlock()
			if s == nil {
				continue
			}
			s.Lock()
			hs := s.handlers[m.Method]
			s.Unlock()
			for _, h := range hs {
				select {
				case h.C <- m:
				default:
					log.Printf("could not enqueue %s %s", m.Method, string(m.Params))
				}
			}
		} else {
			h.Lock()
			c, ok := h.pending[m.ID]
			delete(h.pending, m.ID)
			h.Unlock()
			if ok {
				c <- m
			}
		}
	}
}
