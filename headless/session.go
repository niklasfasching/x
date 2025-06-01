package headless

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"
)

type Session struct {
	id                     string
	targetID               string
	contextID, uaContextID int
	h                      *H
	handlers               map[string][]reflect.Value
	bindings               map[string]reflect.Value
	Err                    chan error
	sync.Mutex
	Response
}

func (s *Session) Handle(method string, f any) {
	fv := reflect.ValueOf(f)
	if t := fv.Type(); t.NumIn() != 1 || t.NumOut() != 0 {
		panic(fmt.Sprintf("handler func must be of type func(T)"))
	}
	s.Lock()
	s.handlers[method] = append(s.handlers[method], fv)
	s.Unlock()
}

func (s *Session) Await(method string, timeout time.Duration) error {
	if timeout < 0 {
		timeout = math.MaxInt64
	}
	done := make(chan struct{})
	s.Handle(method, func(m any) { done <- struct{}{} })
	select {
	case <-done:
		return nil
	case <-time.NewTimer(timeout).C:
		return fmt.Errorf("%s: timeout (%s)", method, timeout)
	}
}

func (s *Session) Exec(method string, params, v any) error {
	return s.h.exec(s.id, method, params, v)
}

func (s *Session) Visit(url string) error {
	return s.Exec("Page.navigate", Params{"url": url}, nil)
}

func (s *Session) Open(html string) error {
	return s.Exec("Page.setDocumentContent", Params{"html": html, "frameId": s.targetID}, nil)
}

func (s *Session) Eval(js string, v any) error {
	return s.eval(js, s.contextID, v)
}

func (s *Session) eval(js string, id int, v any) error {
	r, params := struct {
		Result struct{ Value json.RawMessage }
	}{}, Params{"expression": js, "returnByValue": true, "replMode": true, "awaitPromise": true, "contextId": id}
	if err := s.Exec("Runtime.evaluate", params, &r); err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(r.Result.Value, v)
}

func (s *Session) EvalUA(js string, v any) error {
	if s.uaContextID == 0 {
		r := struct{ ExecutionContextId int }{}
		params := Params{"frameId": s.targetID, "worldName": s.targetID, "grantUniveralAccess": true}
		if err := s.Exec("Page.createIsolatedWorld", params, &r); err != nil {
			return err
		}
		s.uaContextID = r.ExecutionContextId
	}
	return s.eval(js, s.uaContextID, v)
}

func (s *Session) Close() error {
	return s.h.Close(s)
}

func (s *Session) Bind(name string, f any) {
	if err := s.bind(name, f); err != nil {
		panic(err)
	}
}

func (s *Session) bind(name string, f any) error {
	fv := reflect.ValueOf(f)
	ft := fv.Type()
	s.Lock()
	s.bindings[name] = fv
	s.Unlock()
	if err := s.Exec("Runtime.addBinding", Params{"name": name}, nil); err != nil {
		return err
	}
	js := fmt.Sprintf(`(() => {
      const binding = window["%[1]s"];
      window.%[1]s = (...args) => new Promise((resolve, reject) => {
        const id = String(window.%[1]s.nextID++);
        window.%[1]s.pending[id] = {resolve, reject};
        binding(JSON.stringify({id, args}));
      });
      Object.assign(window.%[1]s, {pending: {}, nextID: 0});
    })()`, name)
	if isVoid := ft.NumOut() == 0; isVoid {
		js = fmt.Sprintf(`(() => {
          const binding = window["%[1]s"];
          window.%[1]s = (...args) => binding(JSON.stringify({args}));
        })()`, name)
	}
	if err := s.Exec("Page.addScriptToEvaluateOnNewDocument", Params{"source": js}, nil); err != nil {
		return err
	}
	return s.Eval(js, nil)
}

func (s *Session) onBindingCalled(m struct{ Name, Payload string }) {
	s.Lock()
	fv, ok := s.bindings[m.Name]
	s.Unlock()
	if !ok {
		return
	}
	p := struct {
		ID   string
		Args []json.RawMessage
	}{}
	if err := json.Unmarshal([]byte(m.Payload), &p); err != nil {
		panic(err)
	}
	isVoid, isErr, arg := fv.Type().NumOut() == 0, "false", "null"
	if v, err := callBoundFunc(fv, p.Args); isVoid && err == nil {
		return
	} else if isVoid && err != nil {
		s.Err <- fmt.Errorf("%s %s - %s", m.Name, m.Payload, err)
		return
	} else if err != nil {
		isErr, arg = "true", fmt.Sprintf(`new Error("%s")`, err.Error())
	} else if vbs, err := json.Marshal(v); err != nil {
		isErr, arg = "true", fmt.Sprintf(`new Error("marshal: %s")`, err.Error())
	} else {
		isErr, arg = "false", string(vbs)
	}
	js := fmt.Sprintf(`(() => {
      const id = "%[2]s", isErr = %[3]s, arg = %[4]s;
      window.%[1]s.pending[id][isErr ? "reject" : "resolve"](arg);
      delete window.%[1]s.pending[id];
    })()`, m.Name, p.ID, isErr, arg)
	if err := s.Eval(js, nil); err != nil {
		panic(err)
	}
}

func callBoundFunc(fv reflect.Value, args []json.RawMessage) (any, error) {
	ft, avs := fv.Type(), []reflect.Value{}
	numIn, isVariadic := ft.NumIn(), fv.Type().IsVariadic()
	if !isVariadic && len(args) != ft.NumIn() {
		return nil, fmt.Errorf("wrong number of arguments: %d but expected %d", len(args), ft.NumIn())
	}
	for i := 0; i < len(args); i++ {
		var av reflect.Value
		if isVarArg := i >= numIn-1 && isVariadic; isVarArg {
			av = reflect.New(ft.In(ft.NumIn() - 1).Elem())
		} else {
			av = reflect.New(ft.In(i))
		}
		if err := json.Unmarshal(args[i], av.Interface()); err != nil {
			return nil, err
		}
		avs = append(avs, av.Elem())
	}

	rvs := fv.Call(avs)
	if len(rvs) == 0 {
		return nil, nil
	} else if len(rvs) == 1 {
		if err, ok := rvs[0].Interface().(error); ok {
			return nil, err
		}
		return rvs[0].Interface(), nil
	} else if err := rvs[1].Interface(); err != nil {
		return nil, err.(error)
	}
	return rvs[0].Interface(), nil
}
