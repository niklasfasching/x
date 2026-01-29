package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sync"
)

type Session struct {
	id                     string
	targetID               string
	contextID, uaContextID int
	h                      *H
	handlers               map[string][]*Handler
	bindings               map[string]reflect.Value
	Err                    chan error
	sync.Mutex
	Response
}

type Handler struct {
	reflect.Value
	C chan message
}

func (s *Session) HandleContext(ctx context.Context, method string, f any) {
	cancel := s.Handle(method, f)
	<-ctx.Done()
	cancel()
}

func (s *Session) Handle(method string, f any) func() {
	fv := reflect.ValueOf(f)
	if t := fv.Type(); t.NumIn() != 1 || t.NumOut() != 0 {
		panic(fmt.Sprintf("handler func must be of type func(T): %#v", f))
	}
	h := &Handler{fv, make(chan message, 10)}
	go func() {
		for m := range h.C {
			h.Call(m)
		}
	}()
	s.Lock()
	s.handlers[method] = append(s.handlers[method], h)
	s.Unlock()
	return func() {
		close(h.C)
		s.Lock()
		s.handlers[method] = slices.DeleteFunc(s.handlers[method],
			func(h2 *Handler) bool { return h == h2 })
		s.Unlock()
	}
}

func (s *Session) AwaitC(method string) chan struct{} {
	c, cancel := make(chan struct{}), func() {}
	cancel = s.Handle(method, func(m any) {
		close(c)
		cancel()
	})
	return c
}

func (s *Session) Await(ctx context.Context, method string) error {
	select {
	case <-s.AwaitC(method):
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
	}{}, Params{"expression": js, "returnByValue": true, "replMode": true, "awaitPromise": true}
	if id != 0 {
		params["contextId"] = id
	}
	if err := s.Exec("Runtime.evaluate", params, &r); err != nil {
		return err
	}
	if v == nil || r.Result.Value == nil || string(r.Result.Value) == "null" {
		return nil
	}
	return json.Unmarshal(r.Result.Value, v)
}

func (s *Session) EvalTempUA(js string, v any) error {
	r := struct{ ExecutionContextId int }{}
	params := Params{"frameId": s.targetID, "worldName": s.targetID, "grantUniveralAccess": true}
	if err := s.Exec("Page.createIsolatedWorld", params, &r); err != nil {
		return err
	}
	return s.eval(js, r.ExecutionContextId, v)
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
	if err := s.bind(name, &s.contextID, f); err != nil {
		panic(err)
	}
}

func (s *Session) BindUA(name string, f any) {
	if s.uaContextID == 0 {
		r := struct{ ExecutionContextId int }{}
		params := Params{"frameId": s.targetID, "worldName": s.targetID, "grantUniveralAccess": true}
		if err := s.Exec("Page.createIsolatedWorld", params, &r); err != nil {
			panic(err)
		}
		s.uaContextID = r.ExecutionContextId
	}
	if err := s.bind(name, &s.uaContextID, f); err != nil {
		panic(err)
	}
}

func (s *Session) bind(name string, contextId *int, f any) error {
	fv := reflect.ValueOf(f)
	ft := fv.Type()
	s.Lock()
	s.bindings[name] = fv
	s.Unlock()
	params := Params{"name": name}
	if contextId != nil && *contextId != 0 {
		params["executionContextId"] = *contextId
	}
	if err := s.Exec("Runtime.addBinding", params, nil); err != nil {
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
	return s.eval(js, *contextId, nil)
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

func (h *Handler) Call(m message) {
	av := reflect.New(h.Type().In(0))
	if err := json.Unmarshal(m.Params, av.Interface()); err != nil {
		panic(fmt.Errorf("could not marshal %s into %T", string(m.Params), av.Interface()))
	}
	h.Value.Call([]reflect.Value{av.Elem()})
}
