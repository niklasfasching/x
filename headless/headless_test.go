package headless

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestGeneral(t *testing.T) {
	h, _, err := startAndOpen("about:blank", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	// stop browser
	pid := h.cmd.Process.Pid
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	} else if p, err := os.FindProcess(pid); err != nil {
		t.Fatal(err)
	} else if err := p.Signal(syscall.SIGCONT); err != os.ErrProcessDone {
		t.Fatalf("browser still running: %s", err)
	}
}

func TestBind(t *testing.T) {
	h, s, err := startAndOpen("about:blank", func(s *Session) error {
		s.Bind("console.log", func(i int) string {
			return strconv.Itoa(i)
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	s.Bind("binding", func(code int, fail bool) (int, error) {
		if fail {
			return code, fmt.Errorf("fail")
		}
		return code, nil
	})

	voidBindingArg := 0
	s.Bind("voidBinding", func(arg int) {
		voidBindingArg = arg
	})
	s.Bind("variadicBinding", func(fail bool, xs ...int) ([]int, error) {
		if fail {
			return nil, fmt.Errorf("fail")
		}
		return xs, nil
	})
	check := func(jsCall, jsExpect string) {
		js := fmt.Sprintf(`
          try {
            const result = await %[1]s;
            %[2]s;
          } catch (result) {
            %[2]s;
          }`, jsCall, jsExpect)
		pass := false
		if err := s.Eval(js, &pass); err != nil {
			t.Fatal(err)
		} else if !pass {
			t.Fatalf("js: '%s' != '%s'", jsCall, jsExpect)
		}
	}

	check(`binding(10, false) `, `result === 10`)
	check(`binding(10, true)`, `result.constructor.name === 'Error' && result.message === 'fail'`)
	check(`binding()`, `result.constructor.name === 'Error' && result.message.includes('wrong number of arguments')`)
	check(`binding(10, true, 20)`, `result.constructor.name === 'Error' && result.message.includes('wrong number of arguments')`)
	check(`console.log(123)`, `result === '123'`)
	check(`variadicBinding(false, 1, 2)`, `JSON.stringify(result) === "[1,2]"`)
	check(`variadicBinding(false)`, `JSON.stringify(result) === "[]"`)
	check(`voidBinding(9001)`, `result === undefined`)
	time.Sleep(50 * time.Millisecond)
	if voidBindingArg != 9001 {
		t.Errorf("bad voidBindingArg: %d", voidBindingArg)
	}
}

func startAndOpen(url string, f func(*Session) error) (*H, *Session, error) {
	h, err := Start(nil)
	if err != nil {
		return nil, nil, err
	}
	s, err := h.Open(url, f)
	return h, s, err
}
